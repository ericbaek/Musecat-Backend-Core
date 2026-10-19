package delegation_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/ericbaek/musecat-backend-core/coreapp"
	"github.com/ericbaek/musecat-backend-core/delegation"
	dh "github.com/ericbaek/musecat-backend-core/handlers/delegation"
	"github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/ericbaek/musecat-backend-core/testutil"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const secret = "test-client-secret-at-least-32-characters"
const verifier = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-._~"
const callback = "https://partner.example/callback"

var allScopes = []string{"profile:read", "arcade:read", "arcade:report:create", "arcade:report:react", "campaign:read", "campaign:respond"}

type fixture struct {
	t     *testing.T
	app   *tests.TestApp
	m     *delegation.Manager
	mux   http.Handler
	token string
	user  *core.Record
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, app: testutil.NewTestApp(t)}
	var e error
	f.m, e = delegation.NewManager("https://api.musecat.example", "https://musecat.example/oauth/authorize", []delegation.Client{{ID: "chuniinfo", Name: "CHUNITHM Info", Secret: secret, RedirectURIs: []string{callback}, Scopes: allScopes}, {ID: "noslog", Name: "NosLog", Secret: secret + "2", RedirectURIs: []string{"https://noslog.example/callback"}, Scopes: allScopes}, {ID: "accountapp", Name: "Account App", Secret: secret + "3", RedirectURIs: []string{callback}, Scopes: []string{delegation.AccountAccess}}}, user.CheckActiveUser)
	if e != nil {
		t.Fatal(e)
	}
	if e := delegation.SynchronizeClients(f.app, f.m); e != nil {
		t.Fatal(e)
	}
	r, e := apis.NewRouter(f.app)
	if e != nil {
		t.Fatal(e)
	}
	se := &core.ServeEvent{App: f.app, Router: r}
	coreapp.RegisterAPIRoutes(se)
	dh.RegisterRoutes(se, f.m)
	coreapp.RegisterAccountDelegationRoutes(se, f.m)
	f.mux, e = r.BuildMux()
	if e != nil {
		t.Fatal(e)
	}
	f.token, f.user = f.newUser()
	return f
}
func (f *fixture) newUser() (string, *core.Record) {
	f.t.Helper()
	c, e := f.app.FindCollectionByNameOrId("user")
	if e != nil {
		f.t.Fatal(e)
	}
	u := core.NewRecord(c)
	u.SetEmail(fmt.Sprintf("oauth-%d@example.com", time.Now().UnixNano()))
	u.SetPassword("secret123")
	if e = f.app.Save(u); e != nil {
		f.t.Fatal(e)
	}
	token, e := u.NewAuthToken()
	if e != nil {
		f.t.Fatal(e)
	}
	return token, u
}
func (f *fixture) req(method, path, token, body, ctype string, basic bool) *httptest.ResponseRecorder {
	f.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if ctype != "" {
		r.Header.Set("Content-Type", ctype)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if basic {
		r.SetBasicAuth("chuniinfo", secret)
	}
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	return w
}
func check(t *testing.T, w *httptest.ResponseRecorder, status int) map[string]any {
	t.Helper()
	if w.Code != status {
		t.Fatalf("got %d want %d: %s", w.Code, status, w.Body.String())
	}
	var out map[string]any
	if w.Body.Len() > 0 {
		if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
			t.Fatal(e)
		}
	}
	return out
}
func (f *fixture) begin(client, redirect, scopes string) string {
	f.t.Helper()
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {client}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {scopes}, "state": {"our-client-state"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}}
	w := f.req("GET", "/oauth/authorize?"+q.Encode(), "", "", "", false)
	check(f.t, w, 302)
	target, e := url.Parse(w.Header().Get("Location"))
	if e != nil {
		f.t.Fatal(e)
	}
	if target.Host != "musecat.example" || target.Path != "/oauth/authorize" {
		f.t.Fatalf("unsafe consent redirect: %v", target)
	}
	return target.Query().Get("request")
}
func (f *fixture) read(handle, token string, intStatus int) map[string]any {
	f.t.Helper()
	return check(f.t, f.req("GET", "/oauth/consent?request="+url.QueryEscape(handle), token, "", "", false), intStatus)
}
func (f *fixture) decision(handle, token string, approve bool, scopes []string, status int) string {
	f.t.Helper()
	b, _ := json.Marshal(delegation.Decision{Request: handle, Approve: approve, Scopes: scopes})
	out := check(f.t, f.req("POST", "/oauth/consent", token, string(b), "application/json", false), status)
	if status != 200 {
		return ""
	}
	return out["redirect_uri"].(string)
}
func (f *fixture) authorize(scopes ...string) string {
	f.t.Helper()
	h := f.begin("chuniinfo", callback, strings.Join(allScopes, " "))
	f.read(h, f.token, 200)
	target, _ := url.Parse(f.decision(h, f.token, true, scopes, 200))
	if target.Query().Get("state") != "our-client-state" || target.Query().Get("iss") != "https://api.musecat.example" {
		f.t.Fatal("callback lost state or issuer")
	}
	return target.Query().Get("code")
}
func (f *fixture) exchange(code, v string, status int) map[string]any {
	f.t.Helper()
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {callback}, "code_verifier": {v}}
	return check(f.t, f.req("POST", "/oauth/token", "", form.Encode(), "application/x-www-form-urlencoded", true), status)
}
func (f *fixture) refresh(raw string, status int) map[string]any {
	f.t.Helper()
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {raw}}
	return check(f.t, f.req("POST", "/oauth/token", "", form.Encode(), "application/x-www-form-urlencoded", true), status)
}
func (f *fixture) access(at, path string, status int) map[string]any {
	f.t.Helper()
	return check(f.t, f.req("GET", path, at, "", "", false), status)
}
func firstGrant(t *testing.T, f *fixture) string {
	t.Helper()
	out := check(t, f.req("GET", "/user/authorizations", f.token, "", "", false), 200)
	return out["items"].([]any)[0].(map[string]any)["id"].(string)
}

