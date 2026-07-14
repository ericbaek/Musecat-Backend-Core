package arcade_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	arcadeadmin "github.com/ericbaek/musecat-backend-core/handlers/arcade/admin"
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

func TestRollbackArcadePart_WithReport_CreatesArcadeRequestAdmin(t *testing.T) {
	headers := map[string]string{}
	var userID string
	var arcadeID string
	var targetBasicID string

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

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"part":"basic",
			"value":"%s",
			"report":true,
			"report_message":%q
		}`, arcadeID, targetBasicID, reportMessage))
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
		if got := reqRec.GetString("urgency"); got != "high" {
			tb.Fatalf("expected urgency=high, got %q", got)
		}
		if got := reqRec.GetString("status"); got != "waiting" {
			tb.Fatalf("expected status=waiting, got %q", got)
		}
		if got := reqRec.GetString("createdBy"); got != userID {
			tb.Fatalf("expected createdBy=%q, got %q", userID, got)
		}
		if got := reqRec.GetString("message"); got != reportMessage {
			tb.Fatalf("expected exact report message %q, got %q", reportMessage, got)
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
