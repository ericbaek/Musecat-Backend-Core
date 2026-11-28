package arcade_test

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

var nativeOfficialOps = []struct{ method, path string }{{"GET", "/user/arcade-inquiry-onboarding"}, {"GET", "/arcade/report-settings"}, {"PUT", "/arcade/report-settings"}, {"PUT", "/arcade/inquiry-settings"}, {"PUT", "/user/arcade-account/credentials"}}

func exportNativeOfficial(t *testing.T, name string, value any) {
	t.Helper()
	dir := os.Getenv("MUSECAT_CANONICAL_OFFICIAL_FIXTURES")
	if dir == "" {
		return
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"password", "currentpassword", "newpassword", "email", "access_token", "refresh_token", "token"} {
		if strings.Contains(strings.ToLower(string(raw)), `"`+key+`"`) {
			t.Fatal("credential field in official DTO fixture")
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
func syntheticOfficialPassword(t *testing.T) string {
	t.Helper()
	var b [18]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal("synthetic password generation failed")
	}
	return " " + hex.EncodeToString(b[:]) + " "
}

func TestCanonicalNativeOfficialSettingsShareWebDTOAndOwnedOnboarding(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	_, creator := createAuthUser(t, app)
	id, _ := seedArcade(t, app, creator.Id, arcadeSeed{Name: "Synthetic official venue", Address: "Synthetic road", Location: location{Lat: 37.5, Lon: 127}})
	foreign, _ := seedPublicArcade(t, app, creator.Id, arcadeSeed{Name: "Other synthetic venue", Address: "Synthetic road", Location: location{Lat: 37.5, Lon: 127}})
	owner.Set("owns", []string{id})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	_, session := nativeNearbySession(t, app, owner)
	path := "/arcade/report-settings?arcade=" + id
	initial := nativeBulkRequest(t, app, "GET", path, session.AccessToken, nil, 200)
	if !reflect.DeepEqual(initial, nativeBulkRequest(t, app, "GET", path, web, nil, 200)) {
		t.Fatal("official default report DTO differs")
	}
	exportNativeOfficial(t, "official-report-get", initial)
	body := map[string]any{"arcade": id, "mode": "external", "contactType": "url", "contactValue": " https://example.invalid/report ", "guide": " Synthetic guide ", "future_field": true}
	report := nativeBulkRequest(t, app, "PUT", "/arcade/report-settings", session.AccessToken, body, 200)
	if !reflect.DeepEqual(report, nativeBulkRequest(t, app, "PUT", "/arcade/report-settings", web, body, 200)) {
		t.Fatal("official report write differs from Web")
	}
	if !reflect.DeepEqual(report, nativeBulkRequest(t, app, "GET", path, web, nil, 200)) {
		t.Fatal("official report readback differs")
	}
	exportNativeOfficial(t, "official-report-save", report)
	body["loginRequired"] = true
	body["configured"] = false
	inquiry := nativeBulkRequest(t, app, "PUT", "/arcade/inquiry-settings", session.AccessToken, body, 200).(map[string]any)
	if inquiry["configured"] != true || inquiry["loginRequired"] != true || !reflect.DeepEqual(inquiry, nativeBulkRequest(t, app, "PUT", "/arcade/inquiry-settings", web, body, 200)) {
		t.Fatal("inquiry configured/login contract differs")
	}
	exportNativeOfficial(t, "official-inquiry-save", inquiry)
	onboarding := nativeBulkRequest(t, app, "GET", "/user/arcade-inquiry-onboarding", session.AccessToken, nil, 200).(map[string]any)
	if !reflect.DeepEqual(onboarding, nativeBulkRequest(t, app, "GET", "/user/arcade-inquiry-onboarding", web, nil, 200)) {
		t.Fatal("onboarding Web/Native differs")
	}
	items := onboarding["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["arcadeId"] != id || items[0].(map[string]any)["arcadeName"] != "Synthetic official venue" {
		t.Fatal("onboarding leaked foreign or lost private owned name")
	}
	exportNativeOfficial(t, "official-onboarding", onboarding)
	// A creator without exact official ownership is not a settings manager.
	_, member := createAuthUser(t, app)
	nativeBulkRequest(t, app, "GET", "/arcade/report-settings?arcade="+foreign, session.AccessToken, nil, 403)
	for _, actor := range []*core.Record{creator, member} {
		_, s := nativeNearbySession(t, app, actor)
		out := nativeBulkRequest(t, app, "GET", "/user/arcade-inquiry-onboarding", s.AccessToken, nil, 200).(map[string]any)
		if len(out["items"].([]any)) != 0 {
			t.Fatal("nonofficial onboarding leaked private names")
		}
	}
	before := nativeCampaignState(t, app)
	for _, operation := range []string{"/arcade/report-settings", "/arcade/inquiry-settings"} {
		for _, invalid := range []map[string]any{{"arcade": id, "mode": "external", "contactType": "url", "contactValue": "https://user:secret@example.invalid"}, {"arcade": id, "mode": "external", "contactType": "phone", "contactValue": "bad"}, {"arcade": id, "mode": "internal", "guide": strings.Repeat("x", 2001)}} {
			nativeBulkRequest(t, app, "PUT", operation, session.AccessToken, invalid, 400)
		}
	}
	if !reflect.DeepEqual(before, nativeCampaignState(t, app)) {
		t.Fatal("invalid destination settings changed domain")
	}
	for _, collection := range []string{"arcade_changelog", "user_level_log"} {
		rows, err := app.FindAllRecords(collection)
		if err != nil || len(rows) != 0 {
			t.Fatal("operational settings wrote XP or content history", err)
		}
	}
	// Losing the explicit assignment affects both reads and writes immediately.
	owner.Set("owns", []string{})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	nativeBulkRequest(t, app, "GET", path, session.AccessToken, nil, 403)
	nativeBulkRequest(t, app, "PUT", "/arcade/report-settings", session.AccessToken, body, 403)
	out := nativeBulkRequest(t, app, "GET", "/user/arcade-inquiry-onboarding", session.AccessToken, nil, 200).(map[string]any)
	if len(out["items"].([]any)) != 0 {
		t.Fatal("onboarding retained removed assignment")
	}
}

func TestCanonicalNativeOfficialSettingsKeepRoleAndVisibilityMatrix(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, creator := createAuthUser(t, app)
	id, _ := seedArcade(t, app, creator.Id, arcadeSeed{Name: "Private settings", Address: "Synthetic", Location: location{Lat: 37.5, Lon: 127}})
	setArcadeVisibility(t, app, id, false, true)
	for _, rule := range []struct {
		role     string
		assigned bool
		status   int
	}{{"developer", false, 200}, {"moderator", false, 200}, {"arcade_owner", true, 200}, {"arcade_owner", false, 403}, {"supporter", true, 403}, {"", true, 403}} {
		web, user := createAuthUserWithTags(t, app, []string{rule.role})
		if rule.assigned {
			user.Set("owns", []string{id})
			if err := app.Save(user); err != nil {
				t.Fatal(err)
			}
		}
		_, session := nativeNearbySession(t, app, user)
		body := map[string]any{"arcade": id, "mode": "internal"}
		for _, token := range []string{web, session.AccessToken} {
			nativeBulkRequest(t, app, "GET", "/arcade/report-settings?arcade="+id, token, nil, rule.status)
			nativeBulkRequest(t, app, "PUT", "/arcade/report-settings", token, body, rule.status)
			nativeBulkRequest(t, app, "PUT", "/arcade/inquiry-settings", token, body, rule.status)
		}
		result := nativeBulkRequest(t, app, "GET", "/user/arcade-inquiry-onboarding", session.AccessToken, nil, 200).(map[string]any)
		want := 0
		if rule.role == "arcade_owner" && rule.assigned {
			want = 1
		}
		if len(result["items"].([]any)) != want {
			t.Fatal("onboarding did not require exact official ownership")
		}
		status := 403
		if rule.role == "arcade_owner" {
			status = 400
		}
		nativeBulkRequest(t, app, "PUT", "/user/arcade-account/credentials", session.AccessToken, map[string]any{}, status)
	}
}

func TestCanonicalNativeOfficialCredentialsRotateWebAndNativeKeysAtomically(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	password := syntheticOfficialPassword(t)
	owner.SetPassword(password)
	owner.SetVerified(true)
	owner.SetEmailVisibility(true)
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	web, err := owner.NewAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	service, session := nativeNearbySession(t, app, owner)
	_, other := createAuthUser(t, app)
	next := syntheticOfficialPassword(t)
	path := "/user/arcade-account/credentials"
	oldKey := owner.TokenKey()
	for _, input := range []struct {
		body   map[string]any
		status int
	}{{map[string]any{"currentPassword": strings.TrimSpace(password), "newPassword": next}, 401}, {map[string]any{"currentPassword": password, "email": other.Email(), "newPassword": next}, 409}, {map[string]any{"currentPassword": password, "newPassword": password}, 400}, {map[string]any{"currentPassword": password, "newPassword": "short"}, 400}} {
		nativeBulkRequest(t, app, "PUT", path, session.AccessToken, input.body, input.status)
		saved, err := app.FindRecordById("user", owner.Id)
		if err != nil || saved.TokenKey() != oldKey || !saved.ValidatePassword(password) {
			t.Fatal("failed credential update was not atomic")
		}
	}
	receipt := nativeBulkRequest(t, app, "PUT", path, session.AccessToken, map[string]any{"currentPassword": password, "newPassword": next, "email": " SYNTHETIC-OFFICIAL@example.invalid ", "tags": []string{"developer"}}, 200)
	if !reflect.DeepEqual(receipt, map[string]any{"success": true}) {
		t.Fatal("credential receipt contains extra values")
	}
	exportNativeOfficial(t, "official-credentials", receipt)
	saved, err := app.FindRecordById("user", owner.Id)
	if err != nil || saved.TokenKey() == oldKey || !saved.ValidatePassword(next) || saved.ValidatePassword(password) || saved.Verified() || saved.EmailVisibility() || saved.Email() != "synthetic-official@example.invalid" || !reflect.DeepEqual(saved.GetStringSlice("tags"), []string{"arcade_owner"}) {
		t.Fatal("credential normalization/rotation/role changed")
	}
	if _, err := service.Authenticate(app, session.AccessToken); err == nil {
		t.Fatal("credential rotation left Native access active")
	}
	if _, err := service.Refresh(app, session.RefreshToken); err == nil {
		t.Fatal("credential rotation left Native refresh active")
	}
	for _, token := range []string{web, session.AccessToken} {
		nativeBulkRequest(t, app, "GET", "/user/arcade-inquiry-onboarding", token, nil, 401)
	}
	// The same handler remains usable by a newly authenticated Web official account.
	freshWeb, err := saved.NewAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	webReceipt := nativeBulkRequest(t, app, "PUT", path, freshWeb, map[string]any{"currentPassword": next, "newPassword": syntheticOfficialPassword(t)}, 200)
	if !reflect.DeepEqual(receipt, webReceipt) {
		t.Fatal("Native/Web credential DTO differs")
	}
}

func TestCanonicalNativeOfficialSettingsRejectInvalidInactiveDisabledAndKeepLimits(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	service, session := nativeNearbySession(t, app, owner)
	for _, op := range nativeOfficialOps {
		for _, token := range []string{"", strings.Repeat("z", 43)} {
			nativeBulkRequest(t, app, op.method, op.path, token, map[string]any{}, 401)
		}
		r := executeJSONRequest(t, app, op.method, op.path, "{}", map[string]string{"Authorization": "Bearer " + strings.Repeat("z", 43), "Cookie": "pb_auth=" + web})
		r.Body.Close()
		if r.StatusCode != 401 {
			t.Fatal("invalid bearer fell back to cookie")
		}
	}
	for _, change := range []struct{ deny, restore string }{{"access_expires=0", "access_expires=unixepoch()+600"}, {"expires=0", "expires=unixepoch()+3600"}, {"client='removed-client'", "client='app.musecat.native.dev'"}} {
		for i, sql := range []string{change.deny, change.restore} {
			if _, err := app.DB().NewQuery("UPDATE native_auth_session SET " + sql + " WHERE user_id={:user}").Bind(dbx.Params{"user": owner.Id}).Execute(); err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				for _, op := range nativeOfficialOps {
					nativeBulkRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, 401)
				}
			}
		}
	}
	owner.Set("withdrawn", true)
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	for _, op := range nativeOfficialOps {
		nativeBulkRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, 401)
	}
	owner.Set("withdrawn", false)
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	seedUserBan(t, app, owner.Id, "", "native_official_fixture", time.Now().Add(time.Hour))
	for _, op := range nativeOfficialOps {
		nativeBulkRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, 403)
	}
	if err := service.Revoke(app, session.RefreshToken); err != nil {
		t.Fatal(err)
	}
	for _, op := range nativeOfficialOps {
		nativeBulkRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, 401)
	}
	disabled := nativeNearbyApp(t, false)
	disabledWeb, actor := createAuthUserWithTags(t, disabled, []string{"arcade_owner"})
	s, issued := nativeNearbySession(t, disabled, actor)
	if got, err := s.Authenticate(disabled, issued.AccessToken); err != nil || got.Id != actor.Id {
		t.Fatal("disabled same-DB fixture invalid")
	}
	for _, op := range nativeOfficialOps {
		r := executeJSONRequest(t, disabled, op.method, op.path, "{}", map[string]string{"Authorization": "Bearer " + issued.AccessToken})
		r.Body.Close()
		if r.StatusCode != 401 {
			t.Fatal("disabled adapter accepted credentials")
		}
	}
	nativeBulkRequest(t, disabled, "GET", "/user/arcade-inquiry-onboarding", disabledWeb, nil, 200)
}

