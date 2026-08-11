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

	arcadegame "github.com/ericbaek/musecat-backend-core/handlers/arcade/game"
)

func TestBulkUpdateArcadeGameVersion_Success(t *testing.T) {
	headers := map[string]string{}
	var currentVersionID, newVersionID string
	var entryIDs []string
	var arcadeIDs []string

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/game/bulk_version updates history without uncertainty",
		Method:         http.MethodPost,
		URL:            "/arcade/game/bulk_version",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"count":2`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		token, user := createAuthUserWithTags(tb, app, []string{"moderator"})
		headers["Authorization"] = "Bearer " + token

		seriesID := seedGameSeries(tb, app, 99, "Bulk Series")
		currentVersionID = seedGameSeriesVersionWithSeries(tb, app, seriesID, "2025-01-01", "Current")
		newVersionID = seedGameSeriesVersionWithSeries(tb, app, seriesID, "2026-01-01", "New")

		for _, name := range []string{"Bulk Arcade A", "Bulk Arcade B"} {
			arcadeID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
				Name:     name,
				Address:  name + " Street",
				Nickname: []string{name},
				Location: location{Lat: 37.5, Lon: 127.0},
			})
			arcadeIDs = append(arcadeIDs, arcadeID)
			entries, _ := seedBulkHistoryState(tb, app, arcadeID, user.Id, currentVersionID)
			entryIDs = append(entryIDs, entries[0])
		}

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"game_ids":[%q,%q],
			"current_game_version_series":%q,
			"new_game_version_series":%q
		}`, entryIDs[0], entryIDs[1], currentVersionID, newVersionID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("decode response: %v", err)
		}
		if got := fmt.Sprintf("%v", payload["from"]); got != currentVersionID {
			t.Fatalf("expected from=%q, got %q", currentVersionID, got)
		}
		if got := fmt.Sprintf("%v", payload["to"]); got != newVersionID {
			t.Fatalf("expected to=%q, got %q", newVersionID, got)
		}

		revisions, err := app.FindCollectionByNameOrId("arcade_game_history")
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"uncertain", "previous_version", "last_confirmed_at", "last_confirmed_by"} {
			if revisions.Fields.GetByName(field) != nil {
				t.Fatalf("bulk update schema still exposes %s", field)
			}
		}
		for i, arcadeID := range arcadeIDs {
			arcade, err := app.FindRecordById("arcade", arcadeID)
			if err != nil {
				t.Fatal(err)
			}
			stateID := arcade.GetString("game_state")
			rows, err := app.FindRecordsByFilter("arcade_game_history", "batch={:batch} && entry={:entry}", "", 1, 0, dbx.Params{"batch": stateID, "entry": entryIDs[i]})
			if err != nil || len(rows) != 1 {
				t.Fatalf("expected updated revision for arcade %s, err=%v rows=%d", arcadeID, err, len(rows))
			}
			if got := rows[0].GetString("version"); got != newVersionID {
				t.Fatalf("expected version=%q, got %q", newVersionID, got)
			}
			changes, err := app.FindRecordsByFilter("arcade_changelog", "arcade={:arcade} && changed='game'", "-created", 0, 0, dbx.Params{"arcade": arcadeID})
			if err != nil || len(changes) != 2 {
				t.Fatalf("expected initial and bulk game changelogs for %s, err=%v rows=%d", arcadeID, err, len(changes))
			}
			log := decodeAnyMap(tb, changes[0].Get("log"))
			if got := log["type"]; got != "game_diff" {
				t.Fatalf("expected game_diff, got %v", got)
			}
			if strings.Contains(string(mustJSON(tb, log)), "uncertain") || strings.Contains(string(mustJSON(tb, log)), "previous_version") {
				t.Fatalf("bulk changelog contains removed concepts: %#v", log)
			}
		}
	}

	scenario.Test(t)
}

