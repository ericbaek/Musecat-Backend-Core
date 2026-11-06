package usernotification_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	n "github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/ericbaek/musecat-backend-core/testutil"
)

type aimeTransport func(*http.Request) (*http.Response, error)

func (f aimeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// The positive fixture represents the required login structure, not a claim
// that a live login is currently available. No credentials are submitted.
const aimeLoginFixture = `<html><h2>Login with SEGA ID</h2><form method="post"><input name="sid"><input type="password" name="password"><button>Login</button></form></html>`

func TestAimeProbeRequiresBothRecognisedGameLogins(t *testing.T) {
	for _, tc := range []struct {
		name, target, body, want, contentType string
		code                                  int
		transportError                        bool
	}{
		{name: "both logins despite Aime geo block", code: 200, body: aimeLoginFixture, want: "available"},
		{name: "200 maintenance redirect", target: "/common_auth/maintenance.html", code: 200, body: `<title>メンテナンス</title>`, want: "blocked"},
		{name: "200 maintenance body at login URL", code: 200, body: aimeLoginFixture + `<p>Under maintenance</p>`, want: "blocked"},
		{name: "Japanese maintenance body", code: 200, body: `<p>現在メンテナンスを行っております。</p>`, want: "blocked"},
		{name: "503 maintenance body", code: 503, body: `<h2>Under maintenance</h2>`, want: "blocked"},
		{name: "unknown 200 page", code: 200, body: `<h1>Welcome</h1>`, want: "unknown"},
		{name: "password outside form", code: 200, body: `<h2>SEGA ID</h2><input type="password">`, want: "unknown"},
		{name: "game geo block", code: 403, body: `<title>403 Forbidden</title>`, want: "unknown"},
		{name: "login error response", code: 500, body: aimeLoginFixture, want: "unknown"},
		{name: "timeout", transportError: true, want: "unknown"},
		{name: "oversize body", code: 200, body: aimeLoginFixture + strings.Repeat("x", 512*1024), want: "unknown"},
		{name: "wrong content type", code: 200, body: aimeLoginFixture, contentType: "application/json", want: "unknown"},
		{name: "unexpected redirect host", target: "https://example.com/common_auth/login", code: 200, body: aimeLoginFixture, want: "unknown"},
		{name: "http downgrade", target: "http://lng-tgk-aime-gw.am-all.net/common_auth/login", code: 200, body: aimeLoginFixture, want: "unknown"},
		{name: "redirect loop", target: "/common_auth/loop", code: 200, body: aimeLoginFixture, want: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: aimeTransport(func(req *http.Request) (*http.Response, error) {
				code, body := 200, aimeLoginFixture
				headers := http.Header{"Content-Type": {"text/html; charset=utf-8"}}
				host := req.URL.Hostname()
				if host == "example.com" || req.URL.Scheme != "https" {
					t.Error("followed disallowed redirect")
				}
				if host == "my-aime.net" {
					code, body = 403, "Forbidden"
				} else if host != "lng-tgk-aime-gw.am-all.net" {
					q := url.Values{"redirect_url": {req.URL.String()}, "site_id": {"game"}}
					code, body = 302, ""
					headers.Set("Location", "https://lng-tgk-aime-gw.am-all.net/common_auth/login?"+q.Encode())
				} else if strings.Contains(req.URL.Query().Get("redirect_url"), "chunithm") {
					// Only one game is unhealthy; a healthy peer must not mask it.
					if tc.transportError {
						return nil, errors.New("timeout")
					}
					if tc.target != "" && req.URL.Path == "/common_auth/login" {
						code, body = 302, ""
						headers.Set("Location", tc.target+"?"+req.URL.RawQuery)
					} else if req.URL.Path == "/common_auth/loop" {
						code, body = 302, ""
						headers.Set("Location", req.URL.String())
					} else {
						code, body = tc.code, tc.body
						if tc.contentType != "" {
							headers.Set("Content-Type", tc.contentType)
						}
					}
				}
				return &http.Response{StatusCode: code, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
			})}
			got, _ := n.ProbeAime(context.Background(), client)
			if got.Status != tc.want || len(got.Sources) != 3 {
				t.Fatalf("got %+v, want %s", got, tc.want)
			}
			if tc.want == "blocked" && tc.code == 200 && got.HTTPStatus != 200 {
				t.Fatal("maintenance HTTP status was fabricated", got)
			}
		})
	}
}

func TestAimeClassified200MaintenanceAndUnknownResetConfirmations(t *testing.T) {
	app := testutil.NewTestApp(t)
	now := time.Now().UTC()
	for i, status := range []string{"blocked", "available", "available", "blocked", "available", "unknown", "available", "available", "available"} {
		if err := n.RecordAimeProbe(app, n.AimeObservation{Status: status, HTTPStatus: 200}, now.Add(time.Duration(i)*5*time.Minute)); err != nil {
			t.Fatal(err)
		}
		got, err := n.GetAimeStatus(app, "")
		if err != nil {
			t.Fatal(err)
		}
		if got.Completed != (i == 8) {
			t.Fatalf("observation %d: %+v", i, got)
		}
	}
}

func TestAimeLegacyHTTPConfirmationsCannotCompleteNewProbe(t *testing.T) {
	app := testutil.NewTestApp(t)
	now := time.Now().UTC()
	if err := n.RecordAimeProbe(app, n.AimeObservation{Status: "blocked", HTTPStatus: 200}, now); err != nil {
		t.Fatal(err)
	}
	rows, err := app.FindRecordsByFilter(n.AimeState, "", "", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	row := rows[0]
	row.Set("status", "available") // legacy HTTP-only state
	row.Set("confirmations", 2)
	row.Set("confirmation_at", now)
	if err := app.Save(row); err != nil {
		t.Fatal(err)
	}
	probes, err := app.FindRecordsByFilter(n.AimeProbes, "", "", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	probes[0].Set("status", "available")
	if err := app.Save(probes[0]); err != nil {
		t.Fatal(err)
	}
	before, err := n.GetAimeStatus(app, "")
	if err != nil || before.Status != "unknown" || before.Confirmations != 0 || before.History[0].Status != "unknown" {
		t.Fatalf("legacy projection: %+v, %v", before, err)
	}
	for i := 0; i < 4; i++ {
		if err := n.RecordAimeProbe(app, n.AimeObservation{Status: "available", HTTPStatus: 200}, now.Add(time.Duration(i+1)*5*time.Minute)); err != nil {
			t.Fatal(err)
		}
		got, err := n.GetAimeStatus(app, "")
		if err != nil || got.Completed != (i == 3) || got.Confirmations != i {
			t.Fatalf("new observation %d: %+v, %v", i, got, err)
		}
	}
}
