package arcade_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	pbtypes "github.com/pocketbase/pocketbase/tools/types"
)

func TestUpdateArcadeGameDelta_ModifyKeepsOtherActiveGames(t *testing.T) {
	app := newArcadeTestApp(t)
	token, user := createAuthUser(t, app)
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{Name: "Delta Arcade", Address: "Delta Street", Location: location{Lat: 37.5, Lon: 127}})
	versionA := seedGameSeriesVersion(t, app)
	versionB := seedGameSeriesVersion(t, app)
	headers := map[string]string{"Authorization": "Bearer " + token}

	initial := postGameDelta(t, app, headers, gameDeltaRequest(t, arcadeID, "", []map[string]any{
		testGameObject(versionA, "", "1F", 500, ""),
		testGameObject(versionB, "", "2F", 600, ""),
	}, nil, nil))
	stateID := gameStateID(t, initial)
	items := gameItems(t, initial)
	if len(items) != 2 {
		t.Fatalf("expected two active games after add, got %d", len(items))
	}
	entryA := gameEntryForVersion(t, items, versionA)
	entryB := gameEntryForVersion(t, items, versionB)

	modified := testGameObject(versionA, entryA, "3F", 900, "")
	updated := postGameDelta(t, app, headers, gameDeltaRequest(t, arcadeID, stateID, nil, []map[string]any{modified}, nil))
	updatedItems := gameItems(t, updated)
	if len(updatedItems) != 2 {
		t.Fatalf("single modify deleted another active game: %#v", updatedItems)
	}
	if got := gameEntryForVersion(t, updatedItems, versionB); got != entryB {
		t.Fatalf("expected untouched entry %q to remain active, got %q", entryB, got)
	}
	if got := gameEntryForVersion(t, updatedItems, versionA); got != entryA {
		t.Fatalf("expected modified entry identity %q, got %q", entryA, got)
	}
	if got := updated["count"]; got != float64(2) {
		t.Fatalf("expected active count 2, got %#v", got)
	}

	conflict := executeJSONRequest(t, app, http.MethodPut, "/arcade/game", gameDeltaRequest(t, arcadeID, stateID, nil, []map[string]any{testGameObject(versionA, entryA, "4F", 900, "")}, nil), headers)
	if conflict.StatusCode != http.StatusConflict {
		conflict.Body.Close()
		t.Fatalf("expected stale base_state_id to return 409, got %d", conflict.StatusCode)
	}
	conflict.Body.Close()
}

func TestUpdateArcadeGameDelta_MixesAddModifyRemove(t *testing.T) {
	app := newArcadeTestApp(t)
	token, user := createAuthUser(t, app)
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{Name: "Mixed Delta Arcade", Address: "Mixed Street", Location: location{Lat: 37.5, Lon: 127}})
	versionA := seedGameSeriesVersion(t, app)
	versionB := seedGameSeriesVersion(t, app)
	versionC := seedGameSeriesVersion(t, app)
	versionD := seedGameSeriesVersion(t, app)
	headers := map[string]string{"Authorization": "Bearer " + token}

	initial := postGameDelta(t, app, headers, gameDeltaRequest(t, arcadeID, "", []map[string]any{
		testGameObject(versionA, "", "1F", 500, ""),
		testGameObject(versionB, "", "2F", 600, ""),
		testGameObject(versionC, "", "3F", 700, ""),
	}, nil, nil))
	stateID := gameStateID(t, initial)
	items := gameItems(t, initial)
	entryA := gameEntryForVersion(t, items, versionA)
	entryB := gameEntryForVersion(t, items, versionB)
	entryC := gameEntryForVersion(t, items, versionC)

	updated := postGameDelta(t, app, headers, gameDeltaRequest(t, arcadeID, stateID,
		[]map[string]any{testGameObject(versionD, "", "4F", 800, "")},
		[]map[string]any{testGameObject(versionA, entryA, "A-Floor", 550, "")},
		[]string{entryB},
	))
	updatedItems := gameItems(t, updated)
	if len(updatedItems) != 3 {
		t.Fatalf("expected three active games after mixed delta, got %d", len(updatedItems))
	}
	if gameEntryExists(updatedItems, entryB) {
		t.Fatalf("removed entry %q remained active", entryB)
	}
	if !gameEntryExists(updatedItems, entryA) || !gameEntryExists(updatedItems, entryC) {
		t.Fatalf("modify/remove must preserve expected entries: %#v", updatedItems)
	}

	changes := loadChangelogRecords(t, app, arcadeID, "game")
	if len(changes) != 2 {
		t.Fatalf("expected initial and mixed game changelog rows, got %d", len(changes))
	}
	log := decodeLogObject(t, changes[0].Get("log"))
	itemsLog, ok := log["items"].([]any)
	if !ok || len(itemsLog) != 4 {
		t.Fatalf("expected four mixed changelog items, got %#v", log["items"])
	}
	seen := map[string]bool{}
	for _, raw := range itemsLog {
		item := raw.(map[string]any)
		seen[item["change_type"].(string)] = true
	}
	for _, kind := range []string{"added", "updated", "unchanged", "deleted"} {
		if !seen[kind] {
			t.Fatalf("expected changelog kind %q in %#v", kind, itemsLog)
		}
	}
}

