package user_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

var nativeLoginClients = []nativeauth.Client{{ID: "app.musecat.native.dev", Callback: "musecat-dev://auth/callback"}}

func nativeLoginApp(t *testing.T, enabled bool) *tests.TestApp {
	t.Helper()
	app := newUserFetchTestApp(t)
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		if enabled {
			nativeauth.RegisterRoutes(e, nativeLoginClients)
		} else {
			nativeauth.RegisterRoutes(e, nil)
		}
		return e.Next()
	})
	return app
}

func nativeLoginRequest(t *testing.T, app *tests.TestApp, method, path, token string, body any, status int) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var headers map[string]string
	if token != "" {
		headers = map[string]string{"Authorization": "Bearer " + token}
	}
	r := doUserRequest(t, app, method, path, headers, string(encoded))
	t.Cleanup(func() { r.Body.Close() })
	if r.StatusCode != status {
		t.Fatalf("%s %s: got status %d, want %d", method, path, r.StatusCode, status)
	}
	if strings.HasPrefix(path, "/native/") && status != 404 && r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("%s omitted no-store", path)
	}
	return r
}

func nativeLoginCode(t *testing.T, app *tests.TestApp, web string) (string, string) {
	t.Helper()
	verifier := strings.Repeat("x", 43)
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	r := nativeLoginRequest(t, app, "POST", "/native/session/code", web, map[string]string{"client_id": nativeLoginClients[0].ID, "redirect_uri": nativeLoginClients[0].Callback, "code_challenge": challenge}, 200)
	var result struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Code) != 43 {
		t.Fatal("invalid code shape")
	}
	return result.Code, verifier
}

func nativeLoginExchangeBody(code, verifier string) map[string]string {
	return map[string]string{"code": code, "code_verifier": verifier, "client_id": nativeLoginClients[0].ID, "redirect_uri": nativeLoginClients[0].Callback}
}

func nativeLoginCredentials(t *testing.T, r *http.Response) nativeauth.Credentials {
	t.Helper()
	var result nativeauth.Credentials
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.UserID == "" || len(result.AccessToken) != 43 || len(result.RefreshToken) != 43 || result.ExpiresIn != 600 {
		t.Fatal("invalid credential wire shape")
	}
	return result
}

func nativeLoginIssue(t *testing.T, app *tests.TestApp, web string) nativeauth.Credentials {
	t.Helper()
	code, verifier := nativeLoginCode(t, app, web)
	return nativeLoginCredentials(t, nativeLoginRequest(t, app, "POST", "/native/session/exchange", "", nativeLoginExchangeBody(code, verifier), 200))
}

func TestNativeLoginHTTPAccountScopeRotationAndIsolation(t *testing.T) {
	app := nativeLoginApp(t, true)
	web, account := createAuthUser(t, app, true)
	otherWeb, otherAccount := createAuthUser(t, app, true)
	first := nativeLoginIssue(t, app, web)
	otherDevice := nativeLoginIssue(t, app, web)
	otherUser := nativeLoginIssue(t, app, otherWeb)
	if first.UserID != account.Id || otherUser.UserID != otherAccount.Id {
		t.Fatal("exchange returned another account")
	}
	for _, pair := range []struct{ token, id string }{{first.AccessToken, account.Id}, {otherUser.AccessToken, otherAccount.Id}} {
		var profile struct {
			ID              string `json:"id"`
			FavoriteArcades []any  `json:"favorite_arcades"`
		}
		r := nativeLoginRequest(t, app, "GET", "/native/user/me", pair.token, nil, 200)
		if err := json.NewDecoder(r.Body).Decode(&profile); err != nil {
			t.Fatal(err)
		}
		if profile.ID != pair.id || profile.FavoriteArcades == nil {
			t.Fatal("profile lost account scope or native required favorites wire")
		}
	}
	nativeLoginRequest(t, app, "GET", "/native/user/me", "", nil, 401)
	nativeLoginRequest(t, app, "GET", "/native/user/me", web, nil, 401)
	nativeLoginRequest(t, app, "GET", "/user/me", first.AccessToken, nil, 401)
	nativeLoginRequest(t, app, "GET", "/user/me", web, nil, 200)
	nativeLoginRequest(t, app, "POST", "/native/session/code", first.AccessToken, map[string]string{}, 401)
	rotated := nativeLoginCredentials(t, nativeLoginRequest(t, app, "POST", "/native/session/refresh", "", map[string]string{"refresh_token": first.RefreshToken}, 200))
	nativeLoginRequest(t, app, "GET", "/native/user/me", first.AccessToken, nil, 401)
	nativeLoginRequest(t, app, "GET", "/native/user/me", rotated.AccessToken, nil, 200)
	nativeLoginRequest(t, app, "POST", "/native/session/refresh", "", map[string]string{"refresh_token": first.RefreshToken}, 401)
	nativeLoginRequest(t, app, "GET", "/native/user/me", rotated.AccessToken, nil, 401)
	nativeLoginRequest(t, app, "GET", "/native/user/me", otherDevice.AccessToken, nil, 200)
	for attempt := 0; attempt < 2; attempt++ {
		nativeLoginRequest(t, app, "POST", "/native/session/revoke", "", map[string]string{"refresh_token": otherDevice.RefreshToken}, 204)
	}
	nativeLoginRequest(t, app, "GET", "/native/user/me", otherDevice.AccessToken, nil, 401)
	nativeLoginRequest(t, app, "GET", "/native/user/me", otherUser.AccessToken, nil, 200)
	nativeLoginRequest(t, app, "GET", "/user/me", web, nil, 200)
}

