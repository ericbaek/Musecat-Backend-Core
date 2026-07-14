package arcade_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/tools/types"
)

func TestConfirmArcadeGameInformation_UpdatesAtomAndWritesChangelog(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string
	var moleculeID string
	var atomID string
	oldUpdated := time.Date(2024, 4, 10, 9, 0, 0, 0, time.UTC)

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/game/information/confirm refreshes a single game atom",
		Method:         http.MethodPost,
		URL:            "/arcade/game/information/confirm",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"game":"`,
			`"atom":"`,
			`"updated":"`,
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
			Name:     "Info Confirm Arcade",
			Address:  "Info Confirm Street",
			Nickname: []string{"Confirm"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		versionID := seedGameSeriesVersion(tb, app)
		moleculeID = seedArcadeGameMolecule(tb, app, arcadeID)
		atomID = seedArcadeGameAtom(tb, app, moleculeID, versionID, "1F")

		when := oldUpdated.UTC().Format(types.DefaultDateLayout)
		if _, err := app.NonconcurrentDB().
			NewQuery("UPDATE arcade_game_atoms SET updated={:updated} WHERE id={:id}").
			Bind(dbx.Params{"updated": when, "id": atomID}).
			Execute(); err != nil {
			tb.Fatalf("failed to update atom.updated: %v", err)
		}

		scenario.URL = fmt.Sprintf("/arcade/game/information/confirm?id=%s", atomID)
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		if got := fmt.Sprintf("%v", payload["arcade"]); got != arcadeID {
			tb.Fatalf("expected arcade %q, got %q", arcadeID, got)
		}
		if got := fmt.Sprintf("%v", payload["game"]); got != moleculeID {
			tb.Fatalf("expected game %q, got %q", moleculeID, got)
		}
		if got := fmt.Sprintf("%v", payload["atom"]); got != atomID {
			tb.Fatalf("expected atom %q, got %q", atomID, got)
		}
		updated, ok := payload["updated"].(string)
		if !ok || updated == "" {
			tb.Fatalf("expected updated string, got %#v", payload["updated"])
		}
		if updated == oldUpdated.UTC().Format(types.DefaultDateLayout) {
			tb.Fatalf("expected updated timestamp to change, got %v", updated)
		}

		atomRec, err := app.FindRecordById("arcade_game_atoms", atomID)
		if err != nil {
			tb.Fatalf("failed to reload atom: %v", err)
		}
		if got := atomRec.GetString("updated"); got == "" || got == oldUpdated.UTC().Format(types.DefaultDateLayout) {
			tb.Fatalf("expected atom.updated to be refreshed, got %v", got)
		}

		changes, err := app.FindRecordsByFilter("arcade_changelog", "arcade={:id} && changed='game'", "-created", 0, 0, dbx.Params{"id": arcadeID})
		if err != nil {
			tb.Fatalf("failed to load arcade_changelog: %v", err)
		}
		if len(changes) != 1 {
			tb.Fatalf("expected 1 changelog row, got %d", len(changes))
		}

		logObj := decodeAnyMap(tb, changes[0].Get("log"))
		if got := logObj["type"]; got != "game_information_confirm_diff" {
			tb.Fatalf("expected log.type=game_information_confirm_diff, got %v", got)
		}
		items, ok := logObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected one log item, got %T %#v", logObj["items"], logObj["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected log item object, got %T", items[0])
		}
		if got := fmt.Sprintf("%v", item["atom_id"]); got != atomID {
			tb.Fatalf("expected atom_id %q, got %q", atomID, got)
		}
		if got := fmt.Sprintf("%v", item["updated_from"]); got != oldUpdated.UTC().Format(types.DefaultDateLayout) {
			tb.Fatalf("expected updated_from %q, got %q", oldUpdated.UTC().Format(types.DefaultDateLayout), got)
		}
		if got := fmt.Sprintf("%v", item["updated_to"]); got == "" || got == oldUpdated.UTC().Format(types.DefaultDateLayout) {
			tb.Fatalf("expected updated_to to change, got %q", got)
		}
	}

	scenario.Test(t)
}
