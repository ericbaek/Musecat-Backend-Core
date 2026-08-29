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
	cabinetAID := seedArcadeCandidateCabinet(t, app, "Cabinet A")
	cabinetBID := seedArcadeCandidateCabinet(t, app, "Cabinet B")
	revisionID := seedArcadeCandidateGameState(t, app, arcadeID, versionID, cabinetAID)

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
	if len(candidate.GameInstallations) != 1 || candidate.GameInstallations[0].CabinetID != cabinetAID {
		t.Fatalf("expected cabinet projection %q, got %#v", cabinetAID, candidate.GameInstallations)
	}
	candidate.GameInstallations[0].CabinetID = "mutated clone"
	candidates, err = GetArcadeCandidates(app)
	if err != nil {
		t.Fatalf("expected cached candidate clone to load: %v", err)
	}
	if got := findArcadeCandidate(candidates, arcadeID).GameInstallations[0].CabinetID; got != cabinetAID {
		t.Fatalf("expected cached projection clone to remain %q, got %q", cabinetAID, got)
	}

	revision, err := app.FindRecordById("arcade_game_history", revisionID)
	if err != nil {
		t.Fatalf("failed to load game revision: %v", err)
	}
	revision.Set("cabinet", cabinetBID)
	if err := app.Save(revision); err != nil {
		t.Fatalf("failed to update game revision cabinet: %v", err)
	}
	candidates, err = GetArcadeCandidates(app)
	if err != nil {
		t.Fatalf("expected cache rebuild after cabinet update to succeed: %v", err)
	}
	if got := findArcadeCandidate(candidates, arcadeID).GameInstallations[0].CabinetID; got != cabinetBID {
		t.Fatalf("expected updated cabinet projection %q, got %q", cabinetBID, got)
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

func seedArcadeCandidateCabinet(tb testing.TB, app *tests.TestApp, name string) string {
	tb.Helper()
	series, err := app.FindFirstRecordByFilter("game_series", "", nil)
	if err != nil {
		tb.Fatalf("failed to load game_series: %v", err)
	}
	coll, err := app.FindCollectionByNameOrId("game_cabinet")
	if err != nil {
		tb.Fatalf("failed to load game_cabinet: %v", err)
	}
	record := core.NewRecord(coll)
	record.Set("en", name)
	record.Set("kr", name)
	record.Set("jp", name)
	record.Set("series", series.Id)
	if err := app.Save(record); err != nil {
		tb.Fatalf("failed to save game_cabinet: %v", err)
	}
	return record.Id
}

func seedArcadeCandidateGameState(tb testing.TB, app *tests.TestApp, arcadeID, versionID, cabinetID string) string {
	tb.Helper()

	version, err := app.FindRecordById("game_series_version", versionID)
	if err != nil {
		tb.Fatalf("failed to load game_series_version: %v", err)
	}
	entryColl, err := app.FindCollectionByNameOrId("arcade_game_id")
	if err != nil {
		tb.Fatalf("failed to load arcade_game_id: %v", err)
	}
	entry := core.NewRecord(entryColl)
	entry.Set("arcade", arcadeID)
	entry.Set("series", version.GetString("series"))
	if err := app.Save(entry); err != nil {
		tb.Fatalf("failed to save arcade_game_id: %v", err)
	}

	batchColl, err := app.FindCollectionByNameOrId("arcade_game_history_batch")
	if err != nil {
		tb.Fatalf("failed to load arcade_game_revision_batch: %v", err)
	}
	batch := core.NewRecord(batchColl)
	batch.Set("arcade", arcadeID)
	batch.Set("reason", "candidate cache test")
	if err := app.Save(batch); err != nil {
		tb.Fatalf("failed to save arcade_game_revision_batch: %v", err)
	}

	revisionColl, err := app.FindCollectionByNameOrId("arcade_game_history")
	if err != nil {
		tb.Fatalf("failed to load arcade_game_revision: %v", err)
	}
	revision := core.NewRecord(revisionColl)
	revision.Set("batch", batch.Id)
	revision.Set("entry", entry.Id)
	revision.Set("version", versionID)
	revision.Set("cabinet", cabinetID)
	revision.Set("location", "1F")
	revision.Set("quantity", 1)
	revision.Set("price", map[string]any{
		"currency": "KRW",
		"type":     "custom",
		"list":     []map[string]any{{"value": 500}},
		"accept":   []string{"Cash"},
	})
	if err := app.Save(revision); err != nil {
		tb.Fatalf("failed to save arcade_game_revision: %v", err)
	}

	arcade, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		tb.Fatalf("failed to load arcade: %v", err)
	}
	arcade.Set("game_v2", batch.Id)
	if err := app.Save(arcade); err != nil {
		tb.Fatalf("failed to link arcade.game_v2: %v", err)
	}
	return revision.Id
}
