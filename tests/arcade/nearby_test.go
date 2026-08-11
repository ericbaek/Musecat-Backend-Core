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

func TestNearby_CabinetFilterMatchesSameSeriesRevision(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUser(t, app)
	chunithmID := seedNearbyGameSeries(t, app, "CHUNITHM")
	otherSeriesID := seedNearbyGameSeries(t, app, "Other Series")
	chunithmVersionID := seedNearbyGameSeriesVersion(t, app, chunithmID, "CHUNITHM Version")
	otherVersionID := seedNearbyGameSeriesVersion(t, app, otherSeriesID, "Other Version")
	goldID := seedNearbyGameCabinet(t, app, "Gold")
	silverID := seedNearbyGameCabinet(t, app, "Silver")

	goldArcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name: "CHUNITHM Gold", Address: "Gold Road", Location: location{Lat: 37.5665, Lon: 126.9790},
	})
	setArcadeVisibility(t, app, goldArcadeID, true, false)
	goldBatchID := seedNearbyGameMolecule(t, app, goldArcadeID)
	seedNearbyGameAtomWithCabinet(t, app, goldBatchID, chunithmVersionID, goldID)

	crossMatchArcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name: "Cross Match", Address: "Cross Road", Location: location{Lat: 37.5665, Lon: 126.9800},
	})
	setArcadeVisibility(t, app, crossMatchArcadeID, true, false)
	crossBatchID := seedNearbyGameMolecule(t, app, crossMatchArcadeID)
	seedNearbyGameAtomWithCabinet(t, app, crossBatchID, chunithmVersionID, silverID)
	seedNearbyGameAtomWithCabinet(t, app, crossBatchID, otherVersionID, goldID)

	unknownArcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name: "Unknown Cabinet", Address: "Unknown Road", Location: location{Lat: 37.5665, Lon: 126.9810},
	})
	setArcadeVisibility(t, app, unknownArcadeID, true, false)
	unknownBatchID := seedNearbyGameMolecule(t, app, unknownArcadeID)
	seedNearbyGameAtom(t, app, unknownBatchID, chunithmVersionID)

	seriesOnly := decodeNearbyItems(t, app, "/arcades/nearby?game_series="+chunithmID+"&lat=37.5665&lon=126.9780")
	if len(seriesOnly) != 3 {
		t.Fatalf("expected series-only filter to include all cabinets and unknown, got %#v", seriesOnly)
	}

	paired := decodeNearbyItems(t, app, "/arcades/nearby?game_series="+chunithmID+"&game_cabinet="+goldID+"&lat=37.5665&lon=126.9780")
	if len(paired) != 1 || paired[0]["id"] != goldArcadeID {
		t.Fatalf("expected only same-revision CHUNITHM Gold arcade %q, got %#v", goldArcadeID, paired)
	}
}

func TestNearby_PairedCabinetFiltersAreOrderedAndAllRequired(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUser(t, app)
	seriesA := seedNearbyGameSeries(t, app, "Series A")
	seriesB := seedNearbyGameSeries(t, app, "Series B")
	versionA := seedNearbyGameSeriesVersion(t, app, seriesA, "Version A")
	versionB := seedNearbyGameSeriesVersion(t, app, seriesB, "Version B")
	cabinetA := seedNearbyGameCabinet(t, app, "Cabinet A")
	cabinetB := seedNearbyGameCabinet(t, app, "Cabinet B")

	completeID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name: "Complete", Address: "Complete Road", Location: location{Lat: 37.5665, Lon: 126.9790},
	})
	setArcadeVisibility(t, app, completeID, true, false)
	completeBatch := seedNearbyGameMolecule(t, app, completeID)
	seedNearbyGameAtomWithCabinet(t, app, completeBatch, versionA, cabinetA)
	seedNearbyGameAtomWithCabinet(t, app, completeBatch, versionB, cabinetB)

	partialID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name: "Partial", Address: "Partial Road", Location: location{Lat: 37.5665, Lon: 126.9800},
	})
	setArcadeVisibility(t, app, partialID, true, false)
	partialBatch := seedNearbyGameMolecule(t, app, partialID)
	seedNearbyGameAtomWithCabinet(t, app, partialBatch, versionA, cabinetA)
	seedNearbyGameAtomWithCabinet(t, app, partialBatch, versionB, cabinetA)

	items := decodeNearbyItems(t, app, "/arcades/nearby?game_series="+seriesA+","+seriesB+"&game_cabinet="+cabinetA+","+cabinetB+"&lat=37.5665&lon=126.9780")
	if len(items) != 1 || items[0]["id"] != completeID {
		t.Fatalf("expected every ordered series/cabinet pair to match, got %#v", items)
	}
}

