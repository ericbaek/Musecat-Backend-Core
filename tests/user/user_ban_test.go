package user_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/tools/auth"
	"github.com/pocketbase/pocketbase/tools/router"
)

func hashNormalizedEmailForUserTest(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(sum[:])
}

func seedUserBanForUserTest(tb testing.TB, app *tests.TestApp, userID, hashedEmail, reason string, until time.Time) {
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

func prepareWithdrawnEmailBan(tb testing.TB, app *tests.TestApp, until time.Time) string {
	tb.Helper()

	_, oldUser := createAuthUser(tb, app, false)
	originalEmail := oldUser.Email()
	oldUser.SetEmail(fmt.Sprintf("deleted+%s@invalid.local", oldUser.Id))
	if err := app.Save(oldUser); err != nil {
		tb.Fatalf("failed to anonymize original user email: %v", err)
	}

	seedUserBanForUserTest(tb, app, oldUser.Id, hashNormalizedEmailForUserTest(originalEmail), "withdraw_cooldown", until)
	return originalEmail
}

func TestUserBan_BlocksAuthCreateByHashedEmail(t *testing.T) {
	app := newUserFetchTestApp(t)

	email := prepareWithdrawnEmailBan(t, app, time.Now().UTC().Add(24*time.Hour))

	coll, err := app.FindCollectionByNameOrId("user")
	if err != nil {
		t.Fatalf("failed to load user collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.SetEmail(email)
	rec.Set("username", fmt.Sprintf("banned_rejoin_%d", time.Now().UnixNano()))
	rec.SetPassword("secret123")

	err = app.Save(rec)
	if err == nil {
		t.Fatalf("expected banned email auth create to fail")
	}
	if !strings.Contains(err.Error(), "blocked") && !strings.Contains(err.Error(), "validation_banned_email") {
		t.Fatalf("expected blocked email error, got %v", err)
	}
}

func TestUserBan_ExpiredHashedEmailAllowsAuthCreate(t *testing.T) {
	app := newUserFetchTestApp(t)

	email := prepareWithdrawnEmailBan(t, app, time.Now().UTC().Add(-24*time.Hour))

	coll, err := app.FindCollectionByNameOrId("user")
	if err != nil {
		t.Fatalf("failed to load user collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.SetEmail(email)
	rec.Set("username", fmt.Sprintf("allowed_rejoin_%d", time.Now().UnixNano()))
	rec.SetPassword("secret123")

	if err := app.Save(rec); err != nil {
		t.Fatalf("expected expired ban to allow auth create: %v", err)
	}
}

func TestUserBan_SignUpBlockedForBannedUser(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "POST /user/signup returns 403 for banned user",
		Method:         http.MethodPost,
		URL:            "/user/signup",
		Body:           strings.NewReader(`{"username":"Banned101","nickname":"BannedNick","bio":"banned bio"}`),
		ExpectedStatus: http.StatusForbidden,
		ExpectedContent: []string{
			`"code":"ACCOUNT_BANNED"`,
			`"reason":"manual_suspend"`,
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

		seedUserBanForUserTest(tb, app, userRec.Id, "", "manual_suspend", time.Now().UTC().Add(48*time.Hour))

		headers["Authorization"] = "Bearer " + token
		scenario.Headers = headers
	}

	scenario.Test(t)
}

func TestUserBan_SignUpWithdrawnCooldownReturnsBanDetails(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "POST /user/signup reports active withdrawal cooldown",
		Method:         http.MethodPost,
		URL:            "/user/signup",
		Body:           strings.NewReader(`{"username":"Banned101","nickname":"BannedNick"}`),
		ExpectedStatus: http.StatusForbidden,
		ExpectedContent: []string{
			`"code":"ACCOUNT_BANNED"`,
			`"reason":"withdraw_cooldown"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}
	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)
		token, userRec := createAuthUser(tb, app, false)
		userRec.Set("withdrawn", true)
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to mark user withdrawn: %v", err)
		}
		seedUserBanForUserTest(tb, app, userRec.Id, hashNormalizedEmailForUserTest(userRec.Email()), "withdraw_cooldown", time.Now().UTC().Add(24*time.Hour))
		scenario.Headers = map[string]string{"Authorization": "Bearer " + token}
	}

	scenario.Test(t)
}

func TestUserBan_PasswordAuthBlockedByUserID(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "POST /api/collections/user/auth-with-password returns 400 for user banned by ID",
		Method:         http.MethodPost,
		URL:            "/api/collections/user/auth-with-password",
		ExpectedStatus: http.StatusBadRequest,
		ExpectedContent: []string{
			`"code":"ACCOUNT_BANNED"`,
			`"reason":"terms_violation"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		userColl, err := app.FindCollectionByNameOrId("user")
		if err != nil {
			tb.Fatalf("failed to find user collection: %v", err)
		}
		userColl.PasswordAuth.Enabled = true
		if err := app.Save(userColl); err != nil {
			tb.Fatalf("failed to enable password auth: %v", err)
		}

		_, userRec := createAuthUser(tb, app, false)
		seedUserBanForUserTest(tb, app, userRec.Id, "", "terms_violation", time.Now().UTC().Add(72*time.Hour))

		scenario.Body = strings.NewReader(fmt.Sprintf(`{"identity":%q,"password":"secret123"}`, userRec.Email()))
	}

	scenario.Test(t)
}

func assertOAuthBanResponse(tb testing.TB, app *tests.TestApp, userRec *core.Record, email, reason string) {
	tb.Helper()
	coll, err := app.FindCollectionByNameOrId("user")
	if err != nil {
		tb.Fatalf("failed to load user collection: %v", err)
	}
	response := httptest.NewRecorder()
	event := &core.RecordAuthWithOAuth2RequestEvent{
		RequestEvent: &core.RequestEvent{
			App: app,
			Event: router.Event{
				Response: response,
				Request:  httptest.NewRequest(http.MethodPost, "/api/collections/user/auth-with-oauth2", nil),
			},
		},
		Record:     userRec,
		OAuth2User: &auth.AuthUser{Email: email},
	}
	event.Collection = coll

	err = app.OnRecordAuthWithOAuth2Request().Trigger(event, func(e *core.RecordAuthWithOAuth2RequestEvent) error {
		tb.Error("banned account reached OAuth authentication")
		return nil
	})
	if err != nil {
		tb.Fatalf("OAuth hook failed: %v", err)
	}
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"ACCOUNT_BANNED"`) || !strings.Contains(response.Body.String(), `"reason":"`+reason+`"`) {
		tb.Fatalf("expected structured ban response, got status %d body %s", response.Code, response.Body.String())
	}
}

func TestUserBan_OAuthBlockedByUserID(t *testing.T) {
	app := newUserFetchTestApp(t)
	_, userRec := createAuthUser(t, app, false)
	seedUserBanForUserTest(t, app, userRec.Id, "", "manual_suspend", time.Now().UTC().Add(48*time.Hour))
	assertOAuthBanResponse(t, app, userRec, userRec.Email(), "manual_suspend")
}

func TestUserBan_OAuthBlockedByExistingRecordEmail(t *testing.T) {
	app := newUserFetchTestApp(t)
	_, userRec := createAuthUser(t, app, false)
	_, otherUser := createAuthUser(t, app, false)
	seedUserBanForUserTest(t, app, otherUser.Id, hashNormalizedEmailForUserTest(userRec.Email()), "manual_suspend", time.Now().UTC().Add(48*time.Hour))
	assertOAuthBanResponse(t, app, userRec, "", "manual_suspend")
}

func TestUserBan_OAuthNewAccountBlockedByActiveHashedEmail(t *testing.T) {
	app := newUserFetchTestApp(t)
	_, expiredUser := createAuthUser(t, app, false)
	_, activeUser := createAuthUser(t, app, false)
	email := "banned-rejoin@example.com"
	hash := hashNormalizedEmailForUserTest(email)
	seedUserBanForUserTest(t, app, expiredUser.Id, hash, "withdraw_cooldown", time.Now().UTC().Add(-24*time.Hour))
	seedUserBanForUserTest(t, app, activeUser.Id, hash, "withdraw_cooldown", time.Now().UTC().Add(24*time.Hour))

	assertOAuthBanResponse(t, app, nil, email, "withdraw_cooldown")
}

func TestUserBan_AuthRefreshBlockedByUserID(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "POST /api/collections/user/auth-refresh returns structured ban",
		Method:         http.MethodPost,
		URL:            "/api/collections/user/auth-refresh",
		ExpectedStatus: http.StatusBadRequest,
		ExpectedContent: []string{
			`"code":"ACCOUNT_BANNED"`,
			`"reason":"manual_suspend"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}
	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		token, userRec := createAuthUser(tb, app, false)
		seedUserBanForUserTest(tb, app, userRec.Id, "", "manual_suspend", time.Now().UTC().Add(48*time.Hour))
		scenario.Headers = map[string]string{"Authorization": "Bearer " + token}
	}

	scenario.Test(t)
}
