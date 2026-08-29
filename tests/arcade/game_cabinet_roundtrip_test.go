package arcade_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	arcadegame "github.com/ericbaek/musecat-backend-core/handlers/arcade/game"
)

func TestGameCabinetRoundTripAndCompatibility(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUser(t, app)
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{Name: "Cabinet Test", Address: "Cabinet Street", Location: location{Lat: 37.5, Lon: 127.0}})
	versionID := seedStateTestVersion(t, app)
	cabinetA := seedGameCabinet(t, app, "Gold")
	cabinetB := seedGameCabinet(t, app, "Silver")
	linkVersionCabinet(t, app, versionID, cabinetA)
	linkVersionCabinet(t, app, versionID, cabinetB)

	first := stateTestGame(versionID, "", "1F")
	first.Cabinet = cabinetA
	var state1 string
	if err := app.RunInTransaction(func(tx core.App) error {
		var err error
		state1, err = arcadegame.UpdateArcadeGameTx(tx, arcadegame.UpdateArcadeGameBody{
			Arcade: arcadeID, BaseStateID: "", Games: []arcadegame.GameAtomInput{first},
		}, user.Id)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	clone, err := arcadegame.BuildUpdateBodyFromCurrentState(app, arcadeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(clone.Games) != 1 || clone.Games[0].Cabinet != cabinetA {
		t.Fatalf("expected cloned cabinet %q, got %#v", cabinetA, clone.Games)
	}

	clone.Games[0].Cabinet = cabinetB
	clone.Games[0].Price = first.Price
	clone.Games[0].Tag = first.Tag
	var state2 string
	clone.BaseStateID = state1
	if err := app.RunInTransaction(func(tx core.App) error {
		var err error
		state2, err = arcadegame.UpdateArcadeGameTx(tx, clone, user.Id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if state1 == state2 {
		t.Fatal("expected a new state after cabinet change")
	}

	revisions, err := app.FindRecordsByFilter("arcade_game_history", "batch={:batch}", "", 0, 0, map[string]any{"batch": state2})
	if err != nil || len(revisions) != 1 {
		t.Fatalf("expected one replacement revision, err=%v count=%d", err, len(revisions))
	}
	if got := revisions[0].GetString("cabinet"); got != cabinetB {
		t.Fatalf("expected stored cabinet %q, got %q", cabinetB, got)
	}
}

func seedGameCabinet(tb testing.TB, app core.App, name string) string {
	tb.Helper()
	series, err := app.FindFirstRecordByFilter("game_series", "", nil)
	if err != nil {
		seriesCollection, findErr := app.FindCollectionByNameOrId("game_series")
		if findErr != nil {
			tb.Fatalf("find game series collection: %v", findErr)
		}
		series = core.NewRecord(seriesCollection)
		series.Set("en", "Test Series")
		series.Set("kr", "Test Series")
		series.Set("jp", "Test Series")
		if saveErr := app.Save(series); saveErr != nil {
			tb.Fatalf("save test game series: %v", saveErr)
		}
	}
	coll, err := app.FindCollectionByNameOrId("game_cabinet")
	if err != nil {
		tb.Fatalf("find game cabinet collection: %v", err)
	}
	rec := core.NewRecord(coll)
	rec.Set("en", name)
	rec.Set("kr", name)
	rec.Set("jp", name)
	rec.Set("series", series.Id)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("save game cabinet: %v", err)
	}
	return rec.Id
}

func linkVersionCabinet(tb testing.TB, app core.App, versionID, cabinetID string) {
	tb.Helper()
	coll, err := app.FindCollectionByNameOrId("game_series_version_cabinet")
	if err != nil {
		tb.Fatalf("find version cabinet collection: %v", err)
	}
	rec := core.NewRecord(coll)
	rec.Set("version", versionID)
	rec.Set("cabinet", cabinetID)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("save version cabinet link: %v", err)
	}
}
