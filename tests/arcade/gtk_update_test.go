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

func TestUpdateArcadeGTK_WritesStructuredChangelog(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string
	var updatedPrevID string
	var deletedPrevID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/gtk writes structured changelog",
		Method:         http.MethodPut,
		URL:            "/arcade/gtk",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"gtk":{"id":"`,
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
			Name:     "GTK Arcade",
			Address:  "GTK Street",
			Nickname: []string{"GTK"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		moleculeID := seedGTKMolecule(tb, app, arcadeID, user.Id)
		updatedPrevID = seedGTKAtom(tb, app, moleculeID, user.Id, "FreeWifi", false, "old wifi", nil)
		deletedPrevID = seedGTKAtom(tb, app, moleculeID, user.Id, "Locker", true, "old locker", nil)

			scenario.Body = strings.NewReader(fmt.Sprintf(`{
				"arcade":"%s",
				"gtk":[
					{"type":"FreeWifi","bool":true,"note":"new wifi"},
					{"type":"SmokingRoom","bool":true,"note":"smoking room"}
				]
			}`, arcadeID))
		}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		gtkObj, ok := payload["gtk"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded gtk object in response, got %T", payload["gtk"])
		}
		moleculeID, _ := gtkObj["id"].(string)
		if moleculeID == "" {
			tb.Fatalf("expected gtk molecule id in response")
		}
		items, ok := gtkObj["items"].([]any)
		if !ok || len(items) != 2 {
			tb.Fatalf("expected 2 gtk items in response, got %T %#v", gtkObj["items"], gtkObj["items"])
		}
		firstItem, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected first gtk item object, got %T", items[0])
		}
		if got, _ := firstItem["type"].(string); got != "FreeWifi" {
			tb.Fatalf("expected first gtk item type FreeWifi, got %v", firstItem["type"])
		}
		if got, ok := firstItem["bool"].(bool); !ok || !got {
			tb.Fatalf("expected first gtk item bool true, got %v", firstItem["bool"])
		}

		changes := loadChangelogRecords(tb, app, arcadeID, "gtk")
		if len(changes) != 1 {
			tb.Fatalf("expected 1 gtk changelog row, got %d", len(changes))
		}
		logObj := decodeLogObject(tb, changes[0].Get("log"))
		if got, _ := logObj["type"].(string); got != "gtk_diff" {
			tb.Fatalf("expected changelog.log.type=gtk_diff, got %v", logObj["type"])
		}
		logItems, ok := logObj["items"].([]any)
		if !ok || len(logItems) != 3 {
			tb.Fatalf("expected 3 gtk log items, got %T %#v", logObj["items"], logObj["items"])
		}

		foundUpdated := false
		foundAdded := false
		foundDeleted := false
		for _, raw := range logItems {
			item, ok := raw.(map[string]any)
			if !ok {
				tb.Fatalf("expected gtk log item object, got %T", raw)
			}
			changeType, _ := item["change_type"].(string)
			bullets, ok := item["bullets"].([]any)
			if !ok || len(bullets) == 0 {
				tb.Fatalf("expected gtk bullets, got %T %#v", item["bullets"], item["bullets"])
			}
			keys := i18nBulletKeySet(bullets)
			switch changeType {
			case "updated":
				if got, _ := item["prev_id"].(string); got != updatedPrevID {
					tb.Fatalf("expected updated prev_id=%q, got %v", updatedPrevID, item["prev_id"])
				}
				if !keys["arcade.changelog.gtk.bool.changed"] || !keys["arcade.changelog.gtk.note.changed"] {
					tb.Fatalf("expected gtk bool+note changed bullets, got %#v", keys)
				}
				foundUpdated = true
			case "added":
				if !keys["arcade.changelog.gtk.added"] {
					tb.Fatalf("expected gtk added bullet, got %#v", keys)
				}
				foundAdded = true
			case "deleted":
				if got, _ := item["prev_id"].(string); got != deletedPrevID {
					tb.Fatalf("expected deleted prev_id=%q, got %v", deletedPrevID, item["prev_id"])
				}
				if !keys["arcade.changelog.gtk.deleted"] {
					tb.Fatalf("expected gtk deleted bullet, got %#v", keys)
				}
				foundDeleted = true
			}
		}
		if !foundUpdated || !foundAdded || !foundDeleted {
			tb.Fatalf("expected updated+added+deleted gtk items, got %#v", items)
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeGTK_ParkingMeta(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string
	var parkingPrevID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/gtk writes parking meta",
		Method:         http.MethodPut,
		URL:            "/arcade/gtk",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"gtk":{"id":"`,
			`"meta":{`,
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
			Name:     "Parking Arcade",
			Address:  "Parking Street",
			Nickname: []string{"Park"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		moleculeID := seedGTKMolecule(tb, app, arcadeID, user.Id)
		parkingPrevID = seedGTKAtom(tb, app, moleculeID, user.Id, "Parking", true, "old parking", map[string]any{
			"geo": map[string]any{
				"lat": 37.5,
				"lon": 126.9,
			},
			"availability": "somewhat_difficult",
			"options":      []string{"paid_parking_lot"},
			"ev_charging":  false,
			"gov_parking":  true,
		})

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"gtk":[
				{
					"type":"Parking",
					"bool":false,
					"note":"temporary closure",
					"meta":{
						"geo":{"lat":37.6,"lon":126.98},
						"availability":"always_plenty",
						"options":["free_parking_lot","paid_street_parking"],
						"ev_charging":true,
						"gov_parking":false
					}
				}
			]
		}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		gtkObj, ok := payload["gtk"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded gtk object in response, got %T", payload["gtk"])
		}
		items, ok := gtkObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected 1 gtk item in response, got %T %#v", gtkObj["items"], gtkObj["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected gtk item object, got %T", items[0])
		}
		if got, _ := item["type"].(string); got != "Parking" {
			tb.Fatalf("expected item type Parking, got %v", item["type"])
		}
		if got, ok := item["bool"].(bool); !ok || got {
			tb.Fatalf("expected item bool false, got %v", item["bool"])
		}
		meta, ok := item["meta"].(map[string]any)
		if !ok {
			tb.Fatalf("expected item meta object, got %T", item["meta"])
		}
		if got := meta["availability"]; got != "always_plenty" {
			tb.Fatalf("expected availability always_plenty, got %v", got)
		}
		if got := meta["ev_charging"]; got != true {
			tb.Fatalf("expected ev_charging true, got %v", got)
		}
		if got := meta["gov_parking"]; got != false {
			tb.Fatalf("expected gov_parking false, got %v", got)
		}
		geo, ok := meta["geo"].(map[string]any)
		if !ok {
			tb.Fatalf("expected geo object, got %T", meta["geo"])
		}
		if !floatAlmostEq(geo["lat"].(float64), 37.6) || !floatAlmostEq(geo["lon"].(float64), 126.98) {
			tb.Fatalf("unexpected geo: %v", geo)
		}
		options, ok := meta["options"].([]any)
		if !ok || len(options) != 2 {
			tb.Fatalf("expected 2 parking options, got %T %#v", meta["options"], meta["options"])
		}

		changes := loadChangelogRecords(tb, app, arcadeID, "gtk")
		if len(changes) != 1 {
			tb.Fatalf("expected 1 gtk changelog row, got %d", len(changes))
		}
		logObj := decodeLogObject(tb, changes[0].Get("log"))
		logItems, ok := logObj["items"].([]any)
		if !ok || len(logItems) != 1 {
			tb.Fatalf("expected 1 gtk log item, got %T %#v", logObj["items"], logObj["items"])
		}
		logItem, ok := logItems[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected log item object, got %T", logItems[0])
		}
		if got := logItem["prev_id"]; got != parkingPrevID {
			tb.Fatalf("expected prev_id %q, got %v", parkingPrevID, got)
		}
		keys := i18nBulletKeySet(logItem["bullets"].([]any))
		if !keys["arcade.changelog.gtk.bool.changed"] {
			tb.Fatalf("expected bool changed bullet, got %#v", keys)
		}
		if !keys["arcade.changelog.gtk.meta.changed"] {
			tb.Fatalf("expected meta changed bullet, got %#v", keys)
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeGTK_AllowsEmptyArray(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/gtk allows empty gtk array",
		Method:         http.MethodPut,
		URL:            "/arcade/gtk",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"gtk":{"id":"`,
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
			Name:     "Empty GTK Arcade",
			Address:  "GTK Street",
			Nickname: []string{"GTK"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"gtk":[]
		}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		gtkObj, ok := payload["gtk"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded gtk object in response, got %T", payload["gtk"])
		}
		moleculeID, _ := gtkObj["id"].(string)
		if moleculeID == "" {
			tb.Fatalf("expected gtk molecule id in response")
		}
		items, ok := gtkObj["items"].([]any)
		if !ok {
			tb.Fatalf("expected gtk.items array, got %T", gtkObj["items"])
		}
		if len(items) != 0 {
			tb.Fatalf("expected empty gtk.items array, got %#v", items)
		}
	}

	scenario.Test(t)
}

func seedGTKMolecule(tb testing.TB, app *tests.TestApp, arcadeID, createdBy string) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("arcade_gtk")
	if err != nil {
		tb.Fatalf("failed to load arcade_gtk collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("arcade", arcadeID)
	if createdBy != "" {
		rec.Set("createdBy", createdBy)
	}
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save arcade_gtk record: %v", err)
	}

	arcadeRec, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		tb.Fatalf("failed to load arcade: %v", err)
	}
	arcadeRec.Set("gtk", rec.Id)
	if err := app.Save(arcadeRec); err != nil {
		tb.Fatalf("failed to link arcade.gtk: %v", err)
	}

	return rec.Id
}

func seedGTKAtom(tb testing.TB, app *tests.TestApp, moleculeID, createdBy, gtkType string, value bool, note string, meta any) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("arcade_gtk_atoms")
	if err != nil {
		tb.Fatalf("failed to load arcade_gtk_atoms collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("molecule", moleculeID)
	rec.Set("type", gtkType)
	rec.Set("bool", value)
	rec.Set("note", note)
	if meta != nil {
		rec.Set("meta", meta)
	}
	if createdBy != "" {
		rec.Set("createdBy", createdBy)
	}
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save arcade_gtk_atoms record: %v", err)
	}

	return rec.Id
}