func TestUpdateArcadeGameDelta_ReusesIdentityAndFlags(t *testing.T) {
	app := newArcadeTestApp(t)
	token, user := createAuthUser(t, app)
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{Name: "Identity Arcade", Address: "Identity Street", Location: location{Lat: 37.5, Lon: 127}})
	seriesID := seedGameSeries(t, app, 9001, "Identity Series")
	versionA := seedGameSeriesVersionWithSeries(t, app, seriesID, "", "Identity v1")
	versionB := seedGameSeriesVersionWithSeries(t, app, seriesID, "", "Identity v2")
	cabinetA := seedGameCabinet(t, app, "DX")
	cabinetB := seedGameCabinet(t, app, "SD")
	linkVersionCabinet(t, app, versionA, cabinetA)
	linkVersionCabinet(t, app, versionA, cabinetB)
	linkVersionCabinet(t, app, versionB, cabinetA)
	linkVersionCabinet(t, app, versionB, cabinetB)
	headers := map[string]string{"Authorization": "Bearer " + token}

	initial := postGameDelta(t, app, headers, gameDeltaRequest(t, arcadeID, "", []map[string]any{testGameObject(versionA, "", "1F", 500, cabinetA)}, nil, nil))
	stateID := gameStateID(t, initial)
	entryID := gameEntryForVersion(t, gameItems(t, initial), versionA)

	removed := postGameDelta(t, app, headers, gameDeltaRequest(t, arcadeID, stateID, nil, nil, []string{entryID}))
	stateID = gameStateID(t, removed)

	flagColl, err := app.FindCollectionByNameOrId("arcade_flag")
	if err != nil {
		t.Fatal(err)
	}
	flag := core.NewRecord(flagColl)
	flag.Set("arcade", arcadeID)
	flag.Set("disruption", "major")
	flag.Set("solved", false)
	flag.Set("message", "durable identity")
	flag.Set("game_id", entryID)
	flag.Set("createdBy", user.Id)
	if err := app.Save(flag); err != nil {
		t.Fatal(err)
	}

	readded := postGameDelta(t, app, headers, gameDeltaRequest(t, arcadeID, stateID, []map[string]any{testGameObject(versionB, "", "2F", 600, cabinetA)}, nil, nil))
	readdedItem := gameItemForVersion(t, gameItems(t, readded), versionB)
	if got := readdedItem["id"]; got != entryID {
		t.Fatalf("expected deleted entry identity %q to be reused, got %#v", entryID, got)
	}
	flags, ok := readdedItem["flags"].([]any)
	if !ok || len(flags) != 1 || flags[0].(map[string]any)["id"] != flag.Id {
		t.Fatalf("expected flag %q to follow reused identity, got %#v", flag.Id, readdedItem["flags"])
	}

	stateID = gameStateID(t, readded)
	removed = postGameDelta(t, app, headers, gameDeltaRequest(t, arcadeID, stateID, nil, nil, []string{entryID}))
	readdedDifferentCabinet := postGameDelta(t, app, headers, gameDeltaRequest(t, arcadeID, gameStateID(t, removed), []map[string]any{testGameObject(versionA, "", "3F", 700, cabinetB)}, nil, nil))
	if got := gameEntryForVersion(t, gameItems(t, readdedDifferentCabinet), versionA); got == entryID {
		t.Fatalf("different cabinet must create a new identity, reused %q", got)
	}
}

