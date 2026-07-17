package arcade_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

func TestUpdateArcadeGame_PriceValidation(t *testing.T) {
	type testCase struct {
		name   string
		price  string
		detail string
	}

	cases := []testCase{
		{
			name:   "missing currency",
			price:  `{"type":"custom","list":[{"value":500}],"accept":[]}`,
			detail: `"details":"games[0].price.currency is required"`,
		},
		{
			name:   "missing type",
			price:  `{"currency":"KRW","list":[{"value":500}],"accept":[]}`,
			detail: `"details":"games[0].price.type is required"`,
		},
		{
			name:   "invalid type enum",
			price:  `{"currency":"KRW","type":"package","list":[{"value":500}],"accept":[]}`,
			detail: `"details":"games[0].price.type must be one of gamemode, credit, song, time, free, custom"`,
		},
		{
			name:   "list validation kept",
			price:  `{"currency":"KRW","type":"free","list":[],"accept":[]}`,
			detail: `"details":"games[0].price.list must have at least 1 item"`,
		},
		{
			name:   "value must be positive or null",
			price:  `{"currency":"KRW","type":"custom","list":[{"value":0}],"accept":[]}`,
			detail: `"details":"games[0].price.list[0].value must be \u003e 0 or null"`,
		},
		{
			name:   "accept validation kept",
			price:  `{"currency":"KRW","type":"custom","list":[{"value":500}],"accept":["Invalid"]}`,
			detail: `"details":"games[0].price.accept[0] must be one of enum values"`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			headers := map[string]string{}
			scenario := tests.ApiScenario{
				Name:           "PUT /arcade/game " + tc.name,
				Method:         http.MethodPut,
				URL:            "/arcade/game",
				Headers:        headers,
				ExpectedStatus: http.StatusBadRequest,
				ExpectedContent: []string{
					`"error":"validation failed"`,
					tc.detail,
				},
				TestAppFactory: func(tb testing.TB) *tests.TestApp {
					return newArcadeTestApp(tb)
				},
			}

			scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				tb.Helper()

				token, user := createAuthUser(tb, app)
				headers["Authorization"] = "Bearer " + token

				arcadeID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
					Name:     "Update Target Arcade",
					Address:  "Validation Street",
					Nickname: []string{"Validation"},
					Location: location{Lat: 37.5665, Lon: 126.978},
				})
				versionID := seedGameSeriesVersion(tb, app)

				scenario.Body = strings.NewReader(fmt.Sprintf(`{
					"arcade":"%s",
					"games":[
						{
							"game":"%s",
							"location":"1F",
							"quantity":1,
							"price":%s,
							"tag":[{"category":"기타","quantity":1,"note":"ok"}]
						}
					]
				}`, arcadeID, versionID, tc.price))
			}

			scenario.Test(t)
		})
	}
}

