package user_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/tools/hook"
)

type withdrawHTTP struct {
	app     *tests.TestApp
	server  *httptest.Server
	enabled bool
}

func newWithdrawHTTP(t *testing.T, enabled bool) *withdrawHTTP {
	t.Helper()
	app := nativeReadApp(t, enabled)
	router, e := apis.NewRouter(app)
	if e != nil {
		t.Fatal(e)
	}
	if e = app.OnServe().Trigger(&core.ServeEvent{App: app, Router: router}, func(e *core.ServeEvent) error { return e.Next() }); e != nil {
		t.Fatal(e)
	}
	// Deliberately misuse the exception on two other operations: neither may authenticate Native.
	router.POST("/__test/closure-scope", func(e *core.RequestEvent) error { return e.NoContent(204) }).Bind(nativeauth.FirstPartyAccountClosureAuthentication(nativeLoginClients), apis.RequireAuth("user"))
	router.GET("/user/withdraw", func(e *core.RequestEvent) error { return e.NoContent(204) }).Bind(nativeauth.FirstPartyAccountClosureAuthentication(nativeLoginClients), apis.RequireAuth("user"))
	mux, e := router.BuildMux()
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return &withdrawHTTP{app, server, enabled}
}
func (h *withdrawHTTP) request(t *testing.T, method, path, token, cookie string, body any, want int) map[string]any {
	t.Helper()
	raw, e := json.Marshal(body)
	if e != nil {
		t.Fatal(e)
	}
	r, e := http.NewRequest(method, h.server.URL+path, bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if cookie != "" {
		r.Header.Set("Cookie", "pb_auth="+url.QueryEscape(`{"token":"`+cookie+`"}`))
	}
	res, e := h.server.Client().Do(r)
	if e != nil {
		t.Fatal("local HTTP request failed")
	}
	defer res.Body.Close()
	if res.StatusCode != want {
		t.Fatalf("%s %s got %d want %d", method, path, res.StatusCode, want)
	}
	if h.enabled && len(token) == 43 && path == "/user/withdraw" && method == "POST" && !strings.Contains(res.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("Native closure cache")
	}
	if want == 204 {
		return nil
	}
	var out map[string]any
	if json.NewDecoder(res.Body).Decode(&out) != nil {
		t.Fatal("HTTP JSON receipt")
	}
	return out
}
func (h *withdrawHTTP) login(t *testing.T, web string) nativeauth.Credentials {
	t.Helper()
	verifier := strings.Repeat("a", 64)
	sum := sha256.Sum256([]byte(verifier))
	client := nativeLoginClients[0]
	code := h.request(t, "POST", "/native/session/code", web, "", map[string]any{"client_id": client.ID, "redirect_uri": client.Callback, "code_challenge": base64.RawURLEncoding.EncodeToString(sum[:])}, 200)
	v := h.request(t, "POST", "/native/session/exchange", "", "", map[string]any{"client_id": client.ID, "redirect_uri": client.Callback, "code": code["code"], "code_verifier": verifier}, 200)
	raw, _ := json.Marshal(v)
	var c nativeauth.Credentials
	if json.Unmarshal(raw, &c) != nil || c.AccessToken == "" || c.RefreshToken == "" {
		t.Fatal("Native credentials")
	}
	return c
}
func withdrawReceipt(t *testing.T, v map[string]any) {
	t.Helper()
	if len(v) != 2 || v["success"] != true {
		t.Fatal("withdraw wire")
	}
	stamp, ok := v["withdrawnAt"].(string)
	if !ok {
		t.Fatal("withdraw date")
	}
	if _, e := time.Parse(time.RFC3339Nano, stamp); e != nil {
		t.Fatal("withdraw date format")
	}
}
func externalLogin(t *testing.T, app core.App, u *core.Record) {
	t.Helper()
	v := core.NewExternalAuth(app)
	v.SetCollectionRef(u.Collection().Id)
	v.SetRecordRef(u.Id)
	v.SetProvider("google")
	v.SetProviderId("synthetic-" + u.Id)
	if app.Save(v) != nil {
		t.Fatal("synthetic OAuth link")
	}
}
func withdrawBan(t *testing.T, app core.App, u *core.Record) {
	t.Helper()
	c, e := app.FindCollectionByNameOrId("user_ban")
	if e != nil {
		t.Fatal(e)
	}
	v := core.NewRecord(c)
	v.Id = u.Id
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(u.Email()))))
	v.Set("hashed_email", hex.EncodeToString(sum[:]))
	v.Set("reason", "Synthetic existing moderation")
	v.Set("until", time.Now().Add(time.Hour))
	if app.Save(v) != nil {
		t.Fatal("synthetic ban")
	}
}

