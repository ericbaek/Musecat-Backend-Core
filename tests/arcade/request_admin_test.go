package arcade_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	arcadeadmin "github.com/ericbaek/musecat-backend-core/handlers/arcade/admin"
)

func TestCreateArcadeRequestAdmin_Success(t *testing.T) {
	headers := map[string]string{}
	var userID string
	var sentMessage string

	restore := arcadeadmin.SetTelegramSenderForTest(func(_ context.Context, message string) error {
		sentMessage = message
		return nil
	})
	t.Cleanup(restore)

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/request_admin creates request and sends telegram",
		Method:         http.MethodPost,
		URL:            "/arcade/request_admin",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"urgency":"high"`,
			`"status":"waiting"`,
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
		scenario.Body = strings.NewReader(`{"urgency":"high","message":"need admin support for duplicated arcade"} `)
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		reqID := fmt.Sprintf("%v", payload["id"])
		if reqID == "" || reqID == "<nil>" {
			tb.Fatalf("expected response id, got %v", payload["id"])
		}
		if got := payload["status"]; got != "waiting" {
			tb.Fatalf("expected status waiting, got %v", got)
		}
		if got := payload["urgency"]; got != "high" {
			tb.Fatalf("expected urgency high, got %v", got)
		}
		if got := payload["arcade"]; got != "" {
			tb.Fatalf("expected arcade empty when not provided, got %v", got)
		}

		rec, err := app.FindRecordById("arcade_request_admin", reqID)
		if err != nil {
			tb.Fatalf("failed to load arcade_request_admin record: %v", err)
		}
		if rec.GetString("createdBy") != userID {
			tb.Fatalf("expected createdBy %q, got %q", userID, rec.GetString("createdBy"))
		}
		if rec.GetString("status") != "waiting" {
			tb.Fatalf("expected saved status waiting, got %q", rec.GetString("status"))
		}
		if rec.GetString("urgency") != "high" {
			tb.Fatalf("expected saved urgency high, got %q", rec.GetString("urgency"))
		}

		if sentMessage == "" {
			tb.Fatalf("expected telegram message to be sent")
		}
		if !strings.Contains(sentMessage, reqID) {
			tb.Fatalf("telegram message should include record id %q, got %q", reqID, sentMessage)
		}
		if !strings.Contains(sentMessage, userID) {
			tb.Fatalf("telegram message should include user id %q, got %q", userID, sentMessage)
		}
	}

	scenario.Test(t)
}

func TestCreateArcadeRequestAdmin_DefaultUrgency(t *testing.T) {
	headers := map[string]string{}

	restore := arcadeadmin.SetTelegramSenderForTest(func(_ context.Context, _ string) error {
		return nil
	})
	t.Cleanup(restore)

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/request_admin sets default urgency",
		Method:         http.MethodPost,
		URL:            "/arcade/request_admin",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"urgency":"medium"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		token, _ := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token
		scenario.Body = strings.NewReader(`{"message":"no urgency provided"}`)
	}

	scenario.Test(t)
}

func TestCreateArcadeRequestAdmin_WithArcade(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	restore := arcadeadmin.SetTelegramSenderForTest(func(_ context.Context, _ string) error {
		return nil
	})
	t.Cleanup(restore)

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/request_admin stores arcade relation",
		Method:         http.MethodPost,
		URL:            "/arcade/request_admin",
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
		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token
		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Admin Request Arcade",
			Address:  "Admin Request Street",
			Nickname: []string{"Admin"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		scenario.Body = strings.NewReader(fmt.Sprintf(`{"arcade":"%s","message":"please check this arcade"}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		if got := payload["arcade"]; got != arcadeID {
			tb.Fatalf("expected arcade %q, got %v", arcadeID, got)
		}

		reqID := fmt.Sprintf("%v", payload["id"])
		rec, err := app.FindRecordById("arcade_request_admin", reqID)
		if err != nil {
			tb.Fatalf("failed to load arcade_request_admin record: %v", err)
		}
		if got := rec.GetString("arcade"); got != arcadeID {
			tb.Fatalf("expected saved arcade %q, got %q", arcadeID, got)
		}
	}

	scenario.Test(t)
}

func TestCreateArcadeRequestAdmin_CreateSucceedsWhenTelegramFails(t *testing.T) {
	headers := map[string]string{}
	var userID string

	restore := arcadeadmin.SetTelegramSenderForTest(func(_ context.Context, _ string) error {
		return errors.New("telegram down")
	})
	t.Cleanup(restore)

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/request_admin still creates record when telegram fails",
		Method:         http.MethodPost,
		URL:            "/arcade/request_admin",
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
		token, user := createAuthUser(tb, app)
		userID = user.Id
		headers["Authorization"] = "Bearer " + token
		scenario.Body = strings.NewReader(`{"urgency":"low","message":"rollback me"}`)
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, _ *http.Response) {
		tb.Helper()

		recs, err := app.FindRecordsByFilter(
			"arcade_request_admin",
			"createdBy = {:createdBy} && message = {:message}",
			"",
			0,
			0,
			dbx.Params{
				"createdBy": userID,
				"message":   "rollback me",
			},
		)
		if err != nil {
			tb.Fatalf("failed to query arcade_request_admin records: %v", err)
		}
		if len(recs) != 1 {
			tb.Fatalf("expected record to remain created, found %d", len(recs))
		}
	}

	scenario.Test(t)
}

