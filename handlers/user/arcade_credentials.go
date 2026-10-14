package user

import (
	"encoding/json"
	"errors"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

func isOfficialArcadeAccount(record *core.Record) bool {
	if record == nil || record.Collection().Name != CollectionUser {
		return false
	}
	for _, tag := range record.GetStringSlice("tags") {
		if strings.EqualFold(strings.TrimSpace(tag), "arcade_owner") {
			return true
		}
	}
	return false
}

// UpdateArcadeCredentials changes only the caller's official account credentials.
func UpdateArcadeCredentials(re *core.RequestEvent) error {
	if !isOfficialArcadeAccount(re.Auth) {
		return re.JSON(403, map[string]any{"error": "official arcade account required", "code": "not_official_account"})
	}
	var input struct {
		CurrentPassword string `json:"currentPassword"`
		Email           string `json:"email"`
		NewPassword     string `json:"newPassword"`
	}
	if err := json.NewDecoder(re.Request.Body).Decode(&input); err != nil {
		return re.JSON(400, map[string]any{"error": "invalid JSON body", "code": "invalid_input"})
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if input.CurrentPassword == "" || (input.Email == "" && input.NewPassword == "") {
		return re.JSON(400, map[string]any{"error": "current password and a change are required", "code": "invalid_input"})
	}
	if input.Email != "" {
		address, err := mail.ParseAddress(input.Email)
		if err != nil || address.Address != input.Email || len(input.Email) > 254 {
			return re.JSON(400, map[string]any{"error": "invalid email", "code": "invalid_input"})
		}
	}
	if input.NewPassword != "" && (utf8.RuneCountInString(input.NewPassword) < 8 || utf8.RuneCountInString(input.NewPassword) > 71) {
		return re.JSON(400, map[string]any{"error": "password must contain 8 to 71 characters", "code": "invalid_input"})
	}
	invalidPassword := errors.New("invalid password")
	duplicateEmail := errors.New("duplicate email")
	restrictedAccount := errors.New("restricted account")
	noChanges := errors.New("no changes")
	err := re.App.RunInTransaction(func(app core.App) error {
		account, err := app.FindRecordById(CollectionUser, re.Auth.Id)
		if err != nil {
			return err
		}
		_, code, err := checkAccountRestrictionWithBan(app, account, userBanNow())
		if err != nil {
			return err
		}
		if code != "" || !isOfficialArcadeAccount(account) {
			return restrictedAccount
		}
		if !account.ValidatePassword(input.CurrentPassword) {
			return invalidPassword
		}
		changeEmail := input.Email != "" && input.Email != account.Email()
		changePassword := input.NewPassword != "" && !account.ValidatePassword(input.NewPassword)
		if !changeEmail && !changePassword {
			return noChanges
		}
		if changeEmail {
			existing, err := app.FindFirstRecordByFilter(CollectionUser, "email = {:email} && id != {:id}", dbx.Params{"email": input.Email, "id": account.Id})
			if err != nil && !isNotFoundError(err) {
				return err
			}
			if existing != nil {
				return duplicateEmail
			}
			account.SetEmail(input.Email)
			account.SetVerified(false)
			account.SetEmailVisibility(false)
		}
		if changePassword {
			account.SetPassword(input.NewPassword)
		}
		account.RefreshTokenKey()
		return app.Save(account)
	})
	switch {
	case errors.Is(err, invalidPassword):
		return re.JSON(401, map[string]any{"error": "invalid current password", "code": "invalid_password"})
	case errors.Is(err, duplicateEmail):
		return re.JSON(409, map[string]any{"error": "email already taken", "code": "duplicate_email"})
	case errors.Is(err, restrictedAccount):
		return re.JSON(403, map[string]any{"error": "active official arcade account required", "code": "not_official_account"})
	case errors.Is(err, noChanges):
		return re.JSON(400, map[string]any{"error": "no changes", "code": "no_changes"})
	case err != nil:
		return re.JSON(502, map[string]any{"error": "failed to update account credentials"})
	default:
		return re.JSON(200, map[string]any{"success": true})
	}
}
