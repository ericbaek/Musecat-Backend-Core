package arcade_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func queueDecision(t *testing.T, app *tests.TestApp, path, token, cookie string, body any, want int) map[string]any {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Content-Type": "application/json"}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	if cookie != "" {
		headers["Cookie"] = "pb_auth=" + cookie
	}
	res := executeJSONRequest(t, app, "PUT", path, string(raw), headers)
	defer res.Body.Close()
	if res.StatusCode != want {
		t.Fatalf("queue decision status%d want%d", res.StatusCode, want)
	}
	if len(token) == 43 && want != 401 && !strings.Contains(res.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("Native decision cacheable")
	}
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}
func seedQueueDecision(t *testing.T, app *tests.TestApp, kind string) (*core.Record, *core.Record) {
	t.Helper()
	_, reporter := createAuthUser(t, app)
	_, target := createAuthUser(t, app)
	arcade, _ := seedArcade(t, app, reporter.Id, arcadeSeed{Name: "Synthetic queue", Address: "Synthetic road", Location: location{Lat: 37.5, Lon: 127}})
	changes, err := app.FindCollectionByNameOrId("arcade_changelog")
	if err != nil {
		t.Fatal(err)
	}
	revision := core.NewRecord(changes)
	revision.Set("arcade", arcade)
	revision.Set("by", reporter.Id)
	revision.Set("changed", "basic")
	revision.Set("from", "")
	revision.Set("to", "")
	if err := app.Save(revision); err != nil {
		t.Fatal(err)
	}
	var out []*core.Record
	for _, item := range []struct {
		name   string
		fields map[string]any
	}{
		{"arcade_request_admin", map[string]any{"arcade": arcade, "changelog": revision.Id, "createdBy": reporter.Id, "kind": kind, "urgency": "medium", "message": "Synthetic queue decision", "status": "waiting"}},
		{"user_report", map[string]any{"user": target.Id, "createdBy": reporter.Id, "reason": "Synthetic queue report", "status": "waiting"}},
	} {
		coll, err := app.FindCollectionByNameOrId(item.name)
		if err != nil {
			t.Fatal(err)
		}
		r := core.NewRecord(coll)
		for k, v := range item.fields {
			r.Set(k, v)
		}
		if err := app.Save(r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out[0], out[1]
}
func exportQueueDecision(t *testing.T, name string, value any) {
	t.Helper()
	dir := os.Getenv("MUSECAT_CANONICAL_MODERATION_QUEUE_FIXTURES")
	if dir == "" {
		return
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"password", "email", "access_token", "refresh_token"} {
		if strings.Contains(strings.ToLower(string(raw)), `"`+key+`"`) {
			t.Fatal("credential in fixture")
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestCanonicalNativeModerationQueueActualPolicyAndWebReadback(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, actor := createAuthUserWithTags(t, app, []string{"moderator"})
	_, s := nativeNearbySession(t, app, actor)
	otherWeb, other := createAuthUserWithTags(t, app, []string{"developer"})
	for _, kind := range []string{"general", "edit_report", "rollback_report"} {
		request, report := seedQueueDecision(t, app, kind)
		beforeXP := nativeSubwayState(t, app)
		got := queueDecision(t, app, "/moderation/arcade/request", s.AccessToken, "", map[string]any{"id": " " + request.Id + " ", "outcome": " UPHELD ", "note": "  Reviewed  ", "reviewed_by": other.Id, "createdBy": other.Id, "arcade": "spoof"}, 200)
		stored := mustFindRecord(t, app, "arcade_request_admin", request.Id)
		if got["id"] != request.Id || got["status"] != "done" || got["review_outcome"] != "upheld" || got["review_note"] != "Reviewed" || got["reviewed_by"] != actor.Id || stored.GetString("createdBy") != request.GetString("createdBy") || stored.GetString("arcade") != request.GetString("arcade") || stored.GetString("kind") != kind {
			t.Fatal("canonical reviewer/normalization/context changed")
		}
		list := moderationRead(t, app, "/moderation/arcade/requests", web, "", 200)
		var found any
		for _, row := range list["items"].([]any) {
			if row.(map[string]any)["id"] == request.Id {
				found = row
			}
		}
		if !reflect.DeepEqual(got, found) {
			t.Fatal("Native write and Web canonical readback differ")
		}
		before := moderationReadState(t, app)
		queueDecision(t, app, "/moderation/arcade/request", otherWeb, "", map[string]any{"id": request.Id, "outcome": "dismissed", "note": "stale reviewer"}, 409)
		if before != moderationReadState(t, app) {
			t.Fatal("stale second reviewer overwrote first decision")
		}
		updated := queueDecision(t, app, "/moderation/user/report", s.AccessToken, "", map[string]any{"id": " " + report.Id + " ", "status": " PROCESSING ", "createdBy": actor.Id, "user": actor.Id, "reason": "spoof"}, 200)
		for _, k := range []string{"user", "createdBy", "reason"} {
			if updated[k] != report.GetString(k) {
				t.Fatal("stored report identity/content overwritten")
			}
		}
		if updated["status"] != "processing" {
			t.Fatal("report status normalization changed")
		}
		repeated := queueDecision(t, app, "/moderation/user/report", otherWeb, "", map[string]any{"id": report.Id, "status": "processing"}, 200)
		for _, k := range []string{"id", "user", "createdBy", "reason", "status", "created"} {
			if repeated[k] != updated[k] {
				t.Fatal("accepted same-status repeat changed report content")
			}
		}
		reportList := moderationRead(t, app, "/moderation/user/reports?status=processing", s.AccessToken, "", 200)
		var readback any
		for _, row := range reportList["items"].([]any) {
			if row.(map[string]any)["id"] == report.Id {
				readback = row
			}
		}
		if !reflect.DeepEqual(repeated, readback) {
			t.Fatal("Web write and Native canonical readback differ")
		}
		if beforeXP != nativeSubwayState(t, app) {
			t.Fatal("queue decisions changed XP/content history")
		}
		if kind == "general" {
			exportQueueDecision(t, "moderation-arcade-decision", got)
			exportQueueDecision(t, "moderation-user-decision", updated)
		}
	}
}
func TestCanonicalNativeModerationQueueRolesInvalidInputAndAtomicFailure(t *testing.T) {
	app := nativeNearbyApp(t, true)
	request, report := seedQueueDecision(t, app, "general")
	for _, tags := range [][]string{nil, {"supporter"}, {"founding_supporter"}, {"designer"}, {"arcade_owner"}} {
		web, u := createAuthUserWithTags(t, app, tags)
		_, s := nativeNearbySession(t, app, u)
		before := moderationReadState(t, app)
		for _, token := range []string{web, s.AccessToken} {
			queueDecision(t, app, "/moderation/arcade/request", token, "", map[string]any{"id": request.Id, "outcome": "upheld"}, 403)
			queueDecision(t, app, "/moderation/user/report", token, "", map[string]any{"id": report.Id, "status": "done"}, 403)
		}
		if before != moderationReadState(t, app) {
			t.Fatal("denied role mutated decisions")
		}
	}
	web, u := createAuthUserWithTags(t, app, []string{"developer"})
	_, s := nativeNearbySession(t, app, u)
	before := moderationReadState(t, app)
	for _, token := range []string{"", strings.Repeat("z", 43)} {
		for _, path := range []string{"/moderation/arcade/request", "/moderation/user/report"} {
			queueDecision(t, app, path, token, web, map[string]any{}, 401)
		}
	}
	for _, tc := range []struct {
		path string
		body any
		want int
	}{
		{"/moderation/arcade/request", map[string]any{"id": request.Id, "outcome": "unknown"}, 400},
		{"/moderation/arcade/request", map[string]any{"id": request.Id, "outcome": "upheld", "note": strings.Repeat("界", 1201)}, 400},
		{"/moderation/arcade/request", map[string]any{"id": "missingrecord01", "outcome": "upheld"}, 404},
		{"/moderation/user/report", map[string]any{"id": report.Id, "status": "unknown"}, 400},
		{"/moderation/user/report", map[string]any{"id": "missingrecord01", "status": "done"}, 404},
	} {
		queueDecision(t, app, tc.path, s.AccessToken, "", tc.body, tc.want)
	}
	if before != moderationReadState(t, app) {
		t.Fatal("invalid input mutated state")
	}
	for _, name := range []string{"arcade_request_admin", "user_report"} {
		app.OnRecordUpdate(name).BindFunc(func(*core.RecordEvent) error { return errors.New("synthetic save failure") })
	}
	queueDecision(t, app, "/moderation/arcade/request", s.AccessToken, "", map[string]any{"id": request.Id, "outcome": "upheld"}, 502)
	queueDecision(t, app, "/moderation/user/report", s.AccessToken, "", map[string]any{"id": report.Id, "status": "done"}, 502)
	if before != moderationReadState(t, app) {
		t.Fatal("failed persistence left partial review/XP state")
	}
}
func TestCanonicalNativeModerationQueueLifetimeAndDisabledConfig(t *testing.T) {
	for _, mode := range []string{"disabled", "revoked", "expired", "family-expired", "removed-client", "account-key", "withdrawn", "ban"} {
		t.Run(mode, func(t *testing.T) {
			app := nativeNearbyApp(t, mode != "disabled")
			request, report := seedQueueDecision(t, app, "general")
			web, u := createAuthUserWithTags(t, app, []string{"developer"})
			service, s := nativeNearbySession(t, app, u)
			if owner, err := service.Authenticate(app, s.AccessToken); err != nil || owner.Id != u.Id {
				t.Fatal("fixture session invalid")
			}
			switch mode {
			case "revoked":
				if err := service.Revoke(app, s.RefreshToken); err != nil {
					t.Fatal(err)
				}
			case "withdrawn":
				u.Set("withdrawn", true)
				if err := app.Save(u); err != nil {
					t.Fatal(err)
				}
			case "account-key":
				u.RefreshTokenKey()
				if err := app.Save(u); err != nil {
					t.Fatal(err)
				}
			case "ban":
				seedUserBan(t, app, u.Id, "", "Synthetic queue ban", time.Now().Add(time.Hour))
			case "expired", "family-expired", "removed-client":
				clause := map[string]string{"expired": "access_expires=0", "family-expired": "expires=0", "removed-client": "client='removed'"}[mode]
				if _, err := app.DB().NewQuery("UPDATE native_auth_session SET " + clause + " WHERE user_id={:user}").Bind(dbx.Params{"user": u.Id}).Execute(); err != nil {
					t.Fatal(err)
				}
			}
			before := moderationReadState(t, app)
			want := 401
			if mode == "ban" {
				want = 403
			}
			queueDecision(t, app, "/moderation/arcade/request", s.AccessToken, "", map[string]any{"id": request.Id, "outcome": "upheld"}, want)
			queueDecision(t, app, "/moderation/user/report", s.AccessToken, "", map[string]any{"id": report.Id, "status": "done"}, want)
			if before != moderationReadState(t, app) {
				t.Fatal("inactive Native session changed decisions")
			}
			if mode == "disabled" {
				queueDecision(t, app, "/moderation/user/report", web, "", map[string]any{"id": report.Id, "status": "done"}, 200)
			}
		})
	}
}
func TestCanonicalNativeModerationQueueRateAndUnselectedScope(t *testing.T) {
	app := nativeNearbyApp(t, true)
	request, report := seedQueueDecision(t, app, "general")
	_, u := createAuthUserWithTags(t, app, []string{"moderator"})
	_, s := nativeNearbySession(t, app, u)
	app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: "PUT /moderation/user/report", MaxRequests: 1, Duration: 60}}}
	queueDecision(t, app, "/moderation/user/report", s.AccessToken, "", map[string]any{"id": report.Id, "status": "done"}, 200)
	before := moderationReadState(t, app)
	queueDecision(t, app, "/moderation/user/report", s.AccessToken, "", map[string]any{"id": report.Id, "status": "waiting"}, 429)
	for _, r := range []struct {
		method, path string
		want         int
	}{{"PUT", "/moderation/arcade/status", 401}, {"PUT", "/moderation/supporter/request", 401}, {"POST", "/moderation/user/ban", 401}, {"DELETE", "/moderation/user/ban", 401}, {"POST", "/moderation/user/test-account", 401}, {"POST", "/moderation/user/arcade-account", 401}, {"PUT", "/developer/v1/account/moderation/arcade/request", 403}, {"GET", "/api/collections/arcade/records", 403}, {"PUT", "/native/moderation/arcade/request", 404}, {"PUT", "/native/moderation/user/report", 404}} {
		res := executeJSONRequest(t, app, r.method, r.path, `{}`, map[string]string{"Authorization": "Bearer " + s.AccessToken})
		res.Body.Close()
		if res.StatusCode != r.want {
			t.Fatalf("unselected %s %s status%d want%d", r.method, r.path, res.StatusCode, r.want)
		}
	}
	if before != moderationReadState(t, app) {
		t.Fatal("quota/unselected operation changed state")
	}
	queueDecision(t, app, "/moderation/arcade/request", s.AccessToken, "", map[string]any{"id": request.Id, "outcome": "dismissed", "note": strings.Repeat("界", 1200)}, 200)
}