func TestBulkUpdateArcadeGameVersion_RejectsMismatchedCurrentGameAtomically(t *testing.T) {
	headers := map[string]string{}
	var arcadeID, stateID string
	var currentVersionID, newVersionID, foreignVersionID string
	var entryIDs []string

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/game/bulk_version rejects mismatched current game atomically",
		Method:         http.MethodPost,
		URL:            "/arcade/game/bulk_version",
		Headers:        headers,
		ExpectedStatus: http.StatusBadRequest,
		ExpectedContent: []string{
			`"error":"bulk update failed"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp { return newArcadeTestApp(tb) },
	}
	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		token, user := createAuthUserWithTags(tb, app, []string{"developer"})
		headers["Authorization"] = "Bearer " + token
		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{Name: "Bulk Reject", Address: "Reject Street", Location: location{Lat: 37.5, Lon: 127.0}})
		seriesID := seedGameSeries(tb, app, 100, "Bulk Reject Series")
		foreignSeriesID := seedGameSeries(tb, app, 101, "Foreign Series")
		currentVersionID = seedGameSeriesVersionWithSeries(tb, app, seriesID, "2025-01-01", "Current")
		newVersionID = seedGameSeriesVersionWithSeries(tb, app, seriesID, "2026-01-01", "New")
		foreignVersionID = seedGameSeriesVersionWithSeries(tb, app, foreignSeriesID, "2025-01-01", "Foreign")
		entryIDs, stateID = seedBulkHistoryState(tb, app, arcadeID, user.Id, currentVersionID, foreignVersionID)
		scenario.Body = strings.NewReader(fmt.Sprintf(`{"game_ids":[%q,%q],"current_game_version_series":%q,"new_game_version_series":%q}`, entryIDs[0], entryIDs[1], currentVersionID, newVersionID))
	}
	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()
		if changes, err := app.FindRecordsByFilter("arcade_changelog", "arcade={:arcade} && changed='game'", "", 0, 0, dbx.Params{"arcade": arcadeID}); err != nil || len(changes) != 1 {
			t.Fatalf("expected only initial changelog after failed bulk, err=%v rows=%d", err, len(changes))
		}
		arcade, err := app.FindRecordById("arcade", arcadeID)
		if err != nil || arcade.GetString("game_state") != stateID {
			t.Fatalf("bulk failure moved state pointer: err=%v state=%q", err, arcade.GetString("game_state"))
		}
	}
	scenario.Test(t)
}

func seedBulkHistoryState(tb testing.TB, app *tests.TestApp, arcadeID, userID string, versions ...string) ([]string, string) {
	tb.Helper()
	games := make([]arcadegame.GameAtomInput, 0, len(versions))
	for i, versionID := range versions {
		games = append(games, arcadegame.GameAtomInput{Game: versionID, Location: fmt.Sprintf("%dF", i+1), Quantity: 1, Price: arcadegame.Price{Currency: "KRW", Type: "custom", List: []arcadegame.PriceItem{{Value: ptrFloat32(500)}}, Accept: []string{}}, Tag: []arcadegame.TagItem{}})
	}
	var stateID string
	if err := app.RunInTransaction(func(tx core.App) error {
		var err error
		stateID, err = arcadegame.UpdateArcadeGameTx(tx, arcadegame.UpdateArcadeGameBody{Arcade: arcadeID, Games: games}, userID)
		return err
	}); err != nil {
		tb.Fatalf("seed history state: %v", err)
	}
	rows, err := app.FindRecordsByFilter("arcade_game_history", "batch={:batch}", "entry", 0, 0, dbx.Params{"batch": stateID})
	if err != nil || len(rows) != len(versions) {
		tb.Fatalf("seed history rows: err=%v rows=%d", err, len(rows))
	}
	entries := make([]string, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, row.GetString("entry"))
	}
	return entries, stateID
}

func ptrFloat32(v float32) *float32 { return &v }

func mustJSON(tb testing.TB, value any) []byte {
	tb.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		tb.Fatalf("marshal JSON: %v", err)
	}
	return b
}

func decodeAnyMap(tb testing.TB, raw any) map[string]any {
	tb.Helper()
	buf, err := json.Marshal(raw)
	if err != nil {
		tb.Fatalf("marshal raw value: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(buf, &out); err != nil {
		tb.Fatalf("unmarshal raw value: %v", err)
	}
	return out
}
