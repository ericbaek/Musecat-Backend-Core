package user

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// CreateTestAccount creates an ordinary account and its profile atomically.
func CreateTestAccount(re *core.RequestEvent) error {
	allowed, err := hasActiveStaffAccess(re.App, re.Auth)
	if err != nil {
		return re.JSON(502, map[string]any{"error": "failed to verify staff access"})
	}
	if !allowed {
		return re.JSON(403, map[string]any{"error": "developer or moderator access required"})
	}
	var input struct {
		Email    string `json:"email"`
		Username string `json:"username"`
		Password string `json:"password"`
		Nickname string `json:"nickname"`
	}
	if err := json.NewDecoder(re.Request.Body).Decode(&input); err != nil {
		return re.JSON(400, map[string]any{"error": "invalid JSON body", "code": "invalid_input"})
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.Username = strings.TrimSpace(input.Username)
	input.Nickname = strings.TrimSpace(input.Nickname)
	passwordLength := utf8.RuneCountInString(input.Password)
	address, emailErr := mail.ParseAddress(input.Email)
	if emailErr != nil || address.Address != input.Email || len(input.Email) > 254 ||
		len(input.Username) < 4 || len(input.Username) > signupUsernameMaxLength || !signupUsernamePattern.MatchString(input.Username) ||
		passwordLength < 8 || passwordLength > 71 || utf8.RuneCountInString(input.Nickname) < 1 || utf8.RuneCountInString(input.Nickname) > 25 {
		return re.JSON(400, map[string]any{"error": "invalid email, username, password or nickname", "code": "invalid_input"})
	}
	for _, r := range input.Nickname {
		if (r >= 0x1F1E6 && r <= 0x1F1FF) || (r >= 0xE0020 && r <= 0xE007F) {
			return re.JSON(400, map[string]any{"error": "nickname cannot contain flag emoji", "code": "invalid_input"})
		}
	}
	duplicate := errors.New("duplicate identity")
	var result map[string]string
	err = re.App.RunInTransaction(func(app core.App) error {
		existing, err := app.FindFirstRecordByFilter(CollectionUser, "email = {:email} || username:lower = {:username}", dbx.Params{"email": input.Email, "username": strings.ToLower(input.Username)})
		if err != nil && !isNotFoundError(err) {
			return err
		}
		if existing != nil {
			return duplicate
		}
		users, err := app.FindCollectionByNameOrId(CollectionUser)
		if err != nil {
			return err
		}
		account := core.NewRecord(users)
		account.SetEmail(input.Email)
		account.SetPassword(input.Password)
		account.Set("username", input.Username)
		if err := app.Save(account); err != nil {
			if isTestAccountIdentityUniqueConstraintError(err) {
				return duplicate
			}
			return err
		}
		profiles, err := app.FindCollectionByNameOrId(CollectionUserInfo)
		if err != nil {
			return err
		}
		profile := core.NewRecord(profiles)
		profile.Set("id", account.Id)
		profile.Set("nickname", input.Nickname)
		profile.Set("series_public", true)
		profile.Set("visit_visibility", "summary")
		profile.Set("favorite_visibility", "private")
		profile.Set("countries", []string{})
		profile.Set("country_mode", countryModeAuto)
		profile.Set("auto_primary_country", "")
		profile.Set("avatar", []string{})
		if err := app.Save(profile); err != nil {
			return err
		}
		result = map[string]string{"id": account.Id, "email": account.Email(), "username": input.Username, "nickname": input.Nickname}
		return nil
	})
	if errors.Is(err, duplicate) {
		return re.JSON(409, map[string]any{"error": "email or username already taken", "code": "duplicate_identity"})
	}
	if err != nil {
		return re.JSON(502, map[string]any{"error": "failed to create test account"})
	}
	return re.JSON(http.StatusCreated, result)
}

func isTestAccountIdentityUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "idx_email__pb_users_auth_") ||
		strings.Contains(message, "idx_username_lower__pb_users_auth_") ||
		strings.Contains(message, "unique constraint failed: index")
}
