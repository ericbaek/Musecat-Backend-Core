package user_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/coreapp"
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/ericbaek/musecat-backend-core/testutil"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

func TestCreateArcadeAccount(t *testing.T) {
	app := testutil.NewTestApp(t)
	app.OnServe().BindFunc(func(se *core.ServeEvent) error { coreapp.RegisterAPIRoutes(se); return se.Next() })
	arcade := seedVisitArcade(t, app, "Asia/Seoul")
	token, staff := createAuthUser(t, app, true)
	request := func(token, body string, status int) map[string]any {
		t.Helper()
		res := doUserRequest(t, app, http.MethodPost, "/moderation/user/arcade-account", map[string]string{"Authorization": "Bearer " + token, "Content-Type": "application/json"}, body)
		defer res.Body.Close()
		var result map[string]any
		if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != status {
			t.Fatalf("got %d want %d: %v", res.StatusCode, status, result)
		}
		return result
	}
	payload := func(name, arcadeID string) string {
		return fmt.Sprintf(`{"email":%q,"username":%q,"password":" secret123 ","nickname":"공식 계정","arcadeId":%q,"tags":["developer"],"verified":true}`, name+"@example.com", name, arcadeID)
	}
	request("", payload("Official1", arcade.Id), 401)
	request(token, payload("Official1", arcade.Id), 403)
	for i, role := range []string{"developer", "moderator", "designer"} {
		staff.Set("tags", []string{role})
		if err := app.Save(staff); err != nil {
			t.Fatal(err)
		}
		result := request(token, payload(fmt.Sprintf("Official%d", i), arcade.Id), 201)
		user, err := app.FindRecordById("user", result["id"].(string))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(user.GetStringSlice("tags"), []string{"arcade_owner"}) || !slices.Equal(user.GetStringSlice("owns"), []string{arcade.Id}) || user.Verified() {
			t.Fatal("unexpected account privileges or arcade assignment")
		}
		if _, err := app.FindRecordById("user_info", user.Id); err != nil {
			t.Fatal("profile missing:", err)
		}
		notices, err := app.FindRecordsByFilter(usernotification.Inbox, "user={:user} && event_key={:event}", "", 0, 0, dbx.Params{"user": user.Id, "event": usernotification.ArcadeSetupEvent})
		if err != nil || len(notices) != 1 {
			t.Fatalf("official setup notice missing: %v %v", notices, err)
		}
		if result["arcadeId"] != arcade.Id || result["password"] != nil || result["token"] != nil {
			t.Fatal("invalid or sensitive response:", result)
		}
		if i == 0 {
			login := doUserRequest(t, app, http.MethodPost, "/api/collections/user/auth-with-password", map[string]string{"Content-Type": "application/json"}, fmt.Sprintf(`{"identity":%q,"identityField":"username","password":" secret123 "}`, user.GetString("username")))
			login.Body.Close()
			if login.StatusCode != http.StatusOK {
				t.Fatalf("username password login returned %d", login.StatusCode)
			}
		}
	}
	// Designer provisioning does not grant access to other moderation tools.
	res := doUserRequest(t, app, http.MethodPost, "/moderation/user/test-account", map[string]string{"Authorization": "Bearer " + token, "Content-Type": "application/json"}, payload("Ordinary1", arcade.Id))
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatal("designer can create ordinary test accounts")
	}
	request(token, payload("Official0", arcade.Id), 409)
	request(token, payload("official0", arcade.Id), 409)
	request(token, payload("InvalidId", ""), 400)
	request(token, payload("MissingId", "missingarcade"), 404)
	arcade.Set("public", false)
	if err := app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	request(token, payload("PrivateId", arcade.Id), 404)
	if _, err := app.FindAuthRecordByEmail("user", "privateid@example.com"); err == nil {
		t.Fatal("account created for private arcade")
	}
	seedUserBanForUserTest(t, app, staff.Id, hashNormalizedEmailForUserTest(staff.Email()), "restricted", time.Time{})
	request(token, payload("BannedId", arcade.Id), 403)
}

func TestCreateArcadeAccountRollsBack(t *testing.T) {
	app := testutil.NewTestApp(t)
	app.OnServe().BindFunc(func(se *core.ServeEvent) error { coreapp.RegisterAPIRoutes(se); return se.Next() })
	arcade := seedVisitArcade(t, app, "Asia/Seoul")
	token, staff := createAuthUser(t, app, true)
	staff.Set("tags", []string{"designer"})
	if err := app.Save(staff); err != nil {
		t.Fatal(err)
	}
	app.OnRecordCreate("user_info").BindFunc(func(e *core.RecordEvent) error { return fmt.Errorf("profile save failure") })
	res := doUserRequest(t, app, http.MethodPost, "/moderation/user/arcade-account", map[string]string{"Authorization": "Bearer " + token, "Content-Type": "application/json"}, fmt.Sprintf(`{"email":"rollback@example.com","username":"Rollback","password":"secret123","nickname":"공식 계정","arcadeId":%q}`, arcade.Id))
	res.Body.Close()
	if res.StatusCode != 502 {
		t.Fatalf("got %d", res.StatusCode)
	}
	if _, err := app.FindAuthRecordByEmail("user", "rollback@example.com"); err == nil {
		t.Fatal("partial official account persisted")
	}
	notices, err := app.FindRecordsByFilter(usernotification.Inbox, "event_key={:event}", "", 0, 0, dbx.Params{"event": usernotification.ArcadeSetupEvent})
	if err != nil || len(notices) != 0 {
		t.Fatalf("rolled-back account retained notice: %v %v", notices, err)
	}
}
