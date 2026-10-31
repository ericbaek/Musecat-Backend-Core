package user_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/coreapp"
	ranking "github.com/ericbaek/musecat-backend-core/handlers/ranking"
	user "github.com/ericbaek/musecat-backend-core/handlers/user"
	notifications "github.com/ericbaek/musecat-backend-core/handlers/user/notifications"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func nativeReadApp(t *testing.T, enabled bool) *tests.TestApp {
	t.Helper()
	app := newUserFetchTestApp(t)
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		if enabled {
			coreapp.RegisterNativeAPIRoutes(e, nativeLoginClients)
		} else {
			coreapp.RegisterNativeAPIRoutes(e, nil)
		}
		e.Router.GET("/rankings", ranking.List)
		inbox := e.Router.Group("/user/notifications").Bind(apis.RequireAuth("user"), user.RequireActiveUser())
		inbox.GET("", notifications.List)
		inbox.POST("/read", notifications.Read)
		return e.Next()
	})
	return app
}

func nativeReadRequest(t *testing.T, app *tests.TestApp, method, path, token string, body any, status int) *http.Response {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Content-Type": "application/json"}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	response := doUserRequest(t, app, method, path, headers, string(payload))
	t.Cleanup(func() { response.Body.Close() })
	if response.StatusCode != status {
		t.Fatalf("%s %s status %d want %d", method, path, response.StatusCode, status)
	}
	if status != 404 && strings.HasPrefix(path, "/native/") && !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("native response must not be cached")
	}
	return response
}

var nativeReadPaths = []string{"/user/visits", "/user/passport?year=all", "/user/passport/stamps?year=all&page=1&per_page=24", "/rankings?metric=xp&period=all", "/user/notifications"}

func TestNativeReadsMatchCanonicalWireAndAccountVisibility(t *testing.T) {
	app := nativeReadApp(t, true)
	web, owner := createAuthUser(t, app, true)
	_, other := createAuthUser(t, app, true)
	token := nativeLoginIssue(t, app, web).AccessToken
	arcade := seedVisitArcade(t, app, "Asia/Seoul")
	visits, _ := app.FindCollectionByNameOrId("arcade_visit")
	visit := core.NewRecord(visits)
	visit.Set("user", owner.Id)
	visit.Set("arcade", arcade.Id)
	visit.Set("visit_day", "2026-10-01")
	visit.Set("visited_at", "2026-10-01 10:00:00.000Z")
	if err := app.Save(visit); err != nil {
		t.Fatal(err)
	}
	for _, recipient := range []struct{ id, user string }{{"nativeinbox0001", owner.Id}, {"foreigninbox001", other.Id}} {
		if _, err := app.DB().NewQuery(`INSERT INTO user_notification (id,user,kind,arcade,read,event_key,created) VALUES ({:id},{:user},'report_reaction',{:arcade},false,{:id},'2026-10-01 00:00:00.000Z')`).Bind(dbx.Params{"id": recipient.id, "user": recipient.user, "arcade": arcade.Id}).Execute(); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range nativeReadPaths {
		native := nativeReadRequest(t, app, "GET", "/native"+path, token, nil, 200)
		canonical := doUserRequest(t, app, "GET", path, map[string]string{"Authorization": "Bearer " + web}, "")
		var actual, expected map[string]any
		if err := json.NewDecoder(native.Body).Decode(&actual); err != nil {
			t.Fatal(err)
		}
		if canonical.StatusCode != 200 {
			t.Fatalf("canonical read %s status %d", path, canonical.StatusCode)
		}
		if err := json.NewDecoder(canonical.Body).Decode(&expected); err != nil {
			t.Fatal(err)
		}
		canonical.Body.Close()
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("native read %s changed canonical wire", path)
		}
		if path == "/user/notifications" {
			items := actual["items"].([]any)
			if len(items) != 1 || items[0].(map[string]any)["id"] != "nativeinbox0001" || actual["unread"] != float64(1) {
				t.Fatal("inbox lost owner isolation")
			}
		}
	}
	foreignVisit := core.NewRecord(visits)
	foreignVisit.Set("user", other.Id)
	foreignVisit.Set("arcade", arcade.Id)
	foreignVisit.Set("visit_day", "2026-10-02")
	foreignVisit.Set("visited_at", "2026-10-02 10:00:00.000Z")
	if err := app.Save(foreignVisit); err != nil {
		t.Fatal(err)
	}
	info, _ := app.FindRecordById("user_info", other.Id)
	info.Set("visit_visibility", "summary")
	if err := app.Save(info); err != nil {
		t.Fatal(err)
	}
	summary := nativeReadRequest(t, app, "GET", "/native/user/passport/stamps?user="+other.Id, token, nil, 200)
	var shared map[string]any
	if err := json.NewDecoder(summary.Body).Decode(&shared); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(shared)
	if len(shared["items"].([]any)) != 1 {
		t.Fatal("shared summary lost its visible stamp")
	}
	for _, field := range []string{"first_visit_day", "last_visit_day", "visit_dates", "visited_at", "accuracy_meters"} {
		if strings.Contains(string(encoded), field) {
			t.Fatal("native shared summary exposed visit detail")
		}
	}
	info.Set("visit_visibility", "private")
	if err := app.Save(info); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/native/user/passport?user=", "/native/user/passport/stamps?user="} {
		nativeReadRequest(t, app, "GET", path+other.Id, token, nil, 404)
	}
	inbox := nativeReadRequest(t, app, "GET", "/native/user/notifications?user="+other.Id+"&before=foreigninbox001", token, nil, 200)
	var page struct {
		Items []any `json:"items"`
	}
	if err := json.NewDecoder(inbox.Body).Decode(&page); err != nil || len(page.Items) != 0 {
		t.Fatal("foreign inbox cursor exposed data")
	}
	readPath := "/native/user/notifications/read"
	nativeReadRequest(t, app, "POST", readPath, token, map[string]any{"id": "foreigninbox001"}, 200)
	foreign, err := app.FindRecordById("user_notification", "foreigninbox001")
	if err != nil || foreign.GetBool("read") {
		t.Fatal("foreign notification was changed")
	}
	nativeReadRequest(t, app, "POST", readPath, token, map[string]any{"id": "nativeinbox0001"}, 200)
	own, err := app.FindRecordById("user_notification", "nativeinbox0001")
	if err != nil || !own.GetBool("read") {
		t.Fatal("own notification read did not persist")
	}
	if _, err := app.DB().NewQuery(`INSERT INTO user_notification (id,user,kind,arcade,read,event_key,created) VALUES ('nativeinbox0002',{:user},'report_reaction',{:arcade},false,'nativeinbox0002','2026-10-01 00:00:00.000Z')`).Bind(dbx.Params{"user": owner.Id, "arcade": arcade.Id}).Execute(); err != nil {
		t.Fatal(err)
	}
	nativeReadRequest(t, app, "POST", readPath, token, map[string]any{"all": true}, 200)
	own, err = app.FindRecordById("user_notification", "nativeinbox0002")
	if err != nil || !own.GetBool("read") {
		t.Fatal("own read-all did not persist")
	}
	foreign, err = app.FindRecordById("user_notification", "foreigninbox001")
	if err != nil || foreign.GetBool("read") {
		t.Fatal("read-all crossed account boundary")
	}
	nativeReadRequest(t, app, "POST", readPath, token, map[string]any{}, 400)

}