func TestCanonicalNativeWithdrawActualHTTPWireAndAllSessionInvalidation(t *testing.T) {
	var wires []map[string]any
	for _, mode := range []string{"native", "web"} {
		t.Run(mode, func(t *testing.T) {
			h := newWithdrawHTTP(t, true)
			web, u := createAuthUser(t, h.app, true)
			_, foreign := createAuthUser(t, h.app, true)
			oldKey := u.TokenKey()
			oldEmail := u.Email()
			first := h.login(t, web)
			second := h.login(t, web)
			credential := web
			if mode == "native" {
				credential = first.AccessToken
			}
			h.request(t, "POST", "/user/withdraw", credential, "", map[string]any{}, 400)
			h.request(t, "POST", "/user/withdraw", credential, "", map[string]any{"password": "incorrect"}, 401)
			still, _ := h.app.FindRecordById("user", u.Id)
			if still.TokenKey() != oldKey || still.GetBool("withdrawn") {
				t.Fatal("failed closure mutated account")
			}
			v := h.request(t, "POST", "/user/withdraw", credential, "", map[string]any{"password": " secret123 ", "reason": " QA closure ", "user": foreign.Id}, 200)
			withdrawReceipt(t, v)
			wires = append(wires, v)
			saved, e := h.app.FindRecordById("user", u.Id)
			if e != nil || !saved.GetBool("withdrawn") || saved.TokenKey() == oldKey || saved.GetString("withdrawReason") != "QA closure" || saved.ValidatePassword("secret123") {
				t.Fatal("canonical closure state")
			}
			ban, e := h.app.FindRecordById("user_ban", u.Id)
			stamp, _ := time.Parse(time.RFC3339Nano, v["withdrawnAt"].(string))
			emailHash := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(oldEmail))))
			if e != nil || ban.GetString("hashed_email") != hex.EncodeToString(emailHash[:]) || ban.GetDateTime("until").Time().Sub(stamp.AddDate(0, 0, 30)) > time.Millisecond || ban.GetDateTime("until").Time().Sub(stamp.AddDate(0, 0, 30)) < -time.Millisecond {
				t.Fatal("canonical rejoin cooldown/hash")
			}
			other, _ := h.app.FindRecordById("user", foreign.Id)
			if other.GetBool("withdrawn") {
				t.Fatal("spoofed actor")
			}
			profile, _ := h.app.FindRecordById("user_info", u.Id)
			if profile.GetString("bio") != "" || profile.GetString("nickname") != "탈퇴한 사용자" {
				t.Fatal("profile anonymization")
			}
			service := nativeauth.Service{Clients: nativeLoginClients}
			for _, c := range []nativeauth.Credentials{first, second} {
				if _, e = service.Authenticate(h.app, c.AccessToken); e == nil {
					t.Fatal("old family access usable")
				}
				if _, e = service.Refresh(h.app, c.RefreshToken); e == nil {
					t.Fatal("old family refresh usable")
				}
			}
			h.request(t, "GET", "/user/visits", web, "", nil, 401)
			var physical int
			if e = h.app.DB().NewQuery("SELECT sum(revoked) FROM native_auth_session WHERE user_id={:u}").Bind(dbx.Params{"u": u.Id}).Row(&physical); e != nil || physical != 0 {
				t.Fatal("closure expanded physical family revocation")
			}
			h.request(t, "POST", "/native/session/revoke", "", "", map[string]any{"refresh_token": first.RefreshToken}, 204)
			h.app.DB().NewQuery("SELECT sum(revoked) FROM native_auth_session WHERE user_id={:u}").Bind(dbx.Params{"u": u.Id}).Row(&physical)
			if physical != 1 {
				t.Fatal("official logout must revoke only caller family")
			}
			if mode == "native" && os.Getenv("MUSECAT_CANONICAL_WITHDRAW_FIXTURE") != "" {
				dir := os.Getenv("MUSECAT_CANONICAL_WITHDRAW_FIXTURE")
				if os.MkdirAll(dir, 0700) != nil {
					t.Fatal("fixture directory")
				}
				raw, _ := json.MarshalIndent(v, "", "  ")
				if os.WriteFile(filepath.Join(dir, "account-withdraw.json"), append(raw, '\n'), 0600) != nil {
					t.Fatal("public receipt export")
				}
			}
		})
	}
	for _, v := range wires {
		delete(v, "withdrawnAt")
	}
	if len(wires) != 2 || !reflect.DeepEqual(wires[0], wires[1]) {
		t.Fatal("Web/Native wire differs beyond validated time")
	}
}

