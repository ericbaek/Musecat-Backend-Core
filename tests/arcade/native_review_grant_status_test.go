package arcade_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	arcadequery "github.com/ericbaek/musecat-backend-core/handlers/arcade/query"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func seedNativeGrant(t *testing.T, app *tests.TestApp, tags []string) (*core.Record, *core.Record) {
	t.Helper()
	_, user := createAuthUserWithTags(t, app, tags)
	coll, err := app.FindCollectionByNameOrId("supporter_request")
	if err != nil {
		t.Fatal(err)
	}
	request := core.NewRecord(coll)
	request.Set("user", user.Id)
	request.Set("createdBy", user.Id)
	request.Set("status", "pending")
	request.Set("score_total", 315)
	if err := app.Save(request); err != nil {
		t.Fatal(err)
	}
	return user, request
}
func nativeGrantStatusState(t *testing.T, app *tests.TestApp) string {
	t.Helper()
	users, err := app.FindAllRecords("user")
	if err != nil {
		t.Fatal(err)
	}
	tags := map[string][]string{}
	for _, u := range users {
		tags[u.Id] = u.GetStringSlice("tags")
	}
	raw, err := json.Marshal(map[string]any{"domain": moderationReadState(t, app), "tags": tags})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func exportNativeGrantStatus(t *testing.T, name string, v any) {
	t.Helper()
	dir := os.Getenv("MUSECAT_CANONICAL_REVIEW_GRANT_STATUS_FIXTURES")
	if dir == "" {
		return
	}
	raw, err := json.MarshalIndent(v, "", "  ")
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
func candidateForNativeStatus(t *testing.T, app *tests.TestApp, id string) *arcadequery.ArcadeCandidate {
	t.Helper()
	rows, err := arcadequery.GetArcadeCandidates(app)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == id {
			copy := row
			return &copy
		}
	}
	return nil
}
func TestCanonicalNativeSupporterGrantActualWireAndNoReviewerEscalation(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, reviewer := createAuthUserWithTags(t, app, []string{"moderator"})
	_, s := nativeNearbySession(t, app, reviewer)
	for _, tc := range []struct {
		status string
		tags   []string
	}{{"approved", []string{"supporter"}}, {"rejected", []string{"supporter"}}, {"approved", []string{"supporter", "founding_supporter"}}} {
		applicant, request := seedNativeGrant(t, app, tc.tags)
		beforeXP := nativeSubwayState(t, app)
		got := queueDecision(t, app, "/moderation/supporter/request", s.AccessToken, "", map[string]any{"id": " " + request.Id + " ", "status": tc.status, "reason": "  Synthetic review  ", "user": reviewer.Id, "tags": []string{"developer"}}, 200)
		if got["id"] != request.Id || got["user"] != applicant.Id || got["status"] != tc.status || got["decision_reason"] != "Synthetic review" || got["score_total"] != float64(315) {
			t.Fatal("grant receipt/normalization/target changed")
		}
		stored := mustFindRecord(t, app, "user", applicant.Id)
		expected := append([]string{}, tc.tags...)
		if tc.status == "approved" && !slices.Contains(expected, "founding_supporter") {
			expected = append(expected, "founding_supporter")
		}
		if !reflect.DeepEqual(stored.GetStringSlice("tags"), expected) || arcadequery.HasStrictReviewerAccess(stored) {
			t.Fatal("grant changed existing tags or escalated reviewer permission")
		}
		list := moderationRead(t, app, "/moderation/supporter/requests", web, "", 200)
		var found any
		for _, row := range list["items"].([]any) {
			if row.(map[string]any)["id"] == request.Id {
				found = row
			}
		}
		if !reflect.DeepEqual(got, found) {
			t.Fatal("Native decision/Web readback differs")
		}
		before := nativeGrantStatusState(t, app)
		queueDecision(t, app, "/moderation/supporter/request", web, "", map[string]any{"id": request.Id, "status": "rejected", "reason": "stale reviewer"}, 409)
		if before != nativeGrantStatusState(t, app) || beforeXP != nativeSubwayState(t, app) {
			t.Fatal("repeat changed grant or decision awarded XP/history")
		}
		_, applicantSession := nativeNearbySession(t, app, stored)
		moderationRead(t, app, "/moderation/user/reports", applicantSession.AccessToken, "", 403)
		queueDecision(t, app, "/moderation/supporter/request", applicantSession.AccessToken, "", map[string]any{"id": request.Id, "status": "approved"}, 403)
		if len(tc.tags) == 1 {
			exportNativeGrantStatus(t, "review-supporter-"+tc.status, got)
		}
	}
}
func TestCanonicalNativeArcadeStatusCommitCacheAndRepeatPolicy(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, reviewer := createAuthUserWithTags(t, app, []string{"developer"})
	_, s := nativeNearbySession(t, app, reviewer)
	id, _ := seedPublicArcade(t, app, reviewer.Id, arcadeSeed{Name: "Synthetic status venue", Address: "Synthetic road", Location: location{Lat: 37.5, Lon: 127}})
	if candidateForNativeStatus(t, app, id) == nil {
		t.Fatal("initial public candidate missing")
	}
	updates := 0
	app.OnRecordAfterUpdateSuccess("arcade").BindFunc(func(e *core.RecordEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		updates++
		return nil
	})
	beforeXP := nativeSubwayState(t, app)
	for _, flags := range []struct{ public, closed bool }{{true, true}, {false, false}, {false, true}, {true, false}} {
		body := map[string]any{"arcade": " " + id + " ", "public": flags.public, "closed": flags.closed, "country": " jp ", "timezone": " Asia/Tokyo ", "createdBy": "spoof"}
		old := updates
		got := queueDecision(t, app, "/moderation/arcade/status", s.AccessToken, "", body, 200)
		if updates != old+1 {
			t.Fatal("post-success hook not observed after committed write")
		}
		if got["arcade"] != id || got["public"] != flags.public || got["closed"] != flags.closed || got["country"] != "JP" || got["timezone"] != "Asia/Tokyo" {
			t.Fatal("status canonical fields/normalization changed")
		}
		if !reflect.DeepEqual(got, moderationRead(t, app, "/moderation/arcade/status?arcade="+id, web, "", 200)) {
			t.Fatal("Native status/Web readback differs")
		}
		cached := candidateForNativeStatus(t, app, id)
		if flags.public {
			if cached == nil || cached.Country != "JP" || cached.Closed != flags.closed {
				t.Fatal("committed status left stale discovery cache")
			}
		} else if cached != nil {
			t.Fatal("private status left public candidate")
		}
		repeat := queueDecision(t, app, "/moderation/arcade/status", web, "", body, 200)
		if !reflect.DeepEqual(got, repeat) {
			t.Fatal("accepted repeated status changed projection")
		}
		if flags.public && !flags.closed {
			exportNativeGrantStatus(t, "review-arcade-status", got)
		}
	}
	if beforeXP != nativeSubwayState(t, app) {
		t.Fatal("status write awarded XP or content history")
	}
}
func TestCanonicalNativeGrantAndStatusRollbackAndInvalidInput(t *testing.T) {
	for _, fail := range []string{"user", "supporter_request"} {
		t.Run(fail, func(t *testing.T) {
			app := nativeNearbyApp(t, true)
			_, staff := createAuthUserWithTags(t, app, []string{"developer"})
			_, s := nativeNearbySession(t, app, staff)
			_, request := seedNativeGrant(t, app, []string{"supporter"})
			before := nativeGrantStatusState(t, app)
			app.OnRecordUpdate(fail).BindFunc(func(e *core.RecordEvent) error {
				if err := e.Next(); err != nil {
					return err
				}
				return errors.New("synthetic grant persistence failure")
			})
			queueDecision(t, app, "/moderation/supporter/request", s.AccessToken, "", map[string]any{"id": request.Id, "status": "approved", "reason": "Synthetic approval"}, 502)
			if before != nativeGrantStatusState(t, app) {
				t.Fatal("grant/request two-save transaction did not roll back")
			}
		})
	}
	app := nativeNearbyApp(t, true)
	_, staff := createAuthUserWithTags(t, app, []string{"moderator"})
	_, s := nativeNearbySession(t, app, staff)
	applicant, request := seedNativeGrant(t, app, nil)
	id, _ := seedPublicArcade(t, app, staff.Id, arcadeSeed{Name: "Synthetic rollback venue", Address: "Synthetic road", Location: location{Lat: 37.5, Lon: 127}})
	before := nativeGrantStatusState(t, app)
	for _, body := range []map[string]any{{"id": request.Id, "status": "unknown"}, {"id": request.Id, "status": "approved", "reason": strings.Repeat("界", 1201)}, {"id": "missingrecord01", "status": "approved"}} {
		want := 400
		if body["id"] == "missingrecord01" {
			want = 404
		}
		queueDecision(t, app, "/moderation/supporter/request", s.AccessToken, "", body, want)
	}
	for _, body := range []map[string]any{{"arcade": id, "public": true, "country": "KR", "timezone": "Asia/Seoul"}, {"arcade": id, "public": true, "closed": false, "country": "ZZ", "timezone": "Asia/Seoul"}, {"arcade": id, "public": true, "closed": false, "country": "KR", "timezone": "Local"}, {"arcade": "missingrecord01", "public": true, "closed": false, "country": "KR", "timezone": "Asia/Seoul"}} {
		want := 400
		if body["arcade"] == "missingrecord01" {
			want = 404
		}
		queueDecision(t, app, "/moderation/arcade/status", s.AccessToken, "", body, want)
	}
	if before != nativeGrantStatusState(t, app) {
		t.Fatal("invalid inputs changed grant/status")
	}
	_, exactRequest := seedNativeGrant(t, app, nil)
	exact := queueDecision(t, app, "/moderation/supporter/request", s.AccessToken, "", map[string]any{"id": exactRequest.Id, "status": "rejected", "reason": strings.Repeat("界", 1200)}, 200)
	if len([]rune(exact["decision_reason"].(string))) != 1200 {
		t.Fatal("exact Unicode reason limit changed")
	}
	applicant.Set("withdrawn", true)
	if err := app.Save(applicant); err != nil {
		t.Fatal(err)
	}
	before = nativeGrantStatusState(t, app)
	queueDecision(t, app, "/moderation/supporter/request", s.AccessToken, "", map[string]any{"id": request.Id, "status": "approved"}, 409)
	if before != nativeGrantStatusState(t, app) {
		t.Fatal("withdrawn applicant changed")
	}
	cachedBefore := candidateForNativeStatus(t, app, id)
	callbacks := 0
	app.OnRecordAfterUpdateSuccess("arcade").BindFunc(func(e *core.RecordEvent) error { callbacks++; return e.Next() })
	app.OnRecordUpdate("arcade").BindFunc(func(e *core.RecordEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		return errors.New("synthetic status persistence failure")
	})
	queueDecision(t, app, "/moderation/arcade/status", s.AccessToken, "", map[string]any{"arcade": id, "public": false, "closed": true, "country": "JP", "timezone": "Asia/Tokyo"}, 502)
	if before != nativeGrantStatusState(t, app) || callbacks != 0 || !reflect.DeepEqual(cachedBefore, candidateForNativeStatus(t, app, id)) {
		t.Fatal("failed status changed fields or published success/cache state")
	}
}
func TestCanonicalNativeGrantStatusRolesAndCredentialScope(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, request := seedNativeGrant(t, app, nil)
	for _, tags := range [][]string{nil, {"supporter"}, {"founding_supporter"}, {"designer"}, {"arcade_owner"}} {
		web, u := createAuthUserWithTags(t, app, tags)
		_, s := nativeNearbySession(t, app, u)
		before := nativeGrantStatusState(t, app)
		for _, token := range []string{web, s.AccessToken} {
			queueDecision(t, app, "/moderation/supporter/request", token, "", map[string]any{"id": request.Id, "status": "approved"}, 403)
			queueDecision(t, app, "/moderation/arcade/status", token, "", map[string]any{}, 403)
		}
		if before != nativeGrantStatusState(t, app) {
			t.Fatal("denied role mutated data")
		}
	}
	web, u := createAuthUserWithTags(t, app, []string{"developer"})
	_, s := nativeNearbySession(t, app, u)
	for _, path := range []string{"/moderation/supporter/request", "/moderation/arcade/status"} {
		for _, token := range []string{"", strings.Repeat("z", 43)} {
			queueDecision(t, app, path, token, web, map[string]any{}, 401)
		}
	}
	for _, r := range []struct {
		method, path string
		want         int
	}{{"PUT", "/arcade/report-subscription", 401}, {"POST", "/user/withdraw", 401}, {"POST", "/user/signup", 401}, {"POST", "/api/collections/user/auth-refresh", 401}, {"PUT", "/moderation/arcade/edit-report", 401}, {"PUT", "/developer/v1/account/moderation/supporter/request", 403}, {"PUT", "/native/moderation/supporter/request", 404}, {"PUT", "/native/moderation/arcade/status", 404}} {
		res := executeJSONRequest(t, app, r.method, r.path, "{}", map[string]string{"Authorization": "Bearer " + s.AccessToken})
		res.Body.Close()
		if res.StatusCode != r.want {
			t.Fatal("unselected capability changed")
		}
	}
}
func TestCanonicalNativeGrantStatusLifetimeDisabledAndQuota(t *testing.T) {
	for _, mode := range []string{"disabled", "revoked", "expired", "family-expired", "removed-client", "account-key", "withdrawn", "ban"} {
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
			case "account-key":
				u.RefreshTokenKey()
				if err := app.Save(u); err != nil {
					t.Fatal(err)
				}
			case "withdrawn":
				u.Set("withdrawn", true)
				if err := app.Save(u); err != nil {
					t.Fatal(err)
				}
			case "ban":
				seedUserBan(t, app, u.Id, "", "Synthetic grant/status ban", time.Now().Add(time.Hour))
			case "expired", "family-expired", "removed-client":
				clause := map[string]string{"expired": "access_expires=0", "family-expired": "expires=0", "removed-client": "client='removed'"}[mode]
				if _, err := app.DB().NewQuery("UPDATE native_auth_session SET " + clause + " WHERE user_id={:user}").Bind(dbx.Params{"user": u.Id}).Execute(); err != nil {
					t.Fatal(err)
				}
			}
			before := nativeGrantStatusState(t, app)
			want := 401
			if mode == "ban" {
				want = 403
			}
			for _, path := range []string{"/moderation/supporter/request", "/moderation/arcade/status"} {
				queueDecision(t, app, path, s.AccessToken, "", map[string]any{}, want)
				if mode == "disabled" {
					queueDecision(t, app, path, web, "", map[string]any{}, 400)
				}
			}
			if before != nativeGrantStatusState(t, app) {
				t.Fatal("inactive credentials changed data")
			}
		})
	}
	app := nativeNearbyApp(t, true)
	_, u := createAuthUserWithTags(t, app, []string{"moderator"})
	_, s := nativeNearbySession(t, app, u)
	app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: "PUT /moderation/supporter/request", MaxRequests: 1, Duration: 60}, {Label: "PUT /moderation/arcade/status", MaxRequests: 1, Duration: 60}}}
	before := nativeGrantStatusState(t, app)
	for _, path := range []string{"/moderation/supporter/request", "/moderation/arcade/status"} {
		queueDecision(t, app, path, s.AccessToken, "", map[string]any{}, 400)
		queueDecision(t, app, path, s.AccessToken, "", map[string]any{}, 429)
	}
	if before != nativeGrantStatusState(t, app) {
		t.Fatal("rate rejection changed data")
	}
}
