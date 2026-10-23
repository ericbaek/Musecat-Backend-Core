package user_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
	usernotifications "github.com/ericbaek/musecat-backend-core/handlers/user/notifications"
	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

func TestNativeNotificationsAccountScopeAndRead(t *testing.T) {
	app := newUserFetchTestApp(t)
	web, user := createAuthUser(t, app, true)
	_, other := createAuthUser(t, app, true)
	clients := []nativeauth.Client{{ID: "ios", Callback: "musecat-test://auth/callback"}}
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		routes := nativeauth.RegisterRoutes(e, clients)
		routes.GET("/user/notifications", usernotifications.List)
		routes.POST("/user/notifications/read", usernotifications.Read)
		webRoutes := e.Router.Group("/user/notifications").Bind(apis.RequireAuth("user"), userhandler.RequireActiveUser())
		webRoutes.GET("", usernotifications.List)
		webRoutes.POST("/read", usernotifications.Read)
		return e.Next()
	})
	verifier := strings.Repeat("n", 43)
	digest := sha256.Sum256([]byte(verifier))
	service := nativeauth.Service{Clients: clients}
	code, err := service.IssueCode(app, user, "ios", clients[0].Callback, base64.RawURLEncoding.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := service.Exchange(app, code, verifier, "ios", clients[0].Callback)
	if err != nil {
		t.Fatal(err)
	}
	arcade := seedVisitArcade(t, app, "Asia/Seoul")
	insert := func(id, owner string) {
		t.Helper()
		_, err := app.DB().NewQuery("INSERT INTO user_notification (id,user,kind,arcade,read,event_key,created) VALUES ({:id},{:user},'report_reaction',{:arcade},false,{:id},'2026-10-01 00:00:00.000Z')").Bind(dbx.Params{"id": id, "user": owner, "arcade": arcade.Id}).Execute()
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 21; i++ {
		insert(fmt.Sprintf("nativeinbox%04d", i), user.Id)
	}
	foreign := "foreigninbox001"
	insert(foreign, other.Id)
	request := func(method, path, token, body string, status int) map[string]any {
		t.Helper()
		headers := map[string]string{"Content-Type": "application/json"}
		if token != "" {
			headers["Authorization"] = "Bearer " + token
		}
		response := doUserRequest(t, app, method, path, headers, body)
		defer response.Body.Close()
		var result map[string]any
		_ = json.NewDecoder(response.Body).Decode(&result)
		if response.StatusCode != status {
			t.Fatalf("%s %s got %d want %d: %v", method, path, response.StatusCode, status, result)
		}
		if status == 200 && !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
			t.Fatal("native inbox response can be cached")
		}
		return result
	}
	path, token := "/native/user/notifications", credentials.AccessToken
	for _, rejected := range []string{"", web, "partner-invalid"} {
		request("GET", path, rejected, "", 401)
		request("POST", path+"/read", rejected, `{"all":true}`, 401)
	}
	request("GET", "/user/notifications", token, "", 401)
	request("POST", "/user/notifications/read", token, `{"all":true}`, 401)
	page := request("GET", path+"?user="+other.Id, token, "", 200)
	items := page["items"].([]any)
	if len(items) != 20 || page["unread"] != float64(21) || page["next"] != "nativeinbox0001" || items[0].(map[string]any)["id"] != "nativeinbox0020" {
		t.Fatalf("wrong account scope/order/page: %v", page)
	}
	last := request("GET", path+"?before="+page["next"].(string), token, "", 200)
	if len(last["items"].([]any)) != 1 || last["next"] != "" {
		t.Fatalf("wrong cursor page: %v", last)
	}
	if result := request("POST", path+"/read", token, `{"id":"`+foreign+`"}`, 200); result["ok"] != true {
		t.Fatal(result)
	}
	foreignRow, err := app.FindRecordById("user_notification", foreign)
	if err != nil || foreignRow.GetBool("read") {
		t.Fatal("foreign read modified another account")
	}
	request("POST", path+"/read", token, `{}`, 400)
	request("POST", path+"/read", token, `{"id":"nativeinbox0020"}`, 200)
	page = request("GET", path, token, "", 200)
	if page["unread"] != float64(20) || page["items"].([]any)[0].(map[string]any)["read"] != true {
		t.Fatal("read state did not persist", page)
	}
	request("POST", path+"/read", token, `{"all":true}`, 200)
	if page = request("GET", path, token, "", 200); page["unread"] != float64(0) {
		t.Fatal("read-all did not persist", page)
	}
	// Visibility is checked again on each read, even with the same session.
	arcade.Set("public", false)
	if err := app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	if page = request("GET", path, token, "", 200); len(page["items"].([]any)) != 0 {
		t.Fatal("private arcade leaked through native inbox", page)
	}
	user.Set("withdrawn", true)
	if err := app.Save(user); err != nil {
		t.Fatal(err)
	}
	request("GET", path, token, "", 401)
	request("POST", path+"/read", token, `{"all":true}`, 401)
}
