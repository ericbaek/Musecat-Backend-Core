package connections_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/connections"
)

func TestProviderConfigurationFailsClosed(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	for _, test := range []struct {
		name   string
		change func(*connections.Provider)
	}{
		{"HTTP endpoint", func(p *connections.Provider) { p.TokenURL = "http://rhythm.example/token" }},
		{"query endpoint", func(p *connections.Provider) { p.UserInfoURL += "?token=secret" }},
		{"missing secret", func(p *connections.Provider) { p.ClientSecret = "" }},
		{"implicit client auth", func(p *connections.Provider) { p.ClientAuthMethod = "" }},
		{"invalid ID", func(p *connections.Provider) { p.ID = "../provider" }},
		{"scope injection", func(p *connections.Provider) { p.Scopes = []string{"profile extra"} }},
		{"duplicate service", func(p *connections.Provider) { p.Services = append(p.Services, p.Services[0]) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := provider("rhythm")
			test.change(&p)
			if _, err := connections.NewManager([]connections.Provider{p}, key, nil); err == nil {
				t.Fatal("accepted invalid provider")
			}
		})
	}
	a, b := provider("rhythm"), provider("noslog")
	b.Issuer = a.Issuer
	if _, err := connections.NewManager([]connections.Provider{a, b}, key, nil); err == nil {
		t.Fatal("duplicate issuer accepted")
	}
	b = provider("noslog")
	b.RedirectURI = a.RedirectURI
	if _, err := connections.NewManager([]connections.Provider{a, b}, key, nil); err == nil {
		t.Fatal("shared callback accepted")
	}
	if _, err := connections.NewManager([]connections.Provider{a}, []byte("short"), nil); err == nil {
		t.Fatal("weak encryption key accepted")
	}
	if manager, err := connections.ParseConfig("", ""); manager != nil || err != nil {
		t.Fatal("empty configuration must disable linking")
	}
	// A key alone keeps dynamic (Developer Center) activation possible with no static providers.
	keyOnly, err := connections.ParseConfig("", base64.StdEncoding.EncodeToString(key))
	if err != nil || keyOnly == nil || len(keyOnly.Providers()) != 0 {
		t.Fatal("key-only configuration must construct an empty manager", err)
	}
	if err := keyOnly.Reload([]connections.Provider{a}); err != nil || len(keyOnly.Providers()) != 1 {
		t.Fatal("dynamic provider reload", err)
	}
	dup := a
	dup.ID = "other"
	if err := keyOnly.Check([]connections.Provider{a, dup}); err == nil {
		t.Fatal("duplicate issuer across dynamic providers accepted")
	}
	if err := keyOnly.Reload(nil); err != nil || len(keyOnly.Providers()) != 0 {
		t.Fatal("dynamic providers must be removable", err)
	}
	var sealed struct{ Secret string }
	ciphertext, err := keyOnly.Seal(map[string]string{"Secret": "s3"}, "purpose:a")
	if err != nil || strings.Contains(ciphertext, "s3") {
		t.Fatal("seal", err)
	}
	if err := keyOnly.Open(ciphertext, "purpose:b", &sealed); err == nil {
		t.Fatal("purpose binding ignored")
	}
	if err := keyOnly.Open(ciphertext, "purpose:a", &sealed); err != nil || sealed.Secret != "s3" {
		t.Fatal("open", err)
	}
	if _, err := connections.ParseConfig(`[{"client_secret":"secret-value","typo":true}]`, base64.StdEncoding.EncodeToString(key)); err == nil || strings.Contains(err.Error(), "secret-value") {
		t.Fatal("invalid config accepted or secrets leaked")
	}
}

func TestProviderRegistryDoesNotExposeSecretsOrKeepMutableConfig(t *testing.T) {
	p := provider("rhythm")
	manager, err := connections.NewManager([]connections.Provider{p}, []byte(strings.Repeat("k", 32)), nil)
	if err != nil {
		t.Fatal(err)
	}
	p.Services[0].Name = "mutated"
	p.Scopes[0] = "mutated"
	data, err := json.Marshal(manager.Providers())
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, secret := range []string{"client-secret", "client_id", "token_url", "mutated"} {
		if strings.Contains(text, secret) {
			t.Fatalf("registry contains %s", secret)
		}
	}
}

