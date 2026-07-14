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
)

func TestUpdateArcadeSNS_WritesStructuredChangelog(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string
	var keptPrevID string
	var deletedPrevID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/sns writes structured changelog",
		Method:         http.MethodPut,
		URL:            "/arcade/sns",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"sns":{"id":"`,
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
			Name:     "SNS Arcade",
			Address:  "SNS Street",
			Nickname: []string{"SNS"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		moleculeID := seedSNSMolecule(tb, app, arcadeID, user.Id)
		keptPrevID = seedSNSAtom(tb, app, moleculeID, user.Id, "twitter", "https://old.example/twitter", "Musecat")
		deletedPrevID = seedSNSAtom(tb, app, moleculeID, user.Id, "instagram", "https://old.example/ig", "Musecat IG")

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"sns":[
				{"type":"twitter","link":"https://new.example/twitter","name":"Musecat"},
				{"type":"youtube","link":"https://new.example/youtube","name":"Musecat TV"}
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
		snsObj, ok := payload["sns"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded sns object in response, got %T", payload["sns"])
		}
		moleculeID, _ := snsObj["id"].(string)
		if moleculeID == "" {
			tb.Fatalf("expected sns molecule id in response")
		}
		items, ok := snsObj["items"].([]any)
		if !ok || len(items) != 2 {
			tb.Fatalf("expected 2 sns items in response, got %T %#v", snsObj["items"], snsObj["items"])
		}
		firstItem, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected first sns item object, got %T", items[0])
		}
		if got, _ := firstItem["type"].(string); got != "twitter" {
			tb.Fatalf("expected first sns item type twitter, got %v", firstItem["type"])
		}
		if got, _ := firstItem["link"].(string); got != "https://new.example/twitter" {
			tb.Fatalf("expected first sns item link, got %v", firstItem["link"])
		}

		changes := loadChangelogRecords(tb, app, arcadeID, "sns")
		if len(changes) != 1 {
			tb.Fatalf("expected 1 sns changelog row, got %d", len(changes))
		}
		logObj := decodeLogObject(tb, changes[0].Get("log"))
		if got, _ := logObj["type"].(string); got != "sns_diff" {
			tb.Fatalf("expected changelog.log.type=sns_diff, got %v", logObj["type"])
		}
		if got, _ := logObj["version"].(float64); got != 1 {
			tb.Fatalf("expected changelog.log.version=1, got %v", logObj["version"])
		}
		logItems, ok := logObj["items"].([]any)
		if !ok || len(logItems) != 3 {
			tb.Fatalf("expected 3 sns log items, got %T %#v", logObj["items"], logObj["items"])
		}

		foundUpdated := false
		foundAdded := false
		foundDeleted := false
		for _, raw := range logItems {
			item, ok := raw.(map[string]any)
			if !ok {
				tb.Fatalf("expected sns log item object, got %T", raw)
			}
			changeType, _ := item["change_type"].(string)
			bullets, ok := item["bullets"].([]any)
			if !ok || len(bullets) == 0 {
				tb.Fatalf("expected sns bullets, got %T %#v", item["bullets"], item["bullets"])
			}
			keys := i18nBulletKeySet(bullets)
			switch changeType {
			case "updated":
				if got, _ := item["prev_id"].(string); got != keptPrevID {
					tb.Fatalf("expected updated prev_id=%q, got %v", keptPrevID, item["prev_id"])
				}
				if !keys["arcade.changelog.sns.link.changed"] {
					tb.Fatalf("expected sns link.changed bullet, got %#v", keys)
				}
				foundUpdated = true
			case "added":
				if !keys["arcade.changelog.sns.added"] {
					tb.Fatalf("expected sns added bullet, got %#v", keys)
				}
				foundAdded = true
			case "deleted":
				if got, _ := item["prev_id"].(string); got != deletedPrevID {
					tb.Fatalf("expected deleted prev_id=%q, got %v", deletedPrevID, item["prev_id"])
				}
				if !keys["arcade.changelog.sns.deleted"] {
					tb.Fatalf("expected sns deleted bullet, got %#v", keys)
				}
				foundDeleted = true
			}
		}
		if !foundUpdated || !foundAdded || !foundDeleted {
			tb.Fatalf("expected updated+added+deleted sns items, got %#v", items)
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeSNS_PhoneStoredInPhoneField(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/sns stores phone type in phone field",
		Method:         http.MethodPut,
		URL:            "/arcade/sns",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"sns":{"id":"`,
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
			Name:     "Phone SNS Arcade",
			Address:  "SNS Street",
			Nickname: []string{"SNS"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"sns":[{"type":"phone","link":"010-1234-5678","name":"Main"}]
		}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		snsObj, ok := payload["sns"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded sns object in response, got %T", payload["sns"])
		}
		moleculeID, _ := snsObj["id"].(string)
		if moleculeID == "" {
			tb.Fatalf("expected sns molecule id in response")
		}
		items, ok := snsObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected 1 sns item in response, got %T %#v", snsObj["items"], snsObj["items"])
		}
		firstItem, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected first sns item object, got %T", items[0])
		}
		if got, _ := firstItem["type"].(string); got != "phone" {
			tb.Fatalf("expected first sns item type phone, got %v", firstItem["type"])
		}
		if got, _ := firstItem["link"].(string); got != "01012345678" {
			tb.Fatalf("expected first sns item link 01012345678, got %v", firstItem["link"])
		}

		atoms, err := app.FindRecordsByFilter(
			"arcade_sns_atoms",
			"molecule={:id} && type='phone'",
			"",
			0,
			0,
			dbx.Params{"id": moleculeID},
		)
		if err != nil {
			tb.Fatalf("failed to query phone sns atoms: %v", err)
		}
		if len(atoms) != 1 {
			tb.Fatalf("expected one phone sns atom, got %d", len(atoms))
		}
		if got := atoms[0].GetString("phone"); got != "01012345678" {
			tb.Fatalf("expected normalized phone value %q, got %q", "01012345678", got)
		}
		if got := atoms[0].GetString("link"); got != "" {
			tb.Fatalf("expected empty link for phone type, got %q", got)
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeSNS_AllowsEmptySNSArray(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/sns allows empty sns array",
		Method:         http.MethodPut,
		URL:            "/arcade/sns",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"sns":{"id":"`,
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
			Name:     "Empty SNS Arcade",
			Address:  "SNS Street",
			Nickname: []string{"SNS"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"sns":[]
		}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		snsObj, ok := payload["sns"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded sns object in response, got %T", payload["sns"])
		}
		moleculeID, _ := snsObj["id"].(string)
		if moleculeID == "" {
			tb.Fatalf("expected sns molecule id in response")
		}
		items, ok := snsObj["items"].([]any)
		if !ok {
			tb.Fatalf("expected sns.items array, got %T", snsObj["items"])
		}
		if len(items) != 0 {
			tb.Fatalf("expected empty sns.items array, got %#v", items)
		}

		atoms, err := app.FindRecordsByFilter(
			"arcade_sns_atoms",
			"molecule={:id}",
			"",
			0,
			0,
			dbx.Params{"id": moleculeID},
		)
		if err != nil {
			tb.Fatalf("failed to query sns atoms: %v", err)
		}
		if len(atoms) != 0 {
			tb.Fatalf("expected no sns atoms, got %d", len(atoms))
		}
	}

	scenario.Test(t)
}

func seedSNSMolecule(tb testing.TB, app *tests.TestApp, arcadeID, createdBy string) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("arcade_sns")
	if err != nil {
		tb.Fatalf("failed to load arcade_sns collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("arcade", arcadeID)
	if createdBy != "" {
		rec.Set("createdBy", createdBy)
	}
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save arcade_sns record: %v", err)
	}

	arcadeRec, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		tb.Fatalf("failed to load arcade: %v", err)
	}
	arcadeRec.Set("sns", rec.Id)
	if err := app.Save(arcadeRec); err != nil {
		tb.Fatalf("failed to link arcade.sns: %v", err)
	}

	return rec.Id
}

func seedSNSAtom(tb testing.TB, app *tests.TestApp, moleculeID, createdBy, snsType, link, name string) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("arcade_sns_atoms")
	if err != nil {
		tb.Fatalf("failed to load arcade_sns_atoms collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("molecule", moleculeID)
	rec.Set("type", snsType)
	rec.Set("link", link)
	rec.Set("name", name)
	if createdBy != "" {
		rec.Set("createdBy", createdBy)
	}
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save arcade_sns_atoms record: %v", err)
	}

	return rec.Id
}
