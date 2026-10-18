package partnerapp_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/ericbaek/musecat-backend-core/delegation"
	handler "github.com/ericbaek/musecat-backend-core/handlers/partnerapp"
	"github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/ericbaek/musecat-backend-core/partnerapp"
	"github.com/ericbaek/musecat-backend-core/testutil"
)

type fixture struct {
	t       *testing.T
	app     *tests.TestApp
	mux     http.Handler
	manager *delegation.Manager
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, app: testutil.NewTestApp(t)}
	var err error
	f.manager, err = delegation.NewManager("https://api.musecat.example", "https://musecat.example/oauth/authorize", nil, user.CheckActiveUser)
	if err != nil {
		t.Fatal(err)
	}
	service := partnerapp.NewService(f.manager, nil, nil, "")
	if err := service.Sync(f.app); err != nil {
		t.Fatal(err)
	}
	r, err := apis.NewRouter(f.app)
	if err != nil {
		t.Fatal(err)
	}
	handler.RegisterRoutes(&core.ServeEvent{App: f.app, Router: r}, service)
	f.mux, err = r.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) user(tags ...string) string {
	c, err := f.app.FindCollectionByNameOrId("user")
	if err != nil {
		f.t.Fatal(err)
	}
	u := core.NewRecord(c)
	u.SetEmail(fmt.Sprintf("dc-%d@example.com", time.Now().UnixNano()))
	u.SetPassword("secret123")
	if len(tags) > 0 {
		u.Set("tags", tags)
	}
	if err := f.app.Save(u); err != nil {
		f.t.Fatal(err)
	}
	token, err := u.NewAuthToken()
	if err != nil {
		f.t.Fatal(err)
	}
	return token
}

func (f *fixture) req(method, path, token, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	return w
}

