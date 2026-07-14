package user_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/ericbaek/musecat-backend-core/testutil"
)

func newUserFetchTestApp(tb testing.TB) *tests.TestApp {
	app := testutil.NewTestApp(tb)
	userhandler.RegisterHooks(app)
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/user", userhandler.GetUserByID)
		se.Router.GET("/user/activity", userhandler.GetUserActivity)
		se.Router.GET("/arcade/visits", userhandler.GetArcadeVisitStats)

		authUser := se.Router.Group("/user").Bind(apis.RequireAuth("user"))
		authUser.GET("/me", userhandler.GetMe)
		authUser.POST("/signup", userhandler.SignUp)
		authUser.POST("/check-in", userhandler.CheckIn).Bind(userhandler.RequireActiveUser())
		authUser.GET("/visits", userhandler.GetMyVisits).Bind(userhandler.RequireActiveUser())
		authUser.PUT("/visit-visibility", userhandler.UpdateVisitVisibility).Bind(userhandler.RequireActiveUser())
		se.Router.Group("/arcade").Bind(apis.RequireAuth("user"), userhandler.RequireActiveUser()).POST("/visit", userhandler.VisitArcade)

		return se.Next()
	})

	return app
}

func ensureWithdrawFields(tb testing.TB, app *tests.TestApp) {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("user")
	if err != nil {
		tb.Fatalf("failed to load user collection: %v", err)
	}

	changed := false

	if coll.Fields.GetByName("withdrawn") == nil {
		if err := coll.Fields.AddMarshaledJSONAt(len(coll.Fields), []byte(`{
			"hidden": false,
			"id": "bool_test_withdrawn",
			"name": "withdrawn",
			"presentable": false,
			"required": false,
			"system": false,
			"type": "bool"
		}`)); err != nil {
			tb.Fatalf("failed to add withdrawn field: %v", err)
		}
		changed = true
	}

	if coll.Fields.GetByName("withdrawnAt") == nil {
		if err := coll.Fields.AddMarshaledJSONAt(len(coll.Fields), []byte(`{
			"hidden": false,
			"id": "date_test_withdrawnat",
			"max": "",
			"min": "",
			"name": "withdrawnAt",
			"presentable": false,
			"required": false,
			"system": false,
			"type": "date"
		}`)); err != nil {
			tb.Fatalf("failed to add withdrawnAt field: %v", err)
		}
		changed = true
	}

	if changed {
		if err := app.Save(coll); err != nil {
			tb.Fatalf("failed to save user collection: %v", err)
		}
	}
}

func createAuthUser(tb testing.TB, app *tests.TestApp, withUserInfo bool) (string, *core.Record) {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("user")
	if err != nil {
		tb.Fatalf("failed to load user collection: %v", err)
	}

	unique := time.Now().UnixNano()
	rec := core.NewRecord(coll)
	rec.SetEmail(fmt.Sprintf("user_fetch_%d@example.com", unique))
	rec.Set("username", fmt.Sprintf("user_fetch_%d", unique))
	rec.SetPassword("secret123")

	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to create auth user: %v", err)
	}

	if withUserInfo {
		ensureUserInfo(tb, app, rec.Id)
	}

	token, err := rec.NewAuthToken()
	if err != nil {
		tb.Fatalf("failed to issue auth token: %v", err)
	}

	return token, rec
}

func ensureUserInfo(tb testing.TB, app *tests.TestApp, userID string) *core.Record {
	tb.Helper()

	rec, err := app.FindRecordById("user_info", userID)
	if err == nil {
		return rec
	}

	coll, collErr := app.FindCollectionByNameOrId("user_info")
	if collErr != nil {
		tb.Fatalf("failed to load user_info collection: %v", collErr)
	}

	rec = core.NewRecord(coll)
	rec.Set("id", userID)
	if saveErr := app.Save(rec); saveErr != nil {
		tb.Fatalf("failed to create user_info: %v", saveErr)
	}

	return rec
}

