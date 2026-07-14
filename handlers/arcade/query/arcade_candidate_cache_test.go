package query

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/ericbaek/musecat-backend-core/testutil"
)

func TestGetArcadeCandidates_RebuildsAndInvalidates(t *testing.T) {
	t.Parallel()

	app := testutil.NewTestApp(t)
	RegisterCandidateSnapshotHooks(app)

	arcadeID, basicID := seedArcadeCandidateRecord(t, app, "Original Name", "Seoul Arcade")
	versionID := seedArcadeCandidateVersion(t, app, "Initial Series")
	moleculeID := seedArcadeCandidateGameMolecule(t, app, arcadeID)
	seedArcadeCandidateGameAtom(t, app, moleculeID, versionID)

	candidates, err := GetArcadeCandidates(app)
	if err != nil {
		t.Fatalf("expected initial cache rebuild to succeed: %v", err)
	}
	candidate := findArcadeCandidate(candidates, arcadeID)
	if candidate == nil {
		t.Fatalf("expected candidate %q to be present after cache rebuild", arcadeID)
	}
	if got := candidate.Name; got != "Original Name" {
		t.Fatalf("expected initial candidate name %q, got %q", "Original Name", got)
	}
	if len(candidate.GameSeries) != 1 {
		t.Fatalf("expected one initial game series, got %#v", candidate.GameSeries)
	}

	basicRec, err := app.FindRecordById("arcade_basic", basicID)
	if err != nil {
		t.Fatalf("failed to load arcade_basic: %v", err)
	}
	basicRec.Set("name", "Updated Name")
	if err := app.Save(basicRec); err != nil {
		t.Fatalf("failed to update arcade_basic name: %v", err)
	}

	candidates, err = GetArcadeCandidates(app)
	if err != nil {
		t.Fatalf("expected cache rebuild after basic update to succeed: %v", err)
	}
	candidate = findArcadeCandidate(candidates, arcadeID)
	if candidate == nil {
		t.Fatalf("expected candidate %q after basic update", arcadeID)
	}
	if got := candidate.Name; got != "Updated Name" {
		t.Fatalf("expected updated candidate name %q, got %q", "Updated Name", got)
	}

	seriesID := seedArcadeCandidateSeries(t, app, "Series B")
	versionRec, err := app.FindRecordById("game_series_version", versionID)
	if err != nil {
		t.Fatalf("failed to load game_series_version: %v", err)
	}
	versionRec.Set("series", seriesID)
	if err := app.Save(versionRec); err != nil {
		t.Fatalf("failed to update game_series_version series: %v", err)
	}

	candidates, err = GetArcadeCandidates(app)
	if err != nil {
		t.Fatalf("expected cache rebuild after version update to succeed: %v", err)
	}
	candidate = findArcadeCandidate(candidates, arcadeID)
	if candidate == nil {
		t.Fatalf("expected candidate %q after version update", arcadeID)
	}
	if len(candidate.GameSeries) != 1 || candidate.GameSeries[0] != seriesID {
		t.Fatalf("expected updated game series %q, got %#v", seriesID, candidate.GameSeries)
	}
}

func findArcadeCandidate(candidates []ArcadeCandidate, arcadeID string) *ArcadeCandidate {
	for i := range candidates {
		if candidates[i].ID == arcadeID {
			return &candidates[i]
		}
	}
	return nil
}

func seedArcadeCandidateRecord(tb testing.TB, app *tests.TestApp, name, address string) (arcadeID, basicID string) {
	tb.Helper()

	arcadeColl, err := app.FindCollectionByNameOrId("arcade")
	if err != nil {
		tb.Fatalf("failed to load arcade collection: %v", err)
	}

	arcadeRec := core.NewRecord(arcadeColl)
	arcadeRec.Set("country", "KR")
	arcadeRec.Set("public", true)
	arcadeRec.Set("closed", false)
	if err := app.Save(arcadeRec); err != nil {
		tb.Fatalf("failed to save arcade: %v", err)
	}

	basicColl, err := app.FindCollectionByNameOrId("arcade_basic")
	if err != nil {
		tb.Fatalf("failed to load arcade_basic collection: %v", err)
	}

	basicRec := core.NewRecord(basicColl)
	basicRec.Set("name", name)
	basicRec.Set("address", address)
	basicRec.Set("nickname", []string{"Cache Test"})
	basicRec.Set("arcade", arcadeRec.Id)
	basicRec.Set("location", map[string]any{"lat": 37.5665, "lon": 126.9780})
	if err := app.Save(basicRec); err != nil {
		tb.Fatalf("failed to save arcade_basic: %v", err)
	}

	arcadeRec.Set("basic", basicRec.Id)
	if err := app.Save(arcadeRec); err != nil {
		tb.Fatalf("failed to link arcade.basic: %v", err)
	}

	return arcadeRec.Id, basicRec.Id
}

func seedArcadeCandidateSeries(tb testing.TB, app *tests.TestApp, name string) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("game_series")
	if err != nil {
		tb.Fatalf("failed to load game_series collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("seriesNumber", 999)
	rec.Set("en", name)
	rec.Set("kr", name)
	rec.Set("jp", name)
	rec.Set("en_short", name)
	rec.Set("kr_short", name)
	rec.Set("jp_short", name)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save game_series: %v", err)
	}

	return rec.Id
}

func seedArcadeCandidateVersion(tb testing.TB, app *tests.TestApp, seriesName string) string {
	tb.Helper()

	seriesID := seedArcadeCandidateSeries(tb, app, seriesName)
	coll, err := app.FindCollectionByNameOrId("game_series_version")
	if err != nil {
		tb.Fatalf("failed to load game_series_version collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("series", seriesID)
	rec.Set("released_on", "2025-01-01")
	rec.Set("en", seriesName)
	rec.Set("kr", seriesName)
	rec.Set("jp", seriesName)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save game_series_version: %v", err)
	}

	return rec.Id
}

func seedArcadeCandidateGameMolecule(tb testing.TB, app *tests.TestApp, arcadeID string) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("arcade_game")
	if err != nil {
		tb.Fatalf("failed to load arcade_game collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("arcade", arcadeID)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save arcade_game: %v", err)
	}

	arcadeRec, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		tb.Fatalf("failed to load arcade: %v", err)
	}
	arcadeRec.Set("game", rec.Id)
	if err := app.Save(arcadeRec); err != nil {
		tb.Fatalf("failed to link arcade.game: %v", err)
	}

	return rec.Id
}

func seedArcadeCandidateGameAtom(tb testing.TB, app *tests.TestApp, moleculeID, versionID string) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("arcade_game_atoms")
	if err != nil {
		tb.Fatalf("failed to load arcade_game_atoms collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("molecule", moleculeID)
	rec.Set("game", versionID)
	rec.Set("location", "1F")
	rec.Set("quantity", 1)
	rec.Set("price", map[string]any{
		"currency": "KRW",
		"type":     "custom",
		"list":     []map[string]any{{"value": 500}},
		"accept":   []string{"Cash"},
	})
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save arcade_game_atom: %v", err)
	}

	return rec.Id
}