func TestNativeLoginHTTPBindingReplayAndActiveAccount(t *testing.T) {
	app := nativeLoginApp(t, true)
	web, account := createAuthUser(t, app, true)
	code, verifier := nativeLoginCode(t, app, web)
	wrong := nativeLoginExchangeBody(code, verifier)
	wrong["redirect_uri"] = "https://other.example/callback"
	nativeLoginRequest(t, app, "POST", "/native/session/exchange", "", wrong, 401)
	nativeLoginRequest(t, app, "POST", "/native/session/exchange", "", nativeLoginExchangeBody(code, strings.Repeat("z", 43)), 401)
	valid := nativeLoginCredentials(t, nativeLoginRequest(t, app, "POST", "/native/session/exchange", "", nativeLoginExchangeBody(code, verifier), 200))
	nativeLoginRequest(t, app, "POST", "/native/session/exchange", "", nativeLoginExchangeBody(code, verifier), 401)
	pending, pendingVerifier := nativeLoginCode(t, app, web)
	seedUserBanForUserTest(t, app, account.Id, hashNormalizedEmailForUserTest(account.Email()), "native_login_test", time.Now().Add(time.Hour))
	nativeLoginRequest(t, app, "GET", "/native/user/me", valid.AccessToken, nil, 403)
	nativeLoginRequest(t, app, "POST", "/native/session/code", web, map[string]string{}, 403)
	nativeLoginRequest(t, app, "POST", "/native/session/exchange", "", nativeLoginExchangeBody(pending, pendingVerifier), 403)
	nativeLoginRequest(t, app, "POST", "/native/session/refresh", "", map[string]string{"refresh_token": valid.RefreshToken}, 403)
}

func TestNativeLoginHTTPDisabledAndInvalidRequests(t *testing.T) {
	app := nativeLoginApp(t, false)
	for _, route := range []struct{ method, path string }{{"POST", "/native/session/code"}, {"POST", "/native/session/exchange"}, {"POST", "/native/session/refresh"}, {"POST", "/native/session/revoke"}, {"GET", "/native/user/me"}} {
		nativeLoginRequest(t, app, route.method, route.path, "", map[string]string{}, 404)
	}
	enabled := nativeLoginApp(t, true)
	nativeLoginRequest(t, enabled, "POST", "/native/session/code", "", map[string]string{}, 401)
	nativeLoginRequest(t, enabled, "POST", "/native/session/exchange", "", map[string]string{}, 401)
	nativeLoginRequest(t, enabled, "POST", "/native/session/refresh", "", map[string]string{}, 401)
	nativeLoginRequest(t, enabled, "POST", "/native/session/revoke", "", map[string]string{}, 400)
	nativeLoginRequest(t, enabled, "POST", "/native/session/exchange", "", map[string]string{"code": strings.Repeat("a", 5000)}, 413)
}

func TestNativeLoginHTTPRejectsRemovedClientWhileOtherClientRemains(t *testing.T) {
	app := newUserFetchTestApp(t)
	web, _ := createAuthUser(t, app, true)
	dev := nativeLoginClients[0]
	production := nativeauth.Client{ID: "app.musecat.native", Callback: "https://example.test/native/callback"}
	clients := []nativeauth.Client{dev, production}
	app.OnServe().BindFunc(func(e *core.ServeEvent) error { nativeauth.RegisterRoutes(e, clients); return e.Next() })
	old := nativeLoginIssue(t, app, web)
	service := nativeauth.Service{Clients: clients}
	verifier := strings.Repeat("p", 43)
	digest := sha256.Sum256([]byte(verifier))
	user, err := app.FindRecordById("user", old.UserID)
	if err != nil {
		t.Fatal(err)
	}
	code, err := service.IssueCode(app, user, production.ID, production.Callback, base64.RawURLEncoding.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	current, err := service.Exchange(app, code, verifier, production.ID, production.Callback)
	if err != nil {
		t.Fatal(err)
	}
	clients = []nativeauth.Client{production}
	nativeLoginRequest(t, app, "GET", "/native/user/me", old.AccessToken, nil, 401)
	nativeLoginRequest(t, app, "POST", "/native/session/refresh", "", map[string]string{"refresh_token": old.RefreshToken}, 401)
	nativeLoginRequest(t, app, "GET", "/native/user/me", current.AccessToken, nil, 200)
	nativeLoginRequest(t, app, "POST", "/native/session/refresh", "", map[string]string{"refresh_token": current.RefreshToken}, 200)
	nativeLoginRequest(t, app, "GET", "/user/me", web, nil, 200)
}