func seedGameSeries(tb testing.TB, app *tests.TestApp, seriesNumber int, en, kr, jp string) *core.Record {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("game_series")
	if err != nil {
		tb.Fatalf("failed to load game_series collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("seriesNumber", seriesNumber)
	rec.Set("en", en)
	rec.Set("kr", kr)
	rec.Set("jp", jp)
	rec.Set("en_short", en+" short")
	rec.Set("kr_short", kr+" short")
	rec.Set("jp_short", jp+" short")
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save game_series: %v", err)
	}

	return rec
}

func decodeJSON(tb testing.TB, res *http.Response) map[string]any {
	tb.Helper()
	defer res.Body.Close()

	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		tb.Fatalf("failed to decode response: %v", err)
	}

	return payload
}

func assertProfileShape(tb testing.TB, payload map[string]any) {
	tb.Helper()

	keys := []string{"id", "created", "username", "nickname", "level", "bio", "avatar", "withdrawn", "series_public"}
	for _, k := range keys {
		if _, ok := payload[k]; !ok {
			tb.Fatalf("expected key %q in payload: %#v", k, payload)
		}
	}

	rawSNS, ok := payload["sns"]
	if !ok {
		tb.Fatalf("expected key %q in payload: %#v", "sns", payload)
	}
	snsObj, ok := rawSNS.(map[string]any)
	if !ok {
		tb.Fatalf("expected sns object, got %#v", rawSNS)
	}
	items, ok := snsObj["items"].([]any)
	if !ok {
		tb.Fatalf("expected sns.items array, got %#v", snsObj["items"])
	}
	if items == nil {
		tb.Fatalf("expected sns.items to be non-nil")
	}

	if _, exists := payload["email"]; exists {
		tb.Fatalf("did not expect email field in payload")
	}
	if _, exists := payload["emailVisibility"]; exists {
		tb.Fatalf("did not expect emailVisibility field in payload")
	}
}

func TestGetMe_Success(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /user/me returns merged profile",
		Method:         http.MethodGet,
		URL:            "/user/me",
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"id":"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	headers := map[string]string{}
	var userID string
	var username string
	var createdAt string
	var ownedArcadeID string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		token, userRec := createAuthUser(tb, app, true)
		ownedArcadeID = seedVisitArcade(tb, app, "Asia/Seoul").Id
		userID = userRec.Id
		username = "me_user_" + userRec.Id
		createdAt = userRec.GetString("created")
		userRec.Set("username", username)
		userRec.Set("tags", []string{"arcade_owner"})
		userRec.Set("owns", []string{ownedArcadeID})
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to set username: %v", err)
		}

		userInfo := ensureUserInfo(tb, app, userRec.Id)
		userInfo.Set("nickname", "me_nick")
		userInfo.Set("bio", "hello bio")
		userInfo.Set("warp", true)
		userInfo.Set("sns", `{"items":[{"type":"twitter","link":"https://x.com/me_user","name":"me"},{"type":"website","link":"https://example.com/me"}]}`)
		if err := app.Save(userInfo); err != nil {
			tb.Fatalf("failed to save user_info: %v", err)
		}

		headers["Authorization"] = "Bearer " + token
		scenario.Headers = headers
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		payload := decodeJSON(tb, res)
		assertProfileShape(tb, payload)

		if got := payload["id"]; got != userID {
			tb.Fatalf("expected id %q, got %v", userID, got)
		}
		if got := payload["created"]; got != createdAt {
			tb.Fatalf("expected created %q, got %v", createdAt, got)
		}
		if got := payload["username"]; got != username {
			tb.Fatalf("expected username %q, got %v", username, got)
		}
		if got := payload["nickname"]; got != "me_nick" {
			tb.Fatalf("expected nickname me_nick, got %v", got)
		}
		if got := payload["bio"]; got != "hello bio" {
			tb.Fatalf("expected bio hello bio, got %v", got)
		}
		if got := payload["avatar"]; got != "" {
			tb.Fatalf("expected empty avatar, got %v", got)
		}
		snsObj := nestedMap(tb, payload, "sns")
		rawItems, ok := snsObj["items"].([]any)
		if !ok || len(rawItems) != 2 {
			tb.Fatalf("expected 2 sns items, got %#v", snsObj["items"])
		}
		first, ok := rawItems[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected first sns item object, got %#v", rawItems[0])
		}
		if got := first["type"]; got != "twitter" {
			tb.Fatalf("expected first sns type twitter, got %v", got)
		}
		if got := first["link"]; got != "https://x.com/me_user" {
			tb.Fatalf("expected first sns link, got %v", got)
		}
		if got := first["name"]; got != "me" {
			tb.Fatalf("expected first sns name me, got %v", got)
		}
		if got := payload["withdrawn"]; got != false {
			tb.Fatalf("expected withdrawn false, got %v", got)
		}
		if got := payload["series_public"]; got != false {
			tb.Fatalf("expected series_public false, got %v", got)
		}
		if got := payload["warp"]; got != true {
			tb.Fatalf("expected warp true, got %v", got)
		}
		if got := payload["tag"]; !containsString(got, "arcade_owner") {
			tb.Fatalf("expected arcade_owner tag, got %#v", got)
		}
		if got := payload["owns"]; !containsString(got, ownedArcadeID) {
			tb.Fatalf("expected owned arcade, got %#v", got)
		}
	}

	scenario.Test(t)
}

