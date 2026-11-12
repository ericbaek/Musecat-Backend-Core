package arcade_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	arcadeadmin "github.com/ericbaek/musecat-backend-core/handlers/arcade/admin"
	"github.com/pocketbase/pocketbase/core"
)

func TestCanonicalNativeSupporterRequestSharesWebPolicyAndSideEffects(t *testing.T) {
	app := nativeNearbyApp(t, true)
	arcadeadmin.RegisterTelegramNotifyHooks(app)
	web, user := createAuthUser(t, app)
	_, session := nativeNearbySession(t, app, user)
	telegram, discord := 0, 0
	t.Cleanup(arcadeadmin.SetTelegramSenderForTest(func(context.Context, string) error { telegram++; return nil }))
	t.Cleanup(arcadeadmin.SetDiscordSenderForTest(func(context.Context, string) error { discord++; return nil }))
	request := func(token string, body any, status int) map[string]any {
		return nativeBulkRequest(t, app, "POST", "/supporter/request", token, body, status).(map[string]any)
	}
	body := map[string]any{"user": "ignored-actor", "createdBy": "ignored-actor", "qualified": true, "score_total": 99999, "status": "approved"}
	seedUserLevelExp(t, app, user.Id, 299)
	before := nativeBulkState(t, app)
	if !reflect.DeepEqual(request(web, body, 400), request(session.AccessToken, body, 400)) || !reflect.DeepEqual(before, nativeBulkState(t, app)) {
		t.Fatal("Web/Native threshold denial or XP state differs")
	}
	seedUserLevelExp(t, app, user.Id, 300)
	seedSupporterLedgerEntry(t, app, user.Id, "xp:arcade-visit:synthetic-visit", 0, 300, time.Now().Add(-time.Hour))
	before = nativeBulkState(t, app)
	score := nativeBulkRequest(t, app, "GET", "/supporter/score", web, nil, 200)
	if !reflect.DeepEqual(score, nativeBulkRequest(t, app, "GET", "/supporter/score", session.AccessToken, nil, 200)) {
		t.Fatal("Web/Native score DTO differs")
	}
	created := request(session.AccessToken, body, 200)
	if created["status"] != "pending" || created["qualified"] != true || !reflect.DeepEqual(created["exp"], score) {
		t.Fatal("canonical creation receipt differs from authoritative score")
	}
	stored, err := app.FindRecordById("supporter_request", created["id"].(string))
	if err != nil || stored.GetString("user") != user.Id || stored.GetString("createdBy") != user.Id || stored.GetInt("score_total") != 300 || stored.GetString("status") != "pending" {
		t.Fatal("request trusted client actor/score/status", err)
	}
	actorAfter, err := app.FindRecordById("user", user.Id)
	if err != nil || !reflect.DeepEqual(actorAfter.GetStringSlice("tags"), user.GetStringSlice("tags")) {
		t.Fatal("application creation granted a role", err)
	}
	if telegram != 1 || discord != 1 || !reflect.DeepEqual(before, nativeBulkState(t, app)) {
		t.Fatal("request changed XP/history or notification multiplicity")
	}
	if !reflect.DeepEqual(request(web, body, 409), request(session.AccessToken, body, 409)) {
		t.Fatal("Web/Native duplicate DTO differs")
	}
	if telegram != 1 || discord != 1 {
		t.Fatal("duplicate request sent notifications")
	}
	stored.Set("status", "approved")
	if err := app.Save(stored); err != nil {
		t.Fatal(err)
	}
	request(session.AccessToken, body, 409)
	stored.Set("status", "rejected")
	if err := app.Save(stored); err != nil {
		t.Fatal(err)
	}
	score = nativeBulkRequest(t, app, "GET", "/supporter/score", web, nil, 200)
	if score.(map[string]any)["can_request"] != true {
		t.Fatal("rejected application cannot retry")
	}
	webCreated := request(web, body, 200)
	if webCreated["id"] == created["id"] || webCreated["status"] != "pending" || !reflect.DeepEqual(webCreated["exp"], score) {
		t.Fatal("Web retry changed canonical creation contract")
	}
	if telegram != 2 || discord != 2 || !reflect.DeepEqual(before, nativeBulkState(t, app)) {
		t.Fatal("retry notification/XP contract changed")
	}
	if dir := os.Getenv("MUSECAT_CANONICAL_SUPPORTER_FIXTURES"); dir != "" {
		raw, err := json.MarshalIndent(created, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"password", "email", "access_token", "refresh_token"} {
			if strings.Contains(strings.ToLower(string(raw)), `"`+key+`"`) {
				t.Fatal("credential in domain fixture")
			}
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "supporter-request-create.json"), append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCanonicalNativeSupporterRequestFailsClosedAndKeepsAccountGuards(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, user := createAuthUser(t, app)
	service, session := nativeNearbySession(t, app, user)
	before := nativeBulkState(t, app)
	for _, token := range []string{"", strings.Repeat("z", 43)} {
		nativeBulkRequest(t, app, "POST", "/supporter/request", token, map[string]any{}, 401)
	}
	res := executeJSONRequest(t, app, "POST", "/supporter/request", "{}", map[string]string{"Authorization": "Bearer " + strings.Repeat("z", 43), "Cookie": "pb_auth=" + web})
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("invalid bearer fell back to web cookie")
	}
	seedUserBan(t, app, user.Id, "", "supporter_canary_ban", time.Now().Add(time.Hour))
	for _, token := range []string{web, session.AccessToken} {
		nativeBulkRequest(t, app, "POST", "/supporter/request", token, map[string]any{}, 403)
	}
	if err := service.Revoke(app, session.RefreshToken); err != nil {
		t.Fatal(err)
	}
	nativeBulkRequest(t, app, "POST", "/supporter/request", session.AccessToken, map[string]any{}, 401)
	if !reflect.DeepEqual(before, nativeBulkState(t, app)) {
		t.Fatal("denial changed XP/history")
	}
	n, err := app.CountRecords("supporter_request")
	if err != nil || n != 0 {
		t.Fatal("denied request persisted", err)
	}
}

func TestCanonicalNativeSupporterRequestDisabledUsesValidSameDatabaseSession(t *testing.T) {
	app := nativeNearbyApp(t, false)
	web, user := createAuthUser(t, app)
	service, session := nativeNearbySession(t, app, user)
	seedUserLevelExp(t, app, user.Id, 300)
	if actor, err := service.Authenticate(app, session.AccessToken); err != nil || actor.Id != user.Id {
		t.Fatal("disabled fixture is not a valid same-DB session")
	}
	response := executeJSONRequest(t, app, "POST", "/supporter/request", "{}", map[string]string{"Authorization": "Bearer " + session.AccessToken})
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("disabled adapter accepted valid Native session")
	}
	// Web callers still reach the existing threshold handler while Native binding is disabled.
	seedUserLevelExp(t, app, user.Id, 299)
	nativeBulkRequest(t, app, "POST", "/supporter/request", web, map[string]any{}, 400)
	n, err := app.CountRecords("supporter_request")
	if err != nil || n != 0 {
		t.Fatal("disabled session persisted request", err)
	}
}

