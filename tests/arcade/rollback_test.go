package arcade_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	arcadeadmin "github.com/ericbaek/musecat-backend-core/handlers/arcade/admin"
	arcadegame "github.com/ericbaek/musecat-backend-core/handlers/arcade/game"
)

func TestRollbackArcadePart_Basic_Success(t *testing.T) {
	headers := map[string]string{}
	var userID string
	var arcadeID string
	var targetBasicID string
	var currentBasicID string

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/rollback rolls arcade part back to user-provided value",
		Method:         http.MethodPost,
		URL:            "/arcade/rollback",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"part":"basic"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		userID = user.Id
		headers["Authorization"] = "Bearer " + token

		arcadeID, targetBasicID = seedArcade(tb, app, userID, arcadeSeed{
			Name:      "Rollback Target Arcade",
			Address:   "Rollback Street",
			Direction: "",
			Nickname:  []string{"Rollback"},
			Location:  location{Lat: 37.5665, Lon: 126.9780},
		})

		currentBasicID = seedBasicVersion(tb, app, arcadeID, userID, "Rollback Current Basic")

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		arcadeRec.Set("basic", currentBasicID)
		if err := app.Save(arcadeRec); err != nil {
			tb.Fatalf("failed to set arcade.basic: %v", err)
		}

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"part":"basic",
			"value":"%s"
		}`, arcadeID, targetBasicID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode rollback response: %v", err)
		}

		if got := fmt.Sprintf("%v", payload["from"]); got != currentBasicID {
			tb.Fatalf("expected from=%q, got %q", currentBasicID, got)
		}
		if got := fmt.Sprintf("%v", payload["to"]); got != targetBasicID {
			tb.Fatalf("expected to=%q, got %q", targetBasicID, got)
		}

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade after rollback: %v", err)
		}
		if got := arcadeRec.GetString("basic"); got != targetBasicID {
			tb.Fatalf("expected arcade.basic=%q, got %q", targetBasicID, got)
		}

		changes := loadChangelogRecords(tb, app, arcadeID, "basic")
		found := false
		for _, change := range changes {
			if change.GetString("from") == currentBasicID && change.GetString("to") == targetBasicID {
				if change.GetString("by") != userID {
					tb.Fatalf("expected changelog.by=%q, got %q", userID, change.GetString("by"))
				}
				logObj := decodeLogObject(tb, change.Get("log"))
				items, ok := logObj["items"].([]any)
				if !ok || len(items) == 0 {
					tb.Fatalf("expected rollback changelog.log.items, got %v", logObj["items"])
				}
				first, ok := items[0].(map[string]any)
				if !ok {
					tb.Fatalf("expected rollback log item object, got %T", items[0])
				}
				message, _ := first["message"].(string)
				if !strings.Contains(message, "Rollback applied for basic") {
					tb.Fatalf("expected rollback 안내 message in log, got %q", message)
				}
				found = true
				break
			}
		}
		if !found {
			tb.Fatalf("expected rollback changelog row from %q to %q", currentBasicID, targetBasicID)
		}
	}

	scenario.Test(t)
}

func TestRollbackArcadeGame_RestoresHistoricalBatchAndWritesBatchChangelog(t *testing.T) {
	app := newArcadeTestApp(t)
	token, user := createAuthUser(t, app)
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Game Rollback Arcade",
		Address:  "Game Rollback Street",
		Nickname: []string{"GameRollback"},
		Location: location{Lat: 37.5665, Lon: 126.9780},
	})
	seriesID := seedGameSeries(t, app, 31, "Rollback Series")
	oldVersionID := seedGameSeriesVersionWithSeries(t, app, seriesID, "2024-01-01", "Rollback Old")
	newVersionID := seedGameSeriesVersionWithSeries(t, app, seriesID, "2025-01-01", "Rollback New")

	oldPrice := float32(500)
	oldInput := arcadegame.GameAtomInput{
		Game:     oldVersionID,
		Location: "1F",
		Quantity: 1,
		Price: arcadegame.Price{
			Currency: "KRW",
			Type:     "custom",
			List:     []arcadegame.PriceItem{{Value: &oldPrice}},
			Accept:   []string{},
		},
	}
	var oldStateID string
	if err := app.RunInTransaction(func(tx core.App) error {
		var err error
		oldStateID, err = arcadegame.UpdateArcadeGameTx(tx, arcadegame.UpdateArcadeGameBody{
			Arcade: arcadeID,
			Games:  []arcadegame.GameAtomInput{oldInput},
		}, user.Id)
		return err
	}); err != nil {
		t.Fatalf("failed to seed historical game state: %v", err)
	}

	oldRevisions, err := app.FindRecordsByFilter("arcade_game_history", "batch={:batch}", "", 0, 0, map[string]any{"batch": oldStateID})
	if err != nil {
		t.Fatalf("failed to load historical game revisions: %v", err)
	}
	if len(oldRevisions) != 1 {
		t.Fatalf("expected one historical game revision, got %d", len(oldRevisions))
	}
	entryID := oldRevisions[0].GetString("entry")
	if entryID == "" {
		t.Fatal("expected a durable game entry id")
	}

	newPrice := float32(700)
	newInput := oldInput
	newInput.ID = entryID
	newInput.Game = newVersionID
	newInput.Location = "2F"
	newInput.Quantity = 2
	newInput.Price.List = []arcadegame.PriceItem{{Value: &newPrice}}
	var newStateID string
	if err := app.RunInTransaction(func(tx core.App) error {
		var err error
		newStateID, err = arcadegame.UpdateArcadeGameTx(tx, arcadegame.UpdateArcadeGameBody{
			Arcade:      arcadeID,
			BaseStateID: oldStateID,
			Games:       []arcadegame.GameAtomInput{newInput},
		}, user.Id)
		return err
	}); err != nil {
		t.Fatalf("failed to seed current game state: %v", err)
	}
	if oldStateID == newStateID {
		t.Fatal("expected replacement game history batch")
	}

	changes := loadChangelogRecords(t, app, arcadeID, "game")
	if len(changes) != 2 {
		t.Fatalf("expected two game changelog rows before rollback, got %d", len(changes))
	}
	for _, change := range changes {
		for _, stateID := range []string{change.GetString("from"), change.GetString("to")} {
			if stateID == "" {
				continue
			}
			batch, batchErr := app.FindRecordById("arcade_game_history_batch", stateID)
			if batchErr != nil {
				t.Fatalf("game changelog points to missing batch %q: %v", stateID, batchErr)
			}
			if got := batch.GetString("arcade"); got != arcadeID {
				t.Fatalf("batch %q belongs to arcade %q, expected %q", stateID, got, arcadeID)
			}
		}
	}

	rollbackBody := fmt.Sprintf(`{"arcade":%q,"part":"game","value":%q}`, arcadeID, oldStateID)
	res := executeJSONRequest(t, app, http.MethodPost, "/arcade/rollback", rollbackBody, map[string]string{
		"Authorization": "Bearer " + token,
	})
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		var payload map[string]any
		_ = json.NewDecoder(res.Body).Decode(&payload)
		t.Fatalf("expected game rollback status 200, got %d: %#v", res.StatusCode, payload)
	}

	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode game rollback response: %v", err)
	}
	if got := fmt.Sprintf("%v", payload["from"]); got != newStateID {
		t.Fatalf("expected rollback from=%q, got %q", newStateID, got)
	}
	if got := fmt.Sprintf("%v", payload["to"]); got != oldStateID {
		t.Fatalf("expected rollback to=%q, got %q", oldStateID, got)
	}

	arcade, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		t.Fatalf("failed to load arcade after game rollback: %v", err)
	}
	if got := arcade.GetString("game_v2"); got != oldStateID {
		t.Fatalf("expected arcade.game_v2=%q after rollback, got %q", oldStateID, got)
	}

	rolledBackRevisions, err := app.FindRecordsByFilter("arcade_game_history", "batch={:batch}", "", 0, 0, map[string]any{"batch": oldStateID})
	if err != nil {
		t.Fatalf("failed to load rolled-back revisions: %v", err)
	}
	if len(rolledBackRevisions) != 1 {
		t.Fatalf("expected one revision in rolled-back state, got %d", len(rolledBackRevisions))
	}
	rolledBack := rolledBackRevisions[0]
	if rolledBack.GetString("entry") != entryID || rolledBack.GetString("version") != oldVersionID || rolledBack.GetString("location") != "1F" || rolledBack.GetInt("quantity") != 1 {
		t.Fatalf("historical revision was not restored: entry=%q version=%q location=%q quantity=%d", rolledBack.GetString("entry"), rolledBack.GetString("version"), rolledBack.GetString("location"), rolledBack.GetInt("quantity"))
	}

	changes = loadChangelogRecords(t, app, arcadeID, "game")
	if len(changes) != 3 {
		t.Fatalf("expected one new game rollback changelog row, got %d total rows", len(changes))
	}
	var rollbackChange *core.Record
	for _, change := range changes {
		if change.GetString("from") == newStateID && change.GetString("to") == oldStateID {
			rollbackChange = change
			break
		}
	}
	if rollbackChange == nil {
		t.Fatalf("expected rollback changelog from %q to %q", newStateID, oldStateID)
	}
	if got := rollbackChange.GetString("by"); got != user.Id {
		t.Fatalf("expected rollback changelog.by=%q, got %q", user.Id, got)
	}
	rollbackLog := decodeLogObject(t, rollbackChange.Get("log"))
	if got := rollbackLog["type"]; got != "game_diff" {
		t.Fatalf("expected rollback game log type game_diff, got %v", got)
	}
}

func TestRollbackArcadePart_WithReport_CreatesArcadeRequestAdmin(t *testing.T) {
	headers := map[string]string{}
	var userID string
	var arcadeID string
	var targetBasicID string
	var sourceChangelogID string

	restoreTelegram := arcadeadmin.SetTelegramSenderForTest(func(_ context.Context, _ string) error {
		return nil
	})
	t.Cleanup(restoreTelegram)
	restoreDiscord := arcadeadmin.SetDiscordSenderForTest(func(_ context.Context, _ string) error {
		return nil
	})
	t.Cleanup(restoreDiscord)

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/rollback with report creates high urgency admin request",
		Method:         http.MethodPost,
		URL:            "/arcade/rollback",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"reported":true`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	reportMessage := "  신고 메세지 원문\n공백 유지  "

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		userID = user.Id
		headers["Authorization"] = "Bearer " + token

		arcadeID, targetBasicID = seedArcade(tb, app, userID, arcadeSeed{
			Name:      "Rollback Report Arcade",
			Address:   "Rollback Report Street",
			Direction: "",
			Nickname:  []string{"RollbackReport"},
			Location:  location{Lat: 37.5665, Lon: 126.9780},
		})

		currentBasicID := seedBasicVersion(tb, app, arcadeID, userID, "Rollback Report Current")
		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		arcadeRec.Set("basic", currentBasicID)
		if err := app.Save(arcadeRec); err != nil {
			tb.Fatalf("failed to set arcade.basic: %v", err)
		}
		sourceChangelogID = seedArcadeChangelog(tb, app, arcadeID, "basic", userID, time.Now())

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"part":"basic",
			"value":"%s",
			"report":true,
			"changelog":"%s",
			"report_message":%q
		}`, arcadeID, targetBasicID, sourceChangelogID, reportMessage))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode rollback response: %v", err)
		}

		requestID := fmt.Sprintf("%v", payload["request_admin_id"])
		if requestID == "" || requestID == "<nil>" {
			tb.Fatalf("expected request_admin_id in response, got %v", payload["request_admin_id"])
		}

		reqRec, err := app.FindRecordById("arcade_request_admin", requestID)
		if err != nil {
			tb.Fatalf("failed to load arcade_request_admin: %v", err)
		}
		if got := reqRec.GetString("arcade"); got != arcadeID {
			tb.Fatalf("expected arcade=%q, got %q", arcadeID, got)
		}
		if got := reqRec.GetString("kind"); got != "rollback_report" {
			tb.Fatalf("expected rollback_report kind, got %q", got)
		}
		if got := reqRec.GetString("changelog"); got != sourceChangelogID {
			tb.Fatalf("expected changelog=%q, got %q", sourceChangelogID, got)
		}
		if got := reqRec.GetString("reported_editor"); got != userID {
			tb.Fatalf("expected reported_editor=%q, got %q", userID, got)
		}
		if got := reqRec.GetString("urgency"); got != "high" {
			tb.Fatalf("expected urgency=high, got %q", got)
		}
		if got := reqRec.GetString("status"); got != "waiting" {
			tb.Fatalf("expected status=waiting, got %q", got)
		}
		if got := reqRec.GetString("createdBy"); got != userID {
			tb.Fatalf("expected createdBy=%q, got %q", userID, got)
		}
		if got := reqRec.GetString("message"); got != strings.TrimSpace(reportMessage) {
			tb.Fatalf("expected normalized report message %q, got %q", strings.TrimSpace(reportMessage), got)
		}
	}

	scenario.Test(t)
}

func TestRollbackArcadePart_RejectsForeignValue(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string
	var currentBasicID string
	var foreignBasicID string

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/rollback rejects relation value from another arcade",
		Method:         http.MethodPost,
		URL:            "/arcade/rollback",
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

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:      "Rollback A",
			Address:   "Street A",
			Direction: "",
			Nickname:  []string{"A"},
			Location:  location{Lat: 37.5, Lon: 126.9},
		})
		arcadeBID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
			Name:      "Rollback B",
			Address:   "Street B",
			Direction: "",
			Nickname:  []string{"B"},
			Location:  location{Lat: 35.1, Lon: 129.0},
		})

		currentBasicID = seedBasicVersion(tb, app, arcadeID, user.Id, "Rollback A Current")
		arcadeARec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade A: %v", err)
		}
		arcadeARec.Set("basic", currentBasicID)
		if err := app.Save(arcadeARec); err != nil {
			tb.Fatalf("failed to set arcade A basic: %v", err)
		}

		foreignBasicID = seedBasicVersion(tb, app, arcadeBID, user.Id, "Rollback B Current")

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"part":"basic",
			"value":"%s"
		}`, arcadeID, foreignBasicID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode error response: %v", err)
		}
		details := fmt.Sprintf("%v", payload["details"])
		if !strings.Contains(details, "does not belong to arcade") {
			tb.Fatalf("expected ownership validation message, got %q", details)
		}

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade after failed rollback: %v", err)
		}
		if got := arcadeRec.GetString("basic"); got != currentBasicID {
			tb.Fatalf("expected arcade.basic to remain %q, got %q", currentBasicID, got)
		}
	}

	scenario.Test(t)
}

func seedBasicVersion(tb testing.TB, app *tests.TestApp, arcadeID, createdBy, name string) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("arcade_basic")
	if err != nil {
		tb.Fatalf("failed to load arcade_basic collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("arcade", arcadeID)
	rec.Set("name", name)
	rec.Set("address", "Seed Address")
	rec.Set("direction", "")
	rec.Set("nickname", []string{name})
	rec.Set("subway_line", []string{})
	rec.Set("location", map[string]any{"lat": 37.5665, "lon": 126.9780})
	if createdBy != "" {
		rec.Set("createdBy", createdBy)
	}

	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save arcade_basic seed: %v", err)
	}
	return rec.Id
}