func containsString(value any, expected string) bool {
	values, ok := value.([]any)
	if !ok {
		return false
	}

	for _, value := range values {
		if value == expected {
			return true
		}
	}

	return false
}

func TestGetMe_IncludesPrivateSeries(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /user/me includes private series details",
		Method:         http.MethodGet,
		URL:            "/user/me",
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"id":"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	headers := map[string]string{}
	var seriesA *core.Record
	var userID string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		token, userRec := createAuthUser(tb, app, true)
		userID = userRec.Id

		seriesA = seedGameSeries(tb, app, 11, "Private Stage", "프라이빗 스테이지", "プライベートステージ")

		userInfo := ensureUserInfo(tb, app, userRec.Id)
		userInfo.Set("series_public", false)
		userInfo.Set("series", []string{seriesA.Id})
		if err := app.Save(userInfo); err != nil {
			tb.Fatalf("failed to save user_info: %v", err)
		}

		headers["Authorization"] = "Bearer " + token
		scenario.Headers = headers
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		payload := decodeJSON(tb, res)
		assertProfileShape(tb, payload)

		if got := payload["id"]; got != userID {
			tb.Fatalf("expected id %q, got %v", userID, got)
		}
		if got := payload["series_public"]; got != false {
			tb.Fatalf("expected series_public false, got %v", got)
		}

		rawSeries, ok := payload["series"].([]any)
		if !ok {
			tb.Fatalf("expected series array, got %#v", payload["series"])
		}
		if len(rawSeries) != 1 {
			tb.Fatalf("expected 1 private series entry, got %#v", rawSeries)
		}

		first, ok := rawSeries[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected series object, got %#v", rawSeries[0])
		}
		if got := first["id"]; got != seriesA.Id {
			tb.Fatalf("expected private series id %q, got %v", seriesA.Id, got)
		}
		if got := first["en"]; got != "Private Stage" {
			tb.Fatalf("expected private series en, got %v", got)
		}
	}

	scenario.Test(t)
}

