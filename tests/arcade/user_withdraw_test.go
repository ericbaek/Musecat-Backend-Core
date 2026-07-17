package arcade_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pocketbase/pocketbase/tools/types"

	arcadeflag "github.com/ericbaek/musecat-backend-core/handlers/arcade/flag"
	arcadequery "github.com/ericbaek/musecat-backend-core/handlers/arcade/query"
	"github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/ericbaek/musecat-backend-core/testutil"
)

func newWithdrawTestApp(tb testing.TB) *tests.TestApp {
	app := testutil.NewTestApp(tb)
	user.RegisterHooks(app)
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/arcade", arcadequery.GetArcadeValues)

		authArcade := se.Router.Group("/arcade").Bind(
			apis.RequireAuth("user"),
			user.RequireActiveUser(),
		)
		authArcade.GET("/probe", func(re *core.RequestEvent) error {
			return re.JSON(http.StatusOK, map[string]any{"ok": true})
		})
		authArcade.POST("/flag", arcadeflag.CreateArcadeFlag)
		authArcade.POST("/flag/delete", arcadeflag.DeleteArcadeFlag)
		authArcade.POST("/flag/reaction", arcadeflag.UpdateArcadeFlagReaction)

		authUser := se.Router.Group("/user").Bind(apis.RequireAuth("user"))
		authUser.POST("/withdraw", user.Withdraw)

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

	if coll.Fields.GetByName("withdrawReason") == nil {
		if err := coll.Fields.AddMarshaledJSONAt(len(coll.Fields), []byte(`{
			"autogeneratePattern": "",
			"hidden": true,
			"id": "text_test_withdrawreason",
			"max": 0,
			"min": 0,
			"name": "withdrawReason",
			"pattern": "",
			"presentable": false,
			"primaryKey": false,
			"required": false,
			"system": false,
			"type": "text"
		}`)); err != nil {
			tb.Fatalf("failed to add withdrawReason field: %v", err)
		}
		changed = true
	}

	if changed {
		if err := app.Save(coll); err != nil {
			tb.Fatalf("failed to save user collection: %v", err)
		}
	}
}

func hashNormalizedEmailForTest(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(sum[:])
}

func seedUserBan(tb testing.TB, app *tests.TestApp, userID, hashedEmail, reason string, until time.Time) {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("user_ban")
	if err != nil {
		tb.Fatalf("failed to load user_ban collection: %v", err)
	}

	rec, err := app.FindRecordById("user_ban", userID)
	if err != nil {
		rec = core.NewRecord(coll)
		rec.Set("id", userID)
	}

	rec.Set("hashed_email", hashedEmail)
	rec.Set("reason", reason)
	if until.IsZero() {
		rec.Set("until", "")
	} else {
		rec.Set("until", until.UTC())
	}

	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save user_ban: %v", err)
	}
}

