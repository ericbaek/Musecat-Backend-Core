package coreapp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"

	"github.com/ericbaek/musecat-backend-core/testutil"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testutil.CleanupGoldenDir()
	os.Exit(code)
}

func TestConfigureDefaultRateLimitsPreservesExistingRules(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			app := testutil.NewTestApp(t)
			existing := []core.RateLimitRule{
				{Label: "*:auth", MaxRequests: 2, Duration: 3},
				{Label: "/api/", MaxRequests: 300, Duration: 10},
				{Label: "GET /geo", MaxRequests: 5, Duration: 60, Audience: core.RateLimitRuleAudienceAll},
				{Label: "GET /geocode", MaxRequests: 3, Duration: 60, Audience: core.RateLimitRuleAudienceGuest},
			}
			app.Settings().RateLimits = core.RateLimitsConfig{Enabled: enabled, Rules: append([]core.RateLimitRule(nil), existing...)}
			configureDefaultRateLimits(app)
			configureDefaultRateLimits(app)
			config := app.Settings().RateLimits
			if !config.Enabled || !reflect.DeepEqual(config.Rules[:len(existing)], existing) {
				t.Fatal("must enable limits and preserve existing rules")
			}
			if len(config.Rules) != len(existing)+3 {
				t.Fatalf("expected three missing all-user rules, got %v", config.Rules)
			}
			for _, label := range []string{"GET /geo", "GET /geocode", "GET /reverse_geocode", "POST /support_feedback"} {
				if _, ok := config.FindRateLimitRule([]string{label}, core.RateLimitRuleAudienceAll, core.RateLimitRuleAudienceAuth); !ok {
					t.Errorf("no authenticated limit for %s", label)
				}
			}
		})
	}
}

func TestPublicRouteRateLimitsIncludeAuthenticatedUsers(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		limit        int
	}{
		{http.MethodGet, "/geo", 60},
		{http.MethodGet, "/geocode", 30},
		{http.MethodGet, "/reverse_geocode", 30},
		{http.MethodPost, "/support_feedback", 10},
	} {
		t.Run(tc.path, func(t *testing.T) {
			app := testutil.NewTestApp(t)
			// Exercise the fresh-bootstrap defaults without the startup repair.
			collection, err := app.FindCollectionByNameOrId("user")
			if err != nil {
				t.Fatal(err)
			}
			user := core.NewRecord(collection)
			user.SetEmail("rate-limit@example.com")
			user.SetPassword("TestPassword12345")
			if err := app.Save(user); err != nil {
				t.Fatal(err)
			}
			token, err := user.NewAuthToken()
			if err != nil {
				t.Fatal(err)
			}
			router, err := apis.NewRouter(app)
			if err != nil {
				t.Fatal(err)
			}
			RegisterAPIRoutes(&core.ServeEvent{App: app, Router: router})
			mux, err := router.BuildMux()
			if err != nil {
				t.Fatal(err)
			}
			request := func(auth bool) int {
				req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"message":"test feedback"}`))
				req.Header.Set("Content-Type", "application/json")
				if auth {
					req.Header.Set("Authorization", token)
				}
				response := httptest.NewRecorder()
				mux.ServeHTTP(response, req)
				return response.Code
			}
			for i := 0; i < tc.limit; i++ {
				status := request(i%2 == 0)
				if status != http.StatusBadRequest && status != http.StatusOK {
					t.Fatalf("request %d returned %d before limit", i+1, status)
				}
			}
			for _, auth := range []bool{false, true} {
				if status := request(auth); status != http.StatusTooManyRequests {
					t.Errorf("authenticated=%v: got %d, want 429", auth, status)
				}
			}
		})
	}
}
