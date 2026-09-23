package arcade_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestGameCatalog_LocalizesCompatibilityAndCabinetDefaults(t *testing.T) {
	app := newArcadeTestApp(t)

	seriesCollection, err := app.FindCollectionByNameOrId("game_series")
	if err != nil {
		t.Fatal(err)
	}
	series := core.NewRecord(seriesCollection)
	series.Set("en", "Rhythm Series")
	series.Set("kr", "리듬 시리즈")
	series.Set("jp", "リズムシリーズ")
	series.Set("en_short", "Rhythm")
	series.Set("kr_short", "리듬")
	series.Set("jp_short", "リズム")
	series.Set("hide_at", []string{"jp"})
	series.Set("seriesNumber", 2)
	if err := app.Save(series); err != nil {
		t.Fatal(err)
	}

	versionCollection, err := app.FindCollectionByNameOrId("game_series_version")
	if err != nil {
		t.Fatal(err)
	}
	version := core.NewRecord(versionCollection)
	version.Set("series", series.Id)
	version.Set("en", "Rhythm 2026")
	version.Set("kr", "리듬 2026")
	version.Set("jp", "リズム 2026")
	version.Set("released_on", "2026-01-01")
	version.Set("price_default", map[string]any{"global": map[string]any{"modes": []any{map[string]any{"mode_key": "normal", "represent": true}}}})
	if err := app.Save(version); err != nil {
		t.Fatal(err)
	}

	compatibleCabinet := seedGameCabinet(t, app, "Gold")
	unsupportedCabinet := seedGameCabinet(t, app, "Unlisted")
	linkVersionCabinet(t, app, version.Id, compatibleCabinet)
	compatibility, err := app.FindRecordsByFilter("game_series_version_cabinet", "version={:version} && cabinet={:cabinet}", "", 1, 0, map[string]any{"version": version.Id, "cabinet": compatibleCabinet})
	if err != nil || len(compatibility) != 1 {
		t.Fatalf("load compatibility row: err=%v rows=%d", err, len(compatibility))
	}
	compatibility[0].Set("price_default", map[string]any{"global": map[string]any{"modes": []any{map[string]any{"mode_key": "gold", "represent": true}}}})
	if err := app.Save(compatibility[0]); err != nil {
		t.Fatal(err)
	}

	response := executeJSONRequest(t, app, http.MethodGet, "/game/catalog?locale=ko-KR", "", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.StatusCode)
	}
	var payload struct {
		Series []struct {
			ID           string   `json:"id"`
			Name         string   `json:"name"`
			FullName     string   `json:"full_name"`
			HideAt       []string `json:"hide_at"`
			SeriesNumber int      `json:"series_number"`
			Manufacturer *struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"manufacturer"`
		} `json:"series"`
		Versions []struct {
			ID           string         `json:"id"`
			SeriesID     string         `json:"series_id"`
			Name         string         `json:"name"`
			ReleasedOn   string         `json:"released_on"`
			PriceDefault map[string]any `json:"price_default"`
			Cabinets     []struct {
				ID           string         `json:"id"`
				Name         string         `json:"name"`
				PriceDefault map[string]any `json:"price_default"`
			} `json:"cabinets"`
		} `json:"versions"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Series) != 1 || payload.Series[0].ID != series.Id || payload.Series[0].Name != "리듬" || payload.Series[0].SeriesNumber != 2 {
		t.Fatalf("unexpected localized series: %#v", payload.Series)
	}
	if payload.Series[0].FullName != "리듬 시리즈" || len(payload.Series[0].HideAt) != 1 || payload.Series[0].HideAt[0] != "jp" {
		t.Fatalf("expected full name and visibility codes: %#v", payload.Series[0])
	}
	if payload.Series[0].Manufacturer != nil {
		t.Fatalf("expected null manufacturer for series without manufacturer, got %#v", payload.Series[0].Manufacturer)
	}
	if len(payload.Versions) != 1 {
		t.Fatalf("expected one version, got %#v", payload.Versions)
	}
	gotVersion := payload.Versions[0]
	if gotVersion.ID != version.Id || gotVersion.SeriesID != series.Id || gotVersion.Name != "리듬 2026" || gotVersion.ReleasedOn == "" || gotVersion.PriceDefault["global"] == nil {
		t.Fatalf("unexpected version: %#v", gotVersion)
	}
	if len(gotVersion.Cabinets) != 1 || gotVersion.Cabinets[0].ID != compatibleCabinet || gotVersion.Cabinets[0].Name != "Gold" || gotVersion.Cabinets[0].PriceDefault["global"] == nil {
		t.Fatalf("unexpected compatible cabinets: %#v", gotVersion.Cabinets)
	}
	if gotVersion.Cabinets[0].ID == unsupportedCabinet {
		t.Fatal("unsupported cabinet must not be returned by the catalog")
	}
}