func TestUpdateArcadeGame_AllowsEmptyArray(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/game allows empty games array",
		Method:         http.MethodPut,
		URL:            "/arcade/game",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"count":0`,
			`"game":{"id":"`,
			`"items":[]`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Empty Game Arcade",
			Address:  "Empty Street",
			Nickname: []string{"Empty"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"games":[]
		}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object in response, got %T", payload["game"])
		}
		moleculeID, _ := gameObj["id"].(string)
		if moleculeID == "" {
			tb.Fatalf("expected game molecule id in response")
		}
		items, ok := gameObj["items"].([]any)
		if !ok {
			tb.Fatalf("expected game.items array, got %T", gameObj["items"])
		}
		if len(items) != 0 {
			tb.Fatalf("expected empty game.items array, got %#v", items)
		}

		atoms, err := app.FindRecordsByFilter("arcade_game_atoms", "molecule={:id}", "", 0, 0, dbx.Params{"id": moleculeID})
		if err != nil {
			tb.Fatalf("failed to load game atoms: %v", err)
		}
		if len(atoms) != 0 {
			tb.Fatalf("expected no game atoms, got %d", len(atoms))
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeGame_StoresPriceAcceptAsEmptyArray(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/game stores normalized price",
		Method:         http.MethodPut,
		URL:            "/arcade/game",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"count":1`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Store Arcade",
			Address:  "Store Street",
			Nickname: []string{"Store"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		versionID := seedGameSeriesVersion(tb, app)

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"games":[
				{
					"game":"%s",
					"location":"B1",
					"quantity":2,
					"price":{
						"currency":"JPY",
						"type":"custom",
						"list":[{"value":1000}]
					},
					"tag":[{"category":"기타","quantity":1,"note":"ok"}]
				}
			]
		}`, arcadeID, versionID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object in response, got %T", payload["game"])
		}
		moleculeID, _ := gameObj["id"].(string)
		if moleculeID == "" {
			tb.Fatalf("expected game molecule id in response")
		}

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade record: %v", err)
		}
		if got := arcadeRec.GetString("game"); got != moleculeID {
			tb.Fatalf("expected arcade.game=%q, got %q", moleculeID, got)
		}

		atoms, err := app.FindRecordsByFilter("arcade_game_atoms", "molecule={:id}", "", 0, 0, dbx.Params{"id": moleculeID})
		if err != nil {
			tb.Fatalf("failed to load game atoms: %v", err)
		}
		if len(atoms) != 1 {
			tb.Fatalf("expected 1 game atom, got %d", len(atoms))
		}

		var price map[string]any
		buf, _ := json.Marshal(atoms[0].Get("price"))
		if err := json.Unmarshal(buf, &price); err != nil {
			tb.Fatalf("failed to decode stored price: %v", err)
		}

		if got, _ := price["currency"].(string); got != "JPY" {
			tb.Fatalf("expected price.currency JPY, got %v", price["currency"])
		}
		if got, _ := price["type"].(string); got != "custom" {
			tb.Fatalf("expected price.type custom, got %v", price["type"])
		}

		accept, ok := price["accept"].([]any)
		if !ok {
			tb.Fatalf("expected price.accept to be []any, got %T (%v)", price["accept"], price["accept"])
		}
		if len(accept) != 0 {
			tb.Fatalf("expected empty accept array, got %v", accept)
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeGame_IgnoresTagQuantityWhenStoring(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/game ignores tag quantity when storing",
		Method:         http.MethodPut,
		URL:            "/arcade/game",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"count":1`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Tag Quantity Arcade",
			Address:  "Tag Street",
			Nickname: []string{"Tag"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		versionID := seedGameSeriesVersion(tb, app)

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"games":[
				{
					"game":"%s",
					"location":"B2",
					"quantity":1,
					"price":{
						"currency":"KRW",
						"type":"custom",
						"list":[{"value":1500}],
						"accept":[]
					},
					"tag":[{"category":"기타","quantity":0,"note":"ignore me"}]
				}
			]
		}`, arcadeID, versionID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object in response, got %T", payload["game"])
		}
		moleculeID, _ := gameObj["id"].(string)
		if moleculeID == "" {
			tb.Fatalf("expected game molecule id in response")
		}

		items, ok := gameObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected one game item, got %T %#v", gameObj["items"], gameObj["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected game item object, got %T", items[0])
		}
		tags, ok := item["tag"].([]any)
		if !ok || len(tags) != 1 {
			tb.Fatalf("expected one tag entry, got %T %#v", item["tag"], item["tag"])
		}
		tag, ok := tags[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected tag object, got %T", tags[0])
		}
		if _, exists := tag["quantity"]; exists {
			tb.Fatalf("expected response tag to omit quantity, got %#v", tag)
		}

		atoms, err := app.FindRecordsByFilter("arcade_game_atoms", "molecule={:id}", "", 0, 0, dbx.Params{"id": moleculeID})
		if err != nil {
			tb.Fatalf("failed to load game atoms: %v", err)
		}
		if len(atoms) != 1 {
			tb.Fatalf("expected 1 game atom, got %d", len(atoms))
		}

		var storedTags []map[string]any
		buf, _ := json.Marshal(atoms[0].Get("tag"))
		if err := json.Unmarshal(buf, &storedTags); err != nil {
			tb.Fatalf("failed to decode stored tag: %v", err)
		}
		if len(storedTags) != 1 {
			tb.Fatalf("expected one stored tag, got %#v", storedTags)
		}
		if _, exists := storedTags[0]["quantity"]; exists {
			tb.Fatalf("expected stored tag to omit quantity, got %#v", storedTags[0])
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeGame_AllowsNullPriceValue(t *testing.T) {
	headers := map[string]string{}
	var moleculeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/game allows null price value",
		Method:         http.MethodPut,
		URL:            "/arcade/game",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"count":1`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Null Value Arcade",
			Address:  "Null Street",
			Nickname: []string{"Null"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		versionID := seedGameSeriesVersion(tb, app)

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"games":[
				{
					"game":"%s",
					"location":"2F",
					"quantity":1,
					"price":{
						"currency":"KRW",
						"type":"custom",
						"list":[{"value":null}],
						"accept":[]
					},
					"tag":[{"category":"기타","quantity":1,"note":"ok"}]
				}
			]
		}`, arcadeID, versionID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object in response, got %T", payload["game"])
		}
		moleculeID, _ = gameObj["id"].(string)
		if moleculeID == "" {
			tb.Fatalf("expected game molecule id in response")
		}

		atoms, err := app.FindRecordsByFilter("arcade_game_atoms", "molecule={:id}", "", 0, 0, dbx.Params{"id": moleculeID})
		if err != nil {
			tb.Fatalf("failed to load game atoms: %v", err)
		}
		if len(atoms) != 1 {
			tb.Fatalf("expected 1 game atom, got %d", len(atoms))
		}

		var price map[string]any
		buf, _ := json.Marshal(atoms[0].Get("price"))
		if err := json.Unmarshal(buf, &price); err != nil {
			tb.Fatalf("failed to decode stored price: %v", err)
		}

		list, ok := price["list"].([]any)
		if !ok || len(list) != 1 {
			tb.Fatalf("expected one price.list item, got %T %#v", price["list"], price["list"])
		}
		item, ok := list[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected price.list[0] object, got %T", list[0])
		}
		if value, exists := item["value"]; !exists || value != nil {
			tb.Fatalf("expected price.list[0].value to be null, got %v (exists=%v)", value, exists)
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeGame_AllowsMissingLocation(t *testing.T) {
	headers := map[string]string{}

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/game allows missing location",
		Method:         http.MethodPut,
		URL:            "/arcade/game",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"count":1`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "No Location Arcade",
			Address:  "No Location Street",
			Nickname: []string{"NoLocation"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		versionID := seedGameSeriesVersion(tb, app)

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"games":[
				{
					"game":"%s",
					"quantity":1,
					"price":{
						"currency":"KRW",
						"type":"custom",
						"list":[{"value":500}],
						"accept":[]
					},
					"tag":[{"category":"기타","quantity":1,"note":"ok"}]
				}
			]
		}`, arcadeID, versionID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object in response, got %T", payload["game"])
		}
		moleculeID, _ := gameObj["id"].(string)
		if moleculeID == "" {
			tb.Fatalf("expected game molecule id in response")
		}

		atoms, err := app.FindRecordsByFilter("arcade_game_atoms", "molecule={:id}", "", 0, 0, dbx.Params{"id": moleculeID})
		if err != nil {
			tb.Fatalf("failed to load game atoms: %v", err)
		}
		if len(atoms) != 1 {
			tb.Fatalf("expected 1 game atom, got %d", len(atoms))
		}
		if got := atoms[0].GetString("location"); got != "" {
			tb.Fatalf("expected empty location for missing input, got %q", got)
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeGame_PreservesGamemodeMetadata(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/game preserves gamemode metadata",
		Method:         http.MethodPut,
		URL:            "/arcade/game",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"count":1`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Gamemode Arcade",
			Address:  "Gamemode Street",
			Nickname: []string{"Gamemode"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		versionID := seedGameSeriesVersion(tb, app)

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"games":[
				{
					"game":"%s",
					"location":"3F",
					"quantity":1,
					"price":{
						"currency":"KRW",
						"type":"gamemode",
						"list":[
							{"title":"NORMAL","value":1000,"mode_key":"normal","represent":true},
							{"title":"LIGHT","value":500,"mode_key":"light","represent":false}
						],
						"accept":["Cash"]
					},
					"tag":[{"category":"기타","quantity":1,"note":"ok"}]
				}
			]
		}`, arcadeID, versionID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object in response, got %T", payload["game"])
		}
		moleculeID, _ := gameObj["id"].(string)
		if moleculeID == "" {
			tb.Fatalf("expected game molecule id in response")
		}

		atoms, err := app.FindRecordsByFilter("arcade_game_atoms", "molecule={:id}", "", 0, 0, dbx.Params{"id": moleculeID})
		if err != nil {
			tb.Fatalf("failed to load game atoms: %v", err)
		}
		if len(atoms) != 1 {
			tb.Fatalf("expected 1 game atom, got %d", len(atoms))
		}

		var storedPrice map[string]any
		buf, _ := json.Marshal(atoms[0].Get("price"))
		if err := json.Unmarshal(buf, &storedPrice); err != nil {
			tb.Fatalf("failed to decode stored price: %v", err)
		}

		storedList, ok := storedPrice["list"].([]any)
		if !ok || len(storedList) != 2 {
			tb.Fatalf("expected two stored price.list items, got %T %#v", storedPrice["list"], storedPrice["list"])
		}

		firstStored, ok := storedList[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected first stored price item object, got %T", storedList[0])
		}
		if got, _ := firstStored["mode_key"].(string); got != "normal" {
			tb.Fatalf("expected first stored mode_key normal, got %v", firstStored["mode_key"])
		}
		if got, _ := firstStored["represent"].(bool); !got {
			tb.Fatalf("expected first stored represent=true, got %v", firstStored["represent"])
		}

		secondStored, ok := storedList[1].(map[string]any)
		if !ok {
			tb.Fatalf("expected second stored price item object, got %T", storedList[1])
		}
		if got, _ := secondStored["mode_key"].(string); got != "light" {
			tb.Fatalf("expected second stored mode_key light, got %v", secondStored["mode_key"])
		}
		represent, exists := secondStored["represent"]
		if !exists {
			tb.Fatalf("expected second stored represent field")
		}
		if got, ok := represent.(bool); !ok || got {
			tb.Fatalf("expected second stored represent=false, got %v", represent)
		}

	}

	scenario.Test(t)
}

func TestUpdateArcadeGame_InheritsFlagsFromPrevAtom(t *testing.T) {
	headers := map[string]string{}
	var inheritedFlagID string
	var arcadeID string
	var prevAtomID string
	var userID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/game inherits flags from prev_id",
		Method:         http.MethodPut,
		URL:            "/arcade/game",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"count":1`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token
		userID = user.Id

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Prev Flag Arcade",
			Address:  "Prev Street",
			Nickname: []string{"Prev"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		versionID := seedGameSeriesVersion(tb, app)
		seedGameAtom(tb, app, arcadeID, versionID, map[string]any{
			"currency": "KRW",
			"type":     "custom",
			"list":     []map[string]any{{"value": 500}},
			"accept":   []string{"Cash"},
		})

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		prevMoleculeID := arcadeRec.GetString("game")
		if prevMoleculeID == "" {
			tb.Fatalf("expected previous game molecule id")
		}

		prevAtoms, err := app.FindRecordsByFilter("arcade_game_atoms", "molecule={:id}", "", 0, 0, dbx.Params{"id": prevMoleculeID})
		if err != nil {
			tb.Fatalf("failed to load previous game atoms: %v", err)
		}
		if len(prevAtoms) != 1 {
			tb.Fatalf("expected 1 previous game atom, got %d", len(prevAtoms))
		}

		flagColl, err := app.FindCollectionByNameOrId("arcade_flag")
		if err != nil {
			tb.Fatalf("failed to load arcade_flag collection: %v", err)
		}
		flagRec := core.NewRecord(flagColl)
		flagRec.Set("arcade", arcadeID)
		flagRec.Set("disruption", "major")
		flagRec.Set("solved", false)
		flagRec.Set("message", "inherit me")
		flagRec.Set("createdBy", user.Id)
		if err := app.Save(flagRec); err != nil {
			tb.Fatalf("failed to save arcade_flag: %v", err)
		}
		inheritedFlagID = flagRec.Id

		prevAtoms[0].Set("flags", []string{inheritedFlagID})
		if err := app.Save(prevAtoms[0]); err != nil {
			tb.Fatalf("failed to set flags on previous atom: %v", err)
		}

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"games":[
				{
					"game":"%s",
					"prev_id":"%s",
					"location":"2F",
					"quantity":1,
					"price":{
						"currency":"KRW",
						"type":"custom",
						"list":[{"value":700}],
						"accept":[]
					},
					"tag":[{"category":"기타","quantity":1,"note":"ok"}]
				}
			]
		}`, arcadeID, versionID, prevAtoms[0].Id))
		prevAtomID = prevAtoms[0].Id
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object in response, got %T", payload["game"])
		}
		moleculeID, _ := gameObj["id"].(string)
		if moleculeID == "" {
			tb.Fatalf("expected game molecule id in response")
		}

		atoms, err := app.FindRecordsByFilter("arcade_game_atoms", "molecule={:id}", "", 0, 0, dbx.Params{"id": moleculeID})
		if err != nil {
			tb.Fatalf("failed to load game atoms: %v", err)
		}
		if len(atoms) != 1 {
			tb.Fatalf("expected 1 game atom, got %d", len(atoms))
		}

		flags := atoms[0].GetStringSlice("flags")
		if len(flags) != 1 {
			tb.Fatalf("expected inherited single flag, got %#v", flags)
		}
		if flags[0] != inheritedFlagID {
			tb.Fatalf("expected inherited flag %q, got %q", inheritedFlagID, flags[0])
		}

		changes, err := app.FindRecordsByFilter("arcade_changelog", "arcade={:id} && changed='game'", "-created", 0, 0, dbx.Params{"id": arcadeID})
		if err != nil {
			tb.Fatalf("failed to load arcade_changelog: %v", err)
		}
		if len(changes) != 1 {
			tb.Fatalf("expected 1 game changelog row, got %d", len(changes))
		}
		change := changes[0]
		if got := change.GetString("by"); got != userID {
			tb.Fatalf("expected changelog.by=%q, got %q", userID, got)
		}
		if got := change.GetString("to"); got != moleculeID {
			tb.Fatalf("expected changelog.to=%q, got %q", moleculeID, got)
		}

		rawLog := change.Get("log")
		if rawLog == nil {
			tb.Fatalf("expected changelog.log to be set")
		}

		var logObj map[string]any
		buf, _ := json.Marshal(rawLog)
		if err := json.Unmarshal(buf, &logObj); err != nil {
			tb.Fatalf("failed to decode changelog.log: %v", err)
		}

		if got, _ := logObj["type"].(string); got != "game_diff" {
			tb.Fatalf("expected changelog.log.type=game_diff, got %v", logObj["type"])
		}
		if got, _ := logObj["version"].(float64); got != 1 {
			tb.Fatalf("expected changelog.log.version=1, got %v", logObj["version"])
		}

		items, ok := logObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected changelog.log.items size 1, got %T %#v", logObj["items"], logObj["items"])
		}
		itemObj, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected log.items[0] object, got %T", items[0])
		}
		if got, _ := itemObj["prev_id"].(string); got != prevAtomID {
			tb.Fatalf("expected log.prev_id=%q, got %v", prevAtomID, itemObj["prev_id"])
		}
		if got, _ := itemObj["change_type"].(string); got != "updated" {
			tb.Fatalf("expected log.change_type=updated, got %v", itemObj["change_type"])
		}

		bullets, ok := itemObj["bullets"].([]any)
		if !ok || len(bullets) == 0 {
			tb.Fatalf("expected non-empty log bullets, got %T %#v", itemObj["bullets"], itemObj["bullets"])
		}
		bulletSet := i18nBulletKeySet(bullets)
		if !bulletSet["arcade.changelog.game.location.changed"] {
			tb.Fatalf("expected location diff bullet, got %#v", bulletSet)
		}
		if !bulletSet["arcade.changelog.game.price.changed"] {
			tb.Fatalf("expected price diff bullet, got %#v", bulletSet)
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeGame_LogsUnchangedWhenNoDiff(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string
	var prevAtomID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/game writes unchanged log item when no diff",
		Method:         http.MethodPut,
		URL:            "/arcade/game",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"count":1`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "No Diff Arcade",
			Address:  "No Diff Street",
			Nickname: []string{"NoDiff"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		versionID := seedGameSeriesVersion(tb, app)
		seedGameAtom(tb, app, arcadeID, versionID, map[string]any{
			"currency": "KRW",
			"type":     "custom",
			"list":     []map[string]any{{"value": 500}},
			"accept":   []string{"Cash"},
		})

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		prevMoleculeID := arcadeRec.GetString("game")
		if prevMoleculeID == "" {
			tb.Fatalf("expected previous game molecule id")
		}
		prevAtoms, err := app.FindRecordsByFilter("arcade_game_atoms", "molecule={:id}", "", 0, 0, dbx.Params{"id": prevMoleculeID})
		if err != nil {
			tb.Fatalf("failed to load previous game atoms: %v", err)
		}
		if len(prevAtoms) != 1 {
			tb.Fatalf("expected 1 previous game atom, got %d", len(prevAtoms))
		}
		prevAtomID = prevAtoms[0].Id

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"games":[
				{
					"game":"%s",
					"prev_id":"%s",
					"location":"1F",
					"quantity":1,
					"price":{
						"currency":"KRW",
						"type":"custom",
						"list":[{"value":500}],
						"accept":["Cash"]
					},
					"tag":[{"category":"기타","quantity":1,"note":"ok"}]
				}
			]
		}`, arcadeID, versionID, prevAtomID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		changes, err := app.FindRecordsByFilter("arcade_changelog", "arcade={:id} && changed='game'", "-created", 0, 0, dbx.Params{"id": arcadeID})
		if err != nil {
			tb.Fatalf("failed to load arcade_changelog: %v", err)
		}
		if len(changes) != 1 {
			tb.Fatalf("expected 1 game changelog row, got %d", len(changes))
		}

		var logObj map[string]any
		buf, _ := json.Marshal(changes[0].Get("log"))
		if err := json.Unmarshal(buf, &logObj); err != nil {
			tb.Fatalf("failed to decode changelog.log: %v", err)
		}
		items, ok := logObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected changelog.log.items size 1, got %T %#v", logObj["items"], logObj["items"])
		}
		itemObj, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected log item object, got %T", items[0])
		}
		if got, _ := itemObj["prev_id"].(string); got != prevAtomID {
			tb.Fatalf("expected log.prev_id=%q, got %v", prevAtomID, itemObj["prev_id"])
		}
		if got, _ := itemObj["change_type"].(string); got != "unchanged" {
			tb.Fatalf("expected log.change_type=unchanged, got %v", itemObj["change_type"])
		}
		bullets, ok := itemObj["bullets"].([]any)
		if !ok || len(bullets) == 0 {
			tb.Fatalf("expected non-empty log bullets, got %T %#v", itemObj["bullets"], itemObj["bullets"])
		}
		keys := i18nBulletKeySet(bullets)
		if !keys["arcade.changelog.game.no_changes"] {
			tb.Fatalf("expected no_changes bullet key, got %#v", keys)
		}
		if _, exists := itemObj["diff"]; exists {
			tb.Fatalf("expected unchanged item to omit diff field, got %v", itemObj["diff"])
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeGame_LogsDeletedPrevGameAtom(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string
	var userID string
	var keptPrevID string
	var deletedPrevID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/game writes deleted log items",
		Method:         http.MethodPut,
		URL:            "/arcade/game",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"count":1`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token
		userID = user.Id

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Delete Log Arcade",
			Address:  "Delete Log Street",
			Nickname: []string{"DeleteLog"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		versionID := seedGameSeriesVersion(tb, app)
		seedGameAtom(tb, app, arcadeID, versionID, map[string]any{
			"currency": "KRW",
			"type":     "custom",
			"list":     []map[string]any{{"value": 500}},
			"accept":   []string{"Cash"},
		})

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		prevMoleculeID := arcadeRec.GetString("game")
		if prevMoleculeID == "" {
			tb.Fatalf("expected linked game molecule id")
		}

		atomColl, err := app.FindCollectionByNameOrId("arcade_game_atoms")
		if err != nil {
			tb.Fatalf("failed to load arcade_game_atoms collection: %v", err)
		}
		secondAtom := core.NewRecord(atomColl)
		secondAtom.Set("molecule", prevMoleculeID)
		secondAtom.Set("game", versionID)
		secondAtom.Set("location", "B2")
		secondAtom.Set("quantity", 2)
		secondAtom.Set("price", map[string]any{
			"currency": "KRW",
			"type":     "custom",
			"list":     []map[string]any{{"value": 1000}},
			"accept":   []string{"Cash"},
		})
		secondAtom.Set("tag", []map[string]any{{"category": "기타", "quantity": 1, "note": "old"}})
		if err := app.Save(secondAtom); err != nil {
			tb.Fatalf("failed to create second previous atom: %v", err)
		}
		deletedPrevID = secondAtom.Id

		prevAtoms, err := app.FindRecordsByFilter("arcade_game_atoms", "molecule={:id}", "+created", 0, 0, dbx.Params{"id": prevMoleculeID})
		if err != nil {
			tb.Fatalf("failed to load previous atoms: %v", err)
		}
		if len(prevAtoms) != 2 {
			tb.Fatalf("expected 2 previous atoms, got %d", len(prevAtoms))
		}
		keptPrevID = prevAtoms[0].Id
		if keptPrevID == deletedPrevID {
			keptPrevID = prevAtoms[1].Id
		}

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"games":[
				{
					"game":"%s",
					"prev_id":"%s",
					"location":"2F",
					"quantity":1,
					"price":{
						"currency":"KRW",
						"type":"custom",
						"list":[{"value":700}],
						"accept":[]
					},
					"tag":[{"category":"기타","quantity":1,"note":"ok"}]
				}
			]
		}`, arcadeID, versionID, keptPrevID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object in response, got %T", payload["game"])
		}
		moleculeID, _ := gameObj["id"].(string)
		if moleculeID == "" {
			tb.Fatalf("expected game molecule id in response")
		}

		changes, err := app.FindRecordsByFilter("arcade_changelog", "arcade={:id} && changed='game'", "-created", 0, 0, dbx.Params{"id": arcadeID})
		if err != nil {
			tb.Fatalf("failed to load arcade_changelog: %v", err)
		}
		if len(changes) != 1 {
			tb.Fatalf("expected 1 game changelog row, got %d", len(changes))
		}
		change := changes[0]
		if got := change.GetString("by"); got != userID {
			tb.Fatalf("expected changelog.by=%q, got %q", userID, got)
		}
		if got := change.GetString("to"); got != moleculeID {
			tb.Fatalf("expected changelog.to=%q, got %q", moleculeID, got)
		}

		var logObj map[string]any
		buf, _ := json.Marshal(change.Get("log"))
		if err := json.Unmarshal(buf, &logObj); err != nil {
			tb.Fatalf("failed to decode changelog.log: %v", err)
		}
		items, ok := logObj["items"].([]any)
		if !ok || len(items) != 2 {
			tb.Fatalf("expected 2 log items (updated+deleted), got %T %#v", logObj["items"], logObj["items"])
		}

		foundDeleted := false
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok {
				tb.Fatalf("expected log item object, got %T", raw)
			}
			changeType, _ := item["change_type"].(string)
			if changeType != "deleted" {
				continue
			}
			if got, _ := item["prev_id"].(string); got != deletedPrevID {
				tb.Fatalf("expected deleted prev_id=%q, got %v", deletedPrevID, item["prev_id"])
			}
			bullets, ok := item["bullets"].([]any)
			if !ok || len(bullets) == 0 {
				tb.Fatalf("expected deleted item bullets, got %T %#v", item["bullets"], item["bullets"])
			}
			keys := i18nBulletKeySet(bullets)
			if !keys["arcade.changelog.game.deleted"] {
				tb.Fatalf("expected deleted bullet key, got %#v", keys)
			}
			foundDeleted = true
		}
		if !foundDeleted {
			tb.Fatalf("expected one deleted log item in %#v", items)
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeGame_LogsUncertainAndPrevGameDiffs(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string
	var userID string
	var prevAtomID string
	var currentVersionID string
	var prevVersionID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/game writes uncertain and prev_game changelog diffs",
		Method:         http.MethodPut,
		URL:            "/arcade/game",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"count":1`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token
		userID = user.Id

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Uncertain Log Arcade",
			Address:  "Log Street",
			Nickname: []string{"Log"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		currentVersionID = seedGameSeriesVersion(tb, app)
		prevVersionID = seedGameSeriesVersion(tb, app)

		seedGameAtom(tb, app, arcadeID, currentVersionID, map[string]any{
			"currency": "KRW",
			"type":     "custom",
			"list":     []map[string]any{{"value": 500}},
			"accept":   []string{"Cash"},
		})

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		prevMoleculeID := arcadeRec.GetString("game")
		prevAtoms, err := app.FindRecordsByFilter("arcade_game_atoms", "molecule={:id}", "", 0, 0, dbx.Params{"id": prevMoleculeID})
		if err != nil {
			tb.Fatalf("failed to load previous game atoms: %v", err)
		}
		if len(prevAtoms) != 1 {
			tb.Fatalf("expected 1 previous atom, got %d", len(prevAtoms))
		}
		prevAtomID = prevAtoms[0].Id

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"games":[
				{
					"game":"%s",
					"prev_id":"%s",
					"location":"1F",
					"quantity":1,
					"price":{
						"currency":"KRW",
						"type":"custom",
						"list":[{"value":500}],
						"accept":["Cash"]
					},
					"tag":[{"category":"기타","quantity":1,"note":"ok"}],
					"uncertain":true,
					"prev_game":"%s"
				}
			]
		}`, arcadeID, currentVersionID, prevAtomID, prevVersionID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object in response, got %T", payload["game"])
		}
		moleculeID, _ := gameObj["id"].(string)
		if moleculeID == "" {
			tb.Fatalf("expected game molecule id in response")
		}

		atom, err := app.FindFirstRecordByFilter("arcade_game_atoms", "molecule={:id}", map[string]any{"id": moleculeID})
		if err != nil {
			tb.Fatalf("failed to load created atom: %v", err)
		}
		if got := atom.GetBool("uncertain"); !got {
			tb.Fatalf("expected atom.uncertain=true, got %v", got)
		}
		if got := atom.GetString("prev_game"); got != prevVersionID {
			tb.Fatalf("expected atom.prev_game=%q, got %q", prevVersionID, got)
		}

		changes, err := app.FindRecordsByFilter("arcade_changelog", "arcade={:id} && changed='game'", "-created", 0, 0, dbx.Params{"id": arcadeID})
		if err != nil {
			tb.Fatalf("failed to load arcade_changelog: %v", err)
		}
		if len(changes) != 1 {
			tb.Fatalf("expected 1 game changelog row, got %d", len(changes))
		}
		change := changes[0]
		if got := change.GetString("by"); got != userID {
			tb.Fatalf("expected changelog.by=%q, got %q", userID, got)
		}

		var logObj map[string]any
		buf, _ := json.Marshal(change.Get("log"))
		if err := json.Unmarshal(buf, &logObj); err != nil {
			tb.Fatalf("failed to decode changelog.log: %v", err)
		}
		items, ok := logObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected 1 changelog item, got %T %#v", logObj["items"], logObj["items"])
		}
		itemObj, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected log item object, got %T", items[0])
		}
		if got, _ := itemObj["prev_id"].(string); got != prevAtomID {
			tb.Fatalf("expected log.prev_id=%q, got %v", prevAtomID, itemObj["prev_id"])
		}
		bullets, ok := itemObj["bullets"].([]any)
		if !ok || len(bullets) == 0 {
			tb.Fatalf("expected log bullets, got %T %#v", itemObj["bullets"], itemObj["bullets"])
		}
		keys := i18nBulletKeySet(bullets)
		if !keys["arcade.changelog.game.uncertain.changed"] {
			tb.Fatalf("expected uncertain diff bullet, got %#v", keys)
		}
	}

	scenario.Test(t)
}

