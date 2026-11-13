package user_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/pocketbase/pocketbase/core"
)

const nativeAPNsCapability = "/native/user/notifications/apns"

func TestNativeAPNsCapabilityUnsupportedAndCredentialBoundary(t *testing.T) {
	app := nativeLoginApp(t, true)
	web, _ := createAuthUser(t, app, true)
	session := nativeLoginIssue(t, app, web)
	cookie := "pb_auth=" + url.QueryEscape(`{"token":"`+web+`"}`)
	for _, headers := range []map[string]string{
		nil,
		{"Authorization": "Bearer invalid"},
		{"Authorization": "Bearer " + web},
		{"Cookie": cookie},
		{"Authorization": "Bearer " + strings.Repeat("x", 43), "Cookie": cookie},
	} {
		response := doUserRequest(t, app, "GET", nativeAPNsCapability, headers, "")
		response.Body.Close()
		if response.StatusCode != 401 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("Native-only credential boundary or no-store lost")
		}
	}
	var before, after int
	if err := app.DB().NewQuery("SELECT count(*) FROM native_auth_session").Row(&before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		response := nativeLoginRequest(t, app, "GET", nativeAPNsCapability, session.AccessToken, nil, 200)
		var result map[string]any
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(result, map[string]any{"available": false}) {
			t.Fatal("unsupported capability advertised configuration or changed DTO")
		}
	}
	if err := app.DB().NewQuery("SELECT count(*) FROM native_auth_session").Row(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("capability read created a device family")
	}
	// Ordinary Web JWT authentication remains available on its existing route.
	nativeLoginRequest(t, app, "GET", "/user/me", web, nil, 200)
}

func TestNativeAPNsCapabilityDoesNotRegisterOtherOperations(t *testing.T) {
	app := nativeLoginApp(t, true)
	web, _ := createAuthUser(t, app, true)
	session := nativeLoginIssue(t, app, web)
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		response := doUserRequest(t, app, method, nativeAPNsCapability, map[string]string{"Authorization": "Bearer " + session.AccessToken}, `{}`)
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s capability status %d", method, response.StatusCode)
		}
	}
	for _, operation := range []struct{ method, path string }{
		{"POST", nativeAPNsCapability + "/test"},
		{"POST", "/native/arcade/apns-subscription/status"},
		{"PUT", "/native/arcade/apns-subscription"},
		{"DELETE", "/native/arcade/apns-subscription"},
	} {
		nativeLoginRequest(t, app, operation.method, operation.path, session.AccessToken, nil, 404)
	}
}

func TestNativeAPNsCapabilityRejectsRevokedExpiredAndInactiveAccounts(t *testing.T) {
	app := nativeLoginApp(t, true)
	web, user := createAuthUser(t, app, true)
	session := nativeLoginIssue(t, app, web)
	service := nativeauth.Service{Clients: nativeLoginClients}
	if err := service.Revoke(app, session.RefreshToken); err != nil {
		t.Fatal(err)
	}
	nativeLoginRequest(t, app, "GET", nativeAPNsCapability, session.AccessToken, nil, 401)
	past := time.Now().Add(-11 * time.Minute)
	expiredService := nativeauth.Service{Clients: nativeLoginClients, Now: func() time.Time { return past }}
	verifier := strings.Repeat("a", 43)
	sum := sha256.Sum256([]byte(verifier))
	code, err := expiredService.IssueCode(app, user, nativeLoginClients[0].ID, nativeLoginClients[0].Callback, base64.RawURLEncoding.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	expired, err := expiredService.Exchange(app, code, verifier, nativeLoginClients[0].ID, nativeLoginClients[0].Callback)
	if err != nil {
		t.Fatal(err)
	}
	nativeLoginRequest(t, app, "GET", nativeAPNsCapability, expired.AccessToken, nil, 401)
	active := nativeLoginIssue(t, app, web)
	seedUserBanForUserTest(t, app, user.Id, hashNormalizedEmailForUserTest(user.Email()), "native_capability", time.Now().Add(time.Hour))
	nativeLoginRequest(t, app, "GET", nativeAPNsCapability, active.AccessToken, nil, 403)
	nativeLoginRequest(t, app, "GET", "/native/user/me", active.AccessToken, nil, 403)
}

func TestNativeAPNsCapabilityDisabledAllowlistDoesNotAcceptExistingSession(t *testing.T) {
	app := newUserFetchTestApp(t)
	enabled := true
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		if enabled {
			nativeauth.RegisterRoutes(e, nativeLoginClients)
		} else {
			nativeauth.RegisterRoutes(e, nil)
		}
		return e.Next()
	})
	web, _ := createAuthUser(t, app, true)
	session := nativeLoginIssue(t, app, web)
	if _, err := (nativeauth.Service{Clients: nativeLoginClients}).Authenticate(app, session.AccessToken); err != nil {
		t.Fatal("same-DB Native session must be valid before disabling routes")
	}
	enabled = false
	nativeLoginRequest(t, app, "GET", nativeAPNsCapability, session.AccessToken, nil, 404)
	nativeLoginRequest(t, app, "GET", "/user/me", web, nil, 200)
}
