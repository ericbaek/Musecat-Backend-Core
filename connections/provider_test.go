package connections_test

import (
	"encoding/base64"
	"encoding/json"
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