func check(t *testing.T, w *httptest.ResponseRecorder, status int) map[string]any {
	t.Helper()
	if w.Code != status {
		t.Fatalf("got %d want %d: %s", w.Code, status, w.Body.String())
	}
	var out map[string]any
	if w.Body.Len() > 0 && strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

const form = `{"spec":"musecat-partner-onboarding/v1","partner_id":"%s","display_name":{"ko":"","en":"Partner","ja":""},"description":{"ko":"","en":"","ja":""},"contact_email":"dev@partner.example","direction_a":{"redirect_uris":["https://partner.example/cb"]}}`

func TestDeveloperCenterRoutes(t *testing.T) {
	f := newFixture(t)
	owner, stranger, operator := f.user(), f.user(), f.user("developer")

	check(t, f.req("GET", "/developer/apps", "", ""), 401)
	check(t, f.req("GET", "/moderation/developer/apps", owner, ""), 403)
	registry := check(t, f.req("GET", "/developer/services", "", ""), 200)
	if registry["spec"] != partnerapp.FormSpec {
		t.Fatal("registry", registry)
	}

	bad := check(t, f.req("POST", "/developer/apps", owner, `{"spec":"musecat-partner-onboarding/v1","partner_id":"X","direction_a":{"redirect_uris":[]}}`), 400)
	if bad["error"] != "invalid_form" || bad["fields"].(map[string]any)["partner_id"] == nil {
		t.Fatal("field errors", bad)
	}
	check(t, f.req("POST", "/developer/apps", owner, `{"spec":"musecat-partner-onboarding/v1","unknown":1}`), 400)

	created := check(t, f.req("POST", "/developer/apps", owner, fmt.Sprintf(form, "newpartner")), 201)
	id := created["id"].(string)
	if created["status"] != "draft" || created["has_secret"] != false {
		t.Fatal("created", created)
	}
	check(t, f.req("GET", "/developer/apps/"+id, stranger, ""), 404)
	check(t, f.req("POST", "/developer/apps/"+id+"/secret", owner, ""), 409)
	check(t, f.req("POST", "/developer/apps/"+id+"/submit", owner, ""), 200)
	check(t, f.req("POST", "/developer/apps/"+id+"/submit", owner, ""), 409)

	check(t, f.req("POST", "/moderation/developer/apps/"+id+"/approve", owner, ""), 403)
	queue := check(t, f.req("GET", "/moderation/developer/apps?status=submitted", operator, ""), 200)
	if len(queue["items"].([]any)) != 1 {
		t.Fatal("queue", queue)
	}
	approved := check(t, f.req("POST", "/moderation/developer/apps/"+id+"/approve", operator, `{"note":"ok"}`), 200)
	if approved["status"] != "approved" || approved["live"] != false {
		t.Fatal("approved", approved)
	}
	check(t, f.req("POST", "/moderation/developer/apps/"+id+"/approve", operator, `{"note":"`+strings.Repeat("n", 2001)+`"}`), 400)
	check(t, f.req("POST", "/moderation/developer/apps/"+id+"/approve", operator, ""), 409)

	secret := check(t, f.req("POST", "/developer/apps/"+id+"/secret", owner, ""), 200)
	if secret["client_id"] != "newpartner" || len(secret["client_secret"].(string)) < 32 {
		t.Fatal("secret", secret)
	}
	if err := f.manager.AuthenticateClient("newpartner", secret["client_secret"].(string)); err != nil {
		t.Fatal("client not live", err)
	}
	detail := check(t, f.req("GET", "/developer/apps/"+id, owner, ""), 200)
	if detail["live"] != true || detail["has_secret"] != true || strings.Contains(fmt.Sprint(detail), secret["client_secret"].(string)) {
		t.Fatal("detail leaks or not live", detail)
	}
	events := check(t, f.req("GET", "/developer/apps/"+id+"/events", owner, ""), 200)
	if len(events["items"].([]any)) != 4 {
		t.Fatal("events", events)
	}
	check(t, f.req("POST", "/moderation/developer/apps/"+id+"/suspend", operator, `{"note":"policy"}`), 200)
	if err := f.manager.AuthenticateClient("newpartner", secret["client_secret"].(string)); err == nil {
		t.Fatal("suspended client still authenticates")
	}
	check(t, f.req("PUT", "/developer/apps/"+id, owner, fmt.Sprintf(form, "newpartner")), 409)
	check(t, f.req("POST", "/moderation/developer/apps/"+id+"/reinstate", operator, ""), 200)
	if err := f.manager.AuthenticateClient("newpartner", secret["client_secret"].(string)); err != nil {
		t.Fatal("reinstated client offline", err)
	}
	check(t, f.req("GET", "/moderation/developer/apps/"+id+"/events", operator, ""), 200)
	check(t, f.req("GET", "/moderation/developer/apps/nope", operator, ""), 404)
}

func TestConformanceRoutesAreOwnerAndReviewerOnly(t *testing.T) {
	f := newFixture(t)
	owner := f.user()
	stranger := f.user()
	operator := f.user("developer")
	created := check(t, f.req("POST", "/developer/apps", owner, fmt.Sprintf(form, "checks_app")), 201)
	id := created["id"].(string)
	path := "/developer/apps/" + id + "/checks"
	check(t, f.req("GET", path, "", ""), 401)
	check(t, f.req("GET", path, stranger, ""), 404)
	check(t, f.req("POST", path, stranger, "{}"), 404)
	check(t, f.req("POST", path, owner, `{"refresh_token":"secret","destructive":false}`), 400)
	first := check(t, f.req("POST", path, owner, "{}"), 202)
	if first["status"] != "queued" {
		t.Fatal(first)
	}
	check(t, f.req("POST", path, owner, "{}"), 429)
	items := check(t, f.req("GET", path, owner, ""), 200)
	if len(items["items"].([]any)) != 1 {
		t.Fatal(items)
	}
	operatorPath := "/moderation/developer/apps/" + id + "/checks"
	check(t, f.req("GET", operatorPath, owner, ""), 403)
	check(t, f.req("GET", operatorPath, operator, ""), 200)
	check(t, f.req("POST", operatorPath, operator, "{}"), 429)
}
