package arcade_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

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

func TestGameSeriesVersion_CreateAndUpdateAsModerator(t *testing.T) {
	app := newArcadeTestApp(t)
	headers := map[string]string{}

	token, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	headers["Authorization"] = "Bearer " + token

	seriesID := seedGameSeries(t, app, 12, "Moderator Series")

	createBody := fmt.Sprintf(`{
		"series": %q,
		"released_on": "2026-04-18",
		"en": "Moderator Version",
		"kr": "Moderator Version",
		"jp": "Moderator Version",
		"price_default": {
			"global": {
				"modes": [
					{"mode_key":"mode_1","label":"노멀","represent":true},
					{"mode_key":"mode_2","label":"EXTRA","represent":true},
					{"mode_key":"mode_3","label":"TIME PLAY (10m)","represent":false},
					{"mode_key":"mode_4","label":"TIME PLAY (16m)","represent":false}
				]
			},
			"countries": {
				"KR": {
					"modes": [
						{"mode_key":"mode_1","label":"노멀","represent":true},
						{"mode_key":"mode_2","label":"EXTRA","represent":true},
						{"mode_key":"mode_3","label":"TIME PLAY (10m)","represent":false},
						{"mode_key":"mode_4","label":"TIME PLAY (16m)","represent":false}
					]
				}
			}
		}
	}`, seriesID)

	createRes := executeJSONRequest(
		t,
		app,
		http.MethodPost,
		"/game_series_version",
		createBody,
		headers,
	)
	if createRes.StatusCode != http.StatusOK {
		t.Fatalf("expected create status 200, got %d", createRes.StatusCode)
	}
	defer createRes.Body.Close()

	var createPayload map[string]any
	if err := json.NewDecoder(createRes.Body).Decode(&createPayload); err != nil {
		t.Fatalf("failed to decode create response: %v", err)
	}

	version, ok := createPayload["version"].(map[string]any)
	if !ok {
		t.Fatalf("expected version object, got %T", createPayload["version"])
	}
	createdID, _ := version["id"].(string)
	if createdID == "" {
		t.Fatalf("expected created version id")
	}
	if got := version["en"]; got != "Moderator Version" {
		t.Fatalf("expected version en Moderator Version, got %v", got)
	}

	updateRes := executeJSONRequest(
		t,
		app,
		http.MethodPut,
		"/game_series_version",
		fmt.Sprintf(`{
			"id": %q,
			"series": %q,
			"released_on": "2026-04-19",
			"en": "Moderator Version Updated",
			"kr": "Moderator Version Updated",
			"jp": "Moderator Version Updated",
			"price_default": {
				"global": {
					"modes": [
						{"mode_key":"mode_1","label":"노멀","represent":true},
						{"mode_key":"mode_2","label":"EXTRA","represent":true},
						{"mode_key":"mode_3","label":"TIME PLAY (10m)","represent":false},
						{"mode_key":"mode_4","label":"TIME PLAY (16m)","represent":false}
					]
				},
				"countries": {
					"KR": {
						"modes": [
							{"mode_key":"mode_1","label":"노멀","represent":true},
							{"mode_key":"mode_2","label":"EXTRA","represent":true},
							{"mode_key":"mode_3","label":"TIME PLAY (10m)","represent":false},
							{"mode_key":"mode_4","label":"TIME PLAY (16m)","represent":false}
						]
					}
				}
			}
		}`, createdID, seriesID),
		headers,
	)
	if updateRes.StatusCode != http.StatusOK {
		t.Fatalf("expected update status 200, got %d", updateRes.StatusCode)
	}
	defer updateRes.Body.Close()

	var updatePayload map[string]any
	if err := json.NewDecoder(updateRes.Body).Decode(&updatePayload); err != nil {
		t.Fatalf("failed to decode update response: %v", err)
	}

	updatedVersion, ok := updatePayload["version"].(map[string]any)
	if !ok {
		t.Fatalf("expected version object, got %T", updatePayload["version"])
	}
	if got := updatedVersion["id"]; got != createdID {
		t.Fatalf("expected version id %q, got %v", createdID, got)
	}
	if got := updatedVersion["en"]; got != "Moderator Version Updated" {
		t.Fatalf("expected updated version en, got %v", got)
	}
	releasedOn, _ := updatedVersion["released_on"].(string)
	if !strings.HasPrefix(releasedOn, "2026-04-19") {
		t.Fatalf("expected updated released_on to start with 2026-04-19, got %v", updatedVersion["released_on"])
	}
}

func TestGameSeriesVersion_RejectsMissingRequiredFields(t *testing.T) {
	app := newArcadeTestApp(t)
	headers := map[string]string{}
	token, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	headers["Authorization"] = "Bearer " + token

	res := executeJSONRequest(t, app, http.MethodPost, "/game_series_version", `{"en":"Missing Fields"}`, headers)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", res.StatusCode)
	}
	defer res.Body.Close()

	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if got := payload["details"]; got == nil {
		t.Fatalf("expected validation details for missing required fields")
	}
}

func TestGameSeriesVersion_AllowsGlobalOnlyPriceDefault(t *testing.T) {
	app := newArcadeTestApp(t)
	headers := map[string]string{}
	token, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	headers["Authorization"] = "Bearer " + token

	seriesID := seedGameSeries(t, app, 13, "Global Only Series")

	body := fmt.Sprintf(`{
		"series": %q,
		"released_on": "2026-04-20",
		"en": "Global Only Version",
		"kr": "Global Only Version",
		"jp": "Global Only Version",
		"price_default": {
			"global": {
				"modes": [
					{"mode_key":"default","represent":true}
				]
			}
		}
	}`, seriesID)

	res := executeJSONRequest(t, app, http.MethodPost, "/game_series_version", body, headers)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", res.StatusCode)
	}
	defer res.Body.Close()

	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	version, ok := payload["version"].(map[string]any)
	if !ok {
		t.Fatalf("expected version object, got %T", payload["version"])
	}

	priceDefault, ok := version["price_default"].(map[string]any)
	if !ok {
		t.Fatalf("expected price_default object, got %T", version["price_default"])
	}
	if _, exists := priceDefault["countries"]; exists {
		t.Fatalf("expected countries to be omitted or absent, got %#v", priceDefault["countries"])
	}
}

func TestGameSeriesVersion_RejectsNonModerator(t *testing.T) {
	app := newArcadeTestApp(t)
	headers := map[string]string{}
	token, _ := createAuthUser(t, app)
	headers["Authorization"] = "Bearer " + token

	res := executeJSONRequest(t, app, http.MethodPost, "/game_series_version", `{"en":"No Access Version"}`, headers)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", res.StatusCode)
	}
	defer res.Body.Close()

	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if got := payload["error"]; got != "moderator access required" {
		t.Fatalf("expected moderator access error, got %v", got)
	}
}