func TestGetMe_Unauthorized(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /user/me requires auth",
		Method:         http.MethodGet,
		URL:            "/user/me",
		ExpectedStatus: http.StatusUnauthorized,
		ExpectedContent: []string{
			`"status":401`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	scenario.Test(t)
}

func TestGetUserByID_MissingID(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /user missing id",
		Method:         http.MethodGet,
		URL:            "/user",
		ExpectedStatus: http.StatusBadRequest,
		ExpectedContent: []string{
			`"error":"missing required query param 'id' or 'username'"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	scenario.Test(t)
}

func TestGetUserByID_NotFound(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /user not found",
		Method:         http.MethodGet,
		URL:            "/user?id=not_exist_user",
		ExpectedStatus: http.StatusNotFound,
		ExpectedContent: []string{
			`"error":"user not found"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	scenario.Test(t)
}

func TestGetUserByUsername_NotFound(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /user by username not found",
		Method:         http.MethodGet,
		URL:            "/user?username=not_exist_user",
		ExpectedStatus: http.StatusNotFound,
		ExpectedContent: []string{
			`"error":"user not found"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	scenario.Test(t)
}

func TestGetUserByID_Success(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /user returns merged profile",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"id":"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	var userID string
	var username string
	var createdAt string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		_, userRec := createAuthUser(tb, app, true)
		userID = userRec.Id
		username = "public_user_" + userRec.Id
		createdAt = userRec.GetString("created")
		userRec.Set("username", username)
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to set username: %v", err)
		}

		userInfo := ensureUserInfo(tb, app, userRec.Id)
		userInfo.Set("nickname", "public_nick")
		userInfo.Set("bio", "public bio")
		userInfo.Set("sns", `{"items":[{"type":"instagram","link":"https://instagram.com/public_nick"}]}`)
		if err := app.Save(userInfo); err != nil {
			tb.Fatalf("failed to save user_info: %v", err)
		}

		scenario.URL = "/user?id=" + userID
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		payload := decodeJSON(tb, res)
		assertProfileShape(tb, payload)

		if got := payload["id"]; got != userID {
			tb.Fatalf("expected id %q, got %v", userID, got)
		}
		if got := payload["created"]; got != createdAt {
			tb.Fatalf("expected created %q, got %v", createdAt, got)
		}
		if got := payload["username"]; got != username {
			tb.Fatalf("expected username %q, got %v", username, got)
		}
		if got := payload["nickname"]; got != "public_nick" {
			tb.Fatalf("expected nickname public_nick, got %v", got)
		}
		if got := payload["bio"]; got != "public bio" {
			tb.Fatalf("expected bio public bio, got %v", got)
		}
		snsObj := nestedMap(tb, payload, "sns")
		rawItems, ok := snsObj["items"].([]any)
		if !ok || len(rawItems) != 1 {
			tb.Fatalf("expected 1 sns item, got %#v", snsObj["items"])
		}
		first, ok := rawItems[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected sns item object, got %#v", rawItems[0])
		}
		if got := first["type"]; got != "instagram" {
			tb.Fatalf("expected sns type instagram, got %v", got)
		}
		if got := first["link"]; got != "https://instagram.com/public_nick" {
			tb.Fatalf("expected sns link, got %v", got)
		}
		if got := payload["withdrawn"]; got != false {
			tb.Fatalf("expected withdrawn false, got %v", got)
		}
		if got := payload["series_public"]; got != false {
			tb.Fatalf("expected series_public false, got %v", got)
		}
		if _, exists := payload["warp"]; exists {
			tb.Fatalf("expected warp to be omitted in public profile: %#v", payload)
		}
	}

	scenario.Test(t)
}

func TestGetUserByUsername_Success(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /user by username returns merged profile",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"id":"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	var userID string
	var username string
	var createdAt string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		_, userRec := createAuthUser(tb, app, true)
		userID = userRec.Id
		username = "public_username_" + userRec.Id
		createdAt = userRec.GetString("created")
		userRec.Set("username", username)
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to set username: %v", err)
		}

		userInfo := ensureUserInfo(tb, app, userRec.Id)
		userInfo.Set("nickname", "public_username_nick")
		userInfo.Set("bio", "public username bio")
		if err := app.Save(userInfo); err != nil {
			tb.Fatalf("failed to save user_info: %v", err)
		}

		scenario.URL = "/user?username=" + username
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		payload := decodeJSON(tb, res)
		assertProfileShape(tb, payload)

		if got := payload["id"]; got != userID {
			tb.Fatalf("expected id %q, got %v", userID, got)
		}
		if got := payload["created"]; got != createdAt {
			tb.Fatalf("expected created %q, got %v", createdAt, got)
		}
		if got := payload["username"]; got != username {
			tb.Fatalf("expected username %q, got %v", username, got)
		}
		if got := payload["nickname"]; got != "public_username_nick" {
			tb.Fatalf("expected nickname public_username_nick, got %v", got)
		}
		if got := payload["bio"]; got != "public username bio" {
			tb.Fatalf("expected bio public username bio, got %v", got)
		}
		if got := payload["withdrawn"]; got != false {
			tb.Fatalf("expected withdrawn false, got %v", got)
		}
		if got := payload["series_public"]; got != false {
			tb.Fatalf("expected series_public false, got %v", got)
		}
	}

	scenario.Test(t)
}

func TestGetUserByID_MissingUserInfoFallbackNoWrite(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /user falls back without creating user_info",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"id":"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	var userID string
	var username string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		_, userRec := createAuthUser(tb, app, false)
		userID = userRec.Id
		username = "fallback_user_" + userRec.Id
		userRec.Set("username", username)
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to set username: %v", err)
		}

		scenario.URL = "/user?id=" + userID
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		payload := decodeJSON(tb, res)
		assertProfileShape(tb, payload)

		if got := payload["nickname"]; got != username {
			tb.Fatalf("expected fallback nickname %q, got %v", username, got)
		}
		if got := payload["bio"]; got != "" {
			tb.Fatalf("expected empty bio fallback, got %v", got)
		}
		if got := payload["avatar"]; got != "" {
			tb.Fatalf("expected empty avatar fallback, got %v", got)
		}

		if _, err := app.FindRecordById("user_info", userID); err == nil {
			tb.Fatalf("expected user_info to remain absent (no write fallback)")
		}
	}

	scenario.Test(t)
}

func TestGetUserByID_IncludesPublicSeries(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /user includes public series details",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"id":"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	var userID string
	var seriesA *core.Record
	var seriesB *core.Record

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		_, userRec := createAuthUser(tb, app, true)
		userID = userRec.Id
		userRec.Set("username", "series_public_user")
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to save user: %v", err)
		}

		seriesA = seedGameSeries(tb, app, 7, "Dance Rush", "댄스 러시", "ダンスラッシュ")
		seriesB = seedGameSeries(tb, app, 9, "Beat Stage", "비트 스테이지", "ビートステージ")

		userInfo := ensureUserInfo(tb, app, userRec.Id)
		userInfo.Set("series_public", true)
		userInfo.Set("series", []string{seriesA.Id, seriesB.Id})
		if err := app.Save(userInfo); err != nil {
			tb.Fatalf("failed to save user_info: %v", err)
		}

		scenario.URL = "/user?id=" + userID
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		payload := decodeJSON(tb, res)

		if got := payload["series_public"]; got != true {
			tb.Fatalf("expected series_public true, got %v", got)
		}

		rawSeries, ok := payload["series"].([]any)
		if !ok {
			tb.Fatalf("expected series array, got %#v", payload["series"])
		}
		if len(rawSeries) != 2 {
			tb.Fatalf("expected 2 series entries, got %#v", rawSeries)
		}

		first, ok := rawSeries[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected series object, got %#v", rawSeries[0])
		}
		second, ok := rawSeries[1].(map[string]any)
		if !ok {
			tb.Fatalf("expected series object, got %#v", rawSeries[1])
		}

		if got := first["id"]; got != seriesA.Id {
			tb.Fatalf("expected first series id %q, got %v", seriesA.Id, got)
		}
		if got := first["en"]; got != "Dance Rush" {
			tb.Fatalf("expected first series en, got %v", got)
		}
		if got := first["kr"]; got != "댄스 러시" {
			tb.Fatalf("expected first series kr, got %v", got)
		}
		if got := first["jp"]; got != "ダンスラッシュ" {
			tb.Fatalf("expected first series jp, got %v", got)
		}
		if got := first["en_short"]; got != "Dance Rush short" {
			tb.Fatalf("expected first series en_short, got %v", got)
		}
		if got := first["kr_short"]; got != "댄스 러시 short" {
			tb.Fatalf("expected first series kr_short, got %v", got)
		}
		if got := first["jp_short"]; got != "ダンスラッシュ short" {
			tb.Fatalf("expected first series jp_short, got %v", got)
		}
		if got := second["id"]; got != seriesB.Id {
			tb.Fatalf("expected second series id %q, got %v", seriesB.Id, got)
		}
		if _, ok := first["manufacturer"]; !ok {
			tb.Fatalf("expected manufacturer key in first series: %#v", first)
		}
		if _, ok := second["manufacturer"]; !ok {
			tb.Fatalf("expected manufacturer key in second series: %#v", second)
		}
	}

	scenario.Test(t)
}

func TestGetUserByID_HidesPrivateSeries(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /user omits private series",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"id":"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	var userID string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		_, userRec := createAuthUser(tb, app, true)
		userID = userRec.Id

		series := seedGameSeries(tb, app, 3, "Hidden Mix", "히든 믹스", "ヒドゥンミックス")

		userInfo := ensureUserInfo(tb, app, userRec.Id)
		userInfo.Set("series_public", false)
		userInfo.Set("series", []string{series.Id})
		if err := app.Save(userInfo); err != nil {
			tb.Fatalf("failed to save user_info: %v", err)
		}

		scenario.URL = "/user?id=" + userID
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		payload := decodeJSON(tb, res)
		if got := payload["series_public"]; got != false {
			tb.Fatalf("expected series_public false, got %v", got)
		}
		if _, exists := payload["series"]; exists {
			tb.Fatalf("expected series to be omitted when series_public=false: %#v", payload)
		}
	}

	scenario.Test(t)
}

func TestGetUserByID_WithdrawnMasked(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /user masks withdrawn profile",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"withdrawn":true`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	var userID string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		_, userRec := createAuthUser(tb, app, true)
		userID = userRec.Id
		userRec.Set("username", "origin_user")
		userRec.Set("withdrawn", true)
		userRec.Set("withdrawnAt", time.Now().UTC())
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to mark withdrawn: %v", err)
		}

		userInfo := ensureUserInfo(tb, app, userRec.Id)
		userInfo.Set("nickname", "origin_nick")
		userInfo.Set("bio", "hidden")
		if err := app.Save(userInfo); err != nil {
			tb.Fatalf("failed to save user_info: %v", err)
		}

		scenario.URL = "/user?id=" + userID
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		payload := decodeJSON(tb, res)
		assertProfileShape(tb, payload)

		if got := payload["withdrawn"]; got != true {
			tb.Fatalf("expected withdrawn true, got %v", got)
		}
		if got := payload["series_public"]; got != false {
			tb.Fatalf("expected masked series_public false, got %v", got)
		}
		if got := payload["username"]; got != userhandler.WithdrawnDisplayName() {
			tb.Fatalf("expected masked username %q, got %v", userhandler.WithdrawnDisplayName(), got)
		}
		if got := payload["nickname"]; got != userhandler.WithdrawnDisplayName() {
			tb.Fatalf("expected masked nickname %q, got %v", userhandler.WithdrawnDisplayName(), got)
		}
		if got := payload["bio"]; got != "" {
			tb.Fatalf("expected masked bio empty, got %v", got)
		}
		if got := payload["avatar"]; got != "" {
			tb.Fatalf("expected masked avatar empty, got %v", got)
		}
	}

	scenario.Test(t)
}

func TestGetMe_WithdrawnMasked(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /user/me masks withdrawn profile",
		Method:         http.MethodGet,
		URL:            "/user/me",
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"withdrawn":true`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	headers := map[string]string{}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		token, userRec := createAuthUser(tb, app, true)
		userRec.Set("username", "withdrawn_me_user")
		userRec.Set("withdrawn", true)
		userRec.Set("withdrawnAt", time.Now().UTC())
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to mark withdrawn: %v", err)
		}

		userInfo := ensureUserInfo(tb, app, userRec.Id)
		userInfo.Set("nickname", "withdrawn_me_nick")
		userInfo.Set("bio", "hidden")
		if err := app.Save(userInfo); err != nil {
			tb.Fatalf("failed to save user_info: %v", err)
		}

		headers["Authorization"] = "Bearer " + token
		scenario.Headers = headers
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		payload := decodeJSON(tb, res)
		assertProfileShape(tb, payload)

		if got := payload["withdrawn"]; got != true {
			tb.Fatalf("expected withdrawn true, got %v", got)
		}
		if got := payload["series_public"]; got != false {
			tb.Fatalf("expected masked series_public false, got %v", got)
		}
		if got := payload["username"]; got != userhandler.WithdrawnDisplayName() {
			tb.Fatalf("expected masked username %q, got %v", userhandler.WithdrawnDisplayName(), got)
		}
		if got := payload["nickname"]; got != userhandler.WithdrawnDisplayName() {
			tb.Fatalf("expected masked nickname %q, got %v", userhandler.WithdrawnDisplayName(), got)
		}
		if got := payload["bio"]; got != "" {
			tb.Fatalf("expected masked bio empty, got %v", got)
		}
	}

	scenario.Test(t)
}

func TestGetUserByID_OnlyProfileFieldsExposed(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /user exposes profile fields only",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"id":"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	var userID string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		_, userRec := createAuthUser(tb, app, true)
		userID = userRec.Id
		scenario.URL = "/user?id=" + userID
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		body := decodeJSON(tb, res)
		assertProfileShape(tb, body)

		raw, err := json.Marshal(body)
		if err != nil {
			tb.Fatalf("failed to re-marshal payload: %v", err)
		}
		s := string(raw)
		if strings.Contains(s, "password") || strings.Contains(s, "tokenKey") || strings.Contains(s, "emailVisibility") {
			tb.Fatalf("payload contains sensitive fields: %s", s)
		}
	}

	scenario.Test(t)
}

func TestSignUp_Success(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "POST /user/signup initializes username and user_info",
		Method:         http.MethodPost,
		URL:            "/user/signup",
		Body:           strings.NewReader(`{"username":"Name101","nickname":"Nick101","bio":"hello signup","avatar":""}`),
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"success":true`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	headers := map[string]string{}
	var userID string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		token, userRec := createAuthUser(tb, app, false)
		userID = userRec.Id
		userRec.Set("username", "")
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to clear username: %v", err)
		}

		headers["Authorization"] = "Bearer " + token
		scenario.Headers = headers
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		payload := decodeJSON(tb, res)
		success, _ := payload["success"].(bool)
		if !success {
			tb.Fatalf("expected success=true, got %v", payload["success"])
		}

		profile, ok := payload["profile"].(map[string]any)
		if !ok {
			tb.Fatalf("expected profile object, got %#v", payload["profile"])
		}

		username, _ := profile["username"].(string)
		if username != "Name101" {
			tb.Fatalf("expected username Name101, got %q", username)
		}
		if got := profile["nickname"]; got != "Nick101" {
			tb.Fatalf("expected nickname Nick101, got %v", got)
		}
		if got := profile["bio"]; got != "hello signup" {
			tb.Fatalf("expected bio hello signup, got %v", got)
		}
		if got := profile["series_public"]; got != true {
			tb.Fatalf("expected series_public true, got %v", got)
		}

		userRec, err := app.FindRecordById("user", userID)
		if err != nil {
			tb.Fatalf("failed to load user: %v", err)
		}
		if got := userRec.GetString("username"); got != username {
			tb.Fatalf("expected saved username %q, got %q", username, got)
		}

		userInfo, err := app.FindRecordById("user_info", userID)
		if err != nil {
			tb.Fatalf("expected user_info to be created: %v", err)
		}
		if got := userInfo.GetString("nickname"); got != "Nick101" {
			tb.Fatalf("expected nickname Nick101, got %q", got)
		}
		if got := userInfo.GetString("bio"); got != "hello signup" {
			tb.Fatalf("expected bio hello signup, got %q", got)
		}
		if got := userInfo.GetBool("series_public"); got != true {
			tb.Fatalf("expected stored series_public true, got %v", got)
		}
	}

	scenario.Test(t)
}

