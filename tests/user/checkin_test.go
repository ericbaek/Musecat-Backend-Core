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
	if got := payload["gained_exp"]; got != float64(2) {
		t.Fatalf("expected gained_exp 2, got %v", got)
	}
	if got := payload["exp"]; got != float64(2) {
		t.Fatalf("expected exp 2, got %v", got)
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
	if got := payload["exp"]; got != float64(2) {
		t.Fatalf("expected duplicate exp 2, got %v", got)
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
	if rec.GetInt("exp") != 4 {
		t.Fatalf("expected user_level exp 4, got %d", rec.GetInt("exp"))
	}
}
