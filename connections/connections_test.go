package connections_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/connections"
	"github.com/ericbaek/musecat-backend-core/coreapp"
	"github.com/ericbaek/musecat-backend-core/testutil"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type fixture struct {
	t              *testing.T
	app            *tests.TestApp
	manager        *connections.Manager
	token          string
	user           *core.Record
	providers      []connections.Provider
	mu             sync.Mutex
	exchanges      int
	challenge      string
	granted        *string
	hook           func()
	fail           bool
	subject        string
	identityIssuer string
	refreshes      int
	records        func(*http.Request) *http.Response
}

func provider(id string) connections.Provider {
	return connections.Provider{ID: id, Name: id, Issuer: "https://" + id + ".example", AuthorizationURL: "https://" + id + ".example/authorize", TokenURL: "https://" + id + ".example/token", UserInfoURL: "https://" + id + ".example/userinfo", SubjectField: "sub", ClientID: "musecat", ClientSecret: "client-secret", ClientAuthMethod: "client_secret_basic", RedirectURI: "https://musecat.example/callback/" + id, RequireIssuer: true, Scopes: []string{"profile"}, Services: []connections.Service{{ID: "chunithm", Name: "CHUNITHM", Scopes: []string{"chuni:read"}}, {ID: "sdvx", Name: "SOUND VOLTEX", Scopes: []string{"sdvx:read"}}}, RecordsURL: "https://" + id + ".example/records"}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, app: testutil.NewTestApp(t), providers: []connections.Provider{provider("rhythm"), provider("noslog")}, subject: "verified-subject"}
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/token" {
			f.mu.Lock()
			f.exchanges++
			challenge := f.challenge
			hook := f.hook
			fail := f.fail
			granted := f.granted
			f.mu.Unlock()
			if hook != nil {
				hook()
			}
			if fail {
				return nil, fmt.Errorf("secret-provider-error")
			}
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			id, secret, ok := r.BasicAuth()
			if !ok || id != "musecat" || secret != "client-secret" {
				t.Error("missing client authentication")
			}
			if r.Form.Get("grant_type") == "refresh_token" {
				f.mu.Lock()
				f.refreshes++
				f.mu.Unlock()
				if r.Form.Get("refresh_token") != "secret-refresh" {
					return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":"invalid_grant"}`))}, nil
				}
				return jsonResponse(map[string]any{"access_token": "secret-access-2", "refresh_token": "secret-refresh-2", "token_type": "Bearer", "expires_in": 3600}), nil
			}
			if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") == "" {
				t.Error("invalid code exchange")
			}
			verifier := r.Form.Get("code_verifier")
			sum := sha256.Sum256([]byte(verifier))
			if verifier == "" || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
				t.Error("PKCE verifier does not match the challenge")
			}
			body := map[string]any{"access_token": "secret-access", "refresh_token": "secret-refresh", "token_type": "Bearer", "expires_in": 3600}
			if granted != nil {
				body["scope"] = *granted
			}
			return jsonResponse(body), nil
		}
		if r.URL.Path == "/userinfo" {
			if r.Header.Get("Authorization") != "Bearer secret-access" {
				t.Error("identity lookup was not authenticated")
			}
			body := map[string]any{"sub": f.subject}
			if f.identityIssuer != "" {
				body["iss"] = f.identityIssuer
			}
			return jsonResponse(body), nil
		}
		f.mu.Lock()
		records := f.records
		f.mu.Unlock()
		if records != nil && strings.HasPrefix(r.URL.Path, "/records/") {
			if res := records(r); res != nil {
				return res, nil
			}
		}
		t.Errorf("unexpected external URL: %s", r.URL)
		return nil, fmt.Errorf("unexpected request")
	})}
	manager, err := connections.NewManager(f.providers, []byte(strings.Repeat("k", 32)), client)
	if err != nil {
		t.Fatal(err)
	}
	f.manager = manager
	f.app.OnServe().BindFunc(func(se *core.ServeEvent) error { coreapp.RegisterAPIRoutes(se, f.manager); return se.Next() })
	f.token, f.user = f.newUser()
	return f
}

