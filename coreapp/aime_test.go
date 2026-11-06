package coreapp

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	n "github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/ericbaek/musecat-backend-core/testutil"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

func TestAimeRoutesRequireExplicitActiveAccountConsent(t *testing.T) {
	app := testutil.NewTestApp(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(n.RecordAimeProbe(app, 403, time.Now()))
	for _, id := range []string{"aimeactive00001", "aimewithdrawn01"} {
		_, err := app.DB().NewQuery("INSERT INTO user (id,email,tokenKey,withdrawn) VALUES ({:id},{:email},{:id},{:withdrawn})").Bind(dbx.Params{"id": id, "email": id + "@example.com", "withdrawn": id == "aimewithdrawn01"}).Execute()
		must(err)
	}
	active, err := app.FindRecordById("user", "aimeactive00001")
	must(err)
	token, err := active.NewAuthToken()
	must(err)
	withdrawn, err := app.FindRecordById("user", "aimewithdrawn01")
	must(err)
	withdrawnToken, err := withdrawn.NewAuthToken()
	must(err)
	router, err := apis.NewRouter(app)
	must(err)
	RegisterAPIRoutes(&core.ServeEvent{App: app, Router: router})
	mux, err := router.BuildMux()
	must(err)
	for _, tc := range []struct {
		method, token, body string
		status              int
	}{
		{"GET", "", "", 401}, {"PUT", "", `{"enabled":true}`, 401},
		{"GET", withdrawnToken, "", 403}, {"PUT", withdrawnToken, `{"enabled":true}`, 403},
		{"PUT", token, `{}`, 400}, {"PUT", token, `{"enabled":"true"}`, 400},
		{"GET", token, "", 200}, {"PUT", token, `{"enabled":true}`, 200},
		{"PUT", token, `{"enabled":false}`, 200},
	} {
		req := httptest.NewRequest(tc.method, "/user/notifications/aime", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		if tc.token != "" {
			req.Header.Set("Authorization", tc.token)
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		if response.Code != tc.status {
			t.Fatalf("%s status=%d want=%d body=%s", tc.method, response.Code, tc.status, response.Body.String())
		}
	}
	state, err := n.GetAimeStatus(app, active.Id)
	must(err)
	if state.Subscribed {
		t.Fatal("cancelled consent persisted")
	}
}

func TestAimePublicStatusNeverExposesAccountMembership(t *testing.T) {
	app := testutil.NewTestApp(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(n.RecordAimeProbe(app, 403, time.Now()))
	_, err := app.DB().NewQuery("INSERT INTO user(id,email,tokenKey) VALUES('aimepublicuser1','aimepublic@example.com','aimepublicuser1')").Execute()
	must(err)
	user, err := app.FindRecordById("user", "aimepublicuser1")
	must(err)
	must(n.SetAimeSubscription(app, user.Id, true))
	token, err := user.NewAuthToken()
	must(err)
	router, err := apis.NewRouter(app)
	must(err)
	RegisterAPIRoutes(&core.ServeEvent{App: app, Router: router})
	mux, err := router.BuildMux()
	must(err)
	for _, authorization := range []string{"", token} {
		req := httptest.NewRequest("GET", "/aime/status?user="+user.Id, nil)
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		if response.Code != 200 {
			t.Fatalf("public status=%d body=%s", response.Code, response.Body.String())
		}
		var result n.AimeStatus
		must(json.Unmarshal(response.Body.Bytes(), &result))
		if result.Subscribed || result.Status != "blocked" || len(result.History) != 1 {
			t.Fatalf("unexpected public result: %#v", result)
		}
		if strings.Contains(response.Body.String(), user.Id) || strings.Contains(response.Body.String(), "aimepublic@example.com") {
			t.Fatal("public status leaked account data")
		}
	}
	// The account-specific authenticated endpoint still returns that account's choice.
	req := httptest.NewRequest("GET", "/user/notifications/aime", nil)
	req.Header.Set("Authorization", token)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	var result n.AimeStatus
	must(json.Unmarshal(response.Body.Bytes(), &result))
	if response.Code != 200 || !result.Subscribed {
		t.Fatalf("account opt-in lost: %d %#v", response.Code, result)
	}
	req = httptest.NewRequest("PUT", "/user/notifications/aime", strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	if response.Code != 401 {
		t.Fatalf("anonymous mutation status=%d", response.Code)
	}
}
