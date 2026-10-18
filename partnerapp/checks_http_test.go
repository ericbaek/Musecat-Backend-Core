package partnerapp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestConformanceBlocksPrivateAndSpecialAddresses(t *testing.T) {
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