func TestSignUp_ConflictWhenUsernameExists(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "POST /user/signup rejects existing username",
		Method:         http.MethodPost,
		URL:            "/user/signup",
		Body:           strings.NewReader(`{"username":"Taken101","nickname":"n","bio":"","avatar":""}`),
		ExpectedStatus: http.StatusConflict,
		ExpectedContent: []string{
			`"error":"signup requires empty username"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	headers := map[string]string{}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		token, _ := createAuthUser(tb, app, false)
		headers["Authorization"] = "Bearer " + token
		scenario.Headers = headers
	}

	scenario.Test(t)
}

func TestSignUp_ConflictWhenUserInfoExists(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "POST /user/signup rejects existing user_info",
		Method:         http.MethodPost,
		URL:            "/user/signup",
		Body:           strings.NewReader(`{"username":"newbie101","nickname":"n","bio":"","avatar":""}`),
		ExpectedStatus: http.StatusConflict,
		ExpectedContent: []string{
			`"error":"signup requires missing user_info"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	headers := map[string]string{}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		token, userRec := createAuthUser(tb, app, true)
		userRec.Set("username", "")
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to clear username: %v", err)
		}
		headers["Authorization"] = "Bearer " + token
		scenario.Headers = headers
	}

	scenario.Test(t)
}

