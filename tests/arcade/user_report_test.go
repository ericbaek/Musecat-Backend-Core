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

func TestCreateUserReport_Success(t *testing.T) {
	headers := map[string]string{}
	var reporterID string
	var targetUserID string
	var sentMessage string

	restore := arcadeadmin.SetTelegramSenderForTest(func(_ context.Context, message string) error {
		sentMessage = message
		return nil
	})
	t.Cleanup(restore)

	scenario := tests.ApiScenario{
		Name:           "POST /user/report creates report and sends telegram",
		Method:         http.MethodPost,
		URL:            "/user/report",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"status":"waiting"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		token, reporter := createAuthUser(tb, app)
		_, target := createAuthUser(tb, app)

		reporterID = reporter.Id
		targetUserID = target.Id
		headers["Authorization"] = "Bearer " + token
		scenario.Body = strings.NewReader(fmt.Sprintf(`{"user":"%s","reason":"abusive messages in comments"}`, targetUserID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		reportID := fmt.Sprintf("%v", payload["id"])
		if reportID == "" || reportID == "<nil>" {
			tb.Fatalf("expected response id, got %v", payload["id"])
		}

		rec, err := app.FindRecordById("user_report", reportID)
		if err != nil {
			tb.Fatalf("failed to load user_report record: %v", err)
		}
		if rec.GetString("createdBy") != reporterID {
			tb.Fatalf("expected createdBy %q, got %q", reporterID, rec.GetString("createdBy"))
		}
		if rec.GetString("user") != targetUserID {
			tb.Fatalf("expected user %q, got %q", targetUserID, rec.GetString("user"))
		}
		if rec.GetString("status") != "waiting" {
			tb.Fatalf("expected status waiting, got %q", rec.GetString("status"))
		}

		if sentMessage == "" {
			tb.Fatalf("expected telegram message to be sent")
		}
		if !strings.Contains(sentMessage, "[user_report]") {
			tb.Fatalf("telegram message should include collection tag, got %q", sentMessage)
		}
		if !strings.Contains(sentMessage, reportID) {
			tb.Fatalf("telegram message should include report id %q, got %q", reportID, sentMessage)
		}
	}

	scenario.Test(t)
}

func TestListUserReport_AllOwn(t *testing.T) {
	headers := map[string]string{}
	var reporterID string
	var otherReason string

	restore := arcadeadmin.SetTelegramSenderForTest(func(_ context.Context, _ string) error {
		return nil
	})
	t.Cleanup(restore)

	scenario := tests.ApiScenario{
		Name:           "GET /user/report returns only own records",
		Method:         http.MethodGet,
		URL:            "/user/report",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"total":2`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		token, reporter := createAuthUser(tb, app)
		_, targetA := createAuthUser(tb, app)
		_, targetB := createAuthUser(tb, app)
		_, otherReporter := createAuthUser(tb, app)
		_, otherTarget := createAuthUser(tb, app)

		reporterID = reporter.Id
		otherReason = "this should be hidden"
		headers["Authorization"] = "Bearer " + token

		seedUserReport(tb, app, reporter.Id, targetA.Id, "spam profile", "waiting")
		seedUserReport(tb, app, reporter.Id, targetB.Id, "impersonation", "processing")
		seedUserReport(tb, app, otherReporter.Id, otherTarget.Id, otherReason, "waiting")
	}

	scenario.AfterTestFunc = func(tb testing.TB, _ *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		}
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		if payload.Total != 2 {
			tb.Fatalf("expected 2 own records, got %d", payload.Total)
		}
		for _, item := range payload.Items {
			if got := fmt.Sprintf("%v", item["createdBy"]); got != reporterID {
				tb.Fatalf("expected only own createdBy=%q, got %q", reporterID, got)
			}
			if gotReason := fmt.Sprintf("%v", item["reason"]); gotReason == otherReason {
				tb.Fatalf("response should not include other user's report")
			}
		}
	}

	scenario.Test(t)
}

func TestListUserReport_FilterByUser(t *testing.T) {
	headers := map[string]string{}
	var targetUserID string

	restore := arcadeadmin.SetTelegramSenderForTest(func(_ context.Context, _ string) error {
		return nil
	})
	t.Cleanup(restore)

	scenario := tests.ApiScenario{
		Name:           "GET /user/report filters by reported user id",
		Method:         http.MethodGet,
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"total":2`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		token, reporter := createAuthUser(tb, app)
		_, targetA := createAuthUser(tb, app)
		_, targetB := createAuthUser(tb, app)

		targetUserID = targetA.Id
		headers["Authorization"] = "Bearer " + token

		seedUserReport(tb, app, reporter.Id, targetA.Id, "target-a-1", "waiting")
		seedUserReport(tb, app, reporter.Id, targetA.Id, "target-a-2", "done")
		seedUserReport(tb, app, reporter.Id, targetB.Id, "target-b-1", "processing")

		scenario.URL = fmt.Sprintf("/user/report?user=%s", targetA.Id)
	}

	scenario.AfterTestFunc = func(tb testing.TB, _ *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		}
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		if payload.Total != 2 {
			tb.Fatalf("expected filtered total=2, got %d", payload.Total)
		}
		for _, item := range payload.Items {
			if got := fmt.Sprintf("%v", item["user"]); got != targetUserID {
				tb.Fatalf("expected user=%q, got %q", targetUserID, got)
			}
		}
	}

	scenario.Test(t)
}

func seedUserReport(
	tb testing.TB,
	app *tests.TestApp,
	createdBy string,
	targetUserID string,
	reason string,
	status string,
) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("user_report")
	if err != nil {
		tb.Fatalf("failed to load user_report collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("createdBy", createdBy)
	rec.Set("user", targetUserID)
	rec.Set("reason", reason)
	rec.Set("status", status)

	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save user_report record: %v", err)
	}
	return rec.Id
}