func TestAuthorizationLifecycle(t *testing.T) {
	f := newFixture(t)
	code := f.authorize("profile:read", "arcade:read")
	token := f.exchange(code, verifier, 200)
	at, rt := token["access_token"].(string), token["refresh_token"].(string)
	if token["scope"] != "arcade:read profile:read" || token["token_type"] != "Bearer" || token["expires_in"].(float64) > 600 {
		t.Fatal(token)
	}
	me := f.access(at, "/developer/v1/me", 200)
	sub := me["sub"].(string)
	if sub == f.user.Id || len(sub) != 43 || len(me) != 3 {
		t.Fatal("unsafe profile projection", me)
	}
	f.access(at, "/developer/v1/arcades/nearby?lat=37.5&lon=127", 200)
	// Neither kind of token is accepted in the other security realm.
	f.access(f.token, "/developer/v1/me", 401)
	f.access(rt, "/developer/v1/me", 401)
	f.access(at, "/user/me", 401)
	refreshed := f.refresh(rt, 200)
	if refreshed["refresh_token"] == rt {
		t.Fatal("refresh token not rotated")
	}
	f.access(refreshed["access_token"].(string), "/developer/v1/me", 200)
	// A replay is a committed grant revocation, not an error rolled back by SQL.
	f.refresh(rt, 400)
	f.access(refreshed["access_token"].(string), "/developer/v1/me", 401)
	// Reapproval preserves the app-specific subject and does not resurrect old tokens.
	next := f.exchange(f.authorize("profile:read"), verifier, 200)
	if f.access(next["access_token"].(string), "/developer/v1/me", 200)["sub"] != sub {
		t.Fatal("subject changed")
	}
	f.access(at, "/developer/v1/me", 401)
	form := url.Values{"token": {next["refresh_token"].(string)}}
	check(t, f.req("POST", "/oauth/revoke", "", form.Encode(), "application/x-www-form-urlencoded", true), 200)
	f.access(next["access_token"].(string), "/developer/v1/me", 401)
}
func TestPartialConsentAndScopeEnforcement(t *testing.T) {
	f := newFixture(t)
	token := f.exchange(f.authorize("profile:read"), verifier, 200)
	at := token["access_token"].(string)
	f.access(at, "/developer/v1/me", 200)
	f.access(at, "/developer/v1/arcades/nearby?lat=37.5&lon=127", 403)
	for _, path := range []string{"/developer/v1/arcade/flag", "/developer/v1/arcade/flag/reaction", "/developer/v1/campaign/check"} {
		check(t, f.req("POST", path, at, `{}`, "application/json", false), 403)
	}
	// With the correct scope, the original domain validator executes (still no bypass).
	writes := f.exchange(f.authorize("arcade:report:create", "arcade:report:react", "campaign:respond"), verifier, 200)
	for _, path := range []string{"/developer/v1/arcade/flag", "/developer/v1/arcade/flag/reaction", "/developer/v1/campaign/check"} {
		check(t, f.req("POST", path, writes["access_token"].(string), `{}`, "application/json", false), 400)
	}
}
func TestInvalidAuthorizationRedirectsOnlyToVerifiedCallbacks(t *testing.T) {
	f := newFixture(t)
	s := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {"chuniinfo"}, "redirect_uri": {callback}, "response_type": {"code"}, "scope": {"profile:read"}, "state": {"state"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(s[:])}, "code_challenge_method": {"S256"}}
	for _, tc := range []struct{ k, v string }{{"redirect_uri", "https://evil.example/callback"}, {"client_id", "unknown"}, {"response_type", "token"}, {"scope", "admin:all"}, {"scope", "profile:read profile:read"}, {"code_challenge_method", "plain"}, {"code_challenge", "short"}, {"state", ""}} {
		t.Run(tc.k+tc.v, func(t *testing.T) {
			p := q.Encode()
			values, _ := url.ParseQuery(p)
			values.Set(tc.k, tc.v)
			w := f.req("GET", "/oauth/authorize?"+values.Encode(), "", "", "", false)
			if tc.k == "redirect_uri" || tc.k == "client_id" || tc.k == "state" {
				check(t, w, 400)
				if w.Header().Get("Location") != "" {
					t.Fatal("unvalidated redirect")
				}
			} else {
				check(t, w, 302)
				target, _ := url.Parse(w.Header().Get("Location"))
				if target.Host != "partner.example" || target.Path != "/callback" || target.Query().Get("error") == "" || target.Query().Get("state") != "state" {
					t.Fatal("unsafe protocol error redirect", target)
				}
			}
		})
	}
	check(t, f.req("GET", "/oauth/authorize?"+q.Encode()+"&client_id=noslog", "", "", "", false), 400)
}
func TestConsentBoundToSessionAndCannotExpandScopes(t *testing.T) {
	f := newFixture(t)
	h := f.begin("chuniinfo", callback, "profile:read")
	f.read(h, "", 401)
	f.decision(h, f.token, true, []string{"profile:read"}, 403)
	f.read(h, f.token, 200)
	other, _ := f.newUser()
	f.read(h, other, 403)
	f.decision(h, other, true, []string{"profile:read"}, 403)
	f.decision(h, f.token, true, []string{"arcade:read"}, 400)
	f.decision(h, f.token, true, []string{}, 400)
	// A different but otherwise valid login token must not reuse a viewed consent.
	changed, e := f.user.NewStaticAuthToken(time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if changed == f.token {
		t.Fatal("test requires a different valid user session")
	}
	f.read(h, changed, 403)
	check(t, f.req("POST", "/oauth/consent", f.token, `{"request":"`+h+`"}`, "application/json", false), 400)
	target, _ := url.Parse(f.decision(h, f.token, false, nil, 200))
	if target.Query().Get("error") != "access_denied" {
		t.Fatal(target)
	}
	f.read(h, f.token, 400)
}
func TestCodePKCEClientRedirectAndReplay(t *testing.T) {
	f := newFixture(t)
	code := f.authorize("profile:read")
	f.exchange(code, strings.Repeat("x", 43), 400)
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {"https://evil.example"}, "code_verifier": {verifier}}
	check(t, f.req("POST", "/oauth/token", "", form.Encode(), "application/x-www-form-urlencoded", true), 400)
	w := f.req("POST", "/oauth/token", "", form.Encode(), "application/x-www-form-urlencoded", false)
	check(t, w, 401)
	// An authenticated different client cannot redeem this code.
	req := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(form.Encode()))
	req.SetBasicAuth("noslog", secret+"2")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	check(t, rec, 400)
	token := f.exchange(code, verifier, 200)
	f.exchange(code, verifier, 400)
	f.access(token["access_token"].(string), "/developer/v1/me", 401)
}
func TestReapprovalReplacesScopesAndOldReplayCannotRevokeNewGrant(t *testing.T) {
	f := newFixture(t)
	oldCode := f.authorize("profile:read", "arcade:read")
	old := f.exchange(oldCode, verifier, 200)
	next := f.exchange(f.authorize("profile:read"), verifier, 200)
	f.access(old["access_token"].(string), "/developer/v1/me", 401)
	f.access(next["access_token"].(string), "/developer/v1/arcades/nearby?lat=37.5&lon=127", 403)
	f.refresh(old["refresh_token"].(string), 400)
	f.exchange(oldCode, verifier, 400)
	f.access(next["access_token"].(string), "/developer/v1/me", 200)
}
func TestUserRevocationCancelsPendingAndOwnerIsolation(t *testing.T) {
	f := newFixture(t)
	token := f.exchange(f.authorize("profile:read"), verifier, 200)
	id := firstGrant(t, f)
	h := f.begin("chuniinfo", callback, "profile:read")
	f.read(h, f.token, 200)
	other, _ := f.newUser()
	check(t, f.req("DELETE", "/user/authorizations/"+id, other, "", "", false), 404)
	check(t, f.req("DELETE", "/user/authorizations/"+id, f.token, "", "", false), 204)
	f.access(token["access_token"].(string), "/developer/v1/me", 401)
	f.decision(h, f.token, true, []string{"profile:read"}, 400)
}
func TestBanWithdrawalAndAccountKeyReset(t *testing.T) {
	for _, mode := range []string{"withdrawn", "credentials"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			token := f.exchange(f.authorize("profile:read"), verifier, 200)
			if mode == "withdrawn" {
				f.user.Set("withdrawn", true)
			} else {
				f.user.RefreshTokenKey()
			}
			if e := f.app.Save(f.user); e != nil {
				t.Fatal(e)
			}
			f.access(token["access_token"].(string), "/developer/v1/me", 401)
			f.refresh(token["refresh_token"].(string), 400)
		})
	}
}
func TestSecretsAndCollectionsAreNotExposed(t *testing.T) {
	f := newFixture(t)
	token := f.exchange(f.authorize("profile:read"), verifier, 200)
	for _, name := range []string{delegation.Registrations, delegation.Grants, delegation.Requests, delegation.Codes, delegation.Tokens} {
		c, e := f.app.FindCollectionByNameOrId(name)
		if e != nil || c.ListRule != nil || c.ViewRule != nil || c.CreateRule != nil || c.UpdateRule != nil || c.DeleteRule != nil {
			t.Fatal("unlocked OAuth collection", name, e)
		}
		rows, e := f.app.FindAllRecords(name)
		if e != nil {
			t.Fatal(e)
		}
		stored := []any{}
		for _, row := range rows {
			stored = append(stored, row.FieldsData())
		}
		data, _ := json.Marshal(stored)
		for _, raw := range []string{token["access_token"].(string), token["refresh_token"].(string), f.token, secret} {
			if strings.Contains(string(data), raw) {
				t.Fatal("plaintext secret persisted or projected", name)
			}
		}
	}
	w := f.req("GET", "/user/authorizations", f.token, "", "", false)
	out := check(t, w, 200)
	if len(out["items"].([]any)[0].(map[string]any)) != 6 {
		t.Fatal("unexpected grant fields")
	}
	if w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("personal response cached")
	}
}
func TestConcurrentCodeAndRefreshClaims(t *testing.T) {
	f := newFixture(t)
	code := f.authorize("profile:read")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := f.m.Exchange(f.app, "chuniinfo", delegation.TokenInput{GrantType: "authorization_code", Code: code, RedirectURI: callback, Verifier: verifier})
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("%d simultaneous code successes", success)
	}
	token := f.exchange(f.authorize("profile:read"), verifier, 200)
	results = make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := f.m.Exchange(f.app, "chuniinfo", delegation.TokenInput{GrantType: "refresh_token", RefreshToken: token["refresh_token"].(string)})
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	success = 0
	for e := range results {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("%d simultaneous refresh successes", success)
	}
}
func TestSchemaGuardAndConfiguration(t *testing.T) {
	f := newFixture(t)
	if e := delegation.EnsureSchema(f.app); e != nil {
		t.Fatal(e)
	}
	c, _ := f.app.FindCollectionByNameOrId(delegation.Grants)
	open := ""
	c.ListRule = &open
	if e := f.app.Save(c); e != nil {
		t.Fatal(e)
	}
	if e := delegation.EnsureSchema(f.app); e == nil {
		t.Fatal("accepted open collection")
	}
	for _, raw := range []string{`{}`, `[] {}`, `[{"id":"bad","secret":"short"}]`, `[{"unknown":true}]`} {
		if _, e := delegation.ParseConfig(raw, "https://api.example", "https://musecat.example/authorize", user.CheckActiveUser); e == nil {
			t.Fatal("accepted bad configuration")
		}
	}
	m, e := delegation.ParseConfig("", "", "", user.CheckActiveUser)
	if e != nil || m != nil {
		t.Fatal("empty registry must disable OAuth")
	}
}

