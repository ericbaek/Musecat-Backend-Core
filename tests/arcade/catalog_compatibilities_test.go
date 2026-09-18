package arcade_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestCatalogCompatibilityReplacementIsAtomicAndReplayable(t *testing.T) {
	app := newArcadeTestApp(t)
	token, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	headers := map[string]string{"Authorization": "Bearer " + token}
	seriesID := seedGameSeries(t, app, 1, "Catalog Series")
	versionID := seedGameSeriesVersionWithSeries(t, app, seriesID, "2026-01-01", "Catalog Version")
	cabinetA := seedCatalogCabinet(t, app, seriesID, "Cabinet A")
	cabinetB := seedCatalogCabinet(t, app, seriesID, "Cabinet B")
	compatibilityID := seedCatalogCompatibility(t, app, versionID, cabinetA)

	body := catalogCompatibilityBody(
		"f6996cb4-a6f3-4e3d-9da2-c2fd00410412",
		versionID,
		[]string{cabinetB},
		[]map[string]any{{"id": compatibilityID, "cabinet": cabinetA, "revision": 1}},
	)
	first := executeJSONRequest(t, app, http.MethodPost, "/moderation/game/catalog/compatibilities", string(body), headers)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("expected successful reconciliation, got %d: %s", first.StatusCode, responseText(first))
	}
	first.Body.Close()
	assertCatalogCompatibilityState(t, app, versionID, cabinetA, true)
	assertCatalogCompatibilityState(t, app, versionID, cabinetB, false)
	assertCatalogChangeCount(t, app, 2)

	retry := executeJSONRequest(t, app, http.MethodPost, "/moderation/game/catalog/compatibilities", string(body), headers)
	if retry.StatusCode != http.StatusOK {
		t.Fatalf("expected replay to succeed, got %d", retry.StatusCode)
	}
	var replay struct {
		Replayed bool `json:"replayed"`
	}
	if err := json.NewDecoder(retry.Body).Decode(&replay); err != nil {
		t.Fatal(err)
	}
	retry.Body.Close()
	if !replay.Replayed {
		t.Fatal("expected the duplicate reconciliation to be replayed")
	}
	assertCatalogChangeCount(t, app, 2)
}

func TestCatalogCompatibilityReplacementRollsBackWhenRemovalIsBlocked(t *testing.T) {
	app := newArcadeTestApp(t)
	token, user := createAuthUserWithTags(t, app, []string{"moderator"})
	headers := map[string]string{"Authorization": "Bearer " + token}
	seriesID := seedGameSeries(t, app, 1, "Catalog Series")
	versionID := seedGameSeriesVersionWithSeries(t, app, seriesID, "2026-01-01", "Catalog Version")
	cabinetA := seedCatalogCabinet(t, app, seriesID, "Cabinet A")
	cabinetB := seedCatalogCabinet(t, app, seriesID, "Cabinet B")
	compatibilityID := seedCatalogCompatibility(t, app, versionID, cabinetA)
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{Name: "Compatibility use", Address: "A", Location: location{Lat: 37.5, Lon: 127.0}})
	batchID := seedArcadeGameMolecule(t, app, arcadeID)
	seedArcadeGameAtom(t, app, batchID, versionID, "1F")
	current, err := app.FindFirstRecordByFilter("arcade_game_history", "batch={:batch}", map[string]any{"batch": batchID})
	if err != nil {
		t.Fatal(err)
	}
	current.Set("cabinet", cabinetA)
	if err := app.Save(current); err != nil {
		t.Fatal(err)
	}

	body := catalogCompatibilityBody(
		"4b4b3bed-400e-4b48-96a8-efd80c129fa0",
		versionID,
		[]string{cabinetB},
		[]map[string]any{{"id": compatibilityID, "cabinet": cabinetA, "revision": 1}},
	)
	response := executeJSONRequest(t, app, http.MethodPost, "/moderation/game/catalog/compatibilities", string(body), headers)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("expected blocked reconciliation, got %d: %s", response.StatusCode, responseText(response))
	}
	response.Body.Close()
	assertCatalogCompatibilityState(t, app, versionID, cabinetA, false)
	assertCatalogCompatibilityDoesNotExist(t, app, versionID, cabinetB)
	assertCatalogChangeCount(t, app, 0)
}

func catalogCompatibilityBody(operationID, versionID string, cabinetIDs []string, expected []map[string]any) []byte {
	body, err := json.Marshal(map[string]any{
		"operation_id":             operationID,
		"reason":                   "Update compatible cabinets.",
		"version_id":               versionID,
		"cabinet_ids":              cabinetIDs,
		"expected_compatibilities": expected,
	})
	if err != nil {
		panic(err)
	}
	return body
}

func seedCatalogCabinet(tb testing.TB, app *tests.TestApp, seriesID, name string) string {
	tb.Helper()
	collection, err := app.FindCollectionByNameOrId("game_cabinet")
	if err != nil {
		tb.Fatal(err)
	}
	record := core.NewRecord(collection)
	record.Set("series", seriesID)
	record.Set("en", name)
	record.Set("kr", name)
	record.Set("jp", name)
	record.Set("revision", 1)
	if err := app.Save(record); err != nil {
		tb.Fatal(err)
	}
	return record.Id
}

func seedCatalogCompatibility(tb testing.TB, app *tests.TestApp, versionID, cabinetID string) string {
	tb.Helper()
	collection, err := app.FindCollectionByNameOrId("game_series_version_cabinet")
	if err != nil {
		tb.Fatal(err)
	}
	record := core.NewRecord(collection)
	record.Set("version", versionID)
	record.Set("cabinet", cabinetID)
	record.Set("revision", 1)
	if err := app.Save(record); err != nil {
		tb.Fatal(err)
	}
	return record.Id
}

func assertCatalogCompatibilityState(tb testing.TB, app *tests.TestApp, versionID, cabinetID string, archived bool) {
	tb.Helper()
	record, err := app.FindFirstRecordByFilter("game_series_version_cabinet", "version={:version} && cabinet={:cabinet}", map[string]any{"version": versionID, "cabinet": cabinetID})
	if err != nil {
		tb.Fatal(err)
	}
	if record.GetBool("archived") != archived {
		tb.Fatalf("expected cabinet %s archived=%t, got %t", cabinetID, archived, record.GetBool("archived"))
	}
}

func assertCatalogCompatibilityDoesNotExist(tb testing.TB, app *tests.TestApp, versionID, cabinetID string) {
	tb.Helper()
	rows, err := app.FindRecordsByFilter("game_series_version_cabinet", "version={:version} && cabinet={:cabinet}", "", 0, 0, map[string]any{"version": versionID, "cabinet": cabinetID})
	if err != nil {
		tb.Fatal(err)
	}
	if len(rows) != 0 {
		tb.Fatalf("expected no compatibility for cabinet %s, got %d", cabinetID, len(rows))
	}
}

func assertCatalogChangeCount(tb testing.TB, app *tests.TestApp, expected int) {
	tb.Helper()
	rows, err := app.FindRecordsByFilter("game_catalog_changelog", "", "", 0, 0, nil)
	if err != nil {
		tb.Fatal(err)
	}
	if len(rows) != expected {
		tb.Fatalf("expected %d catalog changes, got %d", expected, len(rows))
	}
}

func responseText(response *http.Response) string {
	bytes, _ := io.ReadAll(response.Body)
	response.Body.Close()
	return string(bytes)
}