func TestGetArcadeValues_ExpandGame_PreservesLegacyPriceShape(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /arcade expand game preserves legacy price shape",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"accept":null`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		_, user := createAuthUser(tb, app)
		arcadeID, _ := seedPublicArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Legacy Price Arcade",
			Address:  "Legacy Road",
			Nickname: []string{"Legacy"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		versionID := seedGameSeriesVersion(tb, app)
		seedGameAtom(tb, app, arcadeID, versionID, map[string]any{
			"list":   []map[string]any{{"value": 500}},
			"accept": nil,
		})

		scenario.URL = fmt.Sprintf("/arcade?id=%s&expand=game", arcadeID)
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object, got %T", payload["game"])
		}
		items, ok := gameObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected one game item, got %T %#v", gameObj["items"], gameObj["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected item object, got %T", items[0])
		}
		price, ok := item["price"].(map[string]any)
		if !ok {
			tb.Fatalf("expected price object, got %T", item["price"])
		}
		if _, exists := price["currency"]; exists {
			tb.Fatalf("expected legacy price to keep missing currency, got %v", price["currency"])
		}
		if _, exists := price["type"]; exists {
			tb.Fatalf("expected legacy price to keep missing type, got %v", price["type"])
		}
		if accept, exists := price["accept"]; !exists || accept != nil {
			tb.Fatalf("expected legacy accept null, got %v (exists=%v)", accept, exists)
		}
	}

	scenario.Test(t)
}