func TestUserWithdraw_Success(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "POST /user/withdraw anonymizes user",
		Method:         http.MethodPost,
		URL:            "/user/withdraw",
		Body:           strings.NewReader(`{"password":"secret123","reason":"privacy"}`),
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"success":true`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newWithdrawTestApp(tb)
		},
	}

	headers := map[string]string{}
	var userID string
	var originalEmail string
	var beforeWithdraw time.Time

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		token, userRec := createAuthUser(tb, app)
		originalEmail = userRec.Email()
		userRec.Set("username", "withdraw_user")
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to seed user username: %v", err)
		}
		userInfo := ensureUserInfo(tb, app, userRec.Id)
		userInfo.Set("nickname", "withdraw_nick")
		userInfo.Set("bio", "about me")
		if err := app.Save(userInfo); err != nil {
			tb.Fatalf("failed to seed user_info profile: %v", err)
		}

		externalAuth := core.NewExternalAuth(app)
		externalAuth.SetCollectionRef(userRec.Collection().Id)
		externalAuth.SetRecordRef(userRec.Id)
		externalAuth.SetProvider("google")
		externalAuth.SetProviderId("oauth-subject-123")
		if err := app.Save(externalAuth); err != nil {
			tb.Fatalf("failed to seed external auth: %v", err)
		}

		userID = userRec.Id
		beforeWithdraw = time.Now().UTC()
		headers["Authorization"] = "Bearer " + token
		scenario.Headers = headers
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		if got, _ := payload["success"].(bool); !got {
			tb.Fatalf("expected success=true, got %v", payload["success"])
		}
		if payload["withdrawnAt"] == nil {
			tb.Fatalf("expected withdrawnAt in response")
		}

		updated, err := app.FindRecordById("user", userID)
		if err != nil {
			tb.Fatalf("failed to load withdrawn user: %v", err)
		}
		if !updated.GetBool("withdrawn") {
			tb.Fatalf("expected withdrawn=true")
		}
		if updated.GetString("withdrawnAt") == "" {
			tb.Fatalf("expected withdrawnAt to be set")
		}
		if got := updated.GetString("withdrawReason"); got != "privacy" {
			tb.Fatalf("expected withdrawReason privacy, got %q", got)
		}
		if got := updated.Email(); got != fmt.Sprintf("deleted+%s@invalid.local", userID) {
			tb.Fatalf("unexpected anonymized email: %q", got)
		}
		if got := updated.GetString("username"); got != userID {
			tb.Fatalf("expected username to be user id %q, got %q", userID, got)
		}
		updatedInfo, err := app.FindRecordById("user_info", userID)
		if err != nil {
			tb.Fatalf("failed to load user_info: %v", err)
		}
		if got := updatedInfo.GetString("nickname"); got != user.WithdrawnDisplayName() {
			tb.Fatalf("expected nickname %q, got %q", user.WithdrawnDisplayName(), got)
		}
		if got := updatedInfo.GetString("bio"); got != "" {
			tb.Fatalf("expected bio empty, got %q", got)
		}
		if got := updated.EmailVisibility(); got {
			tb.Fatalf("expected emailVisibility false")
		}
		if got := updated.Verified(); got {
			tb.Fatalf("expected verified false")
		}

		externalAuths, err := app.FindAllExternalAuthsByRecord(updated)
		if err != nil {
			tb.Fatalf("failed to load external auths: %v", err)
		}
		if len(externalAuths) != 0 {
			tb.Fatalf("expected external auth links to be removed, got %d", len(externalAuths))
		}

		banRec, err := app.FindRecordById("user_ban", userID)
		if err != nil {
			tb.Fatalf("expected user_ban cooldown record: %v", err)
		}
		if got := banRec.GetString("hashed_email"); got != hashNormalizedEmailForTest(originalEmail) {
			tb.Fatalf("expected hashed_email for original email, got %q", got)
		}
		if got := banRec.GetString("reason"); got != "withdraw_cooldown" {
			tb.Fatalf("expected cooldown reason, got %q", got)
		}
		until, err := types.ParseDateTime(banRec.GetString("until"))
		if err != nil {
			tb.Fatalf("failed to parse cooldown until: %v", err)
		}
		if until.IsZero() {
			tb.Fatalf("expected cooldown until to be set")
		}
		minExpected := beforeWithdraw.AddDate(0, 0, 30).Add(-time.Minute)
		maxExpected := beforeWithdraw.AddDate(0, 0, 30).Add(time.Minute)
		if until.Time().Before(minExpected) || until.Time().After(maxExpected) {
			tb.Fatalf("expected cooldown around 30 days, got %s", until.Time().UTC())
		}
	}

	scenario.Test(t)
}

func TestUserWithdraw_InvalidPassword(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "POST /user/withdraw rejects invalid password",
		Method:         http.MethodPost,
		URL:            "/user/withdraw",
		Body:           strings.NewReader(`{"password":"wrong-pass"}`),
		ExpectedStatus: http.StatusUnauthorized,
		ExpectedContent: []string{
			`"error":"invalid credentials"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newWithdrawTestApp(tb)
		},
	}

	headers := map[string]string{}
	var userID string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)
		token, userRec := createAuthUser(tb, app)
		userID = userRec.Id
		headers["Authorization"] = "Bearer " + token
		scenario.Headers = headers
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, _ *http.Response) {
		tb.Helper()
		updated, err := app.FindRecordById("user", userID)
		if err != nil {
			tb.Fatalf("failed to load user: %v", err)
		}
		if updated.GetBool("withdrawn") {
			tb.Fatalf("expected withdrawn=false")
		}
	}

	scenario.Test(t)
}

