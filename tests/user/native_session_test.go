package user_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/pocketbase/pocketbase/core"
	"net/http"
	"strings"
	"testing"
)

func TestNativeSessionUsesExistingFavoriteAndVisitPolicy(t *testing.T) {
	app := newUserFetchTestApp(t)
	web, u := createAuthUser(t, app, true)
	clients := []nativeauth.Client{{ID: "ios", Callback: "https://musecat.app/native/callback"}}
	app.OnServe().BindFunc(func(e *core.ServeEvent) error { nativeauth.RegisterRoutes(e, clients); return e.Next() })
	verifier := strings.Repeat("x", 43)
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	body, _ := json.Marshal(map[string]string{"client_id": "ios", "redirect_uri": clients[0].Callback, "code_challenge": challenge})
	r := doUserRequest(t, app, "POST", "/native/session/code", map[string]string{"Authorization": "Bearer " + web}, string(body))
	if r.StatusCode != 200 {
		t.Fatalf("code status %d", r.StatusCode)
	}
	var code struct {
		Code string `json:"code"`
	}
	json.NewDecoder(r.Body).Decode(&code)
	r.Body.Close()
	body, _ = json.Marshal(map[string]string{"code": code.Code, "code_verifier": verifier, "client_id": "ios", "redirect_uri": clients[0].Callback})
	r = doUserRequest(t, app, "POST", "/native/session/exchange", nil, string(body))
	if r.StatusCode != 200 {
		t.Fatalf("exchange status %d", r.StatusCode)
	}
	var credentials nativeauth.Credentials
	if err := json.NewDecoder(r.Body).Decode(&credentials); err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	headers := map[string]string{"Authorization": "Bearer " + credentials.AccessToken}
	arcade := seedVisitArcade(t, app, "Asia/Seoul")
	r = doUserRequest(t, app, "PUT", "/native/arcade/favorite", headers, `{"arcade":"`+arcade.Id+`","favorited":true}`)
	if r.StatusCode != 200 {
		t.Fatalf("favorite %d", r.StatusCode)
	}
	r.Body.Close()
	rows, e := app.FindRecordsByFilter("arcade_favorite", "user={:user}", "", 0, 0, map[string]any{"user": u.Id})
	if e != nil || len(rows) != 1 {
		t.Fatal("favorite was not persisted")
	}
	r = doUserRequest(t, app, "POST", "/native/arcade/visit", headers, `{"arcade":"`+arcade.Id+`","lat":37,"lon":127,"accuracy":5}`)
	if r.StatusCode != http.StatusForbidden {
		t.Fatalf("out of radius=%d", r.StatusCode)
	}
	r.Body.Close()
	for attempt := 0; attempt < 2; attempt++ {
		r = doUserRequest(t, app, "POST", "/native/arcade/visit", headers, `{"arcade":"`+arcade.Id+`","lat":37.5665,"lon":126.9780,"accuracy":10}`)
		if r.StatusCode != 200 {
			t.Fatalf("visit %d", r.StatusCode)
		}
		var result struct {
			AlreadyVisited bool `json:"already_visited"`
			GainedExp      int  `json:"gained_exp"`
		}
		if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if result.AlreadyVisited != (attempt == 1) {
			t.Fatal("duplicate day policy changed")
		}
		if attempt == 0 && result.GainedExp != 5 {
			t.Fatalf("first XP=%d", result.GainedExp)
		}
		if attempt == 1 && result.GainedExp != 0 {
			t.Fatal("duplicate awarded XP")
		}
	}
	r = doUserRequest(t, app, "GET", "/native/user/me", map[string]string{"Authorization": "Bearer " + web}, "")
	if r.StatusCode != 401 {
		t.Fatalf("web credential accepted=%d", r.StatusCode)
	}
	r.Body.Close()
	r = doUserRequest(t, app, "GET", "/user/me", headers, "")
	if r.StatusCode != 401 {
		t.Fatalf("native credential accepted by web=%d", r.StatusCode)
	}
	r.Body.Close()
	u.Set("withdrawn", true)
	if e = app.Save(u); e != nil {
		t.Fatal(e)
	}
	r = doUserRequest(t, app, "GET", "/native/user/me", headers, "")
	if r.StatusCode != 401 && r.StatusCode != 403 {
		t.Fatalf("withdrawn accepted=%d", r.StatusCode)
	}
	r.Body.Close()
}
