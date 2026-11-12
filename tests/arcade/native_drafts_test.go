package arcade_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/geo"
	"github.com/ericbaek/musecat-backend-core/service/xp"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func nativeDraftApp(t *testing.T, enabled bool) *tests.TestApp {
	t.Helper()
	app := nativeNearbyApp(t, enabled)
	resolver, err := geo.LoadEmbeddedResolver()
	if err != nil {
		t.Fatal(err)
	}
	geo.SetOfflineResolver(resolver, true)
	return app
}
func nativeDraftRequest(t *testing.T, app *tests.TestApp, method, path, token string, body any, status int, selected ...bool) map[string]any {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	res := executeJSONRequest(t, app, method, path, string(raw), headers)
	if res.StatusCode != status {
		t.Fatalf("canonical draft %s %s status %d want %d", method, path, res.StatusCode, status)
	}
	if len(token) == 43 && (len(selected) == 0 || selected[0]) && !strings.Contains(res.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("Native draft response is cacheable")
	}
	return decodeJSONMap(t, res)
}
func nativeDraftInput() map[string]any {
	return map[string]any{"name": "Canonical synthetic draft", "address": "Synthetic test road", "location": map[string]any{"lat": 37.5665, "lon": 126.978}, "direction": "1F", "nickname": []string{"QA"}, "subway_line": []string{}}
}
func nativeDraftState(t *testing.T, app core.App, owner string) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, name := range []string{"arcade", "arcade_basic", "arcade_changelog", "arcade_visit", "user_level_log"} {
		n, err := app.CountRecords(name)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = int(n)
	}
	exp, err := xp.LoadCurrentExp(app, owner)
	if err != nil {
		t.Fatal(err)
	}
	out["xp"] = exp
	return out
}
func exportNativeDraftFixture(t *testing.T, name string, body map[string]any) {
	t.Helper()
	dir := os.Getenv("MUSECAT_CANONICAL_DRAFT_FIXTURES")
	if dir == "" {
		return
	}
	raw, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(raw))
	for _, key := range []string{"\"password\"", "\"access_token\"", "\"refresh_token\"", "\"email\""} {
		if strings.Contains(lower, key) {
			t.Fatal("unexpected credential field in domain fixture")
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestCanonicalNativeDraftLifecycleKeepsWebWireGeoAndPublicationXP(t *testing.T) {
	app := nativeDraftApp(t, true)
	web, owner := createAuthUser(t, app)
	_, session := nativeNearbySession(t, app, owner)
	input := nativeDraftInput()
	input["createdBy"] = "ignored-actor"
	created := nativeDraftRequest(t, app, "POST", "/arcade/new", session.AccessToken, input, 200)
	id := created["id"].(string)
	record, err := app.FindRecordById("arcade", id)
	if err != nil {
		t.Fatal(err)
	}
	if record.GetBool("public") || record.GetString("createdBy") != owner.Id || created["country"] != "KR" || created["timezone"] != "Asia/Seoul" {
		t.Fatal("creation changed creator/private/geo contract")
	}
	exportNativeDraftFixture(t, "draft-create", created)
	nativeRead := nativeDraftRequest(t, app, "GET", "/arcade/draft?id="+id+"&expand=all", session.AccessToken, nil, 200)
	webRead := nativeDraftRequest(t, app, "GET", "/arcade/draft?id="+id+"&expand=all", web, nil, 200)
	if !reflect.DeepEqual(nativeRead, webRead) {
		t.Fatal("Web cannot read Native-created draft identically")
	}
	version := seedGameSeriesVersion(t, app)
	batch := seedArcadeGameMolecule(t, app, id)
	seedArcadeGameAtom(t, app, batch, version, "1F")
	seedHourMolecule(t, app, id, owner.Id, map[string]any{"Monday": map[string]int{"start": 1000, "end": 2200}})
	before := nativeDraftState(t, app, owner.Id)
	proof := nativeDraftRequest(t, app, "POST", "/arcade/location-verification", session.AccessToken, map[string]any{"arcade": id, "lat": 37.5665, "lon": 126.978, "accuracy": 100}, 200)
	if proof["verified"] != true || proof["distance_meters"] != float64(0) || proof["accuracy_meters"] != float64(100) {
		t.Fatal("location proof changed distance/accuracy contract")
	}
	if !reflect.DeepEqual(before, nativeDraftState(t, app, owner.Id)) {
		t.Fatal("publication proof awarded visit/XP/history")
	}
	exportNativeDraftFixture(t, "draft-location", proof)
	preview := nativeDraftRequest(t, app, "GET", "/arcade/public?arcade="+id, session.AccessToken, nil, 200)
	webPreview := nativeDraftRequest(t, app, "GET", "/arcade/public?arcade="+id, web, nil, 200)
	if !reflect.DeepEqual(preview, webPreview) || preview["location_verified"] != true || len(preview["requirements"].([]any)) != 3 || !reflect.DeepEqual(before, nativeDraftState(t, app, owner.Id)) {
		t.Fatal("preview changed Web DTO or persisted state")
	}
	for _, raw := range preview["requirements"].([]any) {
		if raw.(map[string]any)["satisfied"] != true {
			t.Fatal("valid creator requirements unexpectedly unsatisfied")
		}
	}
	xpPreview := preview["xp_preview"].(map[string]any)
	if xpPreview["public_exp"] != float64(5) || xpPreview["backfill_exp"] != float64(2) || xpPreview["estimated_gain"] != float64(7) {
		t.Fatal("publication preview lost initial-basic backfill")
	}
	exportNativeDraftFixture(t, "draft-preview", preview)
	published := nativeDraftRequest(t, app, "PUT", "/arcade/public", session.AccessToken, map[string]any{"arcade": id}, 200)
	if published["public"] != true || published["xp_feedback"].(map[string]any)["diff_exp"] != float64(7) {
		t.Fatal("publication reward or receipt changed")
	}
	exportNativeDraftFixture(t, "draft-publish", published)
	after := nativeDraftState(t, app, owner.Id)
	nativeDraftRequest(t, app, "PUT", "/arcade/public", web, map[string]any{"arcade": id}, 400)
	nativeDraftRequest(t, app, "DELETE", "/arcade/draft?id="+id, session.AccessToken, nil, 409)
	if !reflect.DeepEqual(after, nativeDraftState(t, app, owner.Id)) {
		t.Fatal("repeat publication or public deletion wrote state")
	}
	disposable := nativeDraftRequest(t, app, "POST", "/arcade/new", web, nativeDraftInput(), 200)["id"].(string)
	deleted := nativeDraftRequest(t, app, "DELETE", "/arcade/draft?id="+disposable, session.AccessToken, nil, 200)
	if !reflect.DeepEqual(deleted, map[string]any{"id": disposable, "deleted": true}) {
		t.Fatal("deletion lost its confirmed owner receipt")
	}
	exportNativeDraftFixture(t, "draft-delete", deleted)
	nativeDraftRequest(t, app, "GET", "/arcade/draft?id="+disposable, web, nil, 404)
}
func TestCanonicalNativeDraftsKeepCreatorLevelClosedAndRevisionProofPolicy(t *testing.T) {
	app := nativeDraftApp(t, true)
	web, owner := createAuthUser(t, app)
	_, session := nativeNearbySession(t, app, owner)
	_, foreign := createAuthUser(t, app)
	foreign.Set("tags", []string{"developer", "supporter"})
	if err := app.Save(foreign); err != nil {
		t.Fatal(err)
	}
	setUserLevel(t, app, foreign.Id, 30)
	_, other := nativeNearbySession(t, app, foreign)
	id, _ := seedArcade(t, app, owner.Id, arcadeSeed{Name: "Private policy fixture", Address: "Synthetic road", Location: location{Lat: 37.5665, Lon: 126.978}})
	owner.Set("tags", []string{"developer", "supporter"})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	version := seedGameSeriesVersion(t, app)
	batch := seedArcadeGameMolecule(t, app, id)
	seedArcadeGameAtom(t, app, batch, version, "1F")
	body := map[string]any{"arcade": id}
	locationBody := map[string]any{"arcade": id, "lat": 37.5665, "lon": 126.978, "accuracy": 100}
	for _, token := range []string{web, session.AccessToken} {
		preview := nativeDraftRequest(t, app, "GET", "/arcade/public?arcade="+id, token, nil, 200)
		if len(preview["requirements"].([]any)) != 3 {
			t.Fatal("supporter/staff bypassed low-level requirements")
		}
		nativeDraftRequest(t, app, "PUT", "/arcade/public", token, body, 400)
	}
	for _, op := range []struct {
		method, path string
		body         any
	}{{"GET", "/arcade/public?arcade=" + id, nil}, {"PUT", "/arcade/public", body}, {"POST", "/arcade/location-verification", locationBody}, {"DELETE", "/arcade/draft?id=" + id, nil}} {
		nativeDraftRequest(t, app, op.method, op.path, other.AccessToken, op.body, 403)
	}
	before := nativeDraftState(t, app, owner.Id)
	bad := map[string]any{"arcade": id, "lat": 37.5665, "lon": 126.978, "accuracy": 101}
	nativeDraftRequest(t, app, "POST", "/arcade/location-verification", session.AccessToken, bad, 400)
	bad["accuracy"] = 5
	bad["lat"] = 37.57
	nativeDraftRequest(t, app, "POST", "/arcade/location-verification", session.AccessToken, bad, 403)
	if !reflect.DeepEqual(before, nativeDraftState(t, app, owner.Id)) {
		t.Fatal("invalid/outside proof wrote records or XP")
	}
	nativeDraftRequest(t, app, "POST", "/arcade/location-verification", session.AccessToken, locationBody, 200)
	freshBasic := seedBasicVersion(t, app, id, owner.Id, "New basic revision")
	record, err := app.FindRecordById("arcade", id)
	if err != nil {
		t.Fatal(err)
	}
	record.Set("basic", freshBasic)
	if err := app.Save(record); err != nil {
		t.Fatal(err)
	}
	preview := nativeDraftRequest(t, app, "GET", "/arcade/public?arcade="+id, session.AccessToken, nil, 200)
	if preview["location_verified"] != false {
		t.Fatal("old basic revision proof survived replacement")
	}
	setUserLevel(t, app, owner.Id, 5)
	preview = nativeDraftRequest(t, app, "GET", "/arcade/public?arcade="+id, session.AccessToken, nil, 200)
	if len(preview["requirements"].([]any)) != 2 {
		t.Fatal("level5 requirements changed")
	}
	setUserLevel(t, app, owner.Id, 10)
	preview = nativeDraftRequest(t, app, "GET", "/arcade/public?arcade="+id, session.AccessToken, nil, 200)
	if len(preview["requirements"].([]any)) != 1 {
		t.Fatal("level10 requirements changed")
	}
	record.Set("closed", true)
	if err := app.Save(record); err != nil {
		t.Fatal(err)
	}
	nativeDraftRequest(t, app, "GET", "/arcade/public?arcade="+id, session.AccessToken, nil, 400)
	nativeDraftRequest(t, app, "PUT", "/arcade/public", session.AccessToken, body, 400)
	nativeDraftRequest(t, app, "POST", "/arcade/location-verification", session.AccessToken, locationBody, 400)
}
func TestCanonicalNativeDraftActionsRejectInvalidInactiveAndDisabledSessions(t *testing.T) {
	app := nativeDraftApp(t, true)
	web, owner := createAuthUser(t, app)
	service, session := nativeNearbySession(t, app, owner)
	id, _ := seedArcade(t, app, owner.Id, arcadeSeed{Name: "Auth draft fixture", Address: "Synthetic road", Location: location{Lat: 37.5665, Lon: 126.978}})
	ops := []struct {
		method, path string
		body         any
	}{{"POST", "/arcade/new", nativeDraftInput()}, {"GET", "/arcade/public?arcade=" + id, nil}, {"PUT", "/arcade/public", map[string]any{"arcade": id}}, {"POST", "/arcade/location-verification", map[string]any{"arcade": id, "lat": 37.5665, "lon": 126.978, "accuracy": 5}}, {"DELETE", "/arcade/draft?id=" + id, nil}}
	reject := func(token string, status int) {
		t.Helper()
		for _, op := range ops {
			nativeDraftRequest(t, app, op.method, op.path, token, op.body, status)
		}
	}
	before := nativeDraftState(t, app, owner.Id)
	reject("", 401)
	reject(strings.Repeat("z", 43), 401)
	for _, op := range ops {
		raw, _ := json.Marshal(op.body)
		res := executeJSONRequest(t, app, op.method, op.path, string(raw), map[string]string{"Authorization": "Bearer " + strings.Repeat("z", 43), "Cookie": "pb_auth=" + web})
		res.Body.Close()
		if res.StatusCode != 401 {
			t.Fatal("invalid bearer fell back to cookie")
		}
	}
	for _, change := range []struct{ deny, restore string }{{"access_expires=0", "access_expires=unixepoch()+600"}, {"expires=0", "expires=unixepoch()+3600"}, {"client='removed-client'", "client='app.musecat.native.dev'"}} {
		for i, sql := range []string{change.deny, change.restore} {
			if _, err := app.DB().NewQuery("UPDATE native_auth_session SET " + sql + " WHERE user_id={:user}").Bind(dbx.Params{"user": owner.Id}).Execute(); err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				reject(session.AccessToken, 401)
			}
		}
	}
	owner.Set("withdrawn", true)
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	reject(session.AccessToken, 401)
	owner.Set("withdrawn", false)
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	seedUserBan(t, app, owner.Id, "", "native_draft_fixture", time.Now().Add(time.Hour))
	reject(session.AccessToken, 403)
	reject(web, 403)
	if err := service.Revoke(app, session.RefreshToken); err != nil {
		t.Fatal(err)
	}
	reject(session.AccessToken, 401)
	if !reflect.DeepEqual(before, nativeDraftState(t, app, owner.Id)) {
		t.Fatal("denied draft action wrote domain/XP/history")
	}
	disabled := nativeDraftApp(t, false)
	_, disabledOwner := createAuthUser(t, disabled)
	s, issued := nativeNearbySession(t, disabled, disabledOwner)
	if actor, err := s.Authenticate(disabled, issued.AccessToken); err != nil || actor.Id != disabledOwner.Id {
		t.Fatal("disabled fixture lacks valid same-DB session")
	}
	for _, op := range ops {
		nativeDraftRequest(t, disabled, op.method, op.path, issued.AccessToken, op.body, 401, false)
	}
}
func TestCanonicalNativeDraftActionsKeepRatesAndUnselectedPhotoScopes(t *testing.T) {
	app := nativeDraftApp(t, true)
	_, owner := createAuthUser(t, app)
	_, session := nativeNearbySession(t, app, owner)
	for _, op := range []struct{ method, path string }{{"POST", "/arcade/new"}, {"GET", "/arcade/public"}, {"PUT", "/arcade/public"}, {"POST", "/arcade/location-verification"}, {"DELETE", "/arcade/draft"}} {
		app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: op.method + " " + op.path, MaxRequests: 1, Duration: 60}}}
		nativeDraftRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, 400)
		nativeDraftRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, 429)
	}
	for _, op := range []struct {
		method, path string
		status       int
	}{{"GET", "/arcade/report-settings", 401}, {"POST", "/arcade/check-notes", 401}, {"GET", "/native/arcade/public", 404}, {"POST", "/native/arcade/new", 404}, {"PUT", "/arcade/report-settings", 401}} {
		nativeDraftRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, op.status, false)
	}
}
func TestCanonicalNativeDraftCreationGeoFailurePersistsNothing(t *testing.T) {
	app := nativeDraftApp(t, true)
	_, owner := createAuthUser(t, app)
	_, session := nativeNearbySession(t, app, owner)
	before := nativeDraftState(t, app, owner.Id)
	geo.SetOfflineResolver(nil, true)
	nativeDraftRequest(t, app, "POST", "/arcade/new", session.AccessToken, nativeDraftInput(), 503)
	if !reflect.DeepEqual(before, nativeDraftState(t, app, owner.Id)) {
		t.Fatal("geo failure left partial draft/basic/XP")
	}
}
