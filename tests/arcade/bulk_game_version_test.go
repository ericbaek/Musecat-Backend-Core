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

func TestBulkUpdateArcadeGameVersion_Success(t *testing.T) {
	headers := map[string]string{}
	var currentGameVersionID string
	var newGameVersionID string
	var atomIDs []string
	atomArcades := map[string]struct {
		id   string
		name string
	}{}

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/game/bulk_version updates many atoms and writes one changelog",
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

		arcade1Name := "Bulk Version Arcade A"
		arcade1ID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     arcade1Name,
			Address:  "Bulk Street A",
			Nickname: []string{"BulkA"},
			Location: location{Lat: 37.5665, Lon: 126.9780},
		})
		arcade2Name := "Bulk Version Arcade B"
		arcade2ID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     arcade2Name,
			Address:  "Bulk Street B",
			Nickname: []string{"BulkB"},
			Location: location{Lat: 35.1796, Lon: 129.0756},
		})

		currentGameVersionID = seedGameSeriesVersion(tb, app)
		newGameVersionID = seedGameSeriesVersion(tb, app)

		molecule1ID := seedArcadeGameMolecule(tb, app, arcade1ID)
		molecule2ID := seedArcadeGameMolecule(tb, app, arcade2ID)
		atom1ID := seedArcadeGameAtom(tb, app, molecule1ID, currentGameVersionID, "1F")
		atom2ID := seedArcadeGameAtom(tb, app, molecule2ID, currentGameVersionID, "2F")
		atomIDs = []string{atom1ID, atom2ID}
		atomArcades[atom1ID] = struct {
			id   string
			name string
		}{id: arcade1ID, name: arcade1Name}
		atomArcades[atom2ID] = struct {
			id   string
			name string
		}{id: arcade2ID, name: arcade2Name}

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"atom_ids":[%q,%q],
			"current_game_version_series":%q,
			"new_game_version_series":%q
		}`, atomIDs[0], atomIDs[1], currentGameVersionID, newGameVersionID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		if _, ok := payload["arcade"]; ok {
			tb.Fatalf("expected no arcade field in response, got %v", payload["arcade"])
		}
		if got := fmt.Sprintf("%v", payload["from"]); got != currentGameVersionID {
			tb.Fatalf("expected from=%q, got %q", currentGameVersionID, got)
		}
		if got := fmt.Sprintf("%v", payload["to"]); got != newGameVersionID {
			tb.Fatalf("expected to=%q, got %q", newGameVersionID, got)
		}
		if got := fmt.Sprintf("%v", payload["count"]); got != "2" {
			tb.Fatalf("expected count=2, got %v", payload["count"])
		}

		for _, atomID := range atomIDs {
			atom, err := app.FindRecordById("arcade_game_atoms", atomID)
			if err != nil {
				tb.Fatalf("failed to load updated atom %s: %v", atomID, err)
			}
			if got := atom.GetString("game"); got != newGameVersionID {
				tb.Fatalf("expected atom.game=%q, got %q", newGameVersionID, got)
			}
			if got := atom.GetBool("uncertain"); !got {
				tb.Fatalf("expected atom.uncertain=true, got %v", got)
			}
			if got := atom.GetString("prev_game"); got != currentGameVersionID {
				tb.Fatalf("expected atom.prev_game=%q, got %q", currentGameVersionID, got)
			}
		}

		changes, err := app.FindRecordsByFilter(
			"arcade_changelog",
			"changed='bulk_game_version' && from={:from} && to={:to}",
			"-created",
			0,
			0,
			dbx.Params{"from": currentGameVersionID, "to": newGameVersionID},
		)
		if err != nil {
			tb.Fatalf("failed to load arcade_changelog: %v", err)
		}
		if len(changes) != 1 {
			tb.Fatalf("expected one bulk_game_version changelog row, got %d", len(changes))
		}
		change := changes[0]
		if got := change.GetString("arcade"); got != "" {
			tb.Fatalf("expected changelog.arcade to be empty, got %q", got)
		}
		if got := change.GetString("from"); got != currentGameVersionID {
			tb.Fatalf("expected changelog.from=%q, got %q", currentGameVersionID, got)
		}
		if got := change.GetString("to"); got != newGameVersionID {
			tb.Fatalf("expected changelog.to=%q, got %q", newGameVersionID, got)
		}

		logObj := decodeAnyMap(tb, change.Get("log"))
		if got, _ := logObj["type"].(string); got != "bulk_game_version_diff" {
			tb.Fatalf("expected log.type=bulk_game_version_diff, got %v", logObj["type"])
		}
		if got, _ := logObj["version"].(float64); got != 1 {
			tb.Fatalf("expected log.version=1, got %v", logObj["version"])
		}
		if got, _ := logObj["before_game"].(string); got != currentGameVersionID {
			tb.Fatalf("expected log.before_game=%q, got %v", currentGameVersionID, logObj["before_game"])
		}
		if got, _ := logObj["after_game"].(string); got != newGameVersionID {
			tb.Fatalf("expected log.after_game=%q, got %v", newGameVersionID, logObj["after_game"])
		}

		items, ok := logObj["items"].([]any)
		if !ok || len(items) != 2 {
			tb.Fatalf("expected 2 log items, got %T %#v", logObj["items"], logObj["items"])
		}
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok {
				tb.Fatalf("expected log item object, got %T", raw)
			}
			atomID, _ := item["atom_id"].(string)
			meta, ok := atomArcades[atomID]
			if !ok {
				tb.Fatalf("unexpected atom_id in log: %q", atomID)
			}
			if got, _ := item["arcade_id"].(string); got != meta.id {
				tb.Fatalf("expected item.arcade_id=%q, got %v", meta.id, item["arcade_id"])
			}
			if got, _ := item["arcade_name"].(string); got != meta.name {
				tb.Fatalf("expected item.arcade_name=%q, got %v", meta.name, item["arcade_name"])
			}
		}

	}

	scenario.Test(t)
}

func TestBulkUpdateArcadeGameVersion_RejectsMismatchedCurrentGame(t *testing.T) {
	headers := map[string]string{}
	var currentGameVersionID string
	var newGameVersionID string
	var atomIDs []string

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/game/bulk_version rejects mismatched current game",
		Method:         http.MethodPost,
		URL:            "/arcade/game/bulk_version",
		Headers:        headers,
		ExpectedStatus: http.StatusBadRequest,
		ExpectedContent: []string{
			`"error":"validation failed"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUserWithTags(tb, app, []string{"moderator"})
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Bulk Reject Arcade",
			Address:  "Reject Street",
			Nickname: []string{"Reject"},
			Location: location{Lat: 37.5665, Lon: 126.9780},
		})

		currentGameVersionID = seedGameSeriesVersion(tb, app)
		newGameVersionID = seedGameSeriesVersion(tb, app)
		foreignGameVersionID := seedGameSeriesVersion(tb, app)

		moleculeID := seedArcadeGameMolecule(tb, app, arcadeID)
		atomIDs = []string{
			seedArcadeGameAtom(tb, app, moleculeID, currentGameVersionID, "1F"),
			seedArcadeGameAtom(tb, app, moleculeID, foreignGameVersionID, "2F"),
		}

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"atom_ids":[%q,%q],
			"current_game_version_series":%q,
			"new_game_version_series":%q
		}`, atomIDs[0], atomIDs[1], currentGameVersionID, newGameVersionID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		if got := fmt.Sprintf("%v", payload["details"]); !strings.Contains(got, "must match current_game_version_series") {
			tb.Fatalf("expected current game mismatch error, got %v", payload["details"])
		}

		for _, atomID := range atomIDs {
			atom, err := app.FindRecordById("arcade_game_atoms", atomID)
			if err != nil {
				tb.Fatalf("failed to load atom %s: %v", atomID, err)
			}
			if got := atom.GetString("game"); got == newGameVersionID {
				tb.Fatalf("expected atom %s to remain unchanged", atomID)
			}
			if got := atom.GetBool("uncertain"); got {
				tb.Fatalf("expected atom %s uncertain=false on failure, got true", atomID)
			}
			if got := atom.GetString("prev_game"); got != "" {
				tb.Fatalf("expected atom %s prev_game to remain empty on failure, got %q", atomID, got)
			}
		}

		changes, err := app.FindRecordsByFilter(
			"arcade_changelog",
			"changed='bulk_game_version' && from={:from} && to={:to}",
			"",
			0,
			0,
			dbx.Params{"from": currentGameVersionID, "to": newGameVersionID},
		)
		if err != nil {
			tb.Fatalf("failed to query changelog: %v", err)
		}
		if len(changes) != 0 {
			tb.Fatalf("expected no changelog rows on failure, got %d", len(changes))
		}
	}

	scenario.Test(t)
}

func decodeAnyMap(tb testing.TB, raw any) map[string]any {
	tb.Helper()

	buf, err := json.Marshal(raw)
	if err != nil {
		tb.Fatalf("failed to marshal raw value: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(buf, &out); err != nil {
		tb.Fatalf("failed to unmarshal raw value: %v", err)
	}
	return out
}