func TestGetArcadeValues_ExpandGame_PreservesValidCurrency(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /arcade expand game preserves valid currency",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"currency":"USD"`,
			`"type":"custom"`,
			`"accept":null`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		_, user := createAuthUser(tb, app)
		arcadeID, _ := seedPublicArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Valid Currency Arcade",
			Address:  "Valid Road",
			Nickname: []string{"Valid"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		versionID := seedGameSeriesVersion(tb, app)
		seedGameAtom(tb, app, arcadeID, versionID, map[string]any{
			"currency": "USD",
			"type":     "custom",
			"list":     []map[string]any{{"value": 500}},
			"accept":   nil,
		})

		scenario.URL = fmt.Sprintf("/arcade?id=%s&expand=game", arcadeID)
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object, got %T", payload["game"])
		}
		items, ok := gameObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected one game item, got %T %#v", gameObj["items"], gameObj["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected item object, got %T", items[0])
		}
		price, ok := item["price"].(map[string]any)
		if !ok {
			tb.Fatalf("expected price object, got %T", item["price"])
		}
		if got, _ := price["currency"].(string); got != "USD" {
			tb.Fatalf("expected normalized currency USD, got %v", price["currency"])
		}
		if got, _ := price["type"].(string); got != "custom" {
			tb.Fatalf("expected normalized type custom, got %v", price["type"])
		}
		if accept, exists := price["accept"]; !exists || accept != nil {
			tb.Fatalf("expected preserved accept null, got %v (exists=%v)", accept, exists)
		}
	}

	scenario.Test(t)
}

