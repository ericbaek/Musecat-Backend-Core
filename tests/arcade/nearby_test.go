package arcade_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestNearby_SortsByDistanceFromCachedCandidates(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUser(t, app)
	seriesID := seedNearbyGameSeries(t, app, "Nearby Series")
	versionID := seedNearbyGameSeriesVersion(t, app, seriesID, "Nearby Version")

	nearID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Nearby One",
		Address:  "Near Road",
		Location: location{Lat: 37.5665, Lon: 126.9790},
	})
	setArcadeVisibility(t, app, nearID, true, false)
	nearMoleculeID := seedNearbyGameMolecule(t, app, nearID)
	seedNearbyGameAtom(t, app, nearMoleculeID, versionID)

	midID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Nearby Two",
		Address:  "Mid Road",
		Location: location{Lat: 37.5665, Lon: 127.0100},
	})
	setArcadeVisibility(t, app, midID, true, false)
	midMoleculeID := seedNearbyGameMolecule(t, app, midID)
	seedNearbyGameAtom(t, app, midMoleculeID, versionID)

	farID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Nearby Three",
		Address:  "Far Road",
		Location: location{Lat: 37.5665, Lon: 127.2000},
	})
	setArcadeVisibility(t, app, farID, true, false)
	farMoleculeID := seedNearbyGameMolecule(t, app, farID)
	seedNearbyGameAtom(t, app, farMoleculeID, versionID)

	res := executeJSONRequest(t, app, http.MethodGet, "/arcades/nearby?game_series="+seriesID+"&lat=37.5665&lon=126.9780", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	defer res.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode nearby payload: %v", err)
	}

	rawItems, ok := payload["items"]
	if !ok {
		t.Fatalf("expected items key in payload: %#v", payload)
	}

	buf, err := json.Marshal(rawItems)
	if err != nil {
		t.Fatalf("failed to marshal items: %v", err)
	}

	var items []map[string]any
	if err := json.Unmarshal(buf, &items); err != nil {
		t.Fatalf("failed to unmarshal items: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 nearby items, got %d", len(items))
	}

	wantIDs := []string{nearID, midID, farID}
	lastDistance := -1.0
	for i, item := range items {
		if got := item["id"]; got != wantIDs[i] {
			t.Fatalf("expected item[%d] id %q, got %v", i, wantIDs[i], got)
		}
		series, ok := item["game_series"].([]any)
		if !ok || len(series) != 1 || series[0] != seriesID {
			t.Fatalf("expected item[%d] game_series to contain %q, got %#v", i, seriesID, item["game_series"])
		}
		distance, ok := item["distance_km"].(float64)
		if !ok {
			t.Fatalf("expected distance_km on item[%d], got %#v", i, item["distance_km"])
		}
		if distance < lastDistance {
			t.Fatalf("expected distances to be sorted ascending, got %f before %f", lastDistance, distance)
		}
		lastDistance = distance
	}
}

