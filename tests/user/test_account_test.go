package user_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/coreapp"
	"github.com/ericbaek/musecat-backend-core/testutil"
	"github.com/pocketbase/pocketbase/core"
)

func TestCreateTestAccount(t *testing.T) {
	app := testutil.NewTestApp(t)
	app.OnServe().BindFunc(func(se *core.ServeEvent) error { coreapp.RegisterAPIRoutes(se); return se.Next() })
	token, staff := createAuthUser(t, app, true)
	staff.Set("tags", []string{"developer"})
	if err := app.Save(staff); err != nil {
		t.Fatal(err)
	}
	ordinary, _ := createAuthUser(t, app, true)
	payload := `{"email":"test@example.com","username":"TestUser","password":" secret123 ","nickname":"테스트","tags":["developer"],"verified":true}`
	request := func(token, body string, status int) map[string]any {
		t.Helper()
		res := doUserRequest(t, app, http.MethodPost, "/moderation/user/test-account", map[string]string{"Authorization": "Bearer " + token, "Content-Type": "application/json"}, body)
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
	request("", payload, 401)
	request(ordinary, payload, 403)
	for _, body := range []string{
		`{`,
		`{"email":"invalid","username":"TestUser","password":"secret123","nickname":"테스트"}`,
		`{"email":"test@example.com","username":"bad_id","password":"secret123","nickname":"테스트"}`,
		`{"email":"test@example.com","username":"TestUser","password":"short","nickname":"테스트"}`,
		fmt.Sprintf(`{"email":"test@example.com","username":"TestUser","password":%q,"nickname":"테스트"}`, strings.Repeat("a", 72)),
		`{"email":"test@example.com","username":"TestUser","password":"secret123","nickname":"🇰🇷"}`,
		`{"email":"test@example.com","username":"TestUser","password":"secret123","nickname":""}`,
	} {
		request(token, body, 400)
	}
	result := request(token, payload, 201)
	id, ok := result["id"].(string)
	if !ok {
		t.Fatal("missing id")
	}
	user, err := app.FindRecordById("user", id)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := app.FindRecordById("user_info", id)
	if err != nil {
		t.Fatal(err)
	}
	if !user.ValidatePassword(" secret123 ") || user.GetString("username") != "TestUser" || user.Email() != "test@example.com" {
		t.Fatal("credentials did not persist")
	}
	if len(user.GetStringSlice("tags")) != 0 || user.Verified() {
		t.Fatal("privileged fields were accepted")
	}
	if result["password"] != nil || result["token"] != nil {
		t.Fatal("credentials leaked")
	}
	if profile.GetString("nickname") != "테스트" || profile.GetString("country_mode") != "auto" || profile.GetString("visit_visibility") != "summary" || profile.GetString("favorite_visibility") != "private" {
		t.Fatal("profile defaults incorrect")
	}
	request(token, payload, 409)
	request(token, `{"email":"other@example.com","username":"testuser","password":"secret123","nickname":"테스트"}`, 409)
	request(token, `{"email":"TEST@example.com","username":"OtherUser","password":"secret123","nickname":"테스트"}`, 409)
	staff.Set("tags", []string{"moderator"})
	if err := app.Save(staff); err != nil {
		t.Fatal(err)
	}
	request(token, `{"email":"modtest@example.com","username":"ModTest","password":"secret123","nickname":"테스트"}`, 201)
	seedUserBanForUserTest(t, app, staff.Id, hashNormalizedEmailForUserTest(staff.Email()), "restricted", time.Time{})
	request(token, payload, 403)
}

func TestCreateTestAccountRollsBackProfileFailure(t *testing.T) {
	app := testutil.NewTestApp(t)
	app.OnServe().BindFunc(func(se *core.ServeEvent) error { coreapp.RegisterAPIRoutes(se); return se.Next() })
	token, staff := createAuthUser(t, app, true)
	staff.Set("tags", []string{"moderator"})
	if err := app.Save(staff); err != nil {
		t.Fatal(err)
	}
	app.OnRecordCreate("user_info").BindFunc(func(e *core.RecordEvent) error { return fmt.Errorf("profile save failure") })
	res := doUserRequest(t, app, http.MethodPost, "/moderation/user/test-account", map[string]string{"Authorization": "Bearer " + token, "Content-Type": "application/json"}, `{"email":"rollback@example.com","username":"Rollback","password":"secret123","nickname":"테스트"}`)
	defer res.Body.Close()
	if res.StatusCode != 502 {
		t.Fatalf("got %d", res.StatusCode)
	}
	if _, err := app.FindAuthRecordByEmail("user", "rollback@example.com"); err == nil {
		t.Fatal("partial account persisted")
	}
}
