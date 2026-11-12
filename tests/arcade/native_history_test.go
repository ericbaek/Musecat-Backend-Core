package arcade_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func nativeHistoryRequest(t *testing.T, app *tests.TestApp, path, token string, body any, status int) map[string]any {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	response := executeJSONRequest(t, app, "POST", path, string(raw), headers)
	if response.StatusCode != status {
		t.Fatalf("%s status %d want %d", path, response.StatusCode, status)
	}
	if len(token) == 43 && !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("Native history response is cacheable")
	}
	return decodeJSONMap(t, response)
}

func exportNativeHistoryFixture(t *testing.T, name string, payload map[string]any) {
	t.Helper()
	dir := os.Getenv("MUSECAT_CANONICAL_HISTORY_FIXTURES")
	if dir == "" {
		return
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalNativeHistoryActionsKeepWebTargetsReportsAndOwner(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, owner := createAuthUser(t, app)
	_, session := nativeNearbySession(t, app, owner)
	_, editor := createAuthUser(t, app)
	arcade, target := seedPublicArcade(t, app, owner.Id, arcadeSeed{Name: "Canonical history", Address: "Public test road", Location: location{Lat: 37.5665, Lon: 126.978}})
	current := seedBasicVersion(t, app, arcade, editor.Id, "Current history version")
	record, err := app.FindRecordById("arcade", arcade)
	if err != nil {
		t.Fatal(err)
	}
	record.Set("basic", current)
	if err := app.Save(record); err != nil {
		t.Fatal(err)
	}
	change := seedArcadeChangelog(t, app, arcade, "basic", editor.Id, time.Now())
	rollback := nativeHistoryRequest(t, app, "/arcade/rollback", session.AccessToken, map[string]any{"arcade": arcade, "part": "basic", "value": target, "previous_value": "not-a-cas", "user": editor.Id}, 200)
	if rollback["from"] != current || rollback["to"] != target || rollback["reported"] != false || rollback["request_admin_sent"] != false {
		t.Fatal("rollback changed historical target or invented CAS")
	}
	exportNativeHistoryFixture(t, "history-rollback", rollback)
	for _, row := range loadChangelogRecords(t, app, arcade, "basic") {
		if row.GetString("from") == current && row.GetString("to") == target && row.GetString("by") != owner.Id {
			t.Fatal("rollback trusted a supplied actor")
		}
	}
	message := strings.Repeat("😀", 1200)
	reportBody := map[string]any{"arcade": arcade, "changelog": change, "message": message, "createdBy": editor.Id, "reported_editor": owner.Id}
	report := nativeHistoryRequest(t, app, "/arcade/edit_report", session.AccessToken, reportBody, 200)
	if report["createdBy"] != owner.Id || report["reported_editor"] != editor.Id || report["message"] != message || report["kind"] != "edit_report" {
		t.Fatal("report lost canonical actor, editor or Unicode limit")
	}
	exportNativeHistoryFixture(t, "history-edit-report", report)
	nativeHistoryRequest(t, app, "/arcade/edit_report", web, reportBody, 409)
	nativeHistoryRequest(t, app, "/arcade/edit_report", session.AccessToken, reportBody, 409)
	before := nativeHistoryState(t, app, arcade)
	rollbackReport := map[string]any{"arcade": arcade, "part": "basic", "value": current, "report": true, "changelog": change, "report_message": "Restore and report"}
	nativeHistoryRequest(t, app, "/arcade/rollback", session.AccessToken, rollbackReport, 409)
	if !reflect.DeepEqual(before, nativeHistoryState(t, app, arcade)) {
		t.Fatal("report conflict committed a partial rollback")
	}
	rollbackReport["changelog"] = seedArcadeChangelog(t, app, arcade, "basic", editor.Id, time.Now())
	combined := nativeHistoryRequest(t, app, "/arcade/rollback", session.AccessToken, rollbackReport, 200)
	if combined["reported"] != true || combined["request_admin_sent"] != true || combined["to"] != current {
		t.Fatal("combined rollback lost its report transaction")
	}
	exportNativeHistoryFixture(t, "history-rollback-report", combined)
	reportBody["message"] = message + "😀"
	nativeHistoryRequest(t, app, "/arcade/edit_report", session.AccessToken, reportBody, 400)
	request := nativeHistoryRequest(t, app, "/arcade/request_admin", session.AccessToken, map[string]any{"arcade": arcade, "urgency": " high ", "message": " Canonical request ", "createdBy": editor.Id}, 200)
	if request["createdBy"] != owner.Id || request["message"] != "Canonical request" || request["urgency"] != "high" || request["status"] != "waiting" || request["kind"] != "general" {
		t.Fatal("general request lost canonical normalization or owner")
	}
	exportNativeHistoryFixture(t, "history-admin-request", request)
	list := decodeJSONMap(t, executeJSONRequest(t, app, "GET", "/arcade/request_admin?arcade="+arcade, "", map[string]string{"Authorization": "Bearer " + web}))
	if !contractItemsContainID(list["items"], request["id"].(string)) || !contractItemsContainID(list["items"], report["id"].(string)) {
		t.Fatal("Web owner cannot read Native-created requests")
	}
}

func TestCanonicalNativeHistoryDenialsDoNotWriteOrWidenRoutes(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, owner := createAuthUser(t, app)
	service, session := nativeNearbySession(t, app, owner)
	_, foreign := createAuthUser(t, app)
	_, foreignSession := nativeNearbySession(t, app, foreign)
	arcade, target := seedArcade(t, app, owner.Id, arcadeSeed{Name: "Private history", Address: "Private test road", Location: location{Lat: 37.5665, Lon: 126.978}})
	change := seedArcadeChangelog(t, app, arcade, "basic", owner.Id, time.Now())
	ops := []struct {
		path string
		body map[string]any
	}{
		{"/arcade/rollback", map[string]any{"arcade": arcade, "part": "basic", "value": target}},
		{"/arcade/edit_report", map[string]any{"arcade": arcade, "changelog": change, "message": "Private report"}},
		{"/arcade/request_admin", map[string]any{"message": "General request"}},
	}
	before := nativeHistoryState(t, app, arcade)
	for _, op := range ops {
		nativeHistoryRequest(t, app, op.path, "", op.body, 401)
		nativeHistoryRequest(t, app, op.path, strings.Repeat("z", 43), op.body, 401)
		raw, _ := json.Marshal(op.body)
		response := executeJSONRequest(t, app, "POST", op.path, string(raw), map[string]string{"Authorization": "Bearer " + strings.Repeat("z", 43), "Cookie": "pb_auth=" + web})
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal("invalid bearer fell back to Web cookie")
		}
	}
	nativeHistoryRequest(t, app, ops[0].path, foreignSession.AccessToken, ops[0].body, 403)
	nativeHistoryRequest(t, app, ops[1].path, foreignSession.AccessToken, ops[1].body, 404)
	seedUserBan(t, app, owner.Id, "", "native_history_fixture", time.Now().Add(time.Hour))
	for _, op := range ops {
		nativeHistoryRequest(t, app, op.path, session.AccessToken, op.body, 403)
	}
	if err := service.Revoke(app, session.RefreshToken); err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		nativeHistoryRequest(t, app, op.path, session.AccessToken, op.body, 401)
	}
	if !reflect.DeepEqual(before, nativeHistoryState(t, app, arcade)) {
		t.Fatal("denied action changed domain, history or XP")
	}
	disabled := nativeNearbyApp(t, false)
	_, disabledOwner := createAuthUser(t, disabled)
	disabledService, issued := nativeNearbySession(t, disabled, disabledOwner)
	if actor, err := disabledService.Authenticate(disabled, issued.AccessToken); err != nil || actor.Id != disabledOwner.Id {
		t.Fatal("invalid disabled-session fixture")
	}
	for _, op := range ops {
		raw, _ := json.Marshal(op.body)
		response := executeJSONRequest(t, disabled, "POST", op.path, string(raw), map[string]string{"Authorization": "Bearer " + issued.AccessToken})
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal("disabled adapter granted history mutation")
		}
	}
	for _, path := range []string{"/native/arcade/rollback", "/native/arcade/edit_report", "/native/arcade/request_admin"} {
		response := executeJSONRequest(t, app, "POST", path, "{}", map[string]string{"Authorization": "Bearer " + foreignSession.AccessToken})
		response.Body.Close()
		if response.StatusCode != 404 {
			t.Fatal("history bindings widened an unselected route")
		}
	}
}

func TestCanonicalNativeHistoryKeepsExistingRateLimits(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, owner := createAuthUser(t, app)
	_, session := nativeNearbySession(t, app, owner)
	for _, path := range []string{"/arcade/rollback", "/arcade/edit_report", "/arcade/request_admin"} {
		app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: "POST " + path, MaxRequests: 1, Duration: 60}}}
		nativeHistoryRequest(t, app, path, session.AccessToken, map[string]any{}, 400)
		nativeHistoryRequest(t, app, path, session.AccessToken, map[string]any{}, 429)
	}
}

func nativeHistoryState(t *testing.T, app core.App, arcade string) map[string]any {
	t.Helper()
	record, err := app.FindRecordById("arcade", arcade)
	if err != nil {
		t.Fatal(err)
	}
	state := map[string]any{"basic": record.GetString("basic")}
	for _, collection := range []string{"arcade_request_admin", "arcade_changelog", "user_level", "user_level_log"} {
		rows, err := app.FindAllRecords(collection)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(rows)
		if err != nil {
			t.Fatal(err)
		}
		state[collection] = string(data)
	}
	return state
}
