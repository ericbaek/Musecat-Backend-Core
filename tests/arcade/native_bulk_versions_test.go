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

func nativeBulkRequest(t *testing.T, app *tests.TestApp, method, path, token string, body any, status int) any {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	response := executeJSONRequest(t, app, method, path, string(raw), headers)
	defer response.Body.Close()
	if response.StatusCode != status {
		t.Fatalf("%s %s status %d want %d", method, path, response.StatusCode, status)
	}
	if len(token) == 43 && !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("selected Native bulk response is cacheable")
	}
	var value any
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func exportNativeBulkFixture(t *testing.T, name string, value any) {
	t.Helper()
	dir := os.Getenv("MUSECAT_CANONICAL_BULK_FIXTURES")
	if dir == "" {
		return
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"\"password\"", "\"access_token\"", "\"refresh_token\"", "\"email\""} {
		if strings.Contains(strings.ToLower(string(raw)), key) {
			t.Fatal("credential field in domain fixture")
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func nativeBulkState(t *testing.T, app core.App) map[string]string {
	t.Helper()
	state := map[string]string{}
	for _, collection := range []string{"arcade", "arcade_game_id", "arcade_game_history", "arcade_game_history_batch", "arcade_changelog", "user_level", "user_level_log"} {
		rows, err := app.FindAllRecords(collection)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(rows)
		if err != nil {
			t.Fatal(err)
		}
		state[collection] = string(raw)
	}
	return state
}

func TestCanonicalNativeBulkVersionsShareWebRowsAndPreserveImmutableMachineFields(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, actor := createAuthUserWithTags(t, app, []string{"moderator"})
	_, session := nativeNearbySession(t, app, actor)
	series := seedGameSeries(t, app, 501, "Canonical bulk series")
	from := seedGameSeriesVersionWithSeries(t, app, series, "2025-01-01", "Before")
	to := seedGameSeriesVersionWithSeries(t, app, series, "2026-01-01", "After")
	var entries, arcades []string
	original := map[string][]byte{}
	for _, name := range []string{"Canonical bulk A", "Canonical bulk B"} {
		arcade, _ := seedPublicArcade(t, app, actor.Id, arcadeSeed{Name: name, Address: "Synthetic public road", Location: location{Lat: 37.5, Lon: 127}})
		ids, state := seedBulkHistoryState(t, app, arcade, actor.Id, from)
		entries = append(entries, ids[0])
		arcades = append(arcades, arcade)
		rows, err := app.FindRecordsByFilter("arcade_game_history", "batch={:batch}", "", 0, 0, dbx.Params{"batch": state})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			original[row.Id], err = json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	private, _ := seedArcade(t, app, actor.Id, arcadeSeed{Name: "Private bulk fixture", Address: "Synthetic private road", Location: location{Lat: 37.5, Lon: 127}})
	seedBulkHistoryState(t, app, private, actor.Id, from)
	path := "/arcade/games?country=KR&game_series=" + series + "&game_series_version=" + from
	nativeRows := nativeBulkRequest(t, app, "GET", path, session.AccessToken, nil, 200)
	webRows := nativeBulkRequest(t, app, "GET", path, web, nil, 200)
	if !reflect.DeepEqual(nativeRows, webRows) || len(nativeRows.([]any)) != 2 {
		t.Fatal("Native bulk read changed filters, private visibility or Web DTO")
	}
	exportNativeBulkFixture(t, "bulk-targets", nativeRows)
	body := map[string]any{"game_ids": entries, "current_game_version_series": from, "new_game_version_series": to, "by": "ignored-client-actor"}
	receipt := nativeBulkRequest(t, app, "POST", "/arcade/game/bulk_version", session.AccessToken, body, 200)
	if !reflect.DeepEqual(receipt, map[string]any{"from": from, "to": to, "count": float64(2), "changed": "game"}) {
		t.Fatal("bulk version receipt changed")
	}
	exportNativeBulkFixture(t, "bulk-update", receipt)
	for _, arcade := range arcades {
		record, err := app.FindRecordById("arcade", arcade)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := app.FindRecordsByFilter("arcade_game_history", "batch={:batch}", "", 0, 0, dbx.Params{"batch": record.GetString("game_v2")})
		if err != nil || len(rows) != 1 {
			t.Fatal("missing new immutable game revision", err)
		}
		if rows[0].GetString("version") != to || rows[0].GetString("location") != "1F" || rows[0].GetInt("quantity") != 1 {
			t.Fatal("bulk update replaced unselected machine fields")
		}
		changes := loadChangelogRecords(t, app, arcade, "game")
		found := false
		for _, change := range changes {
			if decodeLogObject(t, change.Get("log"))["source"] == "bulk_version" {
				found = true
				if change.GetString("by") != actor.Id {
					t.Fatal("bulk update trusted a supplied actor")
				}
			}
		}
		if !found {
			t.Fatal("bulk update did not create its canonical timeline event")
		}
	}
	for id, before := range original {
		row, err := app.FindRecordById("arcade_game_history", id)
		if err != nil {
			t.Fatal(err)
		}
		after, err := json.Marshal(row)
		if err != nil || string(before) != string(after) {
			t.Fatal("bulk update changed historical revisions", err)
		}
	}
	before := nativeBulkState(t, app)
	nativeBulkRequest(t, app, "POST", "/arcade/game/bulk_version", session.AccessToken, body, 400)
	if !reflect.DeepEqual(before, nativeBulkState(t, app)) {
		t.Fatal("stale version failure changed history or XP")
	}
	body["current_game_version_series"] = to
	body["new_game_version_series"] = from
	nativeBulkRequest(t, app, "POST", "/arcade/game/bulk_version", web, body, 200)
	stableRows := func(value any) []map[string]any {
		var result []map[string]any
		for _, raw := range value.([]any) {
			row := map[string]any{}
			for key, item := range raw.(map[string]any) {
				if key != "game" && key != "updated" {
					row[key] = item
				}
			}
			result = append(result, row)
		}
		return result
	}
	// Only revision metadata may change: compare all remaining fields and stable IDs.
	afterWeb := nativeBulkRequest(t, app, "GET", path, session.AccessToken, nil, 200)
	if !reflect.DeepEqual(stableRows(nativeRows), stableRows(afterWeb)) {
		t.Fatal("Web reversal changed machine fields or stable identity")
	}

}

func TestCanonicalNativeBulkVersionsKeepSeparateReadAndWriteRoleGates(t *testing.T) {
	app := nativeNearbyApp(t, true)
	for _, rule := range []struct {
		role               string
		level, read, write int
	}{{"developer", 0, 200, 400}, {"moderator", 0, 200, 400}, {"supporter", 29, 403, 403}, {"supporter", 30, 200, 403}, {"founding_supporter", 30, 200, 403}, {"", 30, 403, 403}, {"arcade_owner", 30, 403, 403}} {
		web, user := createAuthUserWithTags(t, app, []string{rule.role})
		setUserLevel(t, app, user.Id, rule.level)
		_, session := nativeNearbySession(t, app, user)
		for _, token := range []string{web, session.AccessToken} {
			nativeBulkRequest(t, app, "GET", "/arcade/games", token, nil, rule.read)
			nativeBulkRequest(t, app, "POST", "/arcade/game/bulk_version", token, map[string]any{}, rule.write)
		}
	}
}

func TestCanonicalNativeBulkVersionsRejectInvalidInactiveAndDisabledCredentials(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, user := createAuthUserWithTags(t, app, []string{"developer"})
	service, session := nativeNearbySession(t, app, user)
	before := nativeBulkState(t, app)
	ops := []struct{ method, path string }{{"GET", "/arcade/games"}, {"POST", "/arcade/game/bulk_version"}}
	for _, op := range ops {
		for _, token := range []string{"", strings.Repeat("z", 43)} {
			nativeBulkRequest(t, app, op.method, op.path, token, map[string]any{}, 401)
		}
		response := executeJSONRequest(t, app, op.method, op.path, "{}", map[string]string{"Authorization": "Bearer " + strings.Repeat("z", 43), "Cookie": "pb_auth=" + web})
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal("invalid Native bearer fell back to a cookie")
		}
	}
	seedUserBan(t, app, user.Id, "", "native_bulk_fixture", time.Now().Add(time.Hour))
	for _, op := range ops {
		nativeBulkRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, 403)
	}
	if err := service.Revoke(app, session.RefreshToken); err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		nativeBulkRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, 401)
	}
	if !reflect.DeepEqual(before, nativeBulkState(t, app)) {
		t.Fatal("denied bulk request changed domain state")
	}
	disabled := nativeNearbyApp(t, false)
	_, actor := createAuthUserWithTags(t, disabled, []string{"developer"})
	disabledService, issued := nativeNearbySession(t, disabled, actor)
	if got, err := disabledService.Authenticate(disabled, issued.AccessToken); err != nil || got.Id != actor.Id {
		t.Fatal("invalid same-database disabled fixture")
	}
	for _, op := range ops {
		response := executeJSONRequest(t, disabled, op.method, op.path, "{}", map[string]string{"Authorization": "Bearer " + issued.AccessToken})
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal("disabled adapter accepted Native bulk credentials")
		}
	}
}

func TestCanonicalNativeBulkVersionsKeepRateLimitsAndDoNotAddAliases(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, actor := createAuthUserWithTags(t, app, []string{"developer"})
	_, session := nativeNearbySession(t, app, actor)
	for _, op := range []struct {
		method, path string
		initial      int
	}{{"GET", "/arcade/games", 200}, {"POST", "/arcade/game/bulk_version", 400}} {
		app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: op.method + " " + op.path, MaxRequests: 1, Duration: 60}}}
		nativeBulkRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, op.initial)
		nativeBulkRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, 429)
	}
	for _, op := range []struct{ method, path string }{{"GET", "/native/arcade/games"}, {"POST", "/native/arcade/game/bulk_version"}} {
		response := executeJSONRequest(t, app, op.method, op.path, "{}", map[string]string{"Authorization": "Bearer " + session.AccessToken})
		response.Body.Close()
		if response.StatusCode != 404 {
			t.Fatal("bulk adapter added a Native domain alias")
		}
	}
}
