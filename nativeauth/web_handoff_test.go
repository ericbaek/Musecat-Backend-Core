package nativeauth_test

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/pocketbase/pocketbase/core"
)

func webHandoff(t *testing.T) (core.App, *core.Record, nativeauth.Service, nativeauth.Credentials) {
	t.Helper()
	app, account, service := setup(t)
	service.WebClients = []nativeauth.WebClient{{ID: "ios", Issuer: "https://musecat.app", Secret: strings.Repeat("s", 43)}}
	return app, account, service, issue(t, service, app, account)
}
func TestWebHandoffBindingSingleUseAndShortNonrefreshableWebToken(t *testing.T) {
	app, account, service, native := webHandoff(t)
	verifier := strings.Repeat("v", 43)
	grant, err := service.IssueWebCode(app, native.AccessToken, "ios", "https://musecat.app", challenge(verifier))
	if err != nil || grant.UserID != account.Id || grant.ExpiresIn != 60 || len(grant.Code) != 43 {
		t.Fatalf("invalid grant: %v", err)
	}
	for _, bad := range []struct{ verifier, client, issuer, secret string }{
		{strings.Repeat("x", 43), "ios", "https://musecat.app", strings.Repeat("s", 43)},
		{verifier, "other", "https://musecat.app", strings.Repeat("s", 43)},
		{verifier, "ios", "https://evil.example", strings.Repeat("s", 43)},
		{verifier, "ios", "https://musecat.app", strings.Repeat("x", 43)},
	} {
		if out, err := service.ExchangeWebCode(app, grant.Code, bad.verifier, bad.client, bad.issuer, bad.secret); err == nil || out.Token != "" {
			t.Fatal("invalid binding yielded credentials")
		}
	}
	var rows []struct {
		Hash string `db:"hash"`
	}
	if err := app.DB().NewQuery("SELECT hash FROM native_auth_web_code").All(&rows); err != nil || len(rows) != 1 || rows[0].Hash == grant.Code || len(rows[0].Hash) != 43 {
		t.Fatal("ticket not stored as hash only")
	}
	var wins atomic.Int32
	var result nativeauth.WebCredentials
	var lock sync.Mutex
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			out, err := service.ExchangeWebCode(app, grant.Code, verifier, "ios", "https://musecat.app", strings.Repeat("s", 43))
			if err == nil {
				wins.Add(1)
				lock.Lock()
				result = out
				lock.Unlock()
			}
		}()
	}
	workers.Wait()
	if wins.Load() != 1 || result.UserID != account.Id || result.ExpiresIn != 900 {
		t.Fatal("ticket must mint exactly one owner-scoped web credential")
	}
	owner, err := app.FindAuthRecordByToken(result.Token, core.TokenTypeAuth)
	if err != nil || owner.Id != account.Id {
		t.Fatal("minted web token not accepted as owner auth")
	}
	if _, err := service.Authenticate(app, result.Token); err == nil {
		t.Fatal("web JWT accepted by native API")
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.Split(result.Token, ".")[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Refreshable bool  `json:"refreshable"`
		Exp         int64 `json:"exp"`
		Iat         int64 `json:"iat"`
	}
	if err := json.Unmarshal(data, &claims); err != nil || claims.Refreshable || claims.Exp < time.Now().Unix()+898 || claims.Exp > time.Now().Unix()+900 {
		t.Fatal("web token must expire in 900 seconds and disallow refresh")
	}
	if _, err := service.ExchangeWebCode(app, grant.Code, verifier, "ios", "https://musecat.app", strings.Repeat("s", 43)); err == nil {
		t.Fatal("replay accepted")
	}
}

func TestWebHandoffRejectsChangedSessionAndAccount(t *testing.T) {
	for _, scenario := range []string{"expired", "revoked", "removed-client", "account-key", "withdrawn"} {
		t.Run(scenario, func(t *testing.T) {
			app, account, service, native := webHandoff(t)
			now := time.Now()
			service.Now = func() time.Time { return now }
			verifier := strings.Repeat("v", 43)
			grant, err := service.IssueWebCode(app, native.AccessToken, "ios", "https://musecat.app", challenge(verifier))
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "expired":
				now = now.Add(60 * time.Second)
			case "revoked":
				if err := service.Revoke(app, native.RefreshToken); err != nil {
					t.Fatal(err)
				}
			case "removed-client":
				service.Clients = nil
			case "account-key":
				account.RefreshTokenKey()
				if err := app.Save(account); err != nil {
					t.Fatal(err)
				}
			case "withdrawn":
				if err := app.Delete(account); err != nil {
					t.Fatal(err)
				}
			}
			if out, err := service.ExchangeWebCode(app, grant.Code, verifier, "ios", "https://musecat.app", strings.Repeat("s", 43)); err == nil || out.Token != "" {
				t.Fatal("stale authorization minted credentials")
			}
		})
	}
}
func TestWebHandoffRequiresMatchingNativeFamilyAndEnabledIssuer(t *testing.T) {
	app, account, service, native := webHandoff(t)
	jwt, err := account.NewAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []struct{ access, client, issuer string }{{jwt, "ios", "https://musecat.app"}, {native.AccessToken, "ios", "https://evil.example"}, {native.AccessToken, "other", "https://musecat.app"}} {
		if _, err := service.IssueWebCode(app, in.access, in.client, in.issuer, challenge(strings.Repeat("v", 43))); err == nil {
			t.Fatal("unbound ticket issued")
		}
	}
	service.Clients = append(service.Clients, nativeauth.Client{ID: "other", Callback: "https://musecat.app/other"})
	service.WebClients = append(service.WebClients, nativeauth.WebClient{ID: "other", Issuer: "https://musecat.app", Secret: strings.Repeat("s", 43)})
	if _, err := service.IssueWebCode(app, native.AccessToken, "other", "https://musecat.app", challenge(strings.Repeat("v", 43))); err == nil {
		t.Fatal("native family issued another client's ticket")
	}
}