func TestUserWithdraw_OAuthWithoutPassword(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "POST /user/withdraw allows oauth account without password",
		Method:         http.MethodPost,
		URL:            "/user/withdraw",
		Body:           strings.NewReader(`{"reason":"oauth only"}`),
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"success":true`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newWithdrawTestApp(tb)
		},
	}

	headers := map[string]string{}
	var userID string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		token, userRec := createAuthUser(tb, app)
		externalAuth := core.NewExternalAuth(app)
		externalAuth.SetCollectionRef(userRec.Collection().Id)
		externalAuth.SetRecordRef(userRec.Id)
		externalAuth.SetProvider("google")
		externalAuth.SetProviderId("oauth-subject-without-password")
		if err := app.Save(externalAuth); err != nil {
			tb.Fatalf("failed to seed external auth: %v", err)
		}

		userID = userRec.Id
		headers["Authorization"] = "Bearer " + token
		scenario.Headers = headers
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, _ *http.Response) {
		tb.Helper()
		updated, err := app.FindRecordById("user", userID)
		if err != nil {
			tb.Fatalf("failed to load user: %v", err)
		}
		if !updated.GetBool("withdrawn") {
			tb.Fatalf("expected withdrawn=true")
		}
	}

	scenario.Test(t)
}

func TestUserWithdraw_PasswordAccountWithoutPassword(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "POST /user/withdraw rejects password account without password",
		Method:         http.MethodPost,
		URL:            "/user/withdraw",
		Body:           strings.NewReader(`{"reason":"no password"}`),
		ExpectedStatus: http.StatusBadRequest,
		ExpectedContent: []string{
			`"error":"password is required for password accounts"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newWithdrawTestApp(tb)
		},
	}

	headers := map[string]string{}
	var userID string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)
		token, userRec := createAuthUser(tb, app)
		userID = userRec.Id
		headers["Authorization"] = "Bearer " + token
		scenario.Headers = headers
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, _ *http.Response) {
		tb.Helper()
		updated, err := app.FindRecordById("user", userID)
		if err != nil {
			tb.Fatalf("failed to load user: %v", err)
		}
		if updated.GetBool("withdrawn") {
			tb.Fatalf("expected withdrawn=false")
		}
	}

	scenario.Test(t)
}

func TestUserWithdraw_AlreadyWithdrawn(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "POST /user/withdraw returns 409 when already withdrawn",
		Method:         http.MethodPost,
		URL:            "/user/withdraw",
		Body:           strings.NewReader(`{"password":"secret123"}`),
		ExpectedStatus: http.StatusConflict,
		ExpectedContent: []string{
			`"error":"account already withdrawn"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newWithdrawTestApp(tb)
		},
	}

	headers := map[string]string{}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)
		_, userRec := createAuthUser(tb, app)
		userRec.Set("withdrawn", true)
		userRec.Set("withdrawnAt", time.Now().UTC())
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to mark user withdrawn: %v", err)
		}

		token, err := userRec.NewAuthToken()
		if err != nil {
			tb.Fatalf("failed to create token for withdrawn user: %v", err)
		}
		headers["Authorization"] = "Bearer " + token
		scenario.Headers = headers
	}

	scenario.Test(t)
}

func TestRequireActiveUser_BlocksWithdrawn(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /arcade/probe returns 403 for withdrawn users",
		Method:         http.MethodGet,
		URL:            "/arcade/probe",
		ExpectedStatus: http.StatusForbidden,
		ExpectedContent: []string{
			`"code":"ACCOUNT_WITHDRAWN"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newWithdrawTestApp(tb)
		},
	}

	headers := map[string]string{}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)
		_, userRec := createAuthUser(tb, app)
		userRec.Set("withdrawn", true)
		userRec.Set("withdrawnAt", time.Now().UTC())
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to mark user withdrawn: %v", err)
		}

		token, err := userRec.NewAuthToken()
		if err != nil {
			tb.Fatalf("failed to create token: %v", err)
		}
		headers["Authorization"] = "Bearer " + token
		scenario.Headers = headers
	}

	scenario.Test(t)
}

