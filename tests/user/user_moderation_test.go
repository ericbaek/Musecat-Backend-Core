package user_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/coreapp"
	"github.com/ericbaek/musecat-backend-core/testutil"
	"github.com/pocketbase/pocketbase/core"
)

func TestUserBanModeration(t *testing.T) {
	app := testutil.NewTestApp(t)
	app.OnServe().BindFunc(func(se *core.ServeEvent) error { coreapp.RegisterAPIRoutes(se); return se.Next() })
	_, target := createAuthUser(t, app, true)
	moderatorToken, moderator := createAuthUser(t, app, true)
	moderator.Set("tags", []string{"moderator"})
	if err := app.Save(moderator); err != nil {
		t.Fatal(err)
	}
	ordinaryToken, _ := createAuthUser(t, app, true)
	body := fmt.Sprintf(`{"user":%q,"reason":"spam"}`, target.Id)
	for _, request := range []struct{ method, url, body string }{
		{http.MethodGet, "/moderation/user/bans", ""},
		{http.MethodGet, "/moderation/user/reports", ""},
		{http.MethodPost, "/moderation/user/ban", body},
		{http.MethodDelete, "/moderation/user/ban?user=" + target.Id, ""},
		{http.MethodPut, "/moderation/user/report", `{"id":"abcdefghijklmn0","status":"done"}`},
		{http.MethodPut, "/support_feedback", `{"id":"abcdefghijklmn0","status":"solved"}`},
	} {
		for _, auth := range []struct {
			token  string
			status int
		}{{"", 401}, {ordinaryToken, 403}} {
			res := doUserRequest(t, app, request.method, request.url, map[string]string{"Authorization": "Bearer " + auth.token, "Content-Type": "application/json"}, request.body)
			res.Body.Close()
			if res.StatusCode != auth.status {
				t.Fatalf("%s %s got %d want %d", request.method, request.url, res.StatusCode, auth.status)
			}
		}
	}
	headers := map[string]string{"Authorization": "Bearer " + moderatorToken, "Content-Type": "application/json"}
	for _, request := range []struct {
		method, url, body string
		status            int
	}{
		{http.MethodPost, "/moderation/user/ban", fmt.Sprintf(`{"user":%q,"reason":"spam","until":"2020-01-01T00:00:00Z"}`, target.Id), 400},
		{http.MethodPost, "/moderation/user/ban", body, 200},
		{http.MethodGet, "/moderation/user/bans", "", 200},
		{http.MethodDelete, "/moderation/user/ban?user=" + target.Id, "", 200},
	} {
		res := doUserRequest(t, app, request.method, request.url, headers, request.body)
		res.Body.Close()
		if res.StatusCode != request.status {
			t.Fatalf("%s %s got %d want %d", request.method, request.url, res.StatusCode, request.status)
		}
	}
	if _, err := app.FindRecordById("user_ban", target.Id); err == nil {
		t.Fatal("ban not deleted")
	}
	seedUserBanForUserTest(t, app, moderator.Id, hashNormalizedEmailForUserTest(moderator.Email()), "restricted", time.Time{})
	res := doUserRequest(t, app, http.MethodPost, "/moderation/user/ban", headers, body)
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("banned moderator got %d", res.StatusCode)
	}
}
