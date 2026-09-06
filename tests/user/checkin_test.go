package user_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

func doUserRequest(tb testing.TB, app *tests.TestApp, method, url string, headers map[string]string, body string) *http.Response {
	tb.Helper()

	baseRouter, err := apis.NewRouter(app)
	if err != nil {
		tb.Fatalf("failed to initialize router: %v", err)
	}

	serveEvent := &core.ServeEvent{
		App:    app,
		Router: baseRouter,
	}
	if err := app.OnServe().Trigger(serveEvent, func(e *core.ServeEvent) error { return e.Next() }); err != nil {
		tb.Fatalf("failed to register routes: %v", err)
	}

	mux, err := serveEvent.Router.BuildMux()
	if err != nil {
		tb.Fatalf("failed to build router mux: %v", err)
	}

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("content-type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, strings.TrimSpace(v))
	}
	mux.ServeHTTP(recorder, req)
	return recorder.Result()
}

func TestLevelFromExpBoundaries(t *testing.T) {
	cases := []struct {
		exp   int
		level int
	}{
		{0, 0},
		{3, 0},
		{4, 1},
		{7, 1},
		{8, 2},
		{12, 2},
		{13, 3},
		{17, 3},
		{18, 4},
		{23, 4},
		{24, 5},
		{29, 5},
		{30, 6},
		{58, 9},
		{59, 10},
		{299, 29},
		{300, 30},
	}

	for _, tc := range cases {
		if got := userhandler.LevelFromExp(tc.exp); got != tc.level {
			t.Fatalf("LevelFromExp(%d) = %d, want %d", tc.exp, got, tc.level)
		}
	}
}

func TestCheckIn_KSTRolloverAndDedup(t *testing.T) {
	app := newUserFetchTestApp(t)
	token, userRec := createAuthUser(t, app, true)

	restore := userhandler.SetAttendanceNowForTest(func() time.Time {
		return time.Date(2026, 6, 1, 14, 59, 0, 0, time.UTC)
	})
	t.Cleanup(restore)

	headers := map[string]string{"Authorization": "Bearer " + token}
	res := doUserRequest(t, app, http.MethodPost, "/user/check-in", headers, `{}`)
	payload := decodeJSON(t, res)
	if got := payload["checked_in"]; got != true {
		t.Fatalf("expected first check-in to succeed, got %v", got)
	}
	if got := payload["gained_exp"]; got != float64(1) {
		t.Fatalf("expected gained_exp 1, got %v", got)
	}
	if got := payload["exp"]; got != float64(1) {
		t.Fatalf("expected exp 1, got %v", got)
	}
	if got := payload["level"]; got != float64(0) {
		t.Fatalf("expected level 0, got %v", got)
	}

	res = doUserRequest(t, app, http.MethodPost, "/user/check-in", headers, `{}`)
	payload = decodeJSON(t, res)
	if got := payload["already_checked_in"]; got != true {
		t.Fatalf("expected duplicate check-in to be rejected, got %v", got)
	}
	if got := payload["gained_exp"]; got != float64(0) {
		t.Fatalf("expected duplicate gained_exp 0, got %v", got)
	}
	if got := payload["exp"]; got != float64(1) {
		t.Fatalf("expected duplicate exp 1, got %v", got)
	}

	restore = userhandler.SetAttendanceNowForTest(func() time.Time {
		return time.Date(2026, 6, 1, 15, 1, 0, 0, time.UTC)
	})
	t.Cleanup(restore)

	res = doUserRequest(t, app, http.MethodPost, "/user/check-in", headers, `{}`)
	payload = decodeJSON(t, res)
	if got := payload["checked_in"]; got != true {
		t.Fatalf("expected next KST day check-in to succeed, got %v", got)
	}
	if got := payload["day"]; got != "2026-06-02" {
		t.Fatalf("expected KST day 2026-06-02, got %v", got)
	}

	rec, err := app.FindRecordById(userhandler.CollectionUserLevel, userRec.Id)
	if err != nil {
		t.Fatalf("failed to load user_level record: %v", err)
	}
	if rec.GetInt("exp") != 2 {
		t.Fatalf("expected user_level exp 2, got %d", rec.GetInt("exp"))
	}
}

func TestCheckInWithoutUsernameDoesNotAwardXP(t *testing.T) {
	app := newUserFetchTestApp(t)
	token, userRec := createAuthUser(t, app, true)
	userRec.Set("username", "")
	if err := app.Save(userRec); err != nil {
		t.Fatalf("failed to clear username: %v", err)
	}

	if _, granted, err := userhandler.AwardArcadeEditExpTx(app, userRec.Id, "arcade", "basic", 3, 0, time.Now()); err != nil {
		t.Fatalf("edit XP eligibility check failed: %v", err)
	} else if granted {
		t.Fatal("expected edit XP to be withheld before username setup")
	}
	if _, granted, err := userhandler.AwardArcadeGameEditExpTx(app, userRec.Id, "arcade", []string{"entry"}, 0, time.Now()); err != nil {
		t.Fatalf("game edit XP eligibility check failed: %v", err)
	} else if granted {
		t.Fatal("expected game edit XP to be withheld before username setup")
	}
	if next, err := userhandler.GrantArcadePublicBackfillTx(app, userRec.Id, "arcade", 0); err != nil {
		t.Fatalf("backfill XP eligibility check failed: %v", err)
	} else if next != 0 {
		t.Fatalf("expected backfill XP to remain 0 before username setup, got %d", next)
	}
	preview, err := userhandler.PreviewArcadePublicExp(app, userRec.Id, "arcade")
	if err != nil {
		t.Fatalf("public XP preview failed: %v", err)
	}
	if preview.PublicExp != 0 || preview.BackfillExp != 0 || preview.EstimatedGain != 0 {
		t.Fatalf("expected no eligible preview XP before username setup, got %#v", preview)
	}

	restore := userhandler.SetAttendanceNowForTest(func() time.Time {
		return time.Date(2026, 6, 1, 14, 59, 0, 0, time.UTC)
	})
	t.Cleanup(restore)

	res := doUserRequest(t, app, http.MethodPost, "/user/check-in", map[string]string{
		"Authorization": "Bearer " + token,
	}, `{}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected check-in success without XP, got status %d", res.StatusCode)
	}
	payload := decodeJSON(t, res)
	if got := payload["gained_exp"]; got != float64(0) {
		t.Fatalf("expected no XP before username setup, got %v", got)
	}
	if got := payload["exp"]; got != float64(0) {
		t.Fatalf("expected exp 0 before username setup, got %v", got)
	}

	if _, err := app.FindRecordById(userhandler.CollectionUserLevel, userRec.Id); err == nil {
		t.Fatal("expected check-in without username not to create user_level")
	}
	logs, err := app.FindRecordsByFilter(userhandler.CollectionUserLevelLog, "user={:user}", "", 0, 0, map[string]any{
		"user": userRec.Id,
	})
	if err != nil {
		t.Fatalf("failed to load XP logs: %v", err)
	}
	if len(logs) != 0 {
		t.Fatalf("expected no XP logs before username setup, got %d", len(logs))
	}
}