func TestRequireActiveUser_BlocksBanned(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /arcade/probe returns 403 for banned users",
		Method:         http.MethodGet,
		URL:            "/arcade/probe",
		ExpectedStatus: http.StatusForbidden,
		ExpectedContent: []string{
			`"code":"ACCOUNT_BANNED"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newWithdrawTestApp(tb)
		},
	}

	headers := map[string]string{}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)
		token, userRec := createAuthUser(tb, app)
		seedUserBan(tb, app, userRec.Id, "", "manual_suspend", time.Now().UTC().Add(24*time.Hour))
		headers["Authorization"] = "Bearer " + token
		scenario.Headers = headers
	}

	scenario.Test(t)
}

func TestArcadeCrudRequest_BlocksBannedUser(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "POST builtin arcade record create is locked for every client",
		Method:         http.MethodPost,
		URL:            "/api/collections/arcade/records",
		ExpectedStatus: http.StatusForbidden,
		ExpectedContent: []string{
			`"message":"Only superusers can perform this action."`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newWithdrawTestApp(tb)
		},
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)
		token, userRec := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token
		scenario.Headers = headers
		scenario.Body = strings.NewReader(fmt.Sprintf(`{"createdBy":"%s","public":false}`, userRec.Id))
	}

	scenario.Test(t)
}

func TestUserWithdraw_ActiveBanBecomesPermanentRejoinBlock(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "POST /user/withdraw upgrades active ban to permanent rejoin block",
		Method:         http.MethodPost,
		URL:            "/user/withdraw",
		Body:           strings.NewReader(`{"password":"secret123","reason":"privacy"}`),
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"success":true`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newWithdrawTestApp(tb)
		},
	}

	headers := map[string]string{}
	var userID string
	var originalEmail string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		token, userRec := createAuthUser(tb, app)
		userID = userRec.Id
		originalEmail = userRec.Email()
		seedUserBan(tb, app, userRec.Id, "", "manual_suspend", time.Now().UTC().Add(7*24*time.Hour))

		headers["Authorization"] = "Bearer " + token
		scenario.Headers = headers
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, _ *http.Response) {
		tb.Helper()
		banRec, err := app.FindRecordById("user_ban", userID)
		if err != nil {
			tb.Fatalf("expected user_ban record: %v", err)
		}
		if got := banRec.GetString("hashed_email"); got != hashNormalizedEmailForTest(originalEmail) {
			tb.Fatalf("expected hashed_email for original email, got %q", got)
		}
		if got := banRec.GetString("until"); got != "" {
			tb.Fatalf("expected permanent rejoin block, got until %q", got)
		}
		if got := banRec.GetString("reason"); got != "manual_suspend" {
			tb.Fatalf("expected original ban reason to be preserved, got %q", got)
		}
	}

	scenario.Test(t)
}

func TestGetArcadeValues_WithdrawnDisplayName(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /arcade expand photo returns withdrawn display name",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"photo":{`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newWithdrawTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		_, userRec := createAuthUser(tb, app)
		userRec.Set("username", "before_user")
		userRec.Set("withdrawn", true)
		userRec.Set("withdrawnAt", time.Now().UTC())
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to update user: %v", err)
		}
		userInfo := ensureUserInfo(tb, app, userRec.Id)
		userInfo.Set("nickname", "before")
		if err := app.Save(userInfo); err != nil {
			tb.Fatalf("failed to update user_info: %v", err)
		}

		arcadeID, _ := seedPublicArcade(tb, app, userRec.Id, arcadeSeed{
			Name:     "Withdrawn Photo Arcade",
			Address:  "Display Street",
			Nickname: []string{"Display"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		atomID := seedPhotoAtom(tb, app, arcadeID, userRec.Id, true)

		photoColl, err := app.FindCollectionByNameOrId("arcade_photo")
		if err != nil {
			tb.Fatalf("failed to load arcade_photo collection: %v", err)
		}
		photoRec := core.NewRecord(photoColl)
		photoRec.Set("arcade", arcadeID)
		photoRec.Set("photos", []string{atomID})
		photoRec.Set("createdBy", userRec.Id)
		if err := app.Save(photoRec); err != nil {
			tb.Fatalf("failed to save arcade_photo: %v", err)
		}

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		arcadeRec.Set("photo", photoRec.Id)
		if err := app.Save(arcadeRec); err != nil {
			tb.Fatalf("failed to link photo: %v", err)
		}

		scenario.URL = fmt.Sprintf("/arcade?id=%s&expand=photo", arcadeID)
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		photoObj, ok := payload["photo"].(map[string]any)
		if !ok {
			tb.Fatalf("expected photo object, got %T", payload["photo"])
		}
		items, ok := photoObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected one photo item, got %T %#v", photoObj["items"], photoObj["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected item object, got %T", items[0])
		}
		createdBy, ok := item["createdBy"].(map[string]any)
		if !ok {
			tb.Fatalf("expected createdBy object, got %T", item["createdBy"])
		}
		if got := createdBy["nickname"]; got != user.WithdrawnDisplayName() {
			tb.Fatalf("expected nickname %q, got %v", user.WithdrawnDisplayName(), got)
		}
		if got := createdBy["username"]; got != user.WithdrawnDisplayName() {
			tb.Fatalf("expected username %q, got %v", user.WithdrawnDisplayName(), got)
		}
	}

	scenario.Test(t)
}