func TestCanonicalNativeSupporterRequestRetainsRateAndUnselectedBoundaries(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, user := createAuthUserWithTags(t, app, []string{"developer"})
	_, session := nativeNearbySession(t, app, user)
	for _, op := range []struct {
		method, path string
		status       int
	}{{"POST", "/native/supporter/request", 404}, {"POST", "/moderation/user/ban", 401}, {"PUT", "/moderation/arcade/status", 401}, {"PUT", "/moderation/supporter/request", 401}, {"POST", "/user/withdraw", 401}} {
		response := executeJSONRequest(t, app, op.method, op.path, "{}", map[string]string{"Authorization": "Bearer " + session.AccessToken})
		response.Body.Close()
		if response.StatusCode != op.status {
			t.Fatal("supporter binding widened an unselected route")
		}
	}
	app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: "POST /supporter/request", MaxRequests: 1, Duration: 60}}}
	nativeBulkRequest(t, app, "POST", "/supporter/request", session.AccessToken, map[string]any{}, 400)
	nativeBulkRequest(t, app, "POST", "/supporter/request", session.AccessToken, map[string]any{}, 429)
	n, err := app.CountRecords("supporter_request")
	if err != nil || n != 0 {
		t.Fatal("rate denied request persisted", err)
	}
}

func TestCanonicalNativeSupporterRequestSaveFailureDoesNotGrantXP(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, user := createAuthUser(t, app)
	_, session := nativeNearbySession(t, app, user)
	seedUserLevelExp(t, app, user.Id, 300)
	before := nativeBulkState(t, app)
	app.OnRecordCreate("supporter_request").BindFunc(func(*core.RecordEvent) error { return errors.New("synthetic persistence failure") })
	nativeBulkRequest(t, app, "POST", "/supporter/request", session.AccessToken, map[string]any{}, 502)
	n, err := app.CountRecords("supporter_request")
	if err != nil || n != 0 || !reflect.DeepEqual(before, nativeBulkState(t, app)) {
		t.Fatal("save failure changed request/XP state", err)
	}
}