func TestNearby_RejectsUnpairedCabinetFilters(t *testing.T) {
	app := newArcadeTestApp(t)
	tests := []string{
		"/arcades/nearby?game_cabinet=cabinet_a&lat=37.5665&lon=126.9780",
		"/arcades/nearby?game_series=series_a,series_b&game_cabinet=cabinet_a&lat=37.5665&lon=126.9780",
	}
	for _, url := range tests {
		res := executeJSONRequest(t, app, http.MethodGet, url, "", nil)
		if res.StatusCode != http.StatusBadRequest {
			res.Body.Close()
			t.Fatalf("expected 400 for unpaired filters %q, got %d", url, res.StatusCode)
		}
		res.Body.Close()
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

func TestNearby_ExpandFiltersAndBoostsOnlyPairedCabinetItems(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUser(t, app)
	seriesID := seedNearbyGameSeries(t, app, "Cabinet Ranking")
	versionID := seedNearbyGameSeriesVersion(t, app, seriesID, "Cabinet Ranking Version")
	goldID := seedNearbyGameCabinet(t, app, "Gold")
	silverID := seedNearbyGameCabinet(t, app, "Silver")

	nearID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name: "Near", Address: "Near Road", Location: location{Lat: 37.5665, Lon: 127.1200},
	})
	setArcadeVisibility(t, app, nearID, true, false)
	nearBatch := seedNearbyGameMolecule(t, app, nearID)
	nearGold := seedNearbyGameAtomWithCabinet(t, app, nearBatch, versionID, goldID)
	setNearbyAtomQuantity(t, app, nearGold, 1)
	nearSilver := seedNearbyGameAtomWithCabinet(t, app, nearBatch, versionID, silverID)
	setNearbyAtomQuantity(t, app, nearSilver, 100)

	farID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name: "Far", Address: "Far Road", Location: location{Lat: 37.5665, Lon: 127.1400},
	})
	setArcadeVisibility(t, app, farID, true, false)
	farBatch := seedNearbyGameMolecule(t, app, farID)
	farGold := seedNearbyGameAtomWithCabinet(t, app, farBatch, versionID, goldID)
	setNearbyAtomQuantity(t, app, farGold, 2)

	items := decodeNearbyItems(t, app, "/arcades/nearby?game_series="+seriesID+"&game_cabinet="+goldID+"&lat=37.5665&lon=126.9780&expand=true")
	if len(items) != 2 || items[0]["id"] != farID || items[1]["id"] != nearID {
		t.Fatalf("expected ranking to use only Gold quantities, got %#v", items)
	}
	for _, arcade := range items {
		game, ok := arcade["game"].(map[string]any)
		if !ok {
			t.Fatalf("expected expanded game object, got %#v", arcade["game"])
		}
		gameItems, ok := game["items"].([]any)
		if !ok || len(gameItems) != 1 {
			t.Fatalf("expected only one paired cabinet item, got %#v", game["items"])
		}
		gameItem, ok := gameItems[0].(map[string]any)
		if !ok || gameItem["cabinet"] != goldID {
			t.Fatalf("expected expanded Gold cabinet %q, got %#v", goldID, gameItems[0])
		}
	}
}