func TestNearby_CountryFilterAndCountryTotalsIncludeNearestArcade(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUser(t, app)

	nearJPID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name: "Near Japan Arcade", Address: "Near Road", Country: "JP",
		Location: location{Lat: 35.6812, Lon: 139.7671},
	})
	setArcadeVisibility(t, app, nearJPID, true, false)

	farJPID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name: "Far Japan Arcade", Address: "Far Road", Country: "JP",
		Location: location{Lat: 35.6895, Lon: 139.6917},
	})
	setArcadeVisibility(t, app, farJPID, true, false)

	krID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name: "Korea Arcade", Address: "Korea Road", Country: "KR",
		Location: location{Lat: 37.5665, Lon: 126.9780},
	})
	setArcadeVisibility(t, app, krID, true, false)

	res := executeJSONRequest(t, app, http.MethodGet, "/arcades/nearby?lat=35.6810&lon=139.7670&country=jp", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	defer res.Body.Close()

	var payload struct {
		Total         int              `json:"total"`
		Items         []map[string]any `json:"items"`
		CountryTotals map[string]struct {
			Total         int `json:"total"`
			NearestArcade struct {
				ID         string  `json:"id"`
				DistanceKm float64 `json:"distance_km"`
			} `json:"nearest_arcade"`
		} `json:"country_totals"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode nearby payload: %v", err)
	}
	if payload.Total != 2 || len(payload.Items) != 2 {
		t.Fatalf("expected only two JP arcades, got total=%d items=%d", payload.Total, len(payload.Items))
	}
	for _, item := range payload.Items {
		if item["country"] != "JP" {
			t.Fatalf("expected only JP items, got %#v", item)
		}
	}
	jpTotal, ok := payload.CountryTotals["JP"]
	if !ok || len(payload.CountryTotals) != 1 {
		t.Fatalf("expected only JP country total, got %#v", payload.CountryTotals)
	}
	if jpTotal.Total != 2 || jpTotal.NearestArcade.ID != nearJPID {
		t.Fatalf("expected JP total to use nearest arcade %q, got %#v", nearJPID, jpTotal)
	}
	if jpTotal.NearestArcade.DistanceKm <= 0 {
		t.Fatalf("expected nearest distance to be positive, got %f", jpTotal.NearestArcade.DistanceKm)
	}
}

func TestNearby_ExpandsQuerySeriesAndAppliesDistanceLimit(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUser(t, app)
	seriesID := seedNearbyGameSeries(t, app, "Nearby Series")
	seriesID2 := seedNearbyGameSeries(t, app, "Other Series")
	versionID := seedNearbyGameSeriesVersion(t, app, seriesID, "Nearby Version")
	versionID2 := seedNearbyGameSeriesVersion(t, app, seriesID2, "Other Version")

	nearID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Nearby One",
		Address:  "Near Road",
		Location: location{Lat: 37.5665, Lon: 126.9790},
	})
	setArcadeVisibility(t, app, nearID, true, false)
	nearMoleculeID := seedNearbyGameMolecule(t, app, nearID)
	seedNearbyGameAtom(t, app, nearMoleculeID, versionID)
	seedNearbyGameAtom(t, app, nearMoleculeID, versionID2)

	midID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Nearby Two",
		Address:  "Mid Road",
		Location: location{Lat: 37.5665, Lon: 127.0100},
	})
	setArcadeVisibility(t, app, midID, true, false)
	midMoleculeID := seedNearbyGameMolecule(t, app, midID)
	seedNearbyGameAtom(t, app, midMoleculeID, versionID)
	seedNearbyGameAtom(t, app, midMoleculeID, versionID2)

	farID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Nearby Three",
		Address:  "Far Road",
		Location: location{Lat: 37.5665, Lon: 127.2000},
	})
	setArcadeVisibility(t, app, farID, true, false)
	farMoleculeID := seedNearbyGameMolecule(t, app, farID)
	seedNearbyGameAtom(t, app, farMoleculeID, versionID)
	seedNearbyGameAtom(t, app, farMoleculeID, versionID2)

	res := executeJSONRequest(t, app, http.MethodGet, "/arcades/nearby?game_series="+seriesID+"&lat=37.5665&lon=126.9780&expand=true&distance_limit=3", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	defer res.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode nearby payload: %v", err)
	}

	rawItems, ok := payload["items"]
	if !ok {
		t.Fatalf("expected items key in payload: %#v", payload)
	}

	buf, err := json.Marshal(rawItems)
	if err != nil {
		t.Fatalf("failed to marshal items: %v", err)
	}

	var items []map[string]any
	if err := json.Unmarshal(buf, &items); err != nil {
		t.Fatalf("failed to unmarshal items: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 nearby items within distance limit, got %d", len(items))
	}

	wantIDs := []string{nearID, midID}
	for i, item := range items {
		if got := item["id"]; got != wantIDs[i] {
			t.Fatalf("expected item[%d] id %q, got %v", i, wantIDs[i], got)
		}
		gameObj, ok := item["game"].(map[string]any)
		if !ok {
			t.Fatalf("expected expanded game object on item[%d], got %#v", i, item["game"])
		}
		gameID, ok := gameObj["id"].(string)
		if !ok || gameID == "" {
			t.Fatalf("expected game object id to be present, got %#v", gameObj["id"])
		}
		itemsList, ok := gameObj["items"].([]any)
		if !ok || len(itemsList) != 1 {
			t.Fatalf("expected one expanded game item on item[%d], got %#v", i, gameObj["items"])
		}
		gameItem, ok := itemsList[0].(map[string]any)
		if !ok {
			t.Fatalf("expected expanded game item object, got %#v", itemsList[0])
		}
		versionObj, ok := gameItem["version"].(map[string]any)
		if !ok {
			t.Fatalf("expected expanded game version object, got %#v", gameItem["version"])
		}
		if got, ok := versionObj["id"].(string); !ok || got == "" {
			t.Fatalf("expected expanded game version id, got %#v", versionObj["id"])
		}
		seriesObj, ok := gameItem["series"].(map[string]any)
		if !ok {
			t.Fatalf("expected expanded game series object, got %#v", gameItem["series"])
		}
		if got, ok := seriesObj["id"].(string); !ok || got != seriesID {
			t.Fatalf("expected expanded game series id %q, got %#v", seriesID, seriesObj["id"])
		}
		versionSeries, ok := gameItem["version"].(map[string]any)
		if !ok {
			t.Fatalf("expected expanded game version object, got %#v", gameItem["version"])
		}
		if got, ok := versionSeries["series"].(string); !ok || got != seriesID {
			t.Fatalf("expected expanded game version to belong to %q, got %#v", seriesID, versionSeries["series"])
		}
		distance, ok := item["distance_km"].(float64)
		if !ok {
			t.Fatalf("expected distance_km on item[%d], got %#v", i, item["distance_km"])
		}
		if distance > 3 {
			t.Fatalf("expected item[%d] to respect distance_limit, got %f", i, distance)
		}
	}
	if _, ok := payload["distance_limit"]; ok {
		t.Fatalf("did not expect distance_limit echo in response: %#v", payload["distance_limit"])
	}
}

func TestNearby_ExpandBoostsMachineCountRanking(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUser(t, app)
	seriesID := seedNearbyGameSeries(t, app, "MyMai")
	versionID := seedNearbyGameSeriesVersion(t, app, seriesID, "MyMai Version")

	aID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Arcade A",
		Address:  "A Road",
		Location: location{Lat: 37.5665, Lon: 127.1200},
	})
	setArcadeVisibility(t, app, aID, true, false)
	aMoleculeID := seedNearbyGameMolecule(t, app, aID)
	aAtomID := seedNearbyGameAtom(t, app, aMoleculeID, versionID)
	setNearbyAtomQuantity(t, app, aAtomID, 1)

	bID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Arcade B",
		Address:  "B Road",
		Location: location{Lat: 37.5665, Lon: 127.1400},
	})
	setArcadeVisibility(t, app, bID, true, false)
	bMoleculeID := seedNearbyGameMolecule(t, app, bID)
	bAtomID := seedNearbyGameAtom(t, app, bMoleculeID, versionID)
	setNearbyAtomQuantity(t, app, bAtomID, 4)

	res := executeJSONRequest(t, app, http.MethodGet, "/arcades/nearby?game_series="+seriesID+"&lat=37.5665&lon=126.9780&expand=true", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	defer res.Body.Close()

	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode nearby payload: %v", err)
	}

	rawItems, ok := payload["items"]
	if !ok {
		t.Fatalf("expected items key in payload: %#v", payload)
	}
	buf, err := json.Marshal(rawItems)
	if err != nil {
		t.Fatalf("failed to marshal items: %v", err)
	}

	var items []map[string]any
	if err := json.Unmarshal(buf, &items); err != nil {
		t.Fatalf("failed to unmarshal items: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 nearby items, got %d", len(items))
	}
	if got := items[0]["id"]; got != bID {
		t.Fatalf("expected boosted arcade %q to rank first, got %v", bID, got)
	}
	if got := items[1]["id"]; got != aID {
		t.Fatalf("expected arcade %q to rank second, got %v", aID, got)
	}

	bGame, ok := items[0]["game"].(map[string]any)
	if !ok {
		t.Fatalf("expected expanded game on boosted item, got %#v", items[0]["game"])
	}
	bItems, ok := bGame["items"].([]any)
	if !ok || len(bItems) != 1 {
		t.Fatalf("expected boosted arcade to expose 1 matching game item, got %#v", bGame["items"])
	}
}

func setNearbyAtomQuantity(tb testing.TB, app *tests.TestApp, atomID string, quantity int) {
	tb.Helper()

	rec, err := app.FindRecordById("arcade_game_atoms", atomID)
	if err != nil {
		tb.Fatalf("failed to load arcade_game_atoms record: %v", err)
	}
	rec.Set("quantity", quantity)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to update arcade_game_atoms quantity: %v", err)
	}
}

func seedNearbyGameSeries(tb testing.TB, app *tests.TestApp, name string) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("game_series")
	if err != nil {
		tb.Fatalf("failed to load game_series collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("seriesNumber", 1001)
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

func seedNearbyGameSeriesVersion(tb testing.TB, app *tests.TestApp, seriesID, name string) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("game_series_version")
	if err != nil {
		tb.Fatalf("failed to load game_series_version collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("series", seriesID)
	rec.Set("released_on", "2025-01-01")
	rec.Set("en", name)
	rec.Set("kr", name)
	rec.Set("jp", name)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save game_series_version: %v", err)
	}

	return rec.Id
}

func seedNearbyGameMolecule(tb testing.TB, app *tests.TestApp, arcadeID string) string {
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

func seedNearbyGameAtom(tb testing.TB, app *tests.TestApp, moleculeID, versionID string) string {
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
