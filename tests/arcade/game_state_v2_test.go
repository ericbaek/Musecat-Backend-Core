package arcade_test

import (
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	arcadegame "github.com/ericbaek/musecat-backend-core/handlers/arcade/game"
)

func seedStateTestVersion(tb testing.TB, app core.App) string {
	tb.Helper()
	seriesColl, err := app.FindCollectionByNameOrId("game_series")
	if err != nil {
		tb.Fatal(err)
	}
	series := core.NewRecord(seriesColl)
	series.Set("en", "State Test Series")
	if err := app.Save(series); err != nil {
		tb.Fatal(err)
	}
	versionColl, err := app.FindCollectionByNameOrId("game_series_version")
	if err != nil {
		tb.Fatal(err)
	}
	version := core.NewRecord(versionColl)
	version.Set("series", series.Id)
	version.Set("en", "State Test Version")
	if err := app.Save(version); err != nil {
		tb.Fatal(err)
	}
	return version.Id
}

func stateTestGame(version, entry, location string) arcadegame.GameAtomInput {
	price := float32(500)
	return arcadegame.GameAtomInput{ID: entry, Game: version, Location: location, Quantity: 1, Price: arcadegame.Price{Currency: "KRW", Type: "custom", List: []arcadegame.PriceItem{{Value: &price}}, Accept: []string{}}, Tag: []arcadegame.TagItem{}}
}

func TestGameStateV2_PointerChangelogAndConflict(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUser(t, app)
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{Name: "State Test", Address: "State Street", Location: location{Lat: 37.5, Lon: 127.0}})
	versionID := seedStateTestVersion(t, app)

	var state1 string
	if err := app.RunInTransaction(func(tx core.App) error {
		var err error
		state1, err = arcadegame.UpdateArcadeGameTx(tx, arcadegame.UpdateArcadeGameBody{Arcade: arcadeID, BaseStateID: "", Games: []arcadegame.GameAtomInput{stateTestGame(versionID, "", "1F")}}, user.Id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	revisions1, err := app.FindRecordsByFilter("arcade_game_revision", "batch={:batch}", "", 0, 0, map[string]any{"batch": state1})
	if err != nil || len(revisions1) != 1 {
		t.Fatalf("expected one first revision, err=%v count=%d", err, len(revisions1))
	}
	entryID := revisions1[0].GetString("entry")

	var state2 string
	if err := app.RunInTransaction(func(tx core.App) error {
		var err error
		state2, err = arcadegame.UpdateArcadeGameTx(tx, arcadegame.UpdateArcadeGameBody{Arcade: arcadeID, BaseStateID: state1, Games: []arcadegame.GameAtomInput{stateTestGame(versionID, entryID, "2F")}}, user.Id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if state1 == state2 {
		t.Fatal("expected a replacement state batch")
	}
	if got := revisions1[0].GetString("location"); got != "1F" {
		t.Fatalf("historical revision was mutated: %q", got)
	}
	arcade, err := app.FindRecordById("arcade", arcadeID)
	if err != nil || arcade.GetString("game_state") != state2 {
		t.Fatalf("arcade pointer was not moved: %v", err)
	}
	changes := loadChangelogRecords(t, app, arcadeID, "game")
	if len(changes) != 2 {
		t.Fatalf("expected two game changelog rows, got %d", len(changes))
	}
	latest := changes[0]
	if latest.GetString("from") != state1 || latest.GetString("to") != state2 || latest.GetString("by") != user.Id {
		t.Fatalf("unexpected changelog pointer/editor values")
	}
	log := decodeLogObject(t, latest.Get("log"))
	if log["version"] != float64(2) || log["state_from"] != state1 || log["state_to"] != state2 {
		t.Fatalf("missing v2 state log: %#v", log)
	}
	items, ok := log["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("expected one game diff item: %#v", log)
	}
	item := items[0].(map[string]any)
	if item["entry_id"] != entryID || item["before"] == nil || item["after"] == nil {
		t.Fatalf("missing entry before/after: %#v", item)
	}
	if err := app.RunInTransaction(func(tx core.App) error {
		_, err := arcadegame.UpdateArcadeGameTx(tx, arcadegame.UpdateArcadeGameBody{Arcade: arcadeID, BaseStateID: state1, Games: []arcadegame.GameAtomInput{stateTestGame(versionID, entryID, "3F")}}, user.Id)
		return err
	}); err == nil || !strings.Contains(err.Error(), "game state conflict") {
		t.Fatalf("expected stale state conflict, got %v", err)
	}
}
