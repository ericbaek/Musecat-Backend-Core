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

func TestUpdateArcadePhoto_PromotesPublicAndWritesChangelog(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string
	var photoAtomID1 string
	var photoAtomID2 string
	var photoAtomID3 string
	var userID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/photo creates molecule and writes changelog",
		Method:         http.MethodPut,
		URL:            "/arcade/photo",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"photo":"`,
			`"count":2`,
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
			Name:     "Photo Arcade",
			Address:  "Photo Street",
			Nickname: []string{"Photo"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		photoAtomID1 = seedPhotoAtom(tb, app, arcadeID, user.Id, false)
		photoAtomID2 = seedPhotoAtom(tb, app, arcadeID, user.Id, true)
		photoAtomID3 = seedPhotoAtom(tb, app, arcadeID, user.Id, true)
		seedPhotoMolecule(tb, app, arcadeID, user.Id, []string{photoAtomID2, photoAtomID3})

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"photos":["%s","%s"]
		}`, arcadeID, photoAtomID1, photoAtomID2))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		moleculeID, _ := payload["photo"].(string)
		if moleculeID == "" {
			tb.Fatalf("expected photo molecule id in response")
		}

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		if got := arcadeRec.GetString("photo"); got != moleculeID {
			tb.Fatalf("expected arcade.photo=%q, got %q", moleculeID, got)
		}

		photoRec, err := app.FindRecordById("arcade_photo", moleculeID)
		if err != nil {
			tb.Fatalf("failed to load arcade_photo: %v", err)
		}
		if got := photoRec.GetString("arcade"); got != arcadeID {
			tb.Fatalf("expected arcade_photo.arcade=%q, got %q", arcadeID, got)
		}

		stored := photoRec.GetStringSlice("photos")
		if len(stored) != 2 {
			tb.Fatalf("expected 2 photos in molecule, got %d (%v)", len(stored), stored)
		}
		storedSet := map[string]struct{}{}
		for _, id := range stored {
			storedSet[id] = struct{}{}
		}
		if _, ok := storedSet[photoAtomID1]; !ok {
			tb.Fatalf("expected photos to include %s, got %v", photoAtomID1, stored)
		}
		if _, ok := storedSet[photoAtomID2]; !ok {
			tb.Fatalf("expected photos to include %s, got %v", photoAtomID2, stored)
		}

		atom1, err := app.FindRecordById("arcade_photo_atoms", photoAtomID1)
		if err != nil {
			tb.Fatalf("failed to load photo atom 1: %v", err)
		}
		if !atom1.GetBool("public") {
			tb.Fatalf("expected photo atom 1 public=true")
		}

		atom2, err := app.FindRecordById("arcade_photo_atoms", photoAtomID2)
		if err != nil {
			tb.Fatalf("failed to load photo atom 2: %v", err)
		}
		if !atom2.GetBool("public") {
			tb.Fatalf("expected photo atom 2 public=true")
		}

		atom3, err := app.FindRecordById("arcade_photo_atoms", photoAtomID3)
		if err != nil {
			tb.Fatalf("failed to load photo atom 3: %v", err)
		}
		if !atom3.GetBool("public") {
			tb.Fatalf("expected photo atom 3 public=true")
		}

		changes, err := app.FindRecordsByFilter("arcade_changelog", "arcade={:id} && changed='photo'", "", 0, 0, dbx.Params{"id": arcadeID})
		if err != nil {
			tb.Fatalf("failed to load arcade_changelog: %v", err)
		}
		if len(changes) != 1 {
			tb.Fatalf("expected 1 photo changelog, got %d", len(changes))
		}
		if got := changes[0].GetString("by"); got != userID {
			tb.Fatalf("expected changelog.by=%q, got %q", userID, got)
		}
		if got := changes[0].GetString("to"); got != moleculeID {
			tb.Fatalf("expected changelog.to=%q, got %q", moleculeID, got)
		}

		logObj := decodeLogObject(tb, changes[0].Get("log"))
		if got, _ := logObj["type"].(string); got != "photo_diff" {
			tb.Fatalf("expected changelog.log.type=photo_diff, got %v", logObj["type"])
		}
		if got, _ := logObj["version"].(float64); got != 1 {
			tb.Fatalf("expected changelog.log.version=1, got %v", logObj["version"])
		}
		items, ok := logObj["items"].([]any)
		if !ok || len(items) != 3 {
			tb.Fatalf("expected 3 photo log items, got %T %#v", logObj["items"], logObj["items"])
		}
		keySet := map[string]bool{}
		changeTypes := map[string]int{}
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok {
				tb.Fatalf("expected photo log item object, got %T", raw)
			}
			changeType, _ := item["change_type"].(string)
			changeTypes[changeType]++
			bullets, ok := item["bullets"].([]any)
			if !ok {
				tb.Fatalf("expected photo bullets array, got %T", item["bullets"])
			}
			for key := range i18nBulletKeySet(bullets) {
				keySet[key] = true
			}
		}
		if changeTypes["added"] != 1 || changeTypes["unchanged"] != 1 {
			tb.Fatalf("expected one added and one unchanged photo item, got %#v", changeTypes)
		}
		if changeTypes["deleted"] != 1 {
			tb.Fatalf("expected one deleted photo item, got %#v", changeTypes)
		}
		if !keySet["arcade.changelog.photo.added"] || !keySet["arcade.changelog.photo.kept"] || !keySet["arcade.changelog.photo.deleted"] {
			tb.Fatalf("expected photo bullet keys added+kept+deleted, got %#v", keySet)
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadePhoto_PublicArcadeAwardsEditOnly(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string
	var userID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/photo awards only edit XP for public arcade",
		Method:         http.MethodPut,
		URL:            "/arcade/photo",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"xp_feedback":{`,
			`"diff_exp":3`,
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
			Name:     "Photo Public Arcade",
			Address:  "Photo Public Street",
			Nickname: []string{"PhotoPublic"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		setArcadeVisibility(tb, app, arcadeID, true, false)

		photoAtomID1 := seedPhotoAtom(tb, app, arcadeID, user.Id, false)
		photoAtomID2 := seedPhotoAtom(tb, app, arcadeID, user.Id, true)
		seedPhotoMolecule(tb, app, arcadeID, user.Id, []string{photoAtomID2})

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"photos":["%s","%s"]
		}`, arcadeID, photoAtomID1, photoAtomID2))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		feedback, ok := payload["xp_feedback"].(map[string]any)
		if !ok {
			tb.Fatalf("expected xp_feedback object, got %T", payload["xp_feedback"])
		}
		if got := feedback["diff_exp"]; got != float64(3) {
			tb.Fatalf("expected diff_exp=3, got %#v", got)
		}

		editRows, err := app.DB().NewQuery(`
SELECT COUNT(*) AS count
FROM user_level_log
WHERE "user" = {:user}
  AND kind LIKE {:kind}
`).Bind(dbx.Params{
			"user": userID,
			"kind": "xp:arcade-edit:photo:" + arcadeID + ":%",
		}).Rows()
		if err != nil {
			tb.Fatalf("failed to query photo edit logs: %v", err)
		}
		defer editRows.Close()

		if !editRows.Next() {
			tb.Fatalf("expected photo edit log count row")
		}
		var editCount int
		if err := editRows.Scan(&editCount); err != nil {
			tb.Fatalf("failed to scan photo edit log count: %v", err)
		}
		if editCount != 1 {
			tb.Fatalf("expected exactly 1 photo edit log, got %d", editCount)
		}

		submissionRows, err := app.DB().NewQuery(`
SELECT COUNT(*) AS count
FROM user_level_log
WHERE "user" = {:user}
  AND kind = {:kind}
`).Bind(dbx.Params{
			"user": userID,
			"kind": "xp:arcade-photo-submission:" + arcadeID,
		}).Rows()
		if err != nil {
			tb.Fatalf("failed to query photo submission logs: %v", err)
		}
		defer submissionRows.Close()

		if !submissionRows.Next() {
			tb.Fatalf("expected photo submission log count row")
		}
		var submissionCount int
		if err := submissionRows.Scan(&submissionCount); err != nil {
			tb.Fatalf("failed to scan photo submission log count: %v", err)
		}
		if submissionCount != 0 {
			tb.Fatalf("expected no photo submission log, got %d", submissionCount)
		}
	}

	scenario.Test(t)
}

func seedPhotoAtom(tb testing.TB, app *tests.TestApp, arcadeID, createdBy string, isPublic bool) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("arcade_photo_atoms")
	if err != nil {
		tb.Fatalf("failed to load arcade_photo_atoms collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("arcade", arcadeID)
	rec.Set("public", isPublic)
	file, err := filesystem.NewFileFromBytes(pngFixtureBytes(), "seed.png")
	if err != nil {
		tb.Fatalf("failed to create seed photo file: %v", err)
	}
	rec.Set("photo", file)
	if createdBy != "" {
		rec.Set("createdBy", createdBy)
	}
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save arcade_photo_atoms record: %v", err)
	}

	return rec.Id
}

func seedPhotoMolecule(tb testing.TB, app *tests.TestApp, arcadeID, createdBy string, photoIDs []string) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("arcade_photo")
	if err != nil {
		tb.Fatalf("failed to load arcade_photo collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("arcade", arcadeID)
	rec.Set("photos", photoIDs)
	if createdBy != "" {
		rec.Set("createdBy", createdBy)
	}
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save arcade_photo record: %v", err)
	}

	arcadeRec, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		tb.Fatalf("failed to load arcade: %v", err)
	}
	arcadeRec.Set("photo", rec.Id)
	if err := app.Save(arcadeRec); err != nil {
		tb.Fatalf("failed to link arcade.photo: %v", err)
	}

	return rec.Id
}
