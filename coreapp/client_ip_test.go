package coreapp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/testutil"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

func TestClientIPForwardingRequiresServerAuthentication(t *testing.T) {
	secret := strings.Repeat("s", 32)
	for _, tc := range []struct {
		name, secret, proof, ip string
		want                    int
	}{
		{"authenticated IPv4", secret, secret, "203.0.113.2", 200},
		{"authenticated IPv6", secret, secret, "2001:db8::2", 200},
		{"disabled", "", secret, "203.0.113.2", 429},
		{"short configuration", "short", "short", "203.0.113.2", 429},
		{"forged secret", secret, "wrong", "203.0.113.2", 429},
		{"no secret", secret, "", "203.0.113.2", 429},
		{"IP list injection", secret, secret, "203.0.113.2, 203.0.113.3", 429},
		{"scoped IPv6", secret, secret, "fe80::1%eth0", 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := testutil.NewTestApp(t)
			app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: "GET /probe", MaxRequests: 1, Duration: 60}}}
			router, err := apis.NewRouter(app)
			if err != nil {
				t.Fatal(err)
			}
			router.Bind(clientIPForwarding(tc.secret))
			router.GET("/probe", func(e *core.RequestEvent) error {
				if e.Request.Header.Get("X-Musecat-Forward-Secret") != "" {
					t.Error("forwarding secret escaped middleware")
				}
				return e.String(http.StatusOK, e.RealIP())
			})
			mux, err := router.BuildMux()
			if err != nil {
				t.Fatal(err)
			}
			request := func(ip, proof string) int {
				req := httptest.NewRequest("GET", "/probe", nil)
				req.RemoteAddr = "192.0.2.1:1234"
				req.Header.Set("X-Musecat-Client-IP", ip)
				req.Header.Set("X-Musecat-Forward-Secret", proof)
				res := httptest.NewRecorder()
				mux.ServeHTTP(res, req)
				return res.Code
			}
			if got := request("", ""); got != 200 {
				t.Fatalf("initial request %d", got)
			}
			if got := request(tc.ip, tc.proof); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
			if got := request(tc.ip, tc.proof); got != 429 {
				t.Fatalf("same attributed IP must share its quota, got %d", got)
			}
		})
	}
}
