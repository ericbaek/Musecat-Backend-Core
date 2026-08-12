package user_test

import (
	"net/http"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestGetUserChangelog_PublicPrivateVisibilityAndCategoryFilter(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:            "GET /user/changelog scopes authored rows and hides private arcades",
		Method:          http.MethodGet,
		ExpectedStatus:  http.StatusOK,
		ExpectedContent: []string{`"items":[{`},
		TestAppFactory:  func(tb testing.TB) *tests.TestApp { return newUserFetchTestApp(tb) },
	}

	var (
		userID       string
		publicChange string
	)
	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		_, user := createAuthUser(tb, app, true)
		userID = user.Id
		publicArcade := seedUserChangelogArcade(tb, app, user.Id, true, "Public Arcade")
		privateArcade := seedUserChangelogArcade(tb, app, user.Id, false, "Private Arcade")
		publicChange = seedUserChangelog(tb, app, publicArcade, "basic", user.Id)
		seedUserChangelog(tb, app, privateArcade, "basic", user.Id)
		seedUserChangelog(tb, app, publicArcade, "flag", user.Id)
		scenario.URL = "/user/changelog?user=" + userID + "&changed=basic&per_page=1"
	}
	scenario.AfterTestFunc = func(tb testing.TB, _ *tests.TestApp, res *http.Response) {
		tb.Helper()
		payload := decodeJSON(tb, res)
		assertJSONNumber(tb, payload["total"], 1)
		assertJSONNumber(tb, payload["last_page"], 1)
		items, ok := payload["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected one filtered item, got %#v", payload["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected changelog item object, got %#v", items[0])
		}
		if item["id"] != publicChange {
			tb.Fatalf("expected public basic changelog %q, got %#v", publicChange, item)
		}
		if item["arcade_name"] != "Public Arcade" {
			tb.Fatalf("expected arcade_name, got %#v", item["arcade_name"])
		}
	}

	scenario.Test(t)
}

func TestGetUserChangelog_OwnerCanReadPrivateRows(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:            "GET /user/changelog permits private rows for owner",
		Method:          http.MethodGet,
		ExpectedStatus:  http.StatusOK,
		ExpectedContent: []string{`"items":[{`},
		TestAppFactory:  func(tb testing.TB) *tests.TestApp { return newUserFetchTestApp(tb) },
	}
	var userID string
	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ownerToken, user := createAuthUser(tb, app, true)
		userID = user.Id
		privateArcade := seedUserChangelogArcade(tb, app, user.Id, false, "Private Arcade")
		seedUserChangelog(tb, app, privateArcade, "game", user.Id)
		scenario.URL = "/user/changelog?user=" + userID
		scenario.Headers = map[string]string{"Authorization": "Bearer " + ownerToken}
	}

	scenario.Test(t)
}

func TestGetUserChangelog_ReviewerCanReadPrivateRows(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:            "GET /user/changelog permits private rows for strict reviewers",
		Method:          http.MethodGet,
		ExpectedStatus:  http.StatusOK,
		ExpectedContent: []string{`"items":[{`},
		TestAppFactory:  func(tb testing.TB) *tests.TestApp { return newUserFetchTestApp(tb) },
	}
	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		_, user := createAuthUser(tb, app, true)
		reviewerToken, reviewer := createAuthUser(tb, app, true)
		reviewer.Set("tags", []string{"moderator"})
		if err := app.Save(reviewer); err != nil {
			tb.Fatalf("failed to save reviewer: %v", err)
		}
		privateArcade := seedUserChangelogArcade(tb, app, user.Id, false, "Private Arcade")
		seedUserChangelog(tb, app, privateArcade, "game", user.Id)
		scenario.URL = "/user/changelog?user=" + user.Id
		scenario.Headers = map[string]string{"Authorization": "Bearer " + reviewerToken}
	}

	scenario.Test(t)
}

func TestGetUserChangelog_RejectsUnknownCategory(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /user/changelog rejects unknown changed category",
		Method:         http.MethodGet,
		URL:            "/user/changelog?user=test&changed=flag",
		ExpectedStatus: http.StatusBadRequest,
		ExpectedContent: []string{
			`"error":"changed must be one of basic,game,hour,sns,gtk,photo"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp { return newUserFetchTestApp(tb) },
	}
	scenario.Test(t)
}

func TestGetUserChangelog_WithdrawnUserReturnsEmptyPage(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:            "GET /user/changelog hides withdrawn user history",
		Method:          http.MethodGet,
		ExpectedStatus:  http.StatusOK,
		ExpectedContent: []string{`"total":0`, `"items":[]`},
		TestAppFactory:  func(tb testing.TB) *tests.TestApp { return newUserFetchTestApp(tb) },
	}
	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)
		_, user := createAuthUser(tb, app, true)
		user.Set("withdrawn", true)
		if err := app.Save(user); err != nil {
			tb.Fatalf("failed to withdraw user: %v", err)
		}
		arcade := seedUserChangelogArcade(tb, app, user.Id, true, "Public Arcade")
		seedUserChangelog(tb, app, arcade, "basic", user.Id)
		scenario.URL = "/user/changelog?user=" + user.Id
	}

	scenario.Test(t)
}

func seedUserChangelogArcade(tb testing.TB, app *tests.TestApp, createdBy string, public bool, name string) string {
	tb.Helper()
	arcadeCollection, err := app.FindCollectionByNameOrId("arcade")
	if err != nil {
		tb.Fatalf("failed to load arcade collection: %v", err)
	}
	arcade := core.NewRecord(arcadeCollection)
	arcade.Set("createdBy", createdBy)
	arcade.Set("public", public)
	if err := app.Save(arcade); err != nil {
		tb.Fatalf("failed to save arcade: %v", err)
	}

	basicCollection, err := app.FindCollectionByNameOrId("arcade_basic")
	if err != nil {
		tb.Fatalf("failed to load arcade_basic collection: %v", err)
	}
	basic := core.NewRecord(basicCollection)
	basic.Set("arcade", arcade.Id)
	basic.Set("name", name)
	basic.Set("location", map[string]any{"lat": 37.5, "lon": 126.9})
	basic.Set("createdBy", createdBy)
	if err := app.Save(basic); err != nil {
		tb.Fatalf("failed to save arcade basic: %v", err)
	}
	arcade.Set("basic", basic.Id)
	if err := app.Save(arcade); err != nil {
		tb.Fatalf("failed to link arcade basic: %v", err)
	}
	return arcade.Id
}

func seedUserChangelog(tb testing.TB, app *tests.TestApp, arcadeID, changed, by string) string {
	tb.Helper()
	coll, err := app.FindCollectionByNameOrId("arcade_changelog")
	if err != nil {
		tb.Fatalf("failed to load changelog collection: %v", err)
	}
	rec := core.NewRecord(coll)
	rec.Set("arcade", arcadeID)
	rec.Set("changed", changed)
	rec.Set("by", by)
	rec.Set("from", "before")
	rec.Set("to", "after")
	rec.Set("log", map[string]any{"type": changed})
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save changelog: %v", err)
	}
	return rec.Id
}