func TestNativeReadCredentialBoundaryBanAndDisabledRoutes(t *testing.T) {
	app := nativeReadApp(t, true)
	web, owner := createAuthUser(t, app, true)
	credentials := nativeLoginIssue(t, app, web)
	for _, path := range nativeReadPaths {
		for _, rejected := range []string{"", web, "not-a-native-session"} {
			nativeReadRequest(t, app, "GET", "/native"+path, rejected, nil, 401)
		}
	}
	for _, rejected := range []string{"", web, "not-a-native-session"} {
		nativeReadRequest(t, app, "POST", "/native/user/notifications/read", rejected, map[string]any{"all": true}, 401)
	}
	nativeReadRequest(t, app, "POST", "/user/notifications/read", credentials.AccessToken, map[string]any{"all": true}, 401)
	for _, path := range []string{"/user/visits", "/user/notifications"} {
		nativeReadRequest(t, app, "GET", path, credentials.AccessToken, nil, 401)
	}
	nativeReadRequest(t, app, "POST", "/native/user/notifications/read", credentials.AccessToken, map[string]any{"id": strings.Repeat("a", 5000)}, 413)
	seedUserBanForUserTest(t, app, owner.Id, hashNormalizedEmailForUserTest(owner.Email()), "native_read_fixture", time.Now().Add(time.Hour))
	for _, path := range nativeReadPaths {
		nativeReadRequest(t, app, "GET", "/native"+path, credentials.AccessToken, nil, 403)
	}
	nativeReadRequest(t, app, "POST", "/native/user/notifications/read", credentials.AccessToken, map[string]any{"all": true}, 403)
	disabled := nativeReadApp(t, false)
	for _, path := range nativeReadPaths {
		nativeReadRequest(t, disabled, "GET", "/native"+path, "", nil, 404)
	}
	for _, path := range []string{"/native/user/passport/reset", "/native/user/notifications/read", "/native/arcade/visit"} {
		nativeReadRequest(t, disabled, "POST", path, strings.Repeat("a", 43), nil, 404)
	}
}