func decodeNearbyItems(tb testing.TB, app *tests.TestApp, url string) []map[string]any {
	tb.Helper()
	res := executeJSONRequest(tb, app, http.MethodGet, url, "", nil)
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		tb.Fatalf("expected 200 for %q, got %d", url, res.StatusCode)
	}
	defer res.Body.Close()
	var payload struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		tb.Fatalf("failed to decode nearby payload: %v", err)
	}
	return payload.Items
}

func setNearbyAtomQuantity(tb testing.TB, app *tests.TestApp, atomID string, quantity int) {
	tb.Helper()

	rec, err := app.FindRecordById("arcade_game_history", atomID)
	if err != nil {
		tb.Fatalf("failed to load arcade_game_history record: %v", err)
	}
	rec.Set("quantity", quantity)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to update arcade_game_history quantity: %v", err)
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

	coll, err := app.FindCollectionByNameOrId("arcade_game_history_batch")
	if err != nil {
		tb.Fatalf("failed to load arcade_game_history_batch collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("arcade", arcadeID)
	rec.Set("reason", "nearby test")
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save arcade_game_history_batch: %v", err)
	}

	arcadeRec, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		tb.Fatalf("failed to load arcade: %v", err)
	}
	arcadeRec.Set("game_v2", rec.Id)
	if err := app.Save(arcadeRec); err != nil {
		tb.Fatalf("failed to link arcade.game_v2: %v", err)
	}

	return rec.Id
}

func seedNearbyGameAtom(tb testing.TB, app *tests.TestApp, moleculeID, versionID string) string {
	return seedNearbyGameAtomWithCabinet(tb, app, moleculeID, versionID, "")
}

func seedNearbyGameAtomWithCabinet(tb testing.TB, app *tests.TestApp, batchID, versionID, cabinetID string) string {
	tb.Helper()

	batch, err := app.FindRecordById("arcade_game_history_batch", batchID)
	if err != nil {
		tb.Fatalf("failed to load arcade_game_history_batch: %v", err)
	}
	version, err := app.FindRecordById("game_series_version", versionID)
	if err != nil {
		tb.Fatalf("failed to load game_series_version: %v", err)
	}
	entryColl, err := app.FindCollectionByNameOrId("arcade_game_id")
	if err != nil {
		tb.Fatalf("failed to load arcade_game_id collection: %v", err)
	}
	entry := core.NewRecord(entryColl)
	entry.Set("arcade", batch.GetString("arcade"))
	entry.Set("series", version.GetString("series"))
	if err := app.Save(entry); err != nil {
		tb.Fatalf("failed to save arcade_game_id: %v", err)
	}

	coll, err := app.FindCollectionByNameOrId("arcade_game_history")
	if err != nil {
		tb.Fatalf("failed to load arcade_game_history collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("batch", batchID)
	rec.Set("entry", entry.Id)
	rec.Set("version", versionID)
	rec.Set("cabinet", cabinetID)
	rec.Set("location", "1F")
	rec.Set("quantity", 1)
	rec.Set("price", map[string]any{
		"currency": "KRW",
		"type":     "custom",
		"list":     []map[string]any{{"value": 500}},
		"accept":   []string{"Cash"},
	})
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save arcade_game_history: %v", err)
	}

	return rec.Id
}

func seedNearbyGameCabinet(tb testing.TB, app *tests.TestApp, name string) string {
	tb.Helper()
	coll, err := app.FindCollectionByNameOrId("game_cabinet")
	if err != nil {
		tb.Fatalf("failed to load game_cabinet: %v", err)
	}
	rec := core.NewRecord(coll)
	rec.Set("en", name)
	rec.Set("kr", name)
	rec.Set("jp", name)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save game_cabinet: %v", err)
	}
	return rec.Id
}