func TestGetArcadeValues_ExpandGame_PreservesNumericPriceTitle(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /arcade expand game preserves numeric price title",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"title":1000`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		_, user := createAuthUser(tb, app)
		arcadeID, _ := seedPublicArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Numeric Title Arcade",
			Address:  "Type Street",
			Nickname: []string{"Typed"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		versionID := seedGameSeriesVersion(tb, app)
		seedGameAtom(tb, app, arcadeID, versionID, map[string]any{
			"currency": "KRW",
			"type":     "custom",
			"list":     []map[string]any{{"title": 1000, "value": 500}},
			"accept":   []string{"Cash"},
		})

		scenario.URL = fmt.Sprintf("/arcade?id=%s&expand=game", arcadeID)
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object, got %T", payload["game"])
		}
		items, ok := gameObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected one game item, got %T %#v", gameObj["items"], gameObj["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected item object, got %T", items[0])
		}
		price, ok := item["price"].(map[string]any)
		if !ok {
			tb.Fatalf("expected price object, got %T", item["price"])
		}
		list, ok := price["list"].([]any)
		if !ok || len(list) != 1 {
			tb.Fatalf("expected one price.list item, got %T %#v", price["list"], price["list"])
		}
		listItem, ok := list[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected price.list[0] object, got %T", list[0])
		}
		if got, _ := listItem["title"].(float64); got != 1000 {
			tb.Fatalf("expected numeric title 1000, got %v", listItem["title"])
		}
	}

	scenario.Test(t)
}