func TestCanonicalNativeWithdrawOAuthAndOfficialExactPassword(t *testing.T) {
	for _, mode := range []string{"native", "web"} {
		for _, official := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "OAuth", true: "Official"}[official], func(t *testing.T) {
				h := newWithdrawHTTP(t, true)
				web, u := createAuthUser(t, h.app, true)
				externalLogin(t, h.app, u)
				if official {
					u.Set("tags", []string{"arcade_owner", "supporter"})
					arcade := seedVisitArcade(t, h.app, "Asia/Seoul")
					u.Set("owns", []string{arcade.Id})
					u.SetPassword(" secret123 ")
					if h.app.Save(u) != nil {
						t.Fatal("official fixture")
					}
					web, _ = u.NewAuthToken()
				}
				credentials := h.login(t, web)
				token := web
				if mode == "native" {
					token = credentials.AccessToken
				}
				body := map[string]any{}
				if official {
					h.request(t, "POST", "/user/withdraw", token, "", body, 400)
					h.request(t, "POST", "/user/withdraw", token, "", map[string]any{"password": "secret123"}, 401)
					body["password"] = " secret123 "
				}
				withdrawReceipt(t, h.request(t, "POST", "/user/withdraw", token, "", body, 200))
				saved, _ := h.app.FindRecordById("user", u.Id)
				links, e := h.app.FindAllExternalAuthsByRecord(saved)
				if e != nil || len(links) != 0 {
					t.Fatal("OAuth links retained")
				}
				if official && (!reflect.DeepEqual(saved.GetStringSlice("tags"), []string{"supporter"}) || len(saved.GetStringSlice("owns")) != 0) {
					t.Fatal("official ownership not cleared or unrelated role lost")
				}
			})
		}
	}
}

func TestCanonicalNativeWithdrawBanAllowedOnlyForClosure(t *testing.T) {
	for _, mode := range []string{"native", "web"} {
		t.Run(mode, func(t *testing.T) {
			h := newWithdrawHTTP(t, true)
			web, u := createAuthUser(t, h.app, true)
			session := h.login(t, web)
			withdrawBan(t, h.app, u)
			h.request(t, "GET", "/user/visits", session.AccessToken, "", nil, 403)
			token := web
			if mode == "native" {
				token = session.AccessToken
			}
			withdrawReceipt(t, h.request(t, "POST", "/user/withdraw", token, "", map[string]any{"password": "secret123"}, 200))
			ban, e := h.app.FindRecordById("user_ban", u.Id)
			if e != nil || ban.GetString("until") != "" || ban.GetString("reason") != "Synthetic existing moderation" {
				t.Fatal("existing ban not upgraded to permanent rejoin block")
			}
		})
	}
}

func TestCanonicalNativeWithdrawRollbackKeepsCredentialsAndBusinessState(t *testing.T) {
	h := newWithdrawHTTP(t, true)
	web, u := createAuthUser(t, h.app, true)
	externalLogin(t, h.app, u)
	arcade := seedVisitArcade(t, h.app, "Asia/Seoul")
	u.Set("tags", []string{"arcade_owner", "supporter"})
	u.Set("owns", []string{arcade.Id})
	u.SetPassword(" exact official ")
	if h.app.Save(u) != nil {
		t.Fatal("rollback official fixture")
	}
	web, _ = u.NewAuthToken()
	session := h.login(t, web)
	oldKey := u.TokenKey()
	oldEmail := u.Email()
	h.app.OnRecordUpdateExecute("user_info").Bind(&hook.Handler[*core.RecordEvent]{Id: "withdrawFailure", Func: func(e *core.RecordEvent) error {
		if e.Record.Id == u.Id {
			return errors.New("synthetic rollback")
		}
		return e.Next()
	}})
	h.request(t, "POST", "/user/withdraw", session.AccessToken, "", map[string]any{"password": " exact official "}, 502)
	saved, _ := h.app.FindRecordById("user", u.Id)
	links, _ := h.app.FindAllExternalAuthsByRecord(saved)
	var bans int
	h.app.DB().NewQuery("SELECT count(*) FROM user_ban WHERE id={:u}").Bind(dbx.Params{"u": u.Id}).Row(&bans)
	if saved.TokenKey() != oldKey || saved.Email() != oldEmail || saved.GetBool("withdrawn") || len(links) != 1 || bans != 0 || !reflect.DeepEqual(saved.GetStringSlice("tags"), []string{"arcade_owner", "supporter"}) || !reflect.DeepEqual(saved.GetStringSlice("owns"), []string{arcade.Id}) {
		t.Fatal("withdraw transaction partially committed")
	}
	if _, e := (nativeauth.Service{Clients: nativeLoginClients}).Authenticate(h.app, session.AccessToken); e != nil {
		t.Fatal("rollback invalidated session")
	}
}

