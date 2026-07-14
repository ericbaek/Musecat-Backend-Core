package arcade_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	arcadeadmin "github.com/ericbaek/musecat-backend-core/handlers/arcade/admin"
)

func TestCreateSupportFeedback_Success(t *testing.T) {
	var sentTelegramMessage string
	var sentDiscordMessage string

	restoreTelegram := arcadeadmin.SetTelegramSenderForTest(func(_ context.Context, message string) error {
		sentTelegramMessage = message
		return nil
	})
	t.Cleanup(restoreTelegram)

	restoreDiscord := arcadeadmin.SetDiscordSenderForTest(func(_ context.Context, message string) error {
		sentDiscordMessage = message
		return nil
	})
	t.Cleanup(restoreDiscord)

	scenario := tests.ApiScenario{
		Name:           "POST /support_feedback creates feedback with waiting status and sends notifications",
		Method:         http.MethodPost,
		URL:            "/support_feedback",
		Body:           strings.NewReader(`{"message":"arcade detail page fails to load after update"}`),
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"status":"waiting"`,
			`"message":"arcade detail page fails to load after update"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		feedbackID := fmt.Sprintf("%v", payload["id"])
		if feedbackID == "" || feedbackID == "<nil>" {
			tb.Fatalf("expected feedback id, got %v", payload["id"])
		}
		if got := fmt.Sprintf("%v", payload["createdBy"]); got != "" {
			tb.Fatalf("expected createdBy empty for unauthenticated request, got %q", got)
		}

		rec, err := app.FindRecordById("support_feedback", feedbackID)
		if err != nil {
			tb.Fatalf("failed to load support_feedback record: %v", err)
		}
		if rec.GetString("status") != "waiting" {
			tb.Fatalf("expected status waiting, got %q", rec.GetString("status"))
		}
		if rec.GetString("createdBy") != "" {
			tb.Fatalf("expected createdBy empty, got %q", rec.GetString("createdBy"))
		}

		if sentTelegramMessage == "" {
			tb.Fatalf("expected telegram message to be sent")
		}
		if !strings.Contains(sentTelegramMessage, "[support_feedback]") {
			tb.Fatalf("telegram message should include collection tag, got %q", sentTelegramMessage)
		}
		if !strings.Contains(sentTelegramMessage, feedbackID) {
			tb.Fatalf("telegram message should include record id %q, got %q", feedbackID, sentTelegramMessage)
		}

		if sentDiscordMessage == "" {
			tb.Fatalf("expected discord message to be sent")
		}
		if !strings.Contains(sentDiscordMessage, "[support_feedback]") {
			tb.Fatalf("discord message should include collection tag, got %q", sentDiscordMessage)
		}
	}

	scenario.Test(t)
}