func TestGetArcadeValues_ExpandGame_SortsBySeriesNumberAndLatestRelease(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /arcade expand game sorts by series number and latest release",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"items":[{`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	var (
		series1LatestID string
		series1OlderID  string
		series2ID       string
	)

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		_, user := createAuthUser(tb, app)
		arcadeID, _ := seedPublicArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Sorted Arcade",
			Address:  "Sort Street",
			Nickname: []string{"Sorted"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		series1ID := seedGameSeries(tb, app, 1, "Series One")
		series2SeriesID := seedGameSeries(tb, app, 2, "Series Two")

		series1OlderID = seedGameSeriesVersionWithSeries(tb, app, series1ID, "2024-01-01", "Series 1 Old")
		series1LatestID = seedGameSeriesVersionWithSeries(tb, app, series1ID, "2025-06-01", "Series 1 New")
		series2ID = seedGameSeriesVersionWithSeries(tb, app, series2SeriesID, "2026-01-01", "Series 2")

		moleculeID := seedArcadeGameMolecule(tb, app, arcadeID)
		seedArcadeGameAtom(tb, app, moleculeID, series2ID, "B1")
		seedArcadeGameAtom(tb, app, moleculeID, series1OlderID, "1F")
		seedArcadeGameAtom(tb, app, moleculeID, series1LatestID, "2F")

		scenario.URL = fmt.Sprintf("/arcade?id=%s&expand=game", arcadeID)
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object, got %T", payload["game"])
		}
		items, ok := gameObj["items"].([]any)
		if !ok || len(items) != 3 {
			tb.Fatalf("expected three game items, got %T %#v", gameObj["items"], gameObj["items"])
		}

		gotVersionIDs := make([]string, 0, len(items))
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok {
				tb.Fatalf("expected item object, got %T", raw)
			}
			version, ok := item["version"].(map[string]any)
			if !ok {
				tb.Fatalf("expected version object, got %T", item["version"])
			}
			versionID, _ := version["id"].(string)
			gotVersionIDs = append(gotVersionIDs, versionID)
		}

		wantVersionIDs := []string{series1LatestID, series1OlderID, series2ID}
		for idx := range wantVersionIDs {
			if gotVersionIDs[idx] != wantVersionIDs[idx] {
				tb.Fatalf("expected version order %v, got %v", wantVersionIDs, gotVersionIDs)
			}
		}
	}

	scenario.Test(t)
}

func TestGetArcadeValues_ExpandGame_IncludesFlagsAndReactions(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /arcade expand game includes flags and reactions",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"flags":[{`,
			`"reactions":[{`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		_, user := createAuthUser(tb, app)
		user.Set("username", "flag_reporter")
		if err := app.Save(user); err != nil {
			tb.Fatalf("failed to update user username: %v", err)
		}
		userInfo := ensureUserInfo(tb, app, user.Id)
		userInfo.Set("nickname", "Flag Reporter")
		if err := app.Save(userInfo); err != nil {
			tb.Fatalf("failed to update user_info: %v", err)
		}
		arcadeID, _ := seedPublicArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Flagged Arcade",
			Address:  "Flag Street",
			Nickname: []string{"Flagged"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		versionID := seedGameSeriesVersion(tb, app)
		seedGameAtom(tb, app, arcadeID, versionID, map[string]any{
			"currency": "KRW",
			"type":     "custom",
			"list":     []map[string]any{{"value": 500}},
			"accept":   []string{"Cash"},
		})

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		moleculeID := arcadeRec.GetString("game")
		if moleculeID == "" {
			tb.Fatalf("expected linked game molecule id")
		}

		atoms, err := app.FindRecordsByFilter("arcade_game_atoms", "molecule={:id}", "", 0, 0, dbx.Params{"id": moleculeID})
		if err != nil {
			tb.Fatalf("failed to load game atoms: %v", err)
		}
		if len(atoms) != 1 {
			tb.Fatalf("expected 1 game atom, got %d", len(atoms))
		}

		flagColl, err := app.FindCollectionByNameOrId("arcade_flag")
		if err != nil {
			tb.Fatalf("failed to load arcade_flag collection: %v", err)
		}
		flagRec := core.NewRecord(flagColl)
		flagRec.Set("arcade", arcadeID)
		flagRec.Set("disruption", "major")
		flagRec.Set("solved", false)
		flagRec.Set("message", "coin slot issue")
		photo1, err := filesystem.NewFileFromBytes(pngFixtureBytes(), "flag-1.png")
		if err != nil {
			tb.Fatalf("failed to create flag photo 1: %v", err)
		}
		photo2, err := filesystem.NewFileFromBytes(jpegFixtureBytes(), "flag-2.jpg")
		if err != nil {
			tb.Fatalf("failed to create flag photo 2: %v", err)
		}
		flagRec.Set("photos", []*filesystem.File{photo1, photo2})
		flagRec.Set("createdBy", user.Id)
		if err := app.Save(flagRec); err != nil {
			tb.Fatalf("failed to save arcade_flag: %v", err)
		}

		reactionColl, err := app.FindCollectionByNameOrId("arcade_flag_reaction")
		if err != nil {
			tb.Fatalf("failed to load arcade_flag_reaction collection: %v", err)
		}

		reaction1 := core.NewRecord(reactionColl)
		reaction1.Set("flag", flagRec.Id)
		reaction1.Set("reaction", "fixed")
		reaction1.Set("createdBy", user.Id)
		if err := app.Save(reaction1); err != nil {
			tb.Fatalf("failed to save first reaction: %v", err)
		}

		reaction2 := core.NewRecord(reactionColl)
		reaction2.Set("flag", flagRec.Id)
		reaction2.Set("reaction", "wrong")
		reaction2.Set("createdBy", user.Id)
		if err := app.Save(reaction2); err != nil {
			tb.Fatalf("failed to save second reaction: %v", err)
		}

		atoms[0].Set("flags", []string{flagRec.Id})
		if err := app.Save(atoms[0]); err != nil {
			tb.Fatalf("failed to update game atom flags: %v", err)
		}

		scenario.URL = fmt.Sprintf("/arcade?id=%s&expand=game", arcadeID)
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object, got %T", payload["game"])
		}
		items, ok := gameObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected one game item, got %T %#v", gameObj["items"], gameObj["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected item object, got %T", items[0])
		}

		flags, ok := item["flags"].([]any)
		if !ok || len(flags) != 1 {
			tb.Fatalf("expected one expanded flag, got %T %#v", item["flags"], item["flags"])
		}

		flagObj, ok := flags[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected flag object, got %T", flags[0])
		}
		if got, _ := flagObj["disruption"].(string); got != "major" {
			tb.Fatalf("expected disruption major, got %v", flagObj["disruption"])
		}
		if got, _ := flagObj["solved"].(bool); got != false {
			tb.Fatalf("expected solved false, got %v", flagObj["solved"])
		}
		if got, _ := flagObj["message"].(string); got != "coin slot issue" {
			tb.Fatalf("expected message coin slot issue, got %v", flagObj["message"])
		}
		photos, ok := flagObj["photos"].([]any)
		if !ok || len(photos) != 2 {
			tb.Fatalf("expected two photos, got %T %#v", flagObj["photos"], flagObj["photos"])
		}
		if _, ok := flagObj["createdByProfile"]; ok {
			tb.Fatalf("expected flag createdByProfile to be removed from payload")
		}

		reactions, ok := flagObj["reactions"].([]any)
		if !ok || len(reactions) != 2 {
			tb.Fatalf("expected two reactions, got %T %#v", flagObj["reactions"], flagObj["reactions"])
		}

		reactionSet := map[string]bool{}
		for _, raw := range reactions {
			reactionObj, ok := raw.(map[string]any)
			if !ok {
				tb.Fatalf("expected reaction object, got %T", raw)
			}
			name, _ := reactionObj["reaction"].(string)
			reactionSet[name] = true
			if _, ok := reactionObj["createdByProfile"]; ok {
				tb.Fatalf("expected reaction createdByProfile to be removed from payload")
			}
		}
		if !reactionSet["fixed"] || !reactionSet["wrong"] {
			tb.Fatalf("expected reaction names fixed and wrong, got %#v", reactionSet)
		}
	}

	scenario.Test(t)
}