func TestGameCatalog_RequiresSupportedLocale(t *testing.T) {
	app := newArcadeTestApp(t)
	response := executeJSONRequest(t, app, http.MethodGet, "/game/catalog?locale=fr-FR", "", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", response.StatusCode)
	}
}

func TestGameCatalog_OrdersSeriesAndVersions(t *testing.T) {
	app := newArcadeTestApp(t)
	secondSeries := seedGameSeries(t, app, 20, "Second")
	firstSeries := seedGameSeries(t, app, 10, "First")
	oldVersion := seedGameSeriesVersionWithSeries(t, app, secondSeries, "2025-01-01", "Old")
	newVersion := seedGameSeriesVersionWithSeries(t, app, firstSeries, "2026-01-01", "New")

	response := executeJSONRequest(t, app, http.MethodGet, "/game/catalog?locale=en-US", "", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.StatusCode)
	}
	var payload struct {
		Series []struct {
			ID           string `json:"id"`
			SeriesNumber int    `json:"series_number"`
		} `json:"series"`
		Versions []struct {
			ID string `json:"id"`
		} `json:"versions"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Series) != 2 || payload.Series[0].ID != firstSeries || payload.Series[0].SeriesNumber != 10 || payload.Series[1].ID != secondSeries {
		t.Fatalf("series must be ordered by seriesNumber: %#v", payload.Series)
	}
	if len(payload.Versions) != 2 || payload.Versions[0].ID != newVersion || payload.Versions[1].ID != oldVersion {
		t.Fatalf("versions must be ordered by latest release: %#v", payload.Versions)
	}
}

func TestGameCatalog_IncludesManufacturer(t *testing.T) {
	app := newArcadeTestApp(t)

	manufacturerColl, err := app.FindCollectionByNameOrId("game_manufacturer")
	if err != nil {
		t.Fatal(err)
	}
	manufacturer := core.NewRecord(manufacturerColl)
	manufacturer.Set("en", "KONAMI")
	manufacturer.Set("kr", "코나미")
	manufacturer.Set("jp", "コナミ")
	if err := app.Save(manufacturer); err != nil {
		t.Fatal(err)
	}

	seriesColl, err := app.FindCollectionByNameOrId("game_series")
	if err != nil {
		t.Fatal(err)
	}
	withMfr := core.NewRecord(seriesColl)
	withMfr.Set("en", "With Manufacturer")
	withMfr.Set("kr", "제조사 있음")
	withMfr.Set("en_short", "With Mfr")
	withMfr.Set("kr_short", "제조사")
	withMfr.Set("seriesNumber", 1)
	withMfr.Set("manufacturer", manufacturer.Id)
	if err := app.Save(withMfr); err != nil {
		t.Fatal(err)
	}

	withoutMfr := core.NewRecord(seriesColl)
	withoutMfr.Set("en", "Without Manufacturer")
	withoutMfr.Set("kr", "제조사 없음")
	withoutMfr.Set("en_short", "No Mfr")
	withoutMfr.Set("kr_short", "없음")
	withoutMfr.Set("seriesNumber", 2)
	if err := app.Save(withoutMfr); err != nil {
		t.Fatal(err)
	}

	response := executeJSONRequest(t, app, http.MethodGet, "/game/catalog?locale=ko-KR", "", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.StatusCode)
	}
	var payload struct {
		Series []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			SeriesNumber int    `json:"series_number"`
			Manufacturer *struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"manufacturer"`
		} `json:"series"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Series) != 2 {
		t.Fatalf("expected 2 series, got %d: %#v", len(payload.Series), payload.Series)
	}
	// Series are ordered by series_number, so index 0 is withMfr (1), index 1 is withoutMfr (2).
	got := payload.Series[0]
	if got.ID != withMfr.Id {
		t.Fatalf("first series should be withMfr (seriesNumber=1), got %s", got.ID)
	}
	if got.Manufacturer == nil {
		t.Fatal("expected manufacturer to be present for series with manufacturer")
	}
	if got.Manufacturer.ID != manufacturer.Id {
		t.Fatalf("expected manufacturer id %s, got %s", manufacturer.Id, got.Manufacturer.ID)
	}
	if got.Manufacturer.Name != "코나미" {
		t.Fatalf("expected localized manufacturer name '코나미', got %q", got.Manufacturer.Name)
	}

	gotWithout := payload.Series[1]
	if gotWithout.ID != withoutMfr.Id {
		t.Fatalf("second series should be withoutMfr, got %s", gotWithout.ID)
	}
	if gotWithout.Manufacturer != nil {
		t.Fatalf("expected null manufacturer for series without manufacturer, got %#v", gotWithout.Manufacturer)
	}
}
