package user_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
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