func TestGetArcadeValues_ExpandGame_IncludesOrphanFlags(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /arcade expand game includes orphan flags",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"orphanFlags":[{`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	var linkedFlagID string
	var orphanFlagID string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		_, user := createAuthUser(tb, app)
		arcadeID, _ := seedPublicArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Orphan Flag Arcade",
			Address:  "Orphan Street",
			Nickname: []string{"Orphan"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		versionID := seedGameSeriesVersion(tb, app)
		seedGameAtom(tb, app, arcadeID, versionID, map[string]any{
			"currency": "KRW",
			"type":     "custom",
			"list":     []map[string]any{{"value": 500}},
			"accept":   []string{"Cash"},
		})

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		moleculeID := arcadeRec.GetString("game")
		if moleculeID == "" {
			tb.Fatalf("expected linked game molecule id")
		}

		atoms, err := app.FindRecordsByFilter("arcade_game_atoms", "molecule={:id}", "", 0, 0, dbx.Params{"id": moleculeID})
		if err != nil {
			tb.Fatalf("failed to load game atoms: %v", err)
		}
		if len(atoms) != 1 {
			tb.Fatalf("expected 1 game atom, got %d", len(atoms))
		}

		flagColl, err := app.FindCollectionByNameOrId("arcade_flag")
		if err != nil {
			tb.Fatalf("failed to load arcade_flag collection: %v", err)
		}

		linkedFlag := core.NewRecord(flagColl)
		linkedFlag.Set("arcade", arcadeID)
		linkedFlag.Set("disruption", "minor")
		linkedFlag.Set("solved", false)
		linkedFlag.Set("message", "linked issue")
		linkedFlag.Set("createdBy", user.Id)
		if err := app.Save(linkedFlag); err != nil {
			tb.Fatalf("failed to save linked flag: %v", err)
		}
		linkedFlagID = linkedFlag.Id

		orphanFlag := core.NewRecord(flagColl)
		orphanFlag.Set("arcade", arcadeID)
		orphanFlag.Set("disruption", "major")
		orphanFlag.Set("solved", false)
		orphanFlag.Set("message", "orphan issue")
		orphanFlag.Set("createdBy", user.Id)
		if err := app.Save(orphanFlag); err != nil {
			tb.Fatalf("failed to save orphan flag: %v", err)
		}
		orphanFlagID = orphanFlag.Id

		reactionColl, err := app.FindCollectionByNameOrId("arcade_flag_reaction")
		if err != nil {
			tb.Fatalf("failed to load arcade_flag_reaction collection: %v", err)
		}
		orphanReaction := core.NewRecord(reactionColl)
		orphanReaction.Set("flag", orphanFlagID)
		orphanReaction.Set("reaction", "fixed")
		orphanReaction.Set("createdBy", user.Id)
		if err := app.Save(orphanReaction); err != nil {
			tb.Fatalf("failed to save orphan reaction: %v", err)
		}

		atoms[0].Set("flags", []string{linkedFlagID})
		if err := app.Save(atoms[0]); err != nil {
			tb.Fatalf("failed to update game atom flags: %v", err)
		}

		scenario.URL = fmt.Sprintf("/arcade?id=%s&expand=game", arcadeID)
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object, got %T", payload["game"])
		}

		items, ok := gameObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected one game item, got %T %#v", gameObj["items"], gameObj["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected item object, got %T", items[0])
		}
		itemFlags, ok := item["flags"].([]any)
		if !ok || len(itemFlags) != 1 {
			tb.Fatalf("expected one linked item flag, got %T %#v", item["flags"], item["flags"])
		}
		linkedObj, ok := itemFlags[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected linked flag object, got %T", itemFlags[0])
		}
		if got, _ := linkedObj["id"].(string); got != linkedFlagID {
			tb.Fatalf("expected linked flag id %q, got %v", linkedFlagID, linkedObj["id"])
		}

		orphanFlags, ok := gameObj["orphanFlags"].([]any)
		if !ok || len(orphanFlags) != 1 {
			tb.Fatalf("expected one orphan flag, got %T %#v", gameObj["orphanFlags"], gameObj["orphanFlags"])
		}
		orphanObj, ok := orphanFlags[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected orphan flag object, got %T", orphanFlags[0])
		}
		if got, _ := orphanObj["id"].(string); got != orphanFlagID {
			tb.Fatalf("expected orphan flag id %q, got %v", orphanFlagID, orphanObj["id"])
		}

		reactions, ok := orphanObj["reactions"].([]any)
		if !ok || len(reactions) != 1 {
			tb.Fatalf("expected one orphan reaction, got %T %#v", orphanObj["reactions"], orphanObj["reactions"])
		}
		reactionObj, ok := reactions[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected orphan reaction object, got %T", reactions[0])
		}
		if got, _ := reactionObj["reaction"].(string); got != "fixed" {
			tb.Fatalf("expected orphan reaction fixed, got %v", reactionObj["reaction"])
		}
	}

	scenario.Test(t)
}