func TestExpiredRequestsCodesAndTokens(t *testing.T) {
	f := newFixture(t)
	handle := f.begin("chuniinfo", callback, "profile:read")
	f.read(handle, f.token, 200)
	requests, _ := f.app.FindAllRecords(delegation.Requests)
	requests[0].Set("expires", time.Now().Add(-time.Minute))
	if e := f.app.Save(requests[0]); e != nil {
		t.Fatal(e)
	}
	f.decision(handle, f.token, true, []string{"profile:read"}, 400)
	code := f.authorize("profile:read")
	codes, _ := f.app.FindAllRecords(delegation.Codes)
	codes[0].Set("expires", time.Now().Add(-time.Minute))
	if e := f.app.Save(codes[0]); e != nil {
		t.Fatal(e)
	}
	f.exchange(code, verifier, 400)
	token := f.exchange(f.authorize("profile:read"), verifier, 200)
	rows, _ := f.app.FindAllRecords(delegation.Tokens)
	rows[0].Set("access_expires", time.Now().Add(-time.Second))
	rows[0].Set("refresh_expires", time.Now().Add(-time.Second))
	if e := f.app.Save(rows[0]); e != nil {
		t.Fatal(e)
	}
	f.access(token["access_token"].(string), "/developer/v1/me", 401)
	f.refresh(token["refresh_token"].(string), 400)
	if e := delegation.CleanupExpired(f.app); e != nil {
		t.Fatal(e)
	}
	remaining, _ := f.app.FindAllRecords(delegation.Requests)
	if len(remaining) != 0 {
		t.Fatal("expired request not purged")
	}
}
func TestBanAndWithdrawalRevokeAllDelegatedData(t *testing.T) {
	t.Run("ban", func(t *testing.T) {
		f := newFixture(t)
		token := f.exchange(f.authorize("profile:read"), verifier, 200)
		c, _ := f.app.FindCollectionByNameOrId("user_ban")
		ban := core.NewRecord(c)
		ban.Id = f.user.Id
		ban.Set("reason", "restricted")
		ban.Set("until", time.Now().Add(time.Hour))
		if e := f.app.Save(ban); e != nil {
			t.Fatal(e)
		}
		f.access(token["access_token"].(string), "/developer/v1/me", 401)
		f.refresh(token["refresh_token"].(string), 400)
	})
	t.Run("withdrawal", func(t *testing.T) {
		f := newFixture(t)
		token := f.exchange(f.authorize("profile:read"), verifier, 200)
		h := f.begin("chuniinfo", callback, "profile:read")
		f.read(h, f.token, 200)
		check(t, f.req("POST", "/user/withdraw", f.token, `{"password":"secret123"}`, "application/json", false), 200)
		for _, name := range []string{delegation.Grants, delegation.Requests, delegation.Codes, delegation.Tokens} {
			rows, e := f.app.FindAllRecords(name)
			if e != nil || len(rows) != 0 {
				t.Fatal("withdrawal retained OAuth data", name, e)
			}
		}
		f.access(token["access_token"].(string), "/developer/v1/me", 401)
	})
}
func TestDifferentClientSubjectsAndRevocationIsolation(t *testing.T) {
	f := newFixture(t)
	token := f.exchange(f.authorize("profile:read"), verifier, 200)
	sub := f.access(token["access_token"].(string), "/developer/v1/me", 200)["sub"]
	h := f.begin("noslog", "https://noslog.example/callback", "profile:read")
	f.read(h, f.token, 200)
	uri, _ := url.Parse(f.decision(h, f.token, true, []string{"profile:read"}, 200))
	form := url.Values{"grant_type": {"authorization_code"}, "code": {uri.Query().Get("code")}, "redirect_uri": {"https://noslog.example/callback"}, "code_verifier": {verifier}}
	r := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(form.Encode()))
	r.SetBasicAuth("noslog", secret+"2")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	other := check(t, w, 200)
	if f.access(other["access_token"].(string), "/developer/v1/me", 200)["sub"] == sub {
		t.Fatal("subject shared across clients")
	}
	form = url.Values{"token": {other["refresh_token"].(string)}}
	check(t, f.req("POST", "/oauth/revoke", "", form.Encode(), "application/x-www-form-urlencoded", true), 200)
	f.access(other["access_token"].(string), "/developer/v1/me", 200)
}
func TestDisabledRegistryStillAllowsUserRevocation(t *testing.T) {
	f := newFixture(t)
	token := f.exchange(f.authorize("profile:read"), verifier, 200)
	id := firstGrant(t, f)
	r, _ := apis.NewRouter(f.app)
	dh.RegisterRoutes(&core.ServeEvent{App: f.app, Router: r}, nil)
	f.mux, _ = r.BuildMux()
	f.access(token["access_token"].(string), "/developer/v1/me", 503)
	check(t, f.req("GET", "/user/authorizations", f.token, "", "", false), 200)
	check(t, f.req("DELETE", "/user/authorizations/"+id, f.token, "", "", false), 204)
}
func TestPrivateArcadeVisibilityIsPreserved(t *testing.T) {
	f := newFixture(t)
	token := f.exchange(f.authorize("arcade:read"), verifier, 200)
	c, _ := f.app.FindCollectionByNameOrId("arcade")
	a := core.NewRecord(c)
	a.Set("country", "KR")
	a.Set("public", false)
	a.Set("createdBy", f.user.Id)
	if e := f.app.Save(a); e != nil {
		t.Fatal(e)
	}
	f.access(token["access_token"].(string), "/developer/v1/arcade?id="+a.Id, 404)
}
func TestRequestPrivacyAndStreamingBodyLimit(t *testing.T) {
	f := newFixture(t)
	r := httptest.NewRequest("GET", "/oauth/consent?request=secret-handle", nil)
	r.Header.Set("Referer", "https://musecat.example/consent?request=secret-handle")
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	if w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" || r.URL.RawQuery != "" || r.Referer() != "" {
		t.Fatal("unauthorized request was cacheable or leaked query to activity logs")
	}
	r = httptest.NewRequest("POST", "/oauth/token", strings.NewReader("grant_type="+strings.Repeat("a", 17000)))
	r.ContentLength = -1
	r.SetBasicAuth("chuniinfo", secret)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	check(t, w, 413)
}
func TestMetadataAndPublicDocs(t *testing.T) {
	f := newFixture(t)
	meta := check(t, f.req("GET", "/.well-known/oauth-authorization-server", "", "", "", false), 200)
	if meta["issuer"] != "https://api.musecat.example" || strings.Contains(fmt.Sprint(meta), secret) {
		t.Fatal("invalid discovery")
	}
	for _, path := range []string{"/developer", "/developer/guide.md", "/developer/openapi.yaml"} {
		w := f.req("GET", path, "", "", "", false)
		if w.Code != 200 || w.Body.Len() < 100 || strings.Contains(w.Body.String(), secret) {
			t.Fatal("public documentation invalid", path, w.Code)
		}
	}
}

