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

func TestConfirmArcadeGameUncertain_Success(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string
	var selectedAtomID string
	var unselectedAtomID string
	var currentVersionID string
	var prevVersionID string

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/game/confirm confirms only selected uncertain atoms",
		Method:         http.MethodPost,
		URL:            "/arcade/game/confirm",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"action":"confirm"`,
			`"selected_count":1`,
			`"game":{"id":"`,
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
			Name:     "Confirm Arcade",
			Address:  "Confirm Street",
			Nickname: []string{"Confirm"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		currentVersionID = seedGameSeriesVersion(tb, app)
		prevVersionID = seedGameSeriesVersion(tb, app)

		moleculeID := seedArcadeGameMolecule(tb, app, arcadeID)
		selectedAtomID = seedArcadeGameAtom(tb, app, moleculeID, currentVersionID, "1F")
		unselectedAtomID = seedArcadeGameAtom(tb, app, moleculeID, currentVersionID, "2F")

		for _, atomID := range []string{selectedAtomID, unselectedAtomID} {
			atom, err := app.FindRecordById("arcade_game_atoms", atomID)
			if err != nil {
				tb.Fatalf("failed to load atom %s: %v", atomID, err)
			}
			atom.Set("uncertain", true)
			atom.Set("prev_game", prevVersionID)
			if err := app.Save(atom); err != nil {
				tb.Fatalf("failed to update atom %s uncertainty: %v", atomID, err)
			}
		}

		scenario.Body = strings.NewReader(fmt.Sprintf(`{"atom_ids":["%s"]}`, selectedAtomID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		if got := fmt.Sprintf("%v", payload["action"]); got != "confirm" {
			tb.Fatalf("expected action confirm, got %q", got)
		}
		if got := fmt.Sprintf("%v", payload["arcade"]); got != arcadeID {
			tb.Fatalf("expected arcade %q, got %q", arcadeID, got)
		}
		if got := fmt.Sprintf("%v", payload["selected_count"]); got != "1" {
			tb.Fatalf("expected selected_count=1, got %v", payload["selected_count"])
		}

		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object in response, got %T", payload["game"])
		}
		newMoleculeID, _ := gameObj["id"].(string)
		if newMoleculeID == "" {
			tb.Fatalf("expected new game molecule id")
		}

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		if got := arcadeRec.GetString("game"); got != newMoleculeID {
			tb.Fatalf("expected arcade.game=%q, got %q", newMoleculeID, got)
		}

		atoms, err := app.FindRecordsByFilter("arcade_game_atoms", "molecule={:id}", "+created", 0, 0, dbx.Params{"id": newMoleculeID})
		if err != nil {
			tb.Fatalf("failed to load new atoms: %v", err)
		}
		if len(atoms) != 2 {
			tb.Fatalf("expected 2 cloned atoms, got %d", len(atoms))
		}

		var selectedAtom, otherAtom *core.Record
		for _, atom := range atoms {
			switch atom.GetString("location") {
			case "1F":
				selectedAtom = atom
			case "2F":
				otherAtom = atom
			}
		}
		if selectedAtom == nil || otherAtom == nil {
			tb.Fatalf("expected both cloned atoms to be present: selected=%v other=%v", selectedAtom, otherAtom)
		}
		if got := selectedAtom.GetString("game"); got != currentVersionID {
			tb.Fatalf("expected selected atom to keep current game %q, got %q", currentVersionID, got)
		}
		if got := selectedAtom.GetBool("uncertain"); got {
			tb.Fatalf("expected selected atom uncertain=false, got true")
		}
		if got := selectedAtom.GetString("prev_game"); got != "" {
			tb.Fatalf("expected selected atom prev_game cleared, got %q", got)
		}
		if got := otherAtom.GetBool("uncertain"); !got {
			tb.Fatalf("expected unselected atom uncertain=true, got false")
		}
		if got := otherAtom.GetString("prev_game"); got != prevVersionID {
			tb.Fatalf("expected unselected atom prev_game=%q, got %q", prevVersionID, got)
		}

		changes, err := app.FindRecordsByFilter("arcade_changelog", "arcade={:id} && changed='game'", "-created", 0, 0, dbx.Params{"id": arcadeID})
		if err != nil {
			tb.Fatalf("failed to load arcade_changelog: %v", err)
		}
		if len(changes) != 1 {
			tb.Fatalf("expected 1 changelog row, got %d", len(changes))
		}
		logObj := decodeAnyMap(tb, changes[0].Get("log"))
		items, ok := logObj["items"].([]any)
		if !ok || len(items) != 2 {
			tb.Fatalf("expected 2 log items, got %T %#v", logObj["items"], logObj["items"])
		}
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok {
				tb.Fatalf("expected log item object, got %T", raw)
			}
			if fmt.Sprintf("%v", item["prev_id"]) != selectedAtomID {
				continue
			}
			keys := i18nBulletKeySet(item["bullets"].([]any))
			if !keys["arcade.changelog.game.uncertain.confirm"] {
				tb.Fatalf("expected confirm diff bullet, got %#v", keys)
			}
			return
		}
		tb.Fatalf("expected changelog item for selected atom %q", selectedAtomID)
	}

	scenario.Test(t)
}

func TestRollbackArcadeGameUncertain_Success(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string
	var selectedAtomID string
	var unselectedAtomID string
	var currentVersionID string
	var prevVersionID string

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/game/rollback rolls selected uncertain atoms back to prev_game",
		Method:         http.MethodPost,
		URL:            "/arcade/game/rollback",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"action":"rollback"`,
			`"game":{"id":"`,
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
			Name:     "Rollback Arcade",
			Address:  "Rollback Street",
			Nickname: []string{"Rollback"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		currentVersionID = seedGameSeriesVersion(tb, app)
		prevVersionID = seedGameSeriesVersion(tb, app)

		moleculeID := seedArcadeGameMolecule(tb, app, arcadeID)
		selectedAtomID = seedArcadeGameAtom(tb, app, moleculeID, currentVersionID, "1F")
		unselectedAtomID = seedArcadeGameAtom(tb, app, moleculeID, currentVersionID, "2F")

		for _, atomID := range []string{selectedAtomID, unselectedAtomID} {
			atom, err := app.FindRecordById("arcade_game_atoms", atomID)
			if err != nil {
				tb.Fatalf("failed to load atom %s: %v", atomID, err)
			}
			atom.Set("uncertain", true)
			atom.Set("prev_game", prevVersionID)
			if err := app.Save(atom); err != nil {
				tb.Fatalf("failed to update atom %s uncertainty: %v", atomID, err)
			}
		}

		scenario.Body = strings.NewReader(fmt.Sprintf(`{"atom_ids":["%s"]}`, selectedAtomID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		if got := fmt.Sprintf("%v", payload["action"]); got != "rollback" {
			tb.Fatalf("expected action rollback, got %q", got)
		}
		if got := fmt.Sprintf("%v", payload["arcade"]); got != arcadeID {
			tb.Fatalf("expected arcade %q, got %q", arcadeID, got)
		}

		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object in response, got %T", payload["game"])
		}
		newMoleculeID, _ := gameObj["id"].(string)
		if newMoleculeID == "" {
			tb.Fatalf("expected new game molecule id")
		}

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		if got := arcadeRec.GetString("game"); got != newMoleculeID {
			tb.Fatalf("expected arcade.game=%q, got %q", newMoleculeID, got)
		}

		atoms, err := app.FindRecordsByFilter("arcade_game_atoms", "molecule={:id}", "+created", 0, 0, dbx.Params{"id": newMoleculeID})
		if err != nil {
			tb.Fatalf("failed to load new atoms: %v", err)
		}
		if len(atoms) != 2 {
			tb.Fatalf("expected 2 cloned atoms, got %d", len(atoms))
		}

		var selectedAtom, otherAtom *core.Record
		for _, atom := range atoms {
			switch atom.GetString("location") {
			case "1F":
				selectedAtom = atom
			case "2F":
				otherAtom = atom
			}
		}
		if selectedAtom == nil || otherAtom == nil {
			tb.Fatalf("expected both cloned atoms to be present: selected=%v other=%v", selectedAtom, otherAtom)
		}
		if got := selectedAtom.GetString("game"); got != prevVersionID {
			tb.Fatalf("expected selected atom to roll back to %q, got %q", prevVersionID, got)
		}
		if got := selectedAtom.GetBool("uncertain"); got {
			tb.Fatalf("expected selected atom uncertain=false, got true")
		}
		if got := selectedAtom.GetString("prev_game"); got != "" {
			tb.Fatalf("expected selected atom prev_game cleared, got %q", got)
		}
		if got := otherAtom.GetString("game"); got != currentVersionID {
			tb.Fatalf("expected unselected atom to keep current game %q, got %q", currentVersionID, got)
		}
		if got := otherAtom.GetBool("uncertain"); !got {
			tb.Fatalf("expected unselected atom uncertain=true, got false")
		}
		if got := otherAtom.GetString("prev_game"); got != prevVersionID {
			tb.Fatalf("expected unselected atom prev_game=%q, got %q", prevVersionID, got)
		}

		changes, err := app.FindRecordsByFilter("arcade_changelog", "arcade={:id} && changed='game'", "-created", 0, 0, dbx.Params{"id": arcadeID})
		if err != nil {
			tb.Fatalf("failed to load arcade_changelog: %v", err)
		}
		if len(changes) != 1 {
			tb.Fatalf("expected 1 changelog row, got %d", len(changes))
		}
		logObj := decodeAnyMap(tb, changes[0].Get("log"))
		items, ok := logObj["items"].([]any)
		if !ok || len(items) != 2 {
			tb.Fatalf("expected 2 log items, got %T %#v", logObj["items"], logObj["items"])
		}
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok {
				tb.Fatalf("expected log item object, got %T", raw)
			}
			if fmt.Sprintf("%v", item["prev_id"]) != selectedAtomID {
				continue
			}
			keys := i18nBulletKeySet(item["bullets"].([]any))
			if !keys["arcade.changelog.game.uncertain.rollback"] {
				tb.Fatalf("expected rollback diff bullet, got %#v", keys)
			}
			if !keys["arcade.changelog.game.name.changed"] {
				tb.Fatalf("expected rollback to change game version, got %#v", keys)
			}
			return
		}
		tb.Fatalf("expected changelog item for selected atom %q", selectedAtomID)
	}

	scenario.Test(t)
}