func TestIdentityIssuerMismatchDoesNotCreateConnection(t *testing.T) {
	f := newFixture(t)
	f.identityIssuer = "https://other.example"
	f.request(f.token, "POST", "/user/connections/complete", f.start(f.token, "rhythm", "chunithm"), 502)
	if f.count(connections.Accounts) != 0 {
		t.Fatal("mismatched issuer was connected")
	}
}

func TestClaimBlocksASecondCompletionDuringExchange(t *testing.T) {
	f := newFixture(t)
	input := f.start(f.token, "rhythm", "chunithm")
	f.hook = func() { f.request(f.token, "POST", "/user/connections/complete", input, 400) }
	f.complete(input)
	if f.exchanges != 1 {
		t.Fatal("concurrent completion exchanged the same code twice")
	}
}

func TestDevelopmentProviderAudienceDiscoveryAndExistingRecords(t *testing.T) {
	f := newFixture(t)
	p := provider("development")
	p.Development = true
	p.AllowedUserIDs = []string{f.user.Id}
	if err := f.manager.Reload([]connections.Provider{p}); err != nil {
		t.Fatal(err)
	}
	otherToken, _ := f.newUser()
	for _, token := range []string{"", otherToken} {
		items := f.request(token, "GET", "/integrations/providers", nil, 200)["items"].([]any)
		if len(items) != 2 {
			t.Fatal("development provider leaked", items)
		}
	}
	if items := f.request(f.token, "GET", "/integrations/providers", nil, 200)["items"].([]any); len(items) != 3 {
		t.Fatal("tester cannot discover development provider", items)
	}
	f.request(otherToken, "POST", "/user/connections/start", map[string]any{"provider": p.ID, "services": []string{"chunithm"}}, 503)
	id := f.complete(f.start(f.token, p.ID, "chunithm"))
	f.records = func(*http.Request) *http.Response { return status(200, envelope("chunithm", f.subject, "public")) }
	f.request(f.token, "GET", "/user/connections/"+id+"/records/chunithm", nil, 200)
	f.request(f.token, "PUT", "/user/connections/"+id+"/services/chunithm", map[string]string{"visibility": "public"}, 200)
	if items := f.request("", "GET", "/user/records?user="+f.user.Id, nil, 200)["items"].([]any); len(items) != 0 {
		t.Fatal("development test data became public", items)
	}
	pending := f.start(f.token, p.ID, "chunithm")
	p.AllowedUserIDs = nil
	if err := f.manager.Reload([]connections.Provider{p}); err != nil {
		t.Fatal(err)
	}
	f.request(f.token, "GET", "/user/connections/"+id+"/records/chunithm", nil, 404)
	f.request(f.token, "POST", "/user/connections/complete", pending, 503)
	// Eligibility removal also applies to a fresh callback whose exchange is already running.
	p.AllowedUserIDs = []string{f.user.Id}
	if err := f.manager.Reload([]connections.Provider{p}); err != nil {
		t.Fatal(err)
	}
	pending = f.start(f.token, p.ID, "chunithm")
	f.hook = func() {
		p.AllowedUserIDs = nil
		if err := f.manager.Reload([]connections.Provider{p}); err != nil {
			t.Fatal(err)
		}
	}
	f.request(f.token, "POST", "/user/connections/complete", pending, 503)
	p.AllowedUserIDs = []string{f.user.Id}
	if err := f.manager.Reload([]connections.Provider{p}); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.InvalidateRecords(f.app, p.ID, f.subject, "chunithm"); err != nil {
		t.Fatal(err)
	}
	f.records = func(*http.Request) *http.Response {
		p.AllowedUserIDs = nil
		if err := f.manager.Reload([]connections.Provider{p}); err != nil {
			t.Fatal(err)
		}
		return status(200, envelope("chunithm", f.subject, "public"))
	}
	f.request(f.token, "GET", "/user/connections/"+id+"/records/chunithm", nil, 404)
}