func jsonResponse(body any) *http.Response {
	data, _ := json.Marshal(body)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(data)))}
}
func (f *fixture) newUser() (string, *core.Record) {
	c, err := f.app.FindCollectionByNameOrId("user")
	if err != nil {
		f.t.Fatal(err)
	}
	r := core.NewRecord(c)
	r.SetEmail(fmt.Sprintf("connections-%d@example.com", time.Now().UnixNano()))
	r.SetPassword("secret123")
	if err := f.app.Save(r); err != nil {
		f.t.Fatal(err)
	}
	token, err := r.NewAuthToken()
	if err != nil {
		f.t.Fatal(err)
	}
	return token, r
}
func (f *fixture) request(token, method, path string, body any, status int) map[string]any {
	f.t.Helper()
	data, _ := json.Marshal(body)
	router, err := apis.NewRouter(f.app)
	if err != nil {
		f.t.Fatal(err)
	}
	event := &core.ServeEvent{App: f.app, Router: router}
	if err := f.app.OnServe().Trigger(event, func(e *core.ServeEvent) error { return e.Next() }); err != nil {
		f.t.Fatal(err)
	}
	mux, err := event.Router.BuildMux()
	if err != nil {
		f.t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, strings.NewReader(string(data)))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != status {
		f.t.Fatalf("%s %s got %d want %d: %s", method, path, response.Code, status, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "secret-access") || strings.Contains(response.Body.String(), "secret-refresh") || strings.Contains(response.Body.String(), "client-secret") || strings.Contains(response.Body.String(), "verified-subject") {
		f.t.Fatal("private provider data leaked")
	}
	result := map[string]any{}
	if response.Body.Len() > 0 {
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			f.t.Fatal(err)
		}
	}
	return result
}
func (f *fixture) start(token, providerID string, services ...string) connections.CompleteInput {
	f.t.Helper()
	body := f.request(token, "POST", "/user/connections/start", map[string]any{"provider": providerID, "services": services}, 200)
	authURL, err := url.Parse(body["authorization_url"].(string))
	if err != nil {
		f.t.Fatal(err)
	}
	q := authURL.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" {
		f.t.Fatal("missing authorization-code PKCE")
	}
	f.challenge = q.Get("code_challenge")
	return connections.CompleteInput{Provider: providerID, State: q.Get("state"), Code: "authorization-code", Issuer: "https://" + providerID + ".example"}
}
func (f *fixture) complete(input connections.CompleteInput) string {
	body := f.request(f.token, "POST", "/user/connections/complete", input, 200)
	return body["id"].(string)
}
func (f *fixture) count(collection string) int64 {
	n, err := f.app.CountRecords(collection)
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

func TestConnectionsLifecycleAndMultipleServices(t *testing.T) {
	f := newFixture(t)
	providers := f.request("", "GET", "/integrations/providers", nil, 200)
	if len(providers["items"].([]any)) != 2 {
		t.Fatal("provider registry missing")
	}
	start := f.start(f.token, "rhythm", "chunithm", "sdvx")
	pending, err := f.app.FindFirstRecordByFilter(connections.Requests, "user = {:u}", dbx.Params{"u": f.user.Id})
	if err != nil {
		t.Fatal(err)
	}
	if pending.GetString("state_hash") == start.State || pending.GetString("session_hash") == f.token || strings.Contains(pending.GetString("verifier"), f.challenge) {
		t.Fatal("request secrets were stored as plaintext")
	}
	id := f.complete(start)
	rows := f.request(f.token, "GET", "/user/connections", nil, 200)["items"].([]any)
	if len(rows) != 1 {
		t.Fatal("missing account")
	}
	services := rows[0].(map[string]any)["services"].([]any)
	if len(services) != 2 {
		t.Fatal("one identity must support multiple game services")
	}
	for _, s := range services {
		if s.(map[string]any)["visibility"] != "private" || len(s.(map[string]any)["fields"].([]any)) != 0 {
			t.Fatal("game data was automatically published")
		}
	}
	account, _ := f.app.FindRecordById(connections.Accounts, id)
	if account.GetString("subject") != "verified-subject" || account.GetString("credentials") == "" || strings.Contains(account.GetString("credentials"), "secret-access") {
		t.Fatal("credentials were not encrypted")
	}
	f.request(f.token, "POST", "/user/connections/complete", start, 400)
	if f.exchanges != 1 || f.count(connections.Requests) != 0 {
		t.Fatal("code replay was exchanged")
	}
	// Reauthorizing the same identity updates a single account and replaces game consent.
	again := f.start(f.token, "rhythm", "chunithm")
	if f.complete(again) != id || f.count(connections.Accounts) != 1 || f.count(connections.Services) != 1 {
		t.Fatal("reauthorization duplicated identities or retained old consent")
	}
	// A separate provider may use the same subject without becoming the same identity.
	f.complete(f.start(f.token, "noslog", "chunithm"))
	if f.count(connections.Accounts) != 2 {
		t.Fatal("independent issuers were merged")
	}
	f.request(f.token, "DELETE", "/user/connections/"+id, nil, 204)
	if f.count(connections.Accounts) != 1 || f.count(connections.Services) != 1 {
		t.Fatal("disconnect did not cascade game grants")
	}
}

func TestConnectionOwnershipAndSessionBinding(t *testing.T) {
	f := newFixture(t)
	otherToken, _ := f.newUser()
	f.request("", "POST", "/user/connections/start", map[string]any{"provider": "rhythm", "services": []string{"chunithm"}}, 401)
	f.request(f.token, "POST", "/user/connections/start", map[string]any{"provider": "rhythm", "services": []string{"unknown"}}, 400)
	f.request(f.token, "POST", "/user/connections/start", map[string]any{"provider": "unknown", "services": []string{"chunithm"}}, 503)
	f.request(f.token, "POST", "/user/connections/start", map[string]any{"provider": "rhythm", "services": []string{"chunithm"}, "subject": "victim"}, 400)
	input := f.start(f.token, "rhythm", "chunithm")
	f.request(otherToken, "POST", "/user/connections/complete", input, 400)
	alternateSession, err := f.user.NewStaticAuthToken(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f.request(alternateSession, "POST", "/user/connections/complete", input, 400)
	bad := input
	bad.Provider = "noslog"
	bad.Issuer = "https://noslog.example"
	f.request(f.token, "POST", "/user/connections/complete", bad, 400)
	bad = input
	bad.Issuer = "https://evil.example"
	f.request(f.token, "POST", "/user/connections/complete", bad, 400)
	if f.exchanges != 0 {
		t.Fatal("untrusted requests reached the provider")
	}
	id := f.complete(input)
	f.request(otherToken, "DELETE", "/user/connections/"+id, nil, 404)
	if len(f.request(otherToken, "GET", "/user/connections", nil, 200)["items"].([]any)) != 0 {
		t.Fatal("connections leaked across users")
	}
	f.request(otherToken, "POST", "/user/connections/complete", f.start(otherToken, "rhythm", "chunithm"), 409)
	f.subject = "different-subject"
	f.request(f.token, "POST", "/user/connections/complete", f.start(f.token, "rhythm", "chunithm"), 409)
	if f.count(connections.Accounts) != 1 {
		t.Fatal("conflict changed an existing connection")
	}
}

func TestConnectionExpiryDenialAndProviderFailure(t *testing.T) {
	f := newFixture(t)
	input := f.start(f.token, "rhythm", "chunithm")
	request, _ := f.app.FindFirstRecordByFilter(connections.Requests, "user = {:u}", dbx.Params{"u": f.user.Id})
	request.Set("expires", time.Now().Add(-time.Minute))
	if err := f.app.Save(request); err != nil {
		t.Fatal(err)
	}
	f.request(f.token, "POST", "/user/connections/complete", input, 400)
	input = f.start(f.token, "rhythm", "chunithm")
	input.Code = ""
	input.Error = "access_denied"
	f.request(f.token, "POST", "/user/connections/complete", input, 400)
	if f.count(connections.Requests) != 0 || f.exchanges != 0 {
		t.Fatal("denial or expiry contacted provider")
	}
	input = f.start(f.token, "rhythm", "chunithm")
	f.fail = true
	f.request(f.token, "POST", "/user/connections/complete", input, 502)
	f.request(f.token, "POST", "/user/connections/complete", input, 400)
	if f.count(connections.Accounts) != 0 || f.count(connections.Requests) != 0 || f.exchanges != 1 {
		t.Fatal("provider failure persisted credentials or allowed replay")
	}
}

func TestOnlyGrantedServiceScopesArePersisted(t *testing.T) {
	f := newFixture(t)
	scopes := "profile chuni:read extra:unrequested"
	f.granted = &scopes
	body := f.request(f.token, "POST", "/user/connections/complete", f.start(f.token, "rhythm", "chunithm", "sdvx"), 200)
	actual, _ := json.Marshal(body["scopes"])
	if strings.Contains(string(actual), "sdvx") || strings.Contains(string(actual), "unrequested") || len(body["services"].([]any)) != 1 {
		t.Fatal("unapproved scopes or services were persisted")
	}
}

func TestDisconnectAndNewStartFenceInflightCallbacks(t *testing.T) {
	f := newFixture(t)
	id := f.complete(f.start(f.token, "rhythm", "chunithm"))
	input := f.start(f.token, "rhythm", "chunithm")
	f.hook = func() { f.request(f.token, "DELETE", "/user/connections/"+id, nil, 204) }
	f.request(f.token, "POST", "/user/connections/complete", input, 400)
	if f.count(connections.Accounts) != 0 || f.count(connections.Requests) != 0 {
		t.Fatal("inflight callback resurrected a disconnected account")
	}
	f.hook = nil
	input = f.start(f.token, "rhythm", "chunithm")
	var newer connections.CompleteInput
	f.hook = func() { newer = f.start(f.token, "rhythm", "sdvx") }
	f.request(f.token, "POST", "/user/connections/complete", input, 400)
	if f.count(connections.Requests) != 1 {
		t.Fatal("old callback deleted the newer attempt")
	}
	f.hook = nil
	f.complete(newer)
}

func TestWithdrawalErasesConnectionsAndInflightRequests(t *testing.T) {
	f := newFixture(t)
	f.complete(f.start(f.token, "rhythm", "chunithm"))
	input := f.start(f.token, "rhythm", "sdvx")
	f.hook = func() { f.request(f.token, "POST", "/user/withdraw", map[string]any{"password": "secret123"}, 200) }
	f.request(f.token, "POST", "/user/connections/complete", input, 401)
	for _, name := range []string{connections.Accounts, connections.Services, connections.Requests} {
		if f.count(name) != 0 {
			t.Fatalf("withdrawal retained %s", name)
		}
	}
}

func TestDisabledProviderStillAllowsDisconnect(t *testing.T) {
	f := newFixture(t)
	id := f.complete(f.start(f.token, "rhythm", "chunithm"))
	f.manager = nil
	f.request(f.token, "POST", "/user/connections/start", map[string]any{"provider": "rhythm", "services": []string{"chunithm"}}, 503)
	f.request(f.token, "DELETE", "/user/connections/"+id, nil, 204)
	if f.count(connections.Accounts) != 0 {
		t.Fatal("disabled provider blocked local disconnect")
	}
}

func TestRawConnectionCollectionsAreLocked(t *testing.T) {
	f := newFixture(t)
	id := f.complete(f.start(f.token, "rhythm", "chunithm"))
	for _, name := range []string{connections.Accounts, connections.Services, connections.Requests} {
		collection, err := f.app.FindCollectionByNameOrId(name)
		if err != nil {
			t.Fatal(err)
		}
		if collection.ListRule != nil || collection.ViewRule != nil || collection.CreateRule != nil || collection.UpdateRule != nil || collection.DeleteRule != nil {
			t.Fatal("raw collection rules are unlocked")
		}
		f.request(f.token, "GET", "/api/collections/"+name+"/records", nil, 403)
		f.request(f.token, "POST", "/api/collections/"+name+"/records", map[string]any{"user": f.user.Id}, 403)
	}
	f.request(f.token, "GET", "/api/collections/"+connections.Accounts+"/records/"+id, nil, 403)
}

func TestAccountRestrictionIsRecheckedAfterProviderIO(t *testing.T) {
	f := newFixture(t)
	input := f.start(f.token, "rhythm", "chunithm")
	f.hook = func() {
		collection, err := f.app.FindCollectionByNameOrId("user_ban")
		if err != nil {
			t.Fatal(err)
		}
		ban := core.NewRecord(collection)
		ban.Id = f.user.Id
		ban.Set("reason", "restricted")
		ban.Set("until", time.Now().Add(time.Hour))
		if err := f.app.Save(ban); err != nil {
			t.Fatal(err)
		}
	}
	f.request(f.token, "POST", "/user/connections/complete", input, 403)
	if f.count(connections.Accounts) != 0 {
		t.Fatal("restricted account was connected")
	}
	f.request(f.token, "POST", "/user/connections/start", map[string]any{"provider": "rhythm", "services": []string{"chunithm"}}, 403)
}

func TestExpiredRequestCleanupPreservesLiveAttempts(t *testing.T) {
	f := newFixture(t)
	input := f.start(f.token, "rhythm", "chunithm")
	if err := connections.PurgeExpiredRequests(f.app, time.Now()); err != nil {
		t.Fatal(err)
	}
	if f.count(connections.Requests) != 1 {
		t.Fatal("cleanup removed live requests")
	}
	if err := connections.PurgeExpiredRequests(f.app, time.Now().Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	f.request(f.token, "POST", "/user/connections/complete", input, 400)
	if f.count(connections.Requests) != 0 || f.exchanges != 0 {
		t.Fatal("cleanup retained an expired verifier")
	}
}