func TestRegistryRestartAndConfigurationRestorationCannotResurrectTokens(t *testing.T) {
	f := newFixture(t)
	token := f.exchange(f.authorize("profile:read"), verifier, 200)
	at := token["access_token"].(string)
	if e := delegation.SynchronizeClients(f.app, f.m); e != nil {
		t.Fatal(e)
	}
	f.access(at, "/developer/v1/me", 200)
	// Disabling and re-enabling the identical registration must not restore its old epoch.
	if e := delegation.SynchronizeClients(f.app, nil); e != nil {
		t.Fatal(e)
	}
	f.access(at, "/developer/v1/me", 401)
	if e := delegation.SynchronizeClients(f.app, f.m); e != nil {
		t.Fatal(e)
	}
	f.access(at, "/developer/v1/me", 401)
	next := f.exchange(f.authorize("profile:read"), verifier, 200)
	f.access(next["access_token"].(string), "/developer/v1/me", 200)
	changed, e := delegation.NewManager("https://api.musecat.example", "https://musecat.example/oauth/authorize", []delegation.Client{{ID: "chuniinfo", Name: "Changed app", Secret: secret, RedirectURIs: []string{callback}, Scopes: allScopes}}, user.CheckActiveUser)
	if e != nil {
		t.Fatal(e)
	}
	if e = delegation.SynchronizeClients(f.app, changed); e != nil {
		t.Fatal(e)
	}
	f.access(next["access_token"].(string), "/developer/v1/me", 401)
	if e = delegation.SynchronizeClients(f.app, f.m); e != nil {
		t.Fatal(e)
	}
	f.access(next["access_token"].(string), "/developer/v1/me", 401)
}