func TestListArcadeRequestAdmin_AllOwn(t *testing.T) {
	headers := map[string]string{}
	var userID string
	var otherMessage string

	restore := arcadeadmin.SetTelegramSenderForTest(func(_ context.Context, _ string) error {
		return nil
	})
	t.Cleanup(restore)

	scenario := tests.ApiScenario{
		Name:           "GET /arcade/request_admin returns own records with status",
		Method:         http.MethodGet,
		URL:            "/arcade/request_admin",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"items":`,
			`"status":"waiting"`,
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

		otherTokenUser := ensureAnotherUser(tb, app)
		otherMessage = "hidden from other user"

		arcadeID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "List Target 1",
			Address:  "Street 1",
			Nickname: []string{"L1"},
			Location: location{Lat: 37.5, Lon: 126.9},
		})
		arcadeID2, _ := seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "List Target 2",
			Address:  "Street 2",
			Nickname: []string{"L2"},
			Location: location{Lat: 37.6, Lon: 127.0},
		})
		otherArcadeID, _ := seedArcade(tb, app, otherTokenUser.Id, arcadeSeed{
			Name:     "Other User Arcade",
			Address:  "Other Street",
			Nickname: []string{"O"},
			Location: location{Lat: 35.1, Lon: 129.0},
		})

		seedArcadeRequestAdmin(tb, app, user.Id, arcadeID, "high", "mine waiting", "waiting")
		seedArcadeRequestAdmin(tb, app, user.Id, arcadeID2, "low", "mine done", "done")
		seedArcadeRequestAdmin(tb, app, otherTokenUser.Id, otherArcadeID, "medium", otherMessage, "processing")
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
		if payload.Total != len(payload.Items) {
			tb.Fatalf("expected total=%d to match items length", len(payload.Items))
		}
		if payload.Total != 2 {
			tb.Fatalf("expected 2 own records, got %d", payload.Total)
		}

		statusCount := map[string]int{}
		for _, item := range payload.Items {
			if got := fmt.Sprintf("%v", item["createdBy"]); got != userID {
				tb.Fatalf("expected only own createdBy=%q, got %q", userID, got)
			}
			status := fmt.Sprintf("%v", item["status"])
			if status == "" {
				tb.Fatalf("status should not be empty")
			}
			statusCount[status]++
			if msg := fmt.Sprintf("%v", item["message"]); msg == otherMessage {
				tb.Fatalf("response should not include other user's request")
			}
		}
		if statusCount["waiting"] != 1 || statusCount["done"] != 1 {
			tb.Fatalf("expected statuses waiting=1 and done=1, got %#v", statusCount)
		}
	}

	scenario.Test(t)
}

func TestListArcadeRequestAdmin_FilterByArcade(t *testing.T) {
	headers := map[string]string{}
	var targetArcadeID string

	restore := arcadeadmin.SetTelegramSenderForTest(func(_ context.Context, _ string) error {
		return nil
	})
	t.Cleanup(restore)

	scenario := tests.ApiScenario{
		Name:           "GET /arcade/request_admin filters by arcade id",
		Method:         http.MethodGet,
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		targetArcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Filter Target",
			Address:  "Filter Street",
			Nickname: []string{"FT"},
			Location: location{Lat: 37.4, Lon: 126.8},
		})
		otherArcadeID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Filter Other",
			Address:  "Other Street",
			Nickname: []string{"FO"},
			Location: location{Lat: 37.7, Lon: 127.1},
		})

		seedArcadeRequestAdmin(tb, app, user.Id, targetArcadeID, "high", "target-1", "waiting")
		seedArcadeRequestAdmin(tb, app, user.Id, targetArcadeID, "low", "target-2", "done")
		seedArcadeRequestAdmin(tb, app, user.Id, otherArcadeID, "medium", "other", "processing")

		scenario.URL = fmt.Sprintf("/arcade/request_admin?arcade=%s", targetArcadeID)
		scenario.ExpectedContent = []string{
			`"total":2`,
			targetArcadeID,
		}
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
			if got := fmt.Sprintf("%v", item["arcade"]); got != targetArcadeID {
				tb.Fatalf("expected arcade=%q, got %q", targetArcadeID, got)
			}
		}
	}

	scenario.Test(t)
}

func TestCreateArcadeRequestAdmin_Validation(t *testing.T) {
	headers := map[string]string{}

	restore := arcadeadmin.SetTelegramSenderForTest(func(_ context.Context, _ string) error {
		return nil
	})
	t.Cleanup(restore)

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/request_admin validates message and urgency",
		Method:         http.MethodPost,
		URL:            "/arcade/request_admin",
		Headers:        headers,
		ExpectedStatus: http.StatusBadRequest,
		ExpectedContent: []string{
			`"error":"validation failed"`,
			`"details":"message is required"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		token, _ := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token
		scenario.Body = strings.NewReader(`{"urgency":"urgent","message":"   "}`)
	}

	scenario.Test(t)
}

func ensureAnotherUser(tb testing.TB, app *tests.TestApp) *core.Record {
	tb.Helper()
	_, user := createAuthUser(tb, app)
	return user
}

func seedArcadeRequestAdmin(
	tb testing.TB,
	app *tests.TestApp,
	createdBy string,
	arcadeID string,
	urgency string,
	message string,
	status string,
) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("arcade_request_admin")
	if err != nil {
		tb.Fatalf("failed to load arcade_request_admin collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("createdBy", createdBy)
	rec.Set("arcade", arcadeID)
	rec.Set("urgency", urgency)
	rec.Set("message", message)
	rec.Set("status", status)

	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save arcade_request_admin record: %v", err)
	}
	return rec.Id
}
