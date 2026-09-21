package arcade_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestGameSeriesVersion_SaveAndReadPriceDefault(t *testing.T) {
	app := newArcadeTestApp(t)

	coll, err := app.FindCollectionByNameOrId("game_series_version")
	if err != nil {
		t.Fatalf("failed to load game_series_version collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("en", "Price Default Version")
	rec.Set("price_default", map[string]any{
		"global": map[string]any{
			"modes": []any{
				map[string]any{
					"mode_key":  "normal",
					"label":     "NORMAL",
					"amount":    1000,
					"represent": true,
				},
				map[string]any{
					"mode_key":  "extra",
					"label":     "EXTRA",
					"represent": true,
				},
				map[string]any{
					"mode_key":  "time_10m",
					"label":     "TIME PLAY (10m)",
					"amount":    nil,
					"represent": false,
				},
			},
		},
		"countries": map[string]any{
			"KR": map[string]any{
				"modes": []any{
					map[string]any{
						"mode_key":  "normal",
						"label":     "노멀",
						"amount":    1000,
						"represent": true,
					},
				},
			},
			"JP": map[string]any{
				"modes": []any{
					map[string]any{
						"mode_key":  "default",
						"label":     nil,
						"represent": true,
					},
				},
			},
		},
	})

	if err := app.Save(rec); err != nil {
		t.Fatalf("expected price_default save to succeed: %v", err)
	}

	scenario := tests.ApiScenario{
		Name:           "GET /game_series_version returns price_default",
		Method:         http.MethodGet,
		URL:            "/game_series_version?id=" + rec.Id,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"price_default"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		coll, err := app.FindCollectionByNameOrId("game_series_version")
		if err != nil {
			tb.Fatalf("failed to load game_series_version collection: %v", err)
		}

		seed := core.NewRecord(coll)
		seed.Set("id", rec.Id)
		seed.Set("en", "Price Default Version")
		seed.Set("price_default", rec.Get("price_default"))
		if err := app.Save(seed); err != nil {
			tb.Fatalf("failed to seed game_series_version: %v", err)
		}
	}

	scenario.AfterTestFunc = func(tb testing.TB, _ *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		version, ok := payload["version"].(map[string]any)
		if !ok {
			tb.Fatalf("expected version object, got %T", payload["version"])
		}

		priceDefault, ok := version["price_default"].(map[string]any)
		if !ok {
			tb.Fatalf("expected price_default object, got %T", version["price_default"])
		}

		global, ok := priceDefault["global"].(map[string]any)
		if !ok {
			tb.Fatalf("expected global object, got %T", priceDefault["global"])
		}
		modes, ok := global["modes"].([]any)
		if !ok || len(modes) != 3 {
			tb.Fatalf("expected 3 global modes, got %T %#v", global["modes"], global["modes"])
		}

		first, ok := modes[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected first mode object, got %T", modes[0])
		}
		if got, _ := first["mode_key"].(string); got != "normal" {
			tb.Fatalf("expected first mode_key normal, got %v", first["mode_key"])
		}
		if got, _ := first["represent"].(bool); !got {
			tb.Fatalf("expected first represent=true, got %v", first["represent"])
		}

		third, ok := modes[2].(map[string]any)
		if !ok {
			tb.Fatalf("expected third mode object, got %T", modes[2])
		}
		if value, exists := third["amount"]; !exists || value != nil {
			tb.Fatalf("expected third amount=null, got exists=%v value=%v", exists, value)
		}

		countries, ok := priceDefault["countries"].(map[string]any)
		if !ok {
			tb.Fatalf("expected countries object, got %T", priceDefault["countries"])
		}
		jp, ok := countries["JP"].(map[string]any)
		if !ok {
			tb.Fatalf("expected countries.JP object, got %T", countries["JP"])
		}
		jpModes, ok := jp["modes"].([]any)
		if !ok || len(jpModes) != 1 {
			tb.Fatalf("expected one JP mode, got %T %#v", jp["modes"], jp["modes"])
		}
		jpMode, ok := jpModes[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected JP mode object, got %T", jpModes[0])
		}
		if value, exists := jpMode["label"]; !exists || value != nil {
			tb.Fatalf("expected JP label=null, got exists=%v value=%v", exists, value)
		}
	}

	scenario.Test(t)
}

func TestGameSeriesVersion_RejectsTooManyRepresentModes(t *testing.T) {
	app := newArcadeTestApp(t)

	coll, err := app.FindCollectionByNameOrId("game_series_version")
	if err != nil {
		t.Fatalf("failed to load game_series_version collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("en", "Too Many Represent")
	rec.Set("price_default", map[string]any{
		"global": map[string]any{
			"modes": []any{
				map[string]any{"mode_key": "a", "represent": true},
				map[string]any{"mode_key": "b", "represent": true},
				map[string]any{"mode_key": "c", "represent": true},
			},
		},
	})

	err = app.Save(rec)
	if err == nil {
		t.Fatal("expected save to fail for more than 2 represent modes")
	}
	if !strings.Contains(err.Error(), "at most 2 represent=true items") {
		t.Fatalf("expected represent validation error, got %v", err)
	}
}

func TestGameSeriesVersion_RejectsInvalidModeShape(t *testing.T) {
	app := newArcadeTestApp(t)

	coll, err := app.FindCollectionByNameOrId("game_series_version")
	if err != nil {
		t.Fatalf("failed to load game_series_version collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("en", "Invalid Price Default")
	rec.Set("price_default", map[string]any{
		"global": map[string]any{
			"modes": []any{
				map[string]any{
					"label":     "NORMAL",
					"amount":    0,
					"represent": true,
				},
			},
		},
	})

	err = app.Save(rec)
	if err == nil {
		t.Fatal("expected save to fail for invalid mode shape")
	}
	if !strings.Contains(err.Error(), "mode_key is required") && !strings.Contains(err.Error(), "amount must be > 0 or null") {
		t.Fatalf("expected mode shape validation error, got %v", err)
	}
}

func catalogVersionBody(tb testing.TB, id string, revision int, values map[string]any) string {
	tb.Helper()
	body := map[string]any{
		"entity":       "version",
		"operation_id": uuid.NewString(),
		"reason":       "Update catalog version in API test",
		"values":       values,
	}
	if id != "" {
		body["id"] = id
		body["expected_revision"] = revision
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		tb.Fatal(err)
	}
	return string(encoded)
}

func catalogVersionValues(seriesID, name, releasedOn string) map[string]any {
	return map[string]any{
		"series":      seriesID,
		"released_on": releasedOn,
		"en":          name,
		"kr":          name,
		"jp":          name,
		"price_default": map[string]any{
			"global": map[string]any{
				"modes": []any{map[string]any{"mode_key": "normal", "label": "NORMAL", "represent": true}},
			},
		},
	}
}

func TestGameSeriesVersion_CreateAndUpdateAsModerator(t *testing.T) {
	app := newArcadeTestApp(t)
	token, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	headers := map[string]string{"Authorization": "Bearer " + token}
	seriesID := seedGameSeries(t, app, 12, "Moderator Series")

	create := executeJSONRequest(t, app, http.MethodPost, "/moderation/game/catalog", catalogVersionBody(t, "", 0, catalogVersionValues(seriesID, "Moderator Version", "2026-04-18")), headers)
	defer create.Body.Close()
	if create.StatusCode != http.StatusOK {
		t.Fatalf("expected create status 200, got %d", create.StatusCode)
	}
	var created struct {
		Item map[string]any `json:"item"`
	}
	if err := json.NewDecoder(create.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	id, _ := created.Item["id"].(string)
	if id == "" || created.Item["en"] != "Moderator Version" {
		t.Fatalf("unexpected created version: %#v", created.Item)
	}

	update := executeJSONRequest(t, app, http.MethodPut, "/moderation/game/catalog", catalogVersionBody(t, id, 1, catalogVersionValues(seriesID, "Moderator Version Updated", "2026-04-19")), headers)
	defer update.Body.Close()
	if update.StatusCode != http.StatusOK {
		t.Fatalf("expected update status 200, got %d", update.StatusCode)
	}
	var updated struct {
		Item map[string]any `json:"item"`
	}
	if err := json.NewDecoder(update.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.Item["id"] != id || updated.Item["en"] != "Moderator Version Updated" {
		t.Fatalf("unexpected updated version: %#v", updated.Item)
	}
}

func TestGameSeriesVersion_RejectsMissingRequiredFields(t *testing.T) {
	app := newArcadeTestApp(t)
	token, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	res := executeJSONRequest(t, app, http.MethodPost, "/moderation/game/catalog", catalogVersionBody(t, "", 0, map[string]any{"en": "Missing Fields"}), map[string]string{"Authorization": "Bearer " + token})
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", res.StatusCode)
	}
}

func TestGameSeriesVersion_AllowsGlobalOnlyPriceDefault(t *testing.T) {
	app := newArcadeTestApp(t)
	token, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	seriesID := seedGameSeries(t, app, 13, "Global Only Series")
	res := executeJSONRequest(t, app, http.MethodPost, "/moderation/game/catalog", catalogVersionBody(t, "", 0, catalogVersionValues(seriesID, "Global Only Version", "2026-04-20")), map[string]string{"Authorization": "Bearer " + token})
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", res.StatusCode)
	}
	var payload struct {
		Item map[string]any `json:"item"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	price, _ := payload.Item["price_default"].(map[string]any)
	if price == nil || price["global"] == nil {
		t.Fatalf("expected global price default, got %#v", price)
	}
	if _, exists := price["countries"]; exists {
		t.Fatalf("expected countries to be absent, got %#v", price)
	}
}

func TestGameSeriesVersion_RejectsNonModerator(t *testing.T) {
	app := newArcadeTestApp(t)
	token, _ := createAuthUser(t, app)
	res := executeJSONRequest(t, app, http.MethodPost, "/moderation/game/catalog", catalogVersionBody(t, "", 0, map[string]any{"en": "No Access Version"}), map[string]string{"Authorization": "Bearer " + token})
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", res.StatusCode)
	}
}

func TestGameSeriesVersion_LegacyMutationRoutesAreAbsent(t *testing.T) {
	app := newArcadeTestApp(t)
	token, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	headers := map[string]string{"Authorization": "Bearer " + token}
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		res := executeJSONRequest(t, app, method, "/game_series_version", `{}`, headers)
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("expected legacy %s route to be absent, got %d", method, res.StatusCode)
		}
	}
}
