package coreapp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ericbaek/musecat-backend-core/testutil"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

func TestReleaseExcludesExternalIntegrations(t *testing.T) {
	app := testutil.NewTestApp(t)
	for _, name := range []string{"external_account_connection", "external_joint_connection", "partner_application", "oauth_authorization_request"} {
		if _, err := app.FindCollectionByNameOrId(name); err == nil {
			t.Errorf("external integration collection %s must not be bootstrapped", name)
		}
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
	for _, path := range []string{"/integrations/providers", "/user/connections", "/user/records", "/user/authorizations", "/oauth/authorize", "/.well-known/openid-configuration", "/developer/apps", "/developer/v1/account/user"} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("%s returned %d, want 404", path, response.Code)
		}
	}
	for _, path := range []string{"/announcements", "/user/conversations"} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code == http.StatusNotFound {
			t.Errorf("release endpoint %s is missing", path)
		}
	}
}