func TestTokenEndpointRejectsAmbiguousOrWrongClientCredentials(t *testing.T) {
	f := newFixture(t)
	code := f.authorize("profile:read")
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {callback}, "code_verifier": {verifier}}
	for _, suffix := range []string{"&grant_type=refresh_token", "&scope=admin", "&client_id=chuniinfo", "&client_secret=secret"} {
		check(t, f.req("POST", "/oauth/token", "", form.Encode()+suffix, "application/x-www-form-urlencoded", true), 400)
	}
	r := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(form.Encode()))
	r.SetBasicAuth("chuniinfo", "incorrect-client-secret")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	check(t, w, 401)
	f.exchange(code, verifier, 200)
}

// Real net/http request bodies can return (n, io.EOF) together, unlike the
// strings.Reader used by direct recorder tests. PocketBase rewinds on that EOF.
func TestConsentDecisionOverRealHTTP(t *testing.T) {
	f := newFixture(t)
	server := httptest.NewServer(f.mux)
	defer server.Close()
	for _, tc := range []struct {
		name, suffix string
		status       int
	}{
		{"single JSON document", "", 200},
		{"trailing JSON is rejected", "{}", 400},
		{"body limit remains enforced", strings.Repeat(" ", 17000), 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handle := f.begin("chuniinfo", callback, "profile:read")
			f.read(handle, f.token, 200)
			body, err := json.Marshal(delegation.Decision{Request: handle, Approve: true, Scopes: []string{"profile:read"}})
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequest("POST", server.URL+"/oauth/consent", strings.NewReader(string(body)+tc.suffix))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+f.token)
			response, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.status {
				t.Fatalf("HTTP decision returned %d, want %d", response.StatusCode, tc.status)
			}
			if tc.status == 200 {
				var out map[string]string
				if err = json.NewDecoder(response.Body).Decode(&out); err != nil {
					t.Fatal(err)
				}
				target, err := url.Parse(out["redirect_uri"])
				if err != nil || target.Query().Get("code") == "" || target.Query().Get("state") != "our-client-state" {
					t.Fatal("missing validated callback")
				}
			}
		})
	}
}