func TestSignUp_InvalidUsername(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "POST /user/signup rejects invalid username",
		Method:         http.MethodPost,
		URL:            "/user/signup",
		Body:           strings.NewReader(`{"username":"bad_name","nickname":"n","bio":"","avatar":""}`),
		ExpectedStatus: http.StatusBadRequest,
		ExpectedContent: []string{
			`"error":"username must contain only letters and digits"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	headers := map[string]string{}
	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)
		token, userRec := createAuthUser(tb, app, false)
		userRec.Set("username", "")
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to clear username: %v", err)
		}
		headers["Authorization"] = "Bearer " + token
		scenario.Headers = headers
	}

	scenario.Test(t)
}

func TestSignUp_UsernameTooShort(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "POST /user/signup rejects too short username",
		Method:         http.MethodPost,
		URL:            "/user/signup",
		Body:           strings.NewReader(`{"username":"abc","nickname":"n","bio":"","avatar":""}`),
		ExpectedStatus: http.StatusBadRequest,
		ExpectedContent: []string{
			`"error":"username must be at least 4 characters"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	headers := map[string]string{}
	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)
		token, userRec := createAuthUser(tb, app, false)
		userRec.Set("username", "")
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to clear username: %v", err)
		}
		headers["Authorization"] = "Bearer " + token
		scenario.Headers = headers
	}

	scenario.Test(t)
}

