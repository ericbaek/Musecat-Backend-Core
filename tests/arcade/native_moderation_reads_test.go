package arcade_test

import (
	"encoding/json"
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

func moderationRead(t *testing.T, app *tests.TestApp, path, token, cookie string, want int) map[string]any {
	t.Helper()
	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	if cookie != "" {
		headers["Cookie"] = "pb_auth=" + cookie
	}
	res := executeJSONRequest(t, app, "GET", path, "", headers)
	defer res.Body.Close()
	if res.StatusCode != want {
		t.Fatalf("GET %s status %d want %d", path, res.StatusCode, want)
	}
	if len(token) == 43 && want != 401 && !strings.HasPrefix(path, "/native/") && !strings.Contains(res.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("Native moderation response cacheable")
	}
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func moderationReadState(t *testing.T, app *tests.TestApp) string {
	t.Helper()
	state := map[string]any{}
	for _, collection := range []string{"arcade", "arcade_request_admin", "supporter_request", "user_report", "user_ban", "user_level", "user_level_log", "arcade_changelog"} {
		rows, err := app.FindAllRecords(collection)
		if err != nil {
			t.Fatal(err)
		}
		state[collection] = rows
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func moderationReadPaths(arcade, request string) []string {
	return []string{"/moderation/arcade/requests", "/moderation/arcade/status?arcade=" + arcade, "/moderation/supporter/requests", "/moderation/supporter/request?id=" + request, "/moderation/user/reports", "/moderation/user/bans"}
}

func seedModerationReads(t *testing.T, app *tests.TestApp) (string, string) {
	t.Helper()
	_, owner := createAuthUser(t, app)
	_, target := createAuthUser(t, app)
	arcade, _ := seedArcade(t, app, owner.Id, arcadeSeed{Name: "Synthetic review venue", Address: "Synthetic road", Location: location{Lat: 37.5, Lon: 127}})
	setArcadeVisibility(t, app, arcade, false, true)
	var request string
	for _, status := range []string{"waiting", "done"} {
		for _, item := range []struct {
			collection string
			fields     map[string]any
		}{
			{"arcade_request_admin", map[string]any{"arcade": arcade, "createdBy": owner.Id, "kind": "general", "status": status, "message": "Synthetic review request", "urgency": "medium"}},
			{"supporter_request", map[string]any{"user": owner.Id, "createdBy": owner.Id, "status": "pending", "score_total": 0}},
			{"user_report", map[string]any{"user": target.Id, "createdBy": owner.Id, "reason": "Synthetic review report", "status": status}},
		} {
			coll, err := app.FindCollectionByNameOrId(item.collection)
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
			if item.collection == "supporter_request" {
				request = r.Id
			}
		}
	}
	seedUserBan(t, app, target.Id, "", "Synthetic permanent ban", time.Time{})
	return arcade, request
}

func TestCanonicalNativeModerationReadWireParityAndNoMutation(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, u := createAuthUserWithTags(t, app, []string{"moderator"})
	_, s := nativeNearbySession(t, app, u)
	arcade, request := seedModerationReads(t, app)
	before := moderationReadState(t, app)
	names := []string{"moderation-arcade-requests", "moderation-arcade-status", "moderation-supporter-requests", "moderation-supporter-request", "moderation-user-reports", "moderation-user-bans"}
	for i, path := range moderationReadPaths(arcade, request) {
		native := moderationRead(t, app, path, s.AccessToken, "", 200)
		if !reflect.DeepEqual(native, moderationRead(t, app, path, web, "", 200)) {
			t.Fatal("Web/Native moderation DTO differs")
		}
		if rows, ok := native["items"].([]any); ok {
			for j := 1; j < len(rows); j++ {
				if rows[j-1].(map[string]any)["created"].(string) < rows[j].(map[string]any)["created"].(string) {
					t.Fatal("newest-first ordering changed")
				}
			}
			if !reflect.DeepEqual(native, moderationRead(t, app, path+"?page=2&perPage=1", s.AccessToken, "", 200)) {
				t.Fatal("complete snapshot unexpectedly paginated")
			}
		}
		if i == 1 && (native["public"] != false || native["closed"] != true) {
			t.Fatal("strict reviewer private status changed")
		}
		if dir := os.Getenv("MUSECAT_CANONICAL_MODERATION_READ_FIXTURES"); dir != "" {
			raw, err := json.MarshalIndent(native, "", "  ")
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
			if err := os.WriteFile(filepath.Join(dir, names[i]+".json"), append(raw, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	filtered := moderationRead(t, app, "/moderation/user/reports?status=done", s.AccessToken, "", 200)
	if filtered["total"] != float64(1) || filtered["items"].([]any)[0].(map[string]any)["status"] != "done" {
		t.Fatal("raw report status filter lost")
	}
	if len(moderationRead(t, app, "/moderation/user/reports?status=absent", s.AccessToken, "", 200)["items"].([]any)) != 0 {
		t.Fatal("empty result changed")
	}
	moderationRead(t, app, "/moderation/arcade/status?arcade=missing", s.AccessToken, "", 404)
	moderationRead(t, app, "/moderation/supporter/request?id=missing", s.AccessToken, "", 404)
	if before != moderationReadState(t, app) {
		t.Fatal("moderation reads mutated domain state")
	}
}

func TestCanonicalNativeModerationReadRolesAndCredentialScope(t *testing.T) {
	app := nativeNearbyApp(t, true)
	arcade, request := seedModerationReads(t, app)
	for _, tags := range [][]string{nil, {"supporter"}, {"founding_supporter"}, {"designer"}, {"arcade_owner"}, {"moderator"}, {"developer"}} {
		web, u := createAuthUserWithTags(t, app, tags)
		_, s := nativeNearbySession(t, app, u)
		want := 403
		if len(tags) > 0 && (tags[0] == "moderator" || tags[0] == "developer") {
			want = 200
		}
		before := moderationReadState(t, app)
		for _, path := range moderationReadPaths(arcade, request) {
			for _, token := range []string{web, s.AccessToken} {
				moderationRead(t, app, path, token, "", want)
			}
		}
		if before != moderationReadState(t, app) {
			t.Fatal("role read mutated state")
		}
	}
	web, u := createAuthUserWithTags(t, app, []string{"developer"})
	_, s := nativeNearbySession(t, app, u)
	for _, path := range moderationReadPaths(arcade, request) {
		moderationRead(t, app, path, "", "", 401)
		moderationRead(t, app, path, strings.Repeat("z", 43), web, 401)
		moderationRead(t, app, "/native"+path, s.AccessToken, "", 404)
	}
	for _, r := range []struct{ method, path string }{{"POST", "/arcade/check-notes"}, {"PUT", "/moderation/arcade/status"}, {"PUT", "/moderation/supporter/request"}, {"PUT", "/arcade/report-subscription"}, {"POST", "/moderation/user/ban"}, {"DELETE", "/moderation/user/ban"}, {"POST", "/moderation/user/test-account"}, {"POST", "/moderation/user/arcade-account"}, {"POST", "/user/withdraw"}, {"GET", "/moderation/arcade/edit-reports"}} {
		res := executeJSONRequest(t, app, r.method, r.path, "", map[string]string{"Authorization": "Bearer " + s.AccessToken})
		res.Body.Close()
		if res.StatusCode != 401 {
			t.Fatalf("unselected %s %s status%d", r.method, r.path, res.StatusCode)
		}
	}
}

func TestCanonicalNativeModerationReadAccountLifetimeAndDisabledRuntime(t *testing.T) {
	for _, mode := range []string{"disabled", "revoked", "withdrawn", "expired", "family-expired", "removed-client", "account-key", "ban"} {
		t.Run(mode, func(t *testing.T) {
			app := nativeNearbyApp(t, mode != "disabled")
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
				seedUserBan(t, app, u.Id, "", "Synthetic review ban", time.Now().Add(time.Hour))
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
			for _, path := range moderationReadPaths("missing", "missing") {
				moderationRead(t, app, path, s.AccessToken, "", want)
			}
			if mode == "disabled" {
				moderationRead(t, app, "/moderation/user/reports", web, "", 200)
			}
			if before != moderationReadState(t, app) {
				t.Fatal("rejected session mutated domain")
			}
		})
	}
}

func TestCanonicalNativeModerationReadRateLimit(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, u := createAuthUserWithTags(t, app, []string{"developer"})
	_, s := nativeNearbySession(t, app, u)
	app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: "GET /moderation/user/reports", MaxRequests: 1, Duration: 60}}}
	before := moderationReadState(t, app)
	moderationRead(t, app, "/moderation/user/reports", s.AccessToken, "", 200)
	moderationRead(t, app, "/moderation/user/reports", s.AccessToken, "", 429)
	if before != moderationReadState(t, app) {
		t.Fatal("read quota mutated domain")
	}
}