func TestCreateSupportFeedback_MultipartWithPhotos(t *testing.T) {
	headers := map[string]string{}
	var userID string

	scenario := tests.ApiScenario{
		Name:           "POST /support_feedback multipart stores photos",
		Method:         http.MethodPost,
		URL:            "/support_feedback",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"status":"waiting"`,
			`"photos":[`,
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

		body, contentType := buildSupportFeedbackMultipart(tb, "photo upload broke", []uploadTestFile{
			{Filename: "support-a.png", Content: pngFixtureBytes()},
			{Filename: "support-b.jpg", Content: jpegFixtureBytes()},
		})
		headers["Content-Type"] = contentType
		scenario.Body = bytes.NewReader(body)
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		feedbackID, _ := payload["id"].(string)
		if feedbackID == "" {
			tb.Fatalf("expected feedback id in response")
		}
		if got, _ := payload["createdBy"].(string); got != userID {
			tb.Fatalf("expected createdBy %q, got %q", userID, got)
		}
		photos, ok := payload["photos"].([]any)
		if !ok || len(photos) != 2 {
			tb.Fatalf("expected two uploaded photos, got %T %#v", payload["photos"], payload["photos"])
		}

		rec, err := app.FindRecordById("support_feedback", feedbackID)
		if err != nil {
			tb.Fatalf("failed to load support_feedback record: %v", err)
		}
		if got := rec.GetString("message"); got != "photo upload broke" {
			tb.Fatalf("expected message to persist, got %q", got)
		}
		if got := rec.GetString("createdBy"); got != userID {
			tb.Fatalf("expected createdBy %q, got %q", userID, got)
		}
		if got := rec.GetStringSlice("photos"); len(got) != 2 {
			tb.Fatalf("expected 2 stored photos, got %#v", got)
		}
	}

	scenario.Test(t)
}

func TestListSupportFeedback_All(t *testing.T) {
	var baselineTotal int

	scenario := tests.ApiScenario{
		Name:           "GET /support_feedback returns all feedback",
		Method:         http.MethodGet,
		URL:            "/support_feedback",
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"items":`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		_, userA := createAuthUser(tb, app)
		_, userB := createAuthUser(tb, app)

		var err error
		baselineTotal, err = countSupportFeedback(tb, app)
		if err != nil {
			tb.Fatalf("failed to count baseline support_feedback records: %v", err)
		}

		seedSupportFeedback(tb, app, userA.Id, "feedback-a", "waiting")
		seedSupportFeedback(tb, app, userB.Id, "feedback-b", "solved")
		seedSupportFeedback(tb, app, "", "feedback-anonymous", "recognised")
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
		if payload.Total != baselineTotal+3 {
			tb.Fatalf("expected total=%d, got %d", baselineTotal+3, payload.Total)
		}
	}

	scenario.Test(t)
}

func TestListSupportFeedback_FilterByCreatedByAndStatus(t *testing.T) {
	var targetUserID string

	scenario := tests.ApiScenario{
		Name:           "GET /support_feedback filters by createdBy and status",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"items":`,
			`"total":1`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		_, userA := createAuthUser(tb, app)
		_, userB := createAuthUser(tb, app)
		targetUserID = userA.Id

		seedSupportFeedback(tb, app, userA.Id, "target-waiting", "waiting")
		seedSupportFeedback(tb, app, userA.Id, "target-solved", "solved")
		seedSupportFeedback(tb, app, userB.Id, "other-waiting", "waiting")
		seedSupportFeedback(tb, app, "", "anonymous-waiting", "waiting")

		scenario.URL = fmt.Sprintf("/support_feedback?createdBy=%s&status=waiting", userA.Id)
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
		if payload.Total != 1 {
			tb.Fatalf("expected total=1, got %d", payload.Total)
		}
		for _, item := range payload.Items {
			if got := fmt.Sprintf("%v", item["createdBy"]); got != targetUserID {
				tb.Fatalf("expected createdBy=%q, got %q", targetUserID, got)
			}
			if got := fmt.Sprintf("%v", item["status"]); got != "waiting" {
				tb.Fatalf("expected status=waiting, got %q", got)
			}
		}
	}

	scenario.Test(t)
}

func seedSupportFeedback(
	tb testing.TB,
	app *tests.TestApp,
	createdBy string,
	message string,
	status string,
) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("support_feedback")
	if err != nil {
		tb.Fatalf("failed to load support_feedback collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("message", message)
	rec.Set("status", status)
	if createdBy != "" {
		rec.Set("createdBy", createdBy)
	}

	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save support_feedback record: %v", err)
	}
	return rec.Id
}

func countSupportFeedback(tb testing.TB, app *tests.TestApp) (int, error) {
	tb.Helper()

	recs, err := app.FindRecordsByFilter("support_feedback", "", "", 0, 0, nil)
	if err != nil {
		return 0, err
	}
	return len(recs), nil
}

func buildSupportFeedbackMultipart(
	tb testing.TB,
	message string,
	files []uploadTestFile,
) ([]byte, string) {
	tb.Helper()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("message", message); err != nil {
		tb.Fatalf("failed to write message field: %v", err)
	}

	for _, f := range files {
		part, err := writer.CreateFormFile("photos", f.Filename)
		if err != nil {
			tb.Fatalf("failed to create form file: %v", err)
		}
		if _, err := part.Write(f.Content); err != nil {
			tb.Fatalf("failed to write form file content: %v", err)
		}
	}

	if err := writer.Close(); err != nil {
		tb.Fatalf("failed to close multipart writer: %v", err)
	}

	return buf.Bytes(), writer.FormDataContentType()
}
