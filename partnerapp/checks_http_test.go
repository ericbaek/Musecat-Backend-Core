package partnerapp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

func TestConformanceBlocksPrivateAndSpecialAddresses(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "::1", "10.0.0.1", "172.16.1.2", "192.168.1.2", "169.254.169.254", "100.100.100.200", "0.0.0.0", "::ffff:127.0.0.1", "fd00::1", "2001:db8::1", "2002:7f00:1::1"} {
		if publicCheckIP(netip.MustParseAddr(raw)) {
			t.Fatal("unsafe address allowed", raw)
		}
	}
	for _, raw := range []string{"1.1.1.1", "2606:4700:4700::1111"} {
		if !publicCheckIP(netip.MustParseAddr(raw)) {
			t.Fatal("public address rejected", raw)
		}
	}
	runner := NewCheckRunner(nil)
	for _, target := range []string{"https://127.0.0.1/", "https://[::1]/", "http://example.com/", "https://example.com/?token=secret"} {
		if _, _, err := runner.call(context.Background(), "GET", target, "", nil, nil, "", nil); err == nil {
			t.Fatal("unsafe target accepted", target)
		}
	}
}

func TestConformanceDoesNotFollowRedirectsOrReportRemoteSecrets(t *testing.T) {
	client := &http.Client{Transport: redirectTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "partner.example" {
			t.Fatal("followed redirect with test credentials", r.URL.Host)
		}
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://other.example/collect"}}, Body: io.NopCloser(strings.NewReader("private-upstream-token"))}, nil
	})}
	runner := NewCheckRunner(client)
	form := Form{DirectionA: DirectionA{RedirectURIs: []string{"https://partner.example/callback"}}, DirectionB: &DirectionB{Issuer: "https://partner.example", AuthorizationURL: "https://partner.example/authorize", TokenURL: "https://partner.example/token", UserInfoURL: "https://partner.example/userinfo", RevocationURL: "https://partner.example/revoke", RecordsURL: "https://partner.example/records", Services: []DirectionBService{{ID: "chunithm", Scope: "chunithm:read"}}}}
	cases := runner.Run(context.Background(), form, "https://musecat.example/callback", Credentials{ClientID: "id", ClientSecret: "secret"}, "", CheckFixtures{AccessToken: "test-access", ExpectedSubject: "test-subject"})
	data, _ := json.Marshal(cases)
	if strings.Contains(string(data), "private-upstream-token") || strings.Contains(string(data), "test-access") {
		t.Fatal("remote body or credentials leaked into report")
	}
	for _, c := range cases {
		if c.ID == "oauth.userinfo" && c.Status != "fail" {
			t.Fatal("redirect was accepted as userinfo")
		}
	}
}

type redirectTransport func(*http.Request) (*http.Response, error)

func (f redirectTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
