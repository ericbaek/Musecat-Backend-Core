package user_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/coreapp"
	"github.com/ericbaek/musecat-backend-core/testutil"
	"github.com/pocketbase/pocketbase/core"
)

func TestOfficialArcadeCredentials(t *testing.T) {
	app := testutil.NewTestApp(t)
	app.OnServe().BindFunc(func(se *core.ServeEvent) error { coreapp.RegisterAPIRoutes(se); return se.Next() })
	token, account := createAuthUser(t, app, true)
	account.SetPassword(" current123 ")
	if err := app.Save(account); err != nil {
		t.Fatal(err)
	}
	token, _ = account.NewAuthToken()
	request := func(token, body string, status int) {
		t.Helper()
		response := doUserRequest(t, app, http.MethodPut, "/user/arcade-account/credentials", map[string]string{"Authorization": "Bearer " + token}, body)
		defer response.Body.Close()
		var result map[string]any
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != status {
			t.Fatalf("got %d want %d: %v", response.StatusCode, status, result)
		}
		if result["token"] != nil || result["password"] != nil || result["email"] != nil {
			t.Fatal("credentials leaked")
		}
	}
	request("", `{"currentPassword":" current123 ","newPassword":"changed123"}`, 401)
	request(token, `{"currentPassword":" current123 ","newPassword":"changed123"}`, 403)
	account.Set("tags", []string{"arcade_owner"})
	if err := app.Save(account); err != nil {
		t.Fatal(err)
	}
	request(token, `{"currentPassword":"wrong","newPassword":"changed123"}`, 401)
	request(token, `{"currentPassword":" current123 "}`, 400)
	request(token, `{"currentPassword":" current123 ","email":"invalid"}`, 400)
	request(token, `{"currentPassword":" current123 ","newPassword":"short"}`, 400)
	_, other := createAuthUser(t, app, true)
	request(token, fmt.Sprintf(`{"currentPassword":" current123 ","email":%q,"newPassword":"changed123"}`, other.Email()), 409)
	saved, _ := app.FindRecordById("user", account.Id)
	if !saved.ValidatePassword(" current123 ") {
		t.Fatal("failed update changed password")
	}
	oldEmail := saved.Email()
	request(token, `{"currentPassword":" current123 ","newPassword":" changed123 "}`, 200)
	saved, _ = app.FindRecordById("user", account.Id)
	if !saved.ValidatePassword(" changed123 ") || saved.ValidatePassword(" current123 ") || saved.Email() != oldEmail {
		t.Fatal("incorrect password-only change")
	}
	request(token, `{"currentPassword":" changed123 ","email":"new@example.com"}`, 401)
	token, _ = saved.NewAuthToken()
	request(token, `{"currentPassword":" changed123 ","email":" NEW@example.com "}`, 200)
	saved, _ = app.FindRecordById("user", account.Id)
	if saved.Email() != "new@example.com" || saved.Verified() || saved.EmailVisibility() || !saved.ValidatePassword(" changed123 ") {
		t.Fatal("incorrect email-only change")
	}
	request(token, `{"currentPassword":" changed123 ","newPassword":"changed456"}`, 401)
	token, _ = saved.NewAuthToken()
	request(token, `{"currentPassword":" changed123 ","email":"new@example.com"}`, 400)
	request(token, `{"currentPassword":" changed123 ","email":"combined@example.com","newPassword":"combined123"}`, 200)
	saved, _ = app.FindRecordById("user", account.Id)
	if saved.Email() != "combined@example.com" || !saved.ValidatePassword("combined123") {
		t.Fatal("incorrect combined credentials change")
	}
	token, _ = saved.NewAuthToken()
	seedUserBanForUserTest(t, app, saved.Id, hashNormalizedEmailForUserTest(saved.Email()), "blocked", time.Now().Add(time.Hour))
	request(token, `{"currentPassword":"combined123","email":"blocked@example.com"}`, 403)
}

func TestOfficialArcadeWithdrawalRequiresExactPasswordAndReleasesOwnership(t *testing.T) {
	app := testutil.NewTestApp(t)
	app.OnServe().BindFunc(func(se *core.ServeEvent) error { coreapp.RegisterAPIRoutes(se); return se.Next() })
	token, account := createAuthUser(t, app, true)
	arcade := seedVisitArcade(t, app, "Asia/Seoul")
	account.Set("tags", []string{"arcade_owner"})
	account.Set("owns", []string{arcade.Id})
	account.SetPassword(" secret123 ")
	if err := app.Save(account); err != nil {
		t.Fatal(err)
	}
	token, _ = account.NewAuthToken()
	for _, test := range []struct {
		body   string
		status int
	}{
		{`{}`, 400},
		{`{"password":"secret123"}`, 401},
		{`{"password":" secret123 "}`, 200},
	} {
		response := doUserRequest(t, app, http.MethodPost, "/user/withdraw", map[string]string{"Authorization": "Bearer " + token}, test.body)
		response.Body.Close()
		if response.StatusCode != test.status {
			t.Fatalf("withdraw got %d want %d", response.StatusCode, test.status)
		}
	}
	saved, err := app.FindRecordById("user", account.Id)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.GetBool("withdrawn") || len(saved.GetStringSlice("owns")) != 0 || len(saved.GetStringSlice("tags")) != 0 || saved.ValidatePassword(" secret123 ") {
		t.Fatal("official account not fully withdrawn")
	}
}
