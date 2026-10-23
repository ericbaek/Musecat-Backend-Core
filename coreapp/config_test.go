package coreapp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	apidocs "github.com/ericbaek/musecat-backend-core/docs"
	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/ericbaek/musecat-backend-core/testutil"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

func TestConfigureDoesNotRegisterMigrationsOrJobs(t *testing.T) {
	app := pocketbase.NewWithConfig(pocketbase.Config{DefaultDataDir: t.TempDir()})
	jobCount := len(app.Cron().Jobs()) // PocketBase installs its own maintenance jobs.
	Configure(app, Config{})
	if len(app.Cron().Jobs()) != jobCount {
		t.Fatal("Core must leave scheduling to the executable")
	}
	for _, command := range app.RootCmd.Commands() {
		if command.Name() == "migrate" {
			t.Fatal("Core must leave migration registration to the executable")
		}
	}
}

func TestNativeExecutableCompositionRequiresAllowlistAndOpaqueSession(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			app := testutil.NewTestApp(t)
			var clients []nativeauth.Client
			if enabled {
				clients = []nativeauth.Client{{ID: "ios", Callback: "musecat-test://auth/callback"}}
			}
			called := false
			Configure(&pocketbase.PocketBase{App: app}, Config{NativeAuthClients: clients, NativeRoutes: func(group *router.RouterGroup[*core.RequestEvent]) {
				called = true
				group.GET("/full/probe", func(e *core.RequestEvent) error { return e.NoContent(204) })
			}})
			r, err := apis.NewRouter(app)
			if err != nil {
				t.Fatal(err)
			}
			event := &core.ServeEvent{App: app, Router: r}
			if err = app.OnServe().Trigger(event, func(e *core.ServeEvent) error { return e.Next() }); err != nil {
				t.Fatal(err)
			}
			if called != enabled {
				t.Fatal("native executable routes ignored allowlist")
			}
			mux, err := r.BuildMux()
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("GET", "/native/full/probe", nil)
			res := httptest.NewRecorder()
			mux.ServeHTTP(res, req)
			want := 404
			if enabled {
				want = 401
			}
			if res.Code != want {
				t.Fatalf("unguarded executable extension: %d want %d", res.Code, want)
			}
		})
	}
}

func TestDocumentationUsesExplicitConfiguration(t *testing.T) {
	t.Setenv("MUSECAT_OPENAPI_SPEC_PATH", "/nonexistent/implicit-spec.yaml")
	t.Setenv("DOCS_BASIC_AUTH_USER", "ambient-user")
	t.Setenv("DOCS_BASIC_AUTH_PASS", "ambient-pass")
	for _, tc := range []struct {
		name       string
		config     DocumentationConfig
		user, pass string
		status     int
	}{
		{"embedded ignores environment", DocumentationConfig{}, "", "", 200},
		{"explicit auth required", DocumentationConfig{Username: "u", Password: "p"}, "", "", 401},
		{"explicit auth accepted", DocumentationConfig{Username: "u", Password: "p"}, "u", "p", 200},
		{"partial username fails closed", DocumentationConfig{Username: "u"}, "u", "", 401},
		{"partial password fails closed", DocumentationConfig{Password: "p"}, "", "p", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := router.NewRouter(func(w http.ResponseWriter, req *http.Request) (*core.RequestEvent, router.EventCleanupFunc) {
				e := &core.RequestEvent{}
				e.Response, e.Request = w, req
				return e, nil
			})
			RegisterDocumentationRoutes(&core.ServeEvent{Router: r}, tc.config)
			mux, err := r.BuildMux()
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil)
			if tc.user != "" || tc.pass != "" {
				req.SetBasicAuth(tc.user, tc.pass)
			}
			res := httptest.NewRecorder()
			mux.ServeHTTP(res, req)
			if res.Code != tc.status {
				t.Fatalf("got %d, want %d", res.Code, tc.status)
			}
			if tc.status == 200 && !bytes.Equal(res.Body.Bytes(), apidocs.OpenAPISpec()) {
				t.Fatal("contract does not match compiled Core")
			}
		})
	}
}
