package user_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestUserBan_CollectionEndpointsArePrivate(t *testing.T) {
	// Seed a ban record with a valid 15-char PocketBase ID
	banRecordID := "banuser00000001"

	// 1. Unauthenticated request to list user_ban records returns 403
	scenarioList := tests.ApiScenario{
		Name:            "GET /api/collections/user_ban/records unauthenticated returns 403",
		Method:          http.MethodGet,
		URL:             "/api/collections/user_ban/records",
		ExpectedStatus:  http.StatusForbidden,
		ExpectedContent: []string{`"status":403`},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			app := newUserFetchTestApp(tb)
			seedUserBanForUserTest(tb, app, banRecordID, hashNormalizedEmailForUserTest("banned@example.com"), "abuse", time.Now().Add(24*time.Hour))
			return app
		},
	}
	scenarioList.Test(t)

	// 2. Unauthenticated request to view single user_ban record returns 403
	scenarioView := tests.ApiScenario{
		Name:            "GET /api/collections/user_ban/records/{id} unauthenticated returns 403",
		Method:          http.MethodGet,
		URL:             "/api/collections/user_ban/records/" + banRecordID,
		ExpectedStatus:  http.StatusForbidden,
		ExpectedContent: []string{`"status":403`},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			app := newUserFetchTestApp(tb)
			seedUserBanForUserTest(tb, app, banRecordID, hashNormalizedEmailForUserTest("banned@example.com"), "abuse", time.Now().Add(24*time.Hour))
			return app
		},
	}
	scenarioView.Test(t)

	// 3. Regular authenticated user request to list user_ban records returns 403
	headersAuth := map[string]string{}
	scenarioAuthList := tests.ApiScenario{
		Name:            "GET /api/collections/user_ban/records as normal user returns 403",
		Method:          http.MethodGet,
		URL:             "/api/collections/user_ban/records",
		Headers:         headersAuth,
		ExpectedStatus:  http.StatusForbidden,
		ExpectedContent: []string{`"status":403`},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
		BeforeTestFunc: func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
			token, _ := createAuthUser(tb, app, false)
			headersAuth["Authorization"] = "Bearer " + token
		},
	}
	scenarioAuthList.Test(t)
}

func TestUser_CreateBlocksServerManagedFields(t *testing.T) {
	testCases := []struct {
		name    string
		payload string
	}{
		{
			name:    "tags cannot be specified on creation",
			payload: `{"email":"hacker1_%d@example.com","password":"secret123456","passwordConfirm":"secret123456","tags":["developer"]}`,
		},
		{
			name:    "owns cannot be specified on creation",
			payload: `{"email":"hacker2_%d@example.com","password":"secret123456","passwordConfirm":"secret123456","owns":["pbc_fakearcade"]}`,
		},
		{
			name:    "withdrawn cannot be specified on creation",
			payload: `{"email":"hacker3_%d@example.com","password":"secret123456","passwordConfirm":"secret123456","withdrawn":true}`,
		},
		{
			name:    "withdrawnAt cannot be specified on creation",
			payload: `{"email":"hacker4_%d@example.com","password":"secret123456","passwordConfirm":"secret123456","withdrawnAt":"2026-01-01 00:00:00"}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			payload := fmt.Sprintf(tc.payload, time.Now().UnixNano())
			scenario := tests.ApiScenario{
				Name:            tc.name,
				Method:          http.MethodPost,
				URL:             "/api/collections/user/records",
				Body:            strings.NewReader(payload),
				Headers:         map[string]string{"Content-Type": "application/json"},
				ExpectedStatus:  http.StatusBadRequest,
				ExpectedContent: []string{`"Failed to create record."`},
				TestAppFactory:  func(tb testing.TB) *tests.TestApp { return newUserFetchTestApp(tb) },
			}
			scenario.Test(t)
		})
	}

	// Legitimate creation without server-managed fields should succeed
	t.Run("normal signup succeeds", func(t *testing.T) {
		email := fmt.Sprintf("legit_user_%d@example.com", time.Now().UnixNano())
		payload := fmt.Sprintf(`{"email":%q,"password":"secret123456","passwordConfirm":"secret123456"}`, email)
		scenario := tests.ApiScenario{
			Name:            "valid user create succeeds",
			Method:          http.MethodPost,
			URL:             "/api/collections/user/records",
			Body:            strings.NewReader(payload),
			Headers:         map[string]string{"Content-Type": "application/json"},
			ExpectedStatus:  http.StatusOK,
			ExpectedContent: []string{`"collectionName":"user"`},
			TestAppFactory:  func(tb testing.TB) *tests.TestApp { return newUserFetchTestApp(tb) },
		}
		scenario.Test(t)
	})
}
