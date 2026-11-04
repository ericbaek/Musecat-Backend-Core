package coreapp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ericbaek/musecat-backend-core/testutil"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

func TestConnectionFoundationExcludesUnpromotedRegistry(t *testing.T) {
	app := testutil.NewTestApp(t)
	for _, name := range []string{"partner_application"} {
		if _, err := app.FindCollectionByNameOrId(name); err == nil {
			t.Errorf("external integration collection %s must not be bootstrapped", name)
		}
	}
	for _, name := range []string{"external_account_connection", "external_service_connection", "external_connection_request", "external_joint_connection", "external_joint_request", "external_joint_outbox", "external_service_record", "oauth_authorization_request", "oauth_authorization_code", "oauth_delegated_token", "oauth_grant", "oauth_client_registration", "oauth_client_usage"} {
		collection, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			t.Fatalf("OAuth schema missing %s: %v", name, err)
		}
		if collection.ListRule != nil || collection.ViewRule != nil || collection.CreateRule != nil || collection.UpdateRule != nil || collection.DeleteRule != nil {
			t.Fatalf("OAuth schema opened raw collection access: %s", name)
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
	for _, path := range []string{"/user/authorizations", "/oauth/authorize", "/.well-known/openid-configuration", "/developer/apps", "/developer/v1/account/user"} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("%s returned %d, want 404", path, response.Code)
		}
	}
	for _, path := range []string{"/announcements", "/user/conversations", "/integrations/providers", "/user/connections", "/user/records"} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code == http.StatusNotFound {
			t.Errorf("release endpoint %s is missing", path)
		}
	}
}