func TestGetArcadeValues_ExpandGame_ExcludesSolvedFlags(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /arcade expand game excludes solved flags",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"flags":[{`,
			`"orphanFlags":[{`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	var linkedUnsolvedID string
	var orphanUnsolvedID string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		_, user := createAuthUser(tb, app)
		arcadeID, _ := seedPublicArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Solved Filter Arcade",
			Address:  "Solved Street",
			Nickname: []string{"SolvedFilter"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		versionID := seedGameSeriesVersion(tb, app)
		seedGameAtom(tb, app, arcadeID, versionID, map[string]any{
			"currency": "KRW",
			"type":     "custom",
			"list":     []map[string]any{{"value": 500}},
			"accept":   []string{"Cash"},
		})

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		moleculeID := arcadeRec.GetString("game")
		if moleculeID == "" {
			tb.Fatalf("expected linked game molecule id")
		}

		atoms, err := app.FindRecordsByFilter("arcade_game_atoms", "molecule={:id}", "", 0, 0, dbx.Params{"id": moleculeID})
		if err != nil {
			tb.Fatalf("failed to load game atoms: %v", err)
		}
		if len(atoms) != 1 {
			tb.Fatalf("expected 1 game atom, got %d", len(atoms))
		}

		flagColl, err := app.FindCollectionByNameOrId("arcade_flag")
		if err != nil {
			tb.Fatalf("failed to load arcade_flag collection: %v", err)
		}

		linkedUnsolved := core.NewRecord(flagColl)
		linkedUnsolved.Set("arcade", arcadeID)
		linkedUnsolved.Set("disruption", "minor")
		linkedUnsolved.Set("solved", false)
		linkedUnsolved.Set("message", "linked unsolved")
		linkedUnsolved.Set("createdBy", user.Id)
		if err := app.Save(linkedUnsolved); err != nil {
			tb.Fatalf("failed to save linked unsolved flag: %v", err)
		}
		linkedUnsolvedID = linkedUnsolved.Id

		linkedSolved := core.NewRecord(flagColl)
		linkedSolved.Set("arcade", arcadeID)
		linkedSolved.Set("disruption", "major")
		linkedSolved.Set("solved", true)
		linkedSolved.Set("message", "linked solved")
		linkedSolved.Set("createdBy", user.Id)
		if err := app.Save(linkedSolved); err != nil {
			tb.Fatalf("failed to save linked solved flag: %v", err)
		}

		orphanUnsolved := core.NewRecord(flagColl)
		orphanUnsolved.Set("arcade", arcadeID)
		orphanUnsolved.Set("disruption", "bearable")
		orphanUnsolved.Set("solved", false)
		orphanUnsolved.Set("message", "orphan unsolved")
		orphanUnsolved.Set("createdBy", user.Id)
		if err := app.Save(orphanUnsolved); err != nil {
			tb.Fatalf("failed to save orphan unsolved flag: %v", err)
		}
		orphanUnsolvedID = orphanUnsolved.Id

		orphanSolved := core.NewRecord(flagColl)
		orphanSolved.Set("arcade", arcadeID)
		orphanSolved.Set("disruption", "unplayable")
		orphanSolved.Set("solved", true)
		orphanSolved.Set("message", "orphan solved")
		orphanSolved.Set("createdBy", user.Id)
		if err := app.Save(orphanSolved); err != nil {
			tb.Fatalf("failed to save orphan solved flag: %v", err)
		}

		atoms[0].Set("flags", []string{linkedUnsolvedID, linkedSolved.Id})
		if err := app.Save(atoms[0]); err != nil {
			tb.Fatalf("failed to update game atom flags: %v", err)
		}

		scenario.URL = fmt.Sprintf("/arcade?id=%s&expand=game", arcadeID)
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object, got %T", payload["game"])
		}

		items, ok := gameObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected one game item, got %T %#v", gameObj["items"], gameObj["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected item object, got %T", items[0])
		}

		itemFlags, ok := item["flags"].([]any)
		if !ok || len(itemFlags) != 1 {
			tb.Fatalf("expected only one unsolved linked flag, got %T %#v", item["flags"], item["flags"])
		}
		linkedObj, ok := itemFlags[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected linked flag object, got %T", itemFlags[0])
		}
		if got, _ := linkedObj["id"].(string); got != linkedUnsolvedID {
			tb.Fatalf("expected unsolved linked id %q, got %v", linkedUnsolvedID, linkedObj["id"])
		}

		orphanFlags, ok := gameObj["orphanFlags"].([]any)
		if !ok || len(orphanFlags) != 1 {
			tb.Fatalf("expected only one unsolved orphan flag, got %T %#v", gameObj["orphanFlags"], gameObj["orphanFlags"])
		}
		orphanObj, ok := orphanFlags[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected orphan flag object, got %T", orphanFlags[0])
		}
		if got, _ := orphanObj["id"].(string); got != orphanUnsolvedID {
			tb.Fatalf("expected unsolved orphan id %q, got %v", orphanUnsolvedID, orphanObj["id"])
		}
	}

	scenario.Test(t)
}

func i18nBulletKeySet(bullets []any) map[string]bool {
	set := map[string]bool{}
	for _, raw := range bullets {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		key, _ := obj["key"].(string)
		if key == "" {
			continue
		}
		set[key] = true
	}
	return set
}

func seedGameAtom(tb testing.TB, app *tests.TestApp, arcadeID, versionID string, price any) {
	tb.Helper()

	gameColl, err := app.FindCollectionByNameOrId("arcade_game")
	if err != nil {
		tb.Fatalf("failed to load arcade_game collection: %v", err)
	}
	molecule := core.NewRecord(gameColl)
	molecule.Set("arcade", arcadeID)
	if err := app.Save(molecule); err != nil {
		tb.Fatalf("failed to create arcade_game molecule: %v", err)
	}

	atomColl, err := app.FindCollectionByNameOrId("arcade_game_atoms")
	if err != nil {
		tb.Fatalf("failed to load arcade_game_atoms collection: %v", err)
	}
	atom := core.NewRecord(atomColl)
	atom.Set("molecule", molecule.Id)
	atom.Set("game", versionID)
	atom.Set("location", "1F")
	atom.Set("quantity", 1)
	atom.Set("price", price)
	atom.Set("tag", []map[string]any{{"category": "기타", "quantity": 1, "note": "ok"}})
	if err := app.Save(atom); err != nil {
		tb.Fatalf("failed to create arcade_game atom: %v", err)
	}

	arcadeRec, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		tb.Fatalf("failed to load arcade: %v", err)
	}
	arcadeRec.Set("game", molecule.Id)
	if err := app.Save(arcadeRec); err != nil {
		tb.Fatalf("failed to link arcade.game: %v", err)
	}
}

func seedGameSeries(tb testing.TB, app *tests.TestApp, seriesNumber int, name string) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("game_series")
	if err != nil {
		tb.Fatalf("failed to load game_series collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("seriesNumber", seriesNumber)
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

func seedGameSeriesVersionWithSeries(tb testing.TB, app *tests.TestApp, seriesID, releasedOn, name string) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("game_series_version")
	if err != nil {
		tb.Fatalf("failed to load game_series_version collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("series", seriesID)
	rec.Set("released_on", releasedOn)
	rec.Set("en", name)
	rec.Set("kr", name)
	rec.Set("jp", name)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save game_series_version: %v", err)
	}

	return rec.Id
}

func seedArcadeGameMolecule(tb testing.TB, app *tests.TestApp, arcadeID string) string {
	tb.Helper()

	gameColl, err := app.FindCollectionByNameOrId("arcade_game")
	if err != nil {
		tb.Fatalf("failed to load arcade_game collection: %v", err)
	}

	molecule := core.NewRecord(gameColl)
	molecule.Set("arcade", arcadeID)
	if err := app.Save(molecule); err != nil {
		tb.Fatalf("failed to create arcade_game molecule: %v", err)
	}

	arcadeRec, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		tb.Fatalf("failed to load arcade: %v", err)
	}
	arcadeRec.Set("game", molecule.Id)
	if err := app.Save(arcadeRec); err != nil {
		tb.Fatalf("failed to link arcade.game: %v", err)
	}

	return molecule.Id
}

func seedArcadeGameAtom(tb testing.TB, app *tests.TestApp, moleculeID, versionID, location string) string {
	tb.Helper()

	atomColl, err := app.FindCollectionByNameOrId("arcade_game_atoms")
	if err != nil {
		tb.Fatalf("failed to load arcade_game_atoms collection: %v", err)
	}

	atom := core.NewRecord(atomColl)
	atom.Set("molecule", moleculeID)
	atom.Set("game", versionID)
	atom.Set("location", location)
	atom.Set("quantity", 1)
	atom.Set("price", map[string]any{
		"currency": "KRW",
		"type":     "custom",
		"list":     []map[string]any{{"value": 500}},
		"accept":   []string{"Cash"},
	})
	atom.Set("tag", []map[string]any{{"category": "기타", "quantity": 1, "note": "ok"}})
	if err := app.Save(atom); err != nil {
		tb.Fatalf("failed to create arcade_game atom: %v", err)
	}

	return atom.Id
}