func TestUpdateArcadeGameDelta_DoesNotReuseUnverifiedCabinet(t *testing.T) {
	app := newArcadeTestApp(t)
	token, user := createAuthUser(t, app)
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{Name: "Unverified Arcade", Address: "Unverified Street", Location: location{Lat: 37.5, Lon: 127}})
	version := seedGameSeriesVersion(t, app)
	headers := map[string]string{"Authorization": "Bearer " + token}

	initial := postGameDelta(t, app, headers, gameDeltaRequest(t, arcadeID, "", []map[string]any{testGameObject(version, "", "1F", 500, "")}, nil, nil))
	entryID := gameEntryForVersion(t, gameItems(t, initial), version)
	removed := postGameDelta(t, app, headers, gameDeltaRequest(t, arcadeID, gameStateID(t, initial), nil, nil, []string{entryID}))
	readded := postGameDelta(t, app, headers, gameDeltaRequest(t, arcadeID, gameStateID(t, removed), []map[string]any{testGameObject(version, "", "2F", 600, "")}, nil, nil))
	if got := gameEntryForVersion(t, gameItems(t, readded), version); got == entryID {
		t.Fatalf("unverified cabinet must not reuse identity %q", got)
	}
}

func TestUpdateArcadeGameDelta_SelectsMostRecentInactiveHistory(t *testing.T) {
	app := newArcadeTestApp(t)
	token, user := createAuthUser(t, app)
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{Name: "Candidate Arcade", Address: "Candidate Street", Location: location{Lat: 37.5, Lon: 127}})
	seriesID := seedGameSeries(t, app, 9002, "Candidate Series")
	version := seedGameSeriesVersionWithSeries(t, app, seriesID, "", "Candidate Version")
	cabinet := seedGameCabinet(t, app, "DX Candidate")
	linkVersionCabinet(t, app, version, cabinet)
	older := seedInactiveGameCandidate(t, app, arcadeID, seriesID, version, cabinet, time.Now().UTC().Add(-2*time.Hour))
	newer := seedInactiveGameCandidate(t, app, arcadeID, seriesID, version, cabinet, time.Now().UTC().Add(-time.Hour))
	headers := map[string]string{"Authorization": "Bearer " + token}

	result := postGameDelta(t, app, headers, gameDeltaRequest(t, arcadeID, "", []map[string]any{testGameObject(version, "", "1F", 500, cabinet)}, nil, nil))
	if got := gameEntryForVersion(t, gameItems(t, result), version); got != newer {
		t.Fatalf("expected most recent inactive candidate %q, got %q (older %q)", newer, got, older)
	}
}

