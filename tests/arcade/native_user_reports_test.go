package arcade_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	arcadeadmin "github.com/ericbaek/musecat-backend-core/handlers/arcade/admin"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func canonicalUserReportRequest(t *testing.T, app *tests.TestApp, method, path, token string, body any, status int) map[string]any {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	response := executeJSONRequest(t, app, method, path, string(raw), headers)
	defer response.Body.Close()
	if response.StatusCode != status {
		t.Fatalf("%s %s status %d want %d", method, path, response.StatusCode, status)
	}
	if len(token) == 43 && !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("Native report response is cacheable")
	}
	var value map[string]any
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestCanonicalNativeUserReportsMatchWebActorAndOwnerList(t *testing.T) {
	app := nativeNearbyApp(t, true)
	restore := arcadeadmin.SetTelegramSenderForTest(func(context.Context, string) error { return nil })
	t.Cleanup(restore)
	web, reporter := createAuthUser(t, app)
	_, session := nativeNearbySession(t, app, reporter)
	_, target := createAuthUser(t, app)
	otherWeb, other := createAuthUser(t, app)
	_, otherSession := nativeNearbySession(t, app, other)
	body := map[string]any{"user": target.Id, "reason": "  Synthetic report reason  ", "createdBy": other.Id, "status": "done"}
	native := canonicalUserReportRequest(t, app, "POST", "/user/report", session.AccessToken, body, 200)
	if native["user"] != target.Id || native["createdBy"] != reporter.Id || native["reason"] != "Synthetic report reason" || native["status"] != "waiting" {
		t.Fatal("report target, actor or canonical normalization changed")
	}
	webCreated := canonicalUserReportRequest(t, app, "POST", "/user/report", web, body, 200)
	for _, key := range []string{"user", "reason", "status", "createdBy"} {
		if webCreated[key] != native[key] {
			t.Fatal("Web and Native report creation differ")
		}
	}
	if webCreated["id"] == native["id"] {
		t.Fatal("existing report creation was unexpectedly deduplicated")
	}
	seedUserReport(t, app, other.Id, target.Id, "Other reporter private reason", "processing")
	path := "/user/report?user=" + target.Id
	own := canonicalUserReportRequest(t, app, "GET", path, session.AccessToken, nil, 200)
	if !reflect.DeepEqual(own, canonicalUserReportRequest(t, app, "GET", path, web, nil, 200)) || own["total"] != float64(2) {
		t.Fatal("Web and Native own lists differ")
	}
	for _, raw := range own["items"].([]any) {
		row := raw.(map[string]any)
		if row["createdBy"] != reporter.Id || row["user"] != target.Id {
			t.Fatal("another reporter leaked into own list")
		}
	}
	foreign := canonicalUserReportRequest(t, app, "GET", path, otherSession.AccessToken, nil, 200)
	if foreign["total"] != float64(1) || !reflect.DeepEqual(foreign, canonicalUserReportRequest(t, app, "GET", path, otherWeb, nil, 200)) {
		t.Fatal("other owner list changed")
	}
	empty := canonicalUserReportRequest(t, app, "GET", "/user/report?user="+other.Id, session.AccessToken, nil, 200)
	if empty["total"] != float64(0) {
		t.Fatal("target filter ignored")
	}
	before := own["total"]
	for _, invalid := range []map[string]any{{"user": reporter.Id, "reason": "self"}, {"user": target.Id, "reason": " "}, {"user": "invalid", "reason": "invalid"}} {
		for _, token := range []string{web, session.AccessToken} {
			canonicalUserReportRequest(t, app, "POST", "/user/report", token, invalid, 400)
		}
	}
	if canonicalUserReportRequest(t, app, "GET", path, session.AccessToken, nil, 200)["total"] != before {
		t.Fatal("invalid report wrote state")
	}
	if dir := os.Getenv("MUSECAT_CANONICAL_USER_REPORT_FIXTURES"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for name, value := range map[string]map[string]any{"user-report-create": native, "user-report-list": own} {
			raw, err := json.MarshalIndent(value, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"\"password\"", "\"access_token\"", "\"refresh_token\"", "\"email\""} {
				if strings.Contains(strings.ToLower(string(raw)), key) {
					t.Fatal("credential in domain fixture")
				}
			}
			if err := os.WriteFile(filepath.Join(dir, name+".json"), append(raw, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestCanonicalNativeUserReportsKeepAuthenticationAndInactiveBoundaries(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, user := createAuthUser(t, app)
	service, session := nativeNearbySession(t, app, user)
	_, target := createAuthUser(t, app)
	body := map[string]any{"user": target.Id, "reason": "must not persist"}
	for _, method := range []string{"GET", "POST"} {
		for _, token := range []string{"", strings.Repeat("z", 43)} {
			canonicalUserReportRequest(t, app, method, "/user/report", token, body, 401)
		}
		response := executeJSONRequest(t, app, method, "/user/report", "{}", map[string]string{"Authorization": "Bearer " + strings.Repeat("z", 43), "Cookie": "pb_auth=" + web})
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal("invalid bearer fell back to cookie")
		}
	}
	seedUserBan(t, app, user.Id, "", "native_user_report_fixture", time.Now().Add(time.Hour))
	for _, method := range []string{"GET", "POST"} {
		canonicalUserReportRequest(t, app, method, "/user/report", session.AccessToken, body, 403)
	}
	if err := service.Revoke(app, session.RefreshToken); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "POST"} {
		canonicalUserReportRequest(t, app, method, "/user/report", session.AccessToken, body, 401)
	}
	reports, err := app.FindAllRecords("user_report")
	if err != nil || len(reports) != 0 {
		t.Fatal("denied report changed state")
	}
	disabled := nativeNearbyApp(t, false)
	disabledWeb, actor := createAuthUser(t, disabled)
	disabledService, issued := nativeNearbySession(t, disabled, actor)
	if got, err := disabledService.Authenticate(disabled, issued.AccessToken); err != nil || got.Id != actor.Id {
		t.Fatal("disabled fixture is not a valid same-database session")
	}
	for _, method := range []string{"GET", "POST"} {
		response := executeJSONRequest(t, disabled, method, "/user/report", "{}", map[string]string{"Authorization": "Bearer " + issued.AccessToken})
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal("disabled adapter accepted Native report credentials")
		}
	}
	canonicalUserReportRequest(t, disabled, "GET", "/user/report", disabledWeb, nil, 200)
}

func TestCanonicalNativeUserReportsKeepRateLimitsAndExcludeModerationAndAliases(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, actor := createAuthUserWithTags(t, app, []string{"developer"})
	_, session := nativeNearbySession(t, app, actor)
	for _, op := range []struct {
		method  string
		initial int
	}{{"GET", 200}, {"POST", 400}} {
		app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: op.method + " /user/report", MaxRequests: 1, Duration: 60}}}
		canonicalUserReportRequest(t, app, op.method, "/user/report", session.AccessToken, map[string]any{}, op.initial)
		canonicalUserReportRequest(t, app, op.method, "/user/report", session.AccessToken, map[string]any{}, 429)
	}
	for _, op := range []struct {
		method, path string
		status       int
	}{{"GET", "/native/user/report", 404}, {"POST", "/native/user/report", 404}, {"PUT", "/moderation/arcade/status", 401}} {
		response := executeJSONRequest(t, app, op.method, op.path, "{}", map[string]string{"Authorization": "Bearer " + session.AccessToken})
		response.Body.Close()
		if response.StatusCode != op.status {
			t.Fatal("report adapter widened route capabilities")
		}
	}
}