func TestSignUp_UsernameTooLong(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "POST /user/signup rejects too long username",
		Method:         http.MethodPost,
		URL:            "/user/signup",
		Body:           strings.NewReader(`{"username":"abcdefghijklmnop","nickname":"n","bio":"","avatar":""}`),
		ExpectedStatus: http.StatusBadRequest,
		ExpectedContent: []string{
			`"error":"username must be at most 15 characters"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	headers := map[string]string{}
	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)
		token, userRec := createAuthUser(tb, app, false)
		userRec.Set("username", "")
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to clear username: %v", err)
		}
		headers["Authorization"] = "Bearer " + token
		scenario.Headers = headers
	}

	scenario.Test(t)
}

func TestSignUp_MultipartWithAvatar(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "POST /user/signup accepts multipart avatar file",
		Method:         http.MethodPost,
		URL:            "/user/signup",
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"success":true`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	headers := map[string]string{}
	var userID string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		token, userRec := createAuthUser(tb, app, false)
		userID = userRec.Id
		userRec.Set("username", "")
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to clear username: %v", err)
		}

		body, contentType := buildSignUpMultipart(tb, map[string]string{
			"username": "AvatarUser1",
			"nickname": "AvatarNick",
			"bio":      "has image",
		}, "avatar.png", pngFixtureBytes())
		scenario.Body = bytes.NewReader(body)

		headers["Authorization"] = "Bearer " + token
		headers["Content-Type"] = contentType
		scenario.Headers = headers
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		payload := decodeJSON(tb, res)
		profile, ok := payload["profile"].(map[string]any)
		if !ok {
			tb.Fatalf("expected profile object, got %#v", payload["profile"])
		}

		avatar, _ := profile["avatar"].(string)
		if avatar == "" {
			tb.Fatalf("expected non-empty avatar filename")
		}

		userInfo, err := app.FindRecordById("user_info", userID)
		if err != nil {
			tb.Fatalf("expected user_info to be created: %v", err)
		}
		files := userInfo.GetStringSlice("avatar")
		if len(files) != 1 || strings.TrimSpace(files[0]) == "" {
			tb.Fatalf("expected exactly one avatar filename, got %#v", files)
		}
		if got := userInfo.GetBool("series_public"); got != true {
			tb.Fatalf("expected stored series_public true, got %v", got)
		}
	}

	scenario.Test(t)
}

func buildSignUpMultipart(tb testing.TB, fields map[string]string, filename string, content []byte) ([]byte, string) {
	tb.Helper()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			tb.Fatalf("failed to write field %q: %v", k, err)
		}
	}
	if filename != "" {
		part, err := writer.CreateFormFile("avatar", filename)
		if err != nil {
			tb.Fatalf("failed to create avatar file part: %v", err)
		}
		if _, err := part.Write(content); err != nil {
			tb.Fatalf("failed to write avatar file part: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		tb.Fatalf("failed to close multipart writer: %v", err)
	}

	return buf.Bytes(), writer.FormDataContentType()
}

func pngFixtureBytes() []byte {
	return []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01,
	}
}