func TestUpdateArcadeGameDelta_AwardsCumulativeDistinctEntryXP(t *testing.T) {
	app := newArcadeTestApp(t)
	token, user := createAuthUser(t, app)
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{Name: "XP Arcade", Address: "XP Street", Location: location{Lat: 37.5, Lon: 127}})
	versionA := seedGameSeriesVersion(t, app)
	versionB := seedGameSeriesVersion(t, app)
	versionC := seedGameSeriesVersion(t, app)
	headers := map[string]string{"Authorization": "Bearer " + token}

	initial := postGameDelta(t, app, headers, gameDeltaRequest(t, arcadeID, "", []map[string]any{
		testGameObject(versionA, "", "1F", 500, ""),
		testGameObject(versionB, "", "2F", 600, ""),
	}, nil, nil))
	if got := xpDiff(t, initial); got != 0 {
		t.Fatalf("private arcade must not award game XP, got %d", got)
	}
	setArcadeVisibility(t, app, arcadeID, true, false)
	items := gameItems(t, initial)
	entryA := gameEntryForVersion(t, items, versionA)
	entryB := gameEntryForVersion(t, items, versionB)
	stateID := gameStateID(t, initial)

	first := postGameDelta(t, app, headers, gameDeltaRequest(t, arcadeID, stateID, nil, []map[string]any{
		testGameObject(versionA, entryA, "3F", 550, ""),
		testGameObject(versionB, entryB, "4F", 650, ""),
	}, nil))
	if got := xpDiff(t, first); got != 5 {
		t.Fatalf("two changed entries should award 5 XP, got %d", got)
	}

	second := postGameDelta(t, app, headers, gameDeltaRequest(t, arcadeID, gameStateID(t, first), []map[string]any{
		testGameObject(versionC, "", "5F", 700, ""),
	}, nil, nil))
	if got := xpDiff(t, second); got != 2 {
		t.Fatalf("one new entry after two prior entries should award the incremental 2 XP, got %d", got)
	}

	third := postGameDelta(t, app, headers, gameDeltaRequest(t, arcadeID, gameStateID(t, second), nil, []map[string]any{testGameObject(versionA, entryA, "6F", 700, "")}, nil))
	if got := xpDiff(t, third); got != 0 {
		t.Fatalf("revisiting an entry already counted in the window should award 0 XP, got %d", got)
	}

	allLogs, err := app.FindRecordsByFilter("user_level_log", "user={:user}", "", 0, 0, dbx.Params{"user": user.Id})
	if err != nil {
		t.Fatal(err)
	}
	if len(allLogs) != 2 {
		t.Fatalf("expected two cumulative game XP logs, got %d", len(allLogs))
	}
	for _, log := range allLogs {
		if _, err := app.NonconcurrentDB().NewQuery("UPDATE user_level_log SET created={:created} WHERE id={:id}").Bind(dbx.Params{
			"created": time.Now().UTC().Add(-8 * 24 * time.Hour).Format(pbtypes.DefaultDateLayout),
			"id":      log.Id,
		}).Execute(); err != nil {
			t.Fatal(err)
		}
	}

	fourth := postGameDelta(t, app, headers, gameDeltaRequest(t, arcadeID, gameStateID(t, third), nil, []map[string]any{testGameObject(versionB, entryB, "7F", 750, "")}, nil))
	if got := xpDiff(t, fourth); got != 3 {
		t.Fatalf("an entry should be eligible again after the seven-day window, got %d", got)
	}
}

func testGameObject(version, entry, location string, value int, cabinet string) map[string]any {
	game := map[string]any{
		"game":     version,
		"location": location,
		"quantity": 1,
		"price": map[string]any{
			"currency": "KRW",
			"type":     "custom",
			"list":     []map[string]any{{"value": value}},
			"accept":   []string{},
		},
		"tag": []map[string]any{{"category": "기타", "note": "test"}},
	}
	if entry != "" {
		game["id"] = entry
	}
	if cabinet != "" {
		game["cabinet"] = cabinet
	}
	return game
}

func gameDeltaRequest(t testing.TB, arcadeID, stateID string, add, modify []map[string]any, remove []string) string {
	t.Helper()
	payload := map[string]any{
		"arcade":        arcadeID,
		"base_state_id": stateID,
		"add":           addOrEmpty(add),
		"modify":        addOrEmpty(modify),
		"remove":        removeOrEmpty(remove),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to encode delta: %v", err)
	}
	return string(encoded)
}

func addOrEmpty(value []map[string]any) []map[string]any {
	if value == nil {
		return []map[string]any{}
	}
	return value
}

func removeOrEmpty(value []string) []string {
	if value == nil {
		return []string{}
	}
	return value
}

