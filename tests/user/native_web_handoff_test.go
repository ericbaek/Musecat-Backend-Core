package user_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/pocketbase/pocketbase/core"
)

func TestNativeWebHandoffHTTPConfidentialExchangeAndIsolation(t *testing.T) {
	app := newUserFetchTestApp(t)
	secret := strings.Repeat("s", 43)
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		nativeauth.RegisterRoutes(e, nativeLoginClients, nativeauth.WebClient{ID: nativeLoginClients[0].ID, Issuer: "https://musecat.app", Secret: secret})
		return e.Next()
	})
	web, account := createAuthUser(t, app, true)
	native := nativeLoginIssue(t, app, web)
	verifier := strings.Repeat("v", 43)
	request := map[string]string{"client_id": nativeLoginClients[0].ID, "issuer": "https://musecat.app", "code_challenge": nativePKCEChallenge(verifier)}
	issue := func() nativeauth.WebCode {
		t.Helper()
		r := nativeLoginRequest(t, app, "POST", "/native/session/web/code", native.AccessToken, request, 200)
		var grant nativeauth.WebCode
		if err := json.NewDecoder(r.Body).Decode(&grant); err != nil {
			t.Fatal(err)
		}
		if grant.UserID != account.Id || grant.ExpiresIn != 60 || len(grant.Code) != 43 {
			t.Fatal("invalid web grant wire")
		}
		return grant
	}
	grant := issue()
	body := map[string]string{"code": grant.Code, "code_verifier": verifier, "client_id": nativeLoginClients[0].ID, "issuer": "https://musecat.app"}
	exchange := func(basic string, status int) nativeauth.WebCredentials {
		t.Helper()
		encoded, _ := json.Marshal(body)
		r := doUserRequest(t, app, "POST", "/native/session/web/exchange", map[string]string{"Authorization": basic}, string(encoded))
		defer r.Body.Close()
		if r.StatusCode != status || r.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("web exchange status=%d want=%d", r.StatusCode, status)
		}
		var out nativeauth.WebCredentials
		if status == 200 {
			if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte(nativeLoginClients[0].ID+":"+secret))
	exchange("Bearer "+native.AccessToken, 401)
	exchange("Bearer "+web, 401)
	exchange("Basic "+base64.StdEncoding.EncodeToString([]byte(nativeLoginClients[0].ID+":wrong")), 401)
	body["code_verifier"] = strings.Repeat("x", 43)
	exchange(basic, 401)
	body["code_verifier"] = verifier
	out := exchange(basic, 200)
	if out.UserID != account.Id || out.ExpiresIn != 900 || out.Token == "" {
		t.Fatal("invalid web exchange wire")
	}
	nativeLoginRequest(t, app, "GET", "/user/me", out.Token, nil, 200)
	nativeLoginRequest(t, app, "GET", "/native/user/me", out.Token, nil, 401)
	nativeLoginRequest(t, app, "GET", "/user/me", native.AccessToken, nil, 401)
	nativeLoginRequest(t, app, "POST", "/native/session/web/code", web, request, 401)
	exchange(basic, 401)
	grant = issue()
	body["code"] = grant.Code
	nativeLoginRequest(t, app, "POST", "/native/session/revoke", "", map[string]string{"refresh_token": native.RefreshToken}, 204)
	exchange(basic, 401)
	// Already-issued short web JWT retains its normal web semantics until expiry.
	nativeLoginRequest(t, app, "GET", "/user/me", out.Token, nil, 200)
}

func nativePKCEChallenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func TestNativeWebHandoffHTTPDisabledLimitsAndBan(t *testing.T) {
	disabled := nativeLoginApp(t, true)
	for _, path := range []string{"/native/session/web/code", "/native/session/web/exchange"} {
		nativeLoginRequest(t, disabled, "POST", path, "", nil, 404)
	}
	app := newUserFetchTestApp(t)
	secret := strings.Repeat("s", 43)
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		nativeauth.RegisterRoutes(e, nativeLoginClients, nativeauth.WebClient{ID: nativeLoginClients[0].ID, Issuer: "https://musecat.app", Secret: secret})
		return e.Next()
	})
	web, account := createAuthUser(t, app, true)
	native := nativeLoginIssue(t, app, web)
	request := map[string]string{"client_id": nativeLoginClients[0].ID, "issuer": "https://musecat.app", "code_challenge": nativePKCEChallenge(strings.Repeat("v", 43))}
	nativeLoginRequest(t, app, "POST", "/native/session/web/code", "", request, 401)
	nativeLoginRequest(t, app, "POST", "/native/session/web/code", native.AccessToken, map[string]string{"code_challenge": "bad"}, 400)
	nativeLoginRequest(t, app, "POST", "/native/session/web/code", native.AccessToken, map[string]string{"code_challenge": strings.Repeat("a", 5000)}, 413)
	grantResponse := nativeLoginRequest(t, app, "POST", "/native/session/web/code", native.AccessToken, request, 200)
	var grant nativeauth.WebCode
	if err := json.NewDecoder(grantResponse.Body).Decode(&grant); err != nil {
		t.Fatal(err)
	}
	seedUserBanForUserTest(t, app, account.Id, hashNormalizedEmailForUserTest(account.Email()), "handoff_fixture", time.Now().Add(time.Hour))
	nativeLoginRequest(t, app, "POST", "/native/session/web/code", native.AccessToken, request, 403)
	encoded, _ := json.Marshal(map[string]string{"client_id": nativeLoginClients[0].ID, "issuer": "https://musecat.app", "code": grant.Code, "code_verifier": strings.Repeat("v", 43)})
	r := doUserRequest(t, app, "POST", "/native/session/web/exchange", map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(nativeLoginClients[0].ID+":"+secret))}, string(encoded))
	defer r.Body.Close()
	if r.StatusCode != 403 {
		t.Fatalf("banned ticket redeemed: %d", r.StatusCode)
	}
}