func TestCanonicalNativeOfficialSettingsKeepBodyRateAndProtocolBoundaries(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	_, session := nativeNearbySession(t, app, owner)
	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.OnServe().Trigger(&core.ServeEvent{App: app, Router: router}, func(e *core.ServeEvent) error { return e.Next() }); err != nil {
		t.Fatal(err)
	}
	mux, err := router.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []struct {
		path  string
		limit int64
	}{{"/user/arcade-account/credentials", 16 << 10}, {"/arcade/report-settings", 64 << 10}, {"/arcade/inquiry-settings", 64 << 10}} {
		for _, extra := range []int64{0, 1} {
			req := httptest.NewRequest("PUT", op.path, strings.NewReader("{}"))
			req.ContentLength = op.limit + extra
			req.Header.Set("Authorization", "Bearer "+session.AccessToken)
			req.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			mux.ServeHTTP(res, req)
			want := 400
			if extra == 1 {
				want = 413
			}
			if res.Code != want {
				t.Fatal("existing official body limit changed")
			}
		}
	}
	for _, op := range nativeOfficialOps {
		initial := 400
		if op.path == "/user/arcade-inquiry-onboarding" {
			initial = 200
		} else if op.method == "GET" {
			initial = 404
		}
		app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: op.method + " " + op.path, MaxRequests: 1, Duration: 60}}}
		nativeBulkRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, initial)
		nativeBulkRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, 429)
		r := executeJSONRequest(t, app, op.method, "/native"+op.path, "{}", map[string]string{"Authorization": "Bearer " + session.AccessToken})
		r.Body.Close()
		if r.StatusCode != 404 {
			t.Fatal("official settings added a Native domain alias")
		}
	}
	for _, op := range []struct {
		method, path string
		status       int
	}{{"DELETE", "/user/notifications/device", 401}, {"PUT", "/arcade/report-subscription", 401}, {"POST", "/arcade/apns-subscription/status", 404}, {"GET", "/api/collections/user/records", 403}} {
		r := executeJSONRequest(t, app, op.method, op.path, "{}", map[string]string{"Authorization": "Bearer " + session.AccessToken})
		r.Body.Close()
		if r.StatusCode != op.status {
			t.Fatal("official settings widened protocol or raw access")
		}
	}
}