func postGameDelta(t testing.TB, app *tests.TestApp, headers map[string]string, body string) map[string]any {
	t.Helper()
	res := executeJSONRequest(t, app, http.MethodPut, "/arcade/game", body, headers)
	if res.StatusCode != http.StatusOK {
		payload := decodeJSONMap(t, res)
		t.Fatalf("expected game delta status 200, got %d: %#v", res.StatusCode, payload)
	}
	return decodeJSONMap(t, res)
}

func gameStateID(t testing.TB, payload map[string]any) string {
	t.Helper()
	game, ok := payload["game"].(map[string]any)
	if !ok {
		t.Fatalf("expected game object, got %#v", payload["game"])
	}
	id, ok := game["id"].(string)
	if !ok || id == "" {
		t.Fatalf("expected game state id, got %#v", game["id"])
	}
	return id
}

func gameItems(t testing.TB, payload map[string]any) []map[string]any {
	t.Helper()
	game, ok := payload["game"].(map[string]any)
	if !ok {
		t.Fatalf("expected game object, got %#v", payload["game"])
	}
	return mapSliceFromAny(t, game["items"])
}

func gameEntryForVersion(t testing.TB, items []map[string]any, version string) string {
	t.Helper()
	return gameItemForVersion(t, items, version)["id"].(string)
}

func gameItemForVersion(t testing.TB, items []map[string]any, version string) map[string]any {
	t.Helper()
	for _, item := range items {
		versionObj, _ := item["version"].(map[string]any)
		if versionObj["id"] == version {
			return item
		}
	}
	t.Fatalf("version %q not found in items %#v", version, items)
	return nil
}

func gameEntryExists(items []map[string]any, entryID string) bool {
	for _, item := range items {
		if item["id"] == entryID {
			return true
		}
	}
	return false
}

func xpDiff(t testing.TB, payload map[string]any) int {
	t.Helper()
	feedback, ok := payload["xp_feedback"].(map[string]any)
	if !ok {
		t.Fatalf("expected xp_feedback, got %#v", payload["xp_feedback"])
	}
	return int(feedback["diff_exp"].(float64))
}

func seedInactiveGameCandidate(t testing.TB, app *tests.TestApp, arcadeID, seriesID, versionID, cabinetID string, created time.Time) string {
	t.Helper()
	entryColl, err := app.FindCollectionByNameOrId("arcade_game_id")
	if err != nil {
		t.Fatal(err)
	}
	entry := core.NewRecord(entryColl)
	entry.Set("arcade", arcadeID)
	entry.Set("series", seriesID)
	if err := app.Save(entry); err != nil {
		t.Fatal(err)
	}

	batchColl, err := app.FindCollectionByNameOrId("arcade_game_history_batch")
	if err != nil {
		t.Fatal(err)
	}
	batch := core.NewRecord(batchColl)
	batch.Set("arcade", arcadeID)
	batch.Set("reason", "candidate fixture")
	if err := app.Save(batch); err != nil {
		t.Fatal(err)
	}

	revisionColl, err := app.FindCollectionByNameOrId("arcade_game_history")
	if err != nil {
		t.Fatal(err)
	}
	revision := core.NewRecord(revisionColl)
	revision.Set("batch", batch.Id)
	revision.Set("entry", entry.Id)
	revision.Set("version", versionID)
	revision.Set("cabinet", cabinetID)
	revision.Set("location", "fixture")
	revision.Set("quantity", 1)
	revision.Set("price", map[string]any{"currency": "KRW", "type": "custom", "list": []map[string]any{{"value": 500}}, "accept": []string{}})
	revision.Set("tag", []map[string]any{{"category": "기타", "note": "fixture"}})
	if err := app.Save(revision); err != nil {
		t.Fatal(err)
	}
	if _, err := app.NonconcurrentDB().NewQuery("UPDATE arcade_game_history SET created={:created} WHERE id={:id}").Bind(dbx.Params{
		"created": created.UTC().Format(pbtypes.DefaultDateLayout),
		"id":      revision.Id,
	}).Execute(); err != nil {
		t.Fatal(err)
	}
	return entry.Id
}
