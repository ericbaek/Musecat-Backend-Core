package user

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

const withdrawnDisplayName = "탈퇴한 사용자"

func WithdrawnDisplayName() string {
	return withdrawnDisplayName
}

type withdrawBody struct {
	Password string `json:"password"`
	Reason   string `json:"reason"`
}

// Withdraw anonymizes the authenticated user and invalidates active sessions.
func Withdraw(re *core.RequestEvent) error {
	if re.Auth == nil {
		return re.JSON(http.StatusUnauthorized, map[string]any{
			"error": "authentication required",
		})
	}

	var body withdrawBody
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}

	if re.Auth.GetBool("withdrawn") {
		return re.JSON(http.StatusConflict, map[string]any{
			"error": "account already withdrawn",
		})
	}

	externalAuths, err := re.App.FindAllExternalAuthsByRecord(re.Auth)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "withdraw failed",
			"details": fmt.Sprintf("failed to resolve external auths: %v", err),
		})
	}

	password := strings.TrimSpace(body.Password)
	isOAuthAccount := len(externalAuths) > 0

	if password == "" && !isOAuthAccount {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "password is required for password accounts",
		})
	}

	if password != "" && !re.Auth.ValidatePassword(password) {
		return re.JSON(http.StatusUnauthorized, map[string]any{
			"error": "invalid credentials",
		})
	}

	withdrawnAt := time.Now().UTC()

	err = re.App.RunInTransaction(func(txApp core.App) error {
		userRec, err := txApp.FindRecordById(CollectionUser, re.Auth.Id)
		if err != nil {
			return fmt.Errorf("failed to load user: %w", err)
		}
		if userRec.GetBool("withdrawn") {
			return fmt.Errorf("already_withdrawn")
		}

		hashedEmail := hashNormalizedEmail(userRec.Email())
		existingBan, err := findBanByUserID(txApp, userRec.Id)
		if err != nil {
			return fmt.Errorf("failed to load user_ban: %w", err)
		}
		if existingBan != nil && isBanActive(existingBan.GetString("until"), withdrawnAt) {
			// Existing active moderation becomes a permanent rejoin block on withdraw.
			if _, err := upsertUserBanByUserID(txApp, userRec.Id, hashedEmail, existingBan.GetString("reason"), time.Time{}); err != nil {
				return fmt.Errorf("failed to upgrade user_ban: %w", err)
			}
		} else {
			cooldownUntil := withdrawnAt.AddDate(0, 0, 30)
			if _, err := upsertUserBanByUserID(txApp, userRec.Id, hashedEmail, withdrawCooldownReason, cooldownUntil); err != nil {
				return fmt.Errorf("failed to save user_ban cooldown: %w", err)
			}
		}

		tombstoneEmail := fmt.Sprintf("deleted+%s@invalid.local", userRec.Id)
		userRec.SetEmail(tombstoneEmail)
		userRec.Set("username", userRec.Id)
		userRec.SetEmailVisibility(false)
		userRec.SetVerified(false)
		userRec.Set("withdrawn", true)
		userRec.Set("withdrawnAt", withdrawnAt)
		if strings.TrimSpace(body.Reason) != "" {
			userRec.Set("withdrawReason", strings.TrimSpace(body.Reason))
		} else {
			userRec.Set("withdrawReason", "")
		}
		userRec.SetRandomPassword()
		userRec.RefreshTokenKey()

		if err := txApp.Save(userRec); err != nil {
			return fmt.Errorf("failed to update user: %w", err)
		}

		userInfoRec, err := txApp.FindRecordById(CollectionUserInfo, userRec.Id)
		if err != nil {
			userInfoColl, collErr := txApp.FindCollectionByNameOrId(CollectionUserInfo)
			if collErr != nil {
				return fmt.Errorf("failed to load user_info collection: %w", collErr)
			}

			userInfoRec = core.NewRecord(userInfoColl)
			userInfoRec.Set("id", userRec.Id)
		}
		userInfoRec.Set("nickname", withdrawnDisplayName)
		userInfoRec.Set("bio", "")
		userInfoRec.Set("avatar", []string{})
		if err := txApp.Save(userInfoRec); err != nil {
			return fmt.Errorf("failed to update user_info: %w", err)
		}

		// Unlink all OAuth providers so the same external account can sign up again later.
		externalAuths, err := txApp.FindAllExternalAuthsByRecord(userRec)
		if err != nil {
			return fmt.Errorf("failed to load external auths: %w", err)
		}
		for _, ea := range externalAuths {
			if ea == nil {
				continue
			}
			if err := txApp.Delete(ea); err != nil {
				return fmt.Errorf("failed to unlink external auth: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		if err.Error() == "already_withdrawn" {
			return re.JSON(http.StatusConflict, map[string]any{
				"error": "account already withdrawn",
			})
		}
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "withdraw failed",
			"details": err.Error(),
		})
	}

	return re.JSON(http.StatusOK, map[string]any{
		"success":     true,
		"withdrawnAt": withdrawnAt,
	})
}