func TestCanonicalNativeWithdrawAuthScopeLifetimeAndDisabled(t *testing.T) {
	for _, scenario := range []string{"guest", "invalid-cookie", "revoked", "expired", "family-expired", "withdrawn", "account-key", "removed-client", "disabled"} {
		t.Run(scenario, func(t *testing.T) {
			h := newWithdrawHTTP(t, scenario != "disabled")
			web, u := createAuthUser(t, h.app, true)
			service := nativeauth.Service{Clients: nativeLoginClients}
			// Disabled runtime still contains a valid independently issued session in the same DB.
			var session nativeauth.Credentials
			if scenario == "disabled" {
				code, e := service.IssueCode(h.app, u, nativeLoginClients[0].ID, nativeLoginClients[0].Callback, base64.RawURLEncoding.EncodeToString(func() []byte { x := sha256.Sum256([]byte(strings.Repeat("a", 64))); return x[:] }()))
				if e != nil {
					t.Fatal(e)
				}
				session, e = service.Exchange(h.app, code, strings.Repeat("a", 64), nativeLoginClients[0].ID, nativeLoginClients[0].Callback)
				if e != nil {
					t.Fatal(e)
				}
			} else {
				session = h.login(t, web)
			}
			if _, e := service.Authenticate(h.app, session.AccessToken); e != nil {
				t.Fatal("session must start valid")
			}
			token, cookie := session.AccessToken, ""
			switch scenario {
			case "guest":
				token = ""
			case "invalid-cookie":
				token = strings.Repeat("x", 43)
				cookie = web
			case "revoked":
				if service.Revoke(h.app, session.RefreshToken) != nil {
					t.Fatal("revoke")
				}
			case "expired":
				h.app.DB().NewQuery("UPDATE native_auth_session SET access_expires=0 WHERE user_id={:u}").Bind(dbx.Params{"u": u.Id}).Execute()
			case "family-expired":
				h.app.DB().NewQuery("UPDATE native_auth_session SET expires=0 WHERE user_id={:u}").Bind(dbx.Params{"u": u.Id}).Execute()
			case "withdrawn":
				u.Set("withdrawn", true)
				h.app.Save(u)
			case "account-key":
				u.RefreshTokenKey()
				h.app.Save(u)
			case "removed-client":
				h.app.DB().NewQuery("UPDATE native_auth_session SET client='removed' WHERE user_id={:u}").Bind(dbx.Params{"u": u.Id}).Execute()
			}
			h.request(t, "POST", "/user/withdraw", token, cookie, map[string]any{"password": "secret123"}, 401)
			saved, _ := h.app.FindRecordById("user", u.Id)
			if scenario != "withdrawn" && saved.GetBool("withdrawn") {
				t.Fatal("denied request withdrew account")
			}
			if scenario == "disabled" {
				withdrawReceipt(t, h.request(t, "POST", "/user/withdraw", web, "", map[string]any{"password": "secret123"}, 200))
			}
		})
	}
	h := newWithdrawHTTP(t, true)
	web, _ := createAuthUser(t, h.app, true)
	session := h.login(t, web)
	h.request(t, "POST", "/__test/closure-scope", session.AccessToken, "", nil, 401)
	h.request(t, "GET", "/user/withdraw", session.AccessToken, "", nil, 401)
	h.request(t, "POST", "/native/user/withdraw", session.AccessToken, "", nil, 404)
	// A test-owned quota exercises the unchanged limiter around the selected route.
	h.app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: "POST /user/withdraw", MaxRequests: 1, Duration: 60}}}
	h.request(t, "POST", "/user/withdraw", session.AccessToken, "", map[string]any{}, 400)
	h.request(t, "POST", "/user/withdraw", session.AccessToken, "", map[string]any{}, 429)
}