func TestGetArcadeValues_FlagDisplayNameForWithdrawnUser(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:            "GET /arcade expand game returns withdrawn display for flags and reactions",
		Method:          http.MethodGet,
		ExpectedStatus:  http.StatusOK,
		ExpectedContent: []string{`"reactions":[{`},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newWithdrawTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		_, userRec := createAuthUser(tb, app)
		userRec.Set("withdrawn", true)
		userRec.Set("withdrawnAt", time.Now().UTC())
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to set withdrawn user: %v", err)
		}

		arcadeID, _ := seedPublicArcade(tb, app, userRec.Id, arcadeSeed{
			Name:     "Flag Display Arcade",
			Address:  "Flag Street",
			Nickname: []string{"FlagDisplay"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		versionID := seedGameSeriesVersion(tb, app)
		seedGameAtom(tb, app, arcadeID, versionID, map[string]any{"coin": 500})

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		gameID := arcadeRec.GetString("game")
		if gameID == "" {
			tb.Fatalf("expected arcade.game relation to be set")
		}
		atomRec, err := app.FindFirstRecordByFilter("arcade_game_atoms", "molecule={:id}", map[string]any{"id": gameID})
		if err != nil {
			tb.Fatalf("failed to load game atom: %v", err)
		}

		flagColl, err := app.FindCollectionByNameOrId("arcade_flag")
		if err != nil {
			tb.Fatalf("failed to load arcade_flag collection: %v", err)
		}
		flagRec := core.NewRecord(flagColl)
		flagRec.Set("arcade", arcadeID)
		flagRec.Set("disruption", "major")
		flagRec.Set("solved", false)
		flagRec.Set("message", "broken")
		photo, err := filesystem.NewFileFromBytes(pngFixtureBytes(), "flag-withdraw-1.png")
		if err != nil {
			tb.Fatalf("failed to create flag photo: %v", err)
		}
		flagRec.Set("photos", []*filesystem.File{photo})
		flagRec.Set("createdBy", userRec.Id)
		if err := app.Save(flagRec); err != nil {
			tb.Fatalf("failed to save flag: %v", err)
		}

		reactionColl, err := app.FindCollectionByNameOrId("arcade_flag_reaction")
		if err != nil {
			tb.Fatalf("failed to load reaction collection: %v", err)
		}
		reactionRec := core.NewRecord(reactionColl)
		reactionRec.Set("flag", flagRec.Id)
		reactionRec.Set("reaction", "fixed")
		reactionRec.Set("createdBy", userRec.Id)
		if err := app.Save(reactionRec); err != nil {
			tb.Fatalf("failed to save reaction: %v", err)
		}

		atomRec.Set("flags", []string{flagRec.Id})
		if err := app.Save(atomRec); err != nil {
			tb.Fatalf("failed to link flag to atom: %v", err)
		}

		scenario.URL = fmt.Sprintf("/arcade?id=%s&expand=game", arcadeID)
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected game object, got %T", payload["game"])
		}
		items, ok := gameObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected one game item, got %T %#v", gameObj["items"], gameObj["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected item object, got %T", items[0])
		}
		flags, ok := item["flags"].([]any)
		if !ok || len(flags) != 1 {
			tb.Fatalf("expected one flag, got %T %#v", item["flags"], item["flags"])
		}
		flagObj, ok := flags[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected flag object, got %T", flags[0])
		}
		if _, ok := flagObj["createdByDisplay"]; ok {
			tb.Fatalf("expected createdByDisplay to be removed from flag payload")
		}
		if got := flagObj["createdBy"]; got == "" {
			tb.Fatalf("expected flag createdBy id to be present")
		}
		if photos, ok := flagObj["photos"].([]any); !ok || len(photos) != 1 {
			tb.Fatalf("expected one flag photo, got %T %#v", flagObj["photos"], flagObj["photos"])
		}
		if _, ok := flagObj["createdByProfile"]; ok {
			tb.Fatalf("expected flag createdByProfile to be removed from payload")
		}
		reactions, ok := flagObj["reactions"].([]any)
		if !ok || len(reactions) != 1 {
			tb.Fatalf("expected one reaction, got %T %#v", flagObj["reactions"], flagObj["reactions"])
		}
		reactionObj, ok := reactions[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected reaction object, got %T", reactions[0])
		}
		if _, ok := reactionObj["createdByDisplay"]; ok {
			tb.Fatalf("expected createdByDisplay to be removed from reaction payload")
		}
		if got := reactionObj["createdBy"]; got == "" {
			tb.Fatalf("expected reaction createdBy id to be present")
		}
		if _, ok := reactionObj["createdByProfile"]; ok {
			tb.Fatalf("expected reaction createdByProfile to be removed from payload")
		}
	}

	scenario.Test(t)
}
