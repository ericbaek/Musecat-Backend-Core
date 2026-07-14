package user

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/types"
)

const (
	AccountWithdrawnCode    = "ACCOUNT_WITHDRAWN"
	AccountBannedCode       = "ACCOUNT_BANNED"
	withdrawCooldownReason  = "withdraw_cooldown"
	userBanBlockedErrorText = "account banned"
)

var userBanNow = func() time.Time {
	return time.Now().UTC()
}

var arcadeWriteProtectedCollections = []string{
	"arcade",
	"arcade_basic",
	"arcade_hour",
	"arcade_sns",
	"arcade_sns_atoms",
	"arcade_gtk",
	"arcade_gtk_atoms",
	"arcade_game",
	"arcade_game_atoms",
	"arcade_photo",
	"arcade_photo_atoms",
	"arcade_flag",
	"arcade_flag_reaction",
	"arcade_ticket_request",
	"arcade_changelog",
}

func RegisterHooks(app core.App) {
	registerUserBanHooks(app)
}

func registerUserBanHooks(app core.App) {
	app.OnRecordCreate(CollectionUser).Bind(&hook.Handler[*core.RecordEvent]{
		Id: "blockBannedUserAuthCreate",
		Func: func(e *core.RecordEvent) error {
			hashedEmail := hashNormalizedEmail(e.Record.Email())
			if hashedEmail == "" {
				return e.Next()
			}

			banRec, err := findActiveBanByHashedEmail(e.App, hashedEmail, userBanNow())
			if err != nil {
				return fmt.Errorf("failed to check user ban: %w", err)
			}
			if banRec != nil {
				return validation.Errors{
					"email": validation.NewError("validation_banned_email", "This email is blocked."),
				}
			}

			return e.Next()
		},
	})

	app.OnRecordAuthWithOAuth2Request(CollectionUser).Bind(&hook.Handler[*core.RecordAuthWithOAuth2RequestEvent]{
		Id: "blockBannedUserOAuth2Auth",
		Func: func(e *core.RecordAuthWithOAuth2RequestEvent) error {
			email := ""
			if e.OAuth2User != nil {
				email = e.OAuth2User.Email
			}

			banRec, err := findActiveBanByHashedEmail(e.App, hashNormalizedEmail(email), userBanNow())
			if err != nil {
				return e.JSON(http.StatusBadGateway, map[string]any{
					"error":   "failed to check user ban",
					"details": err.Error(),
				})
			}
			if banRec != nil {
				return e.JSON(http.StatusBadRequest, buildBanAuthResponse(banRec))
			}

			return e.Next()
		},
	})

	app.OnRecordCreateRequest(arcadeWriteProtectedCollections...).Bind(&hook.Handler[*core.RecordRequestEvent]{
		Id:   "blockRestrictedArcadeCreateRequests",
		Func: enforceArcadeWriteAllowed,
	})
	app.OnRecordUpdateRequest(arcadeWriteProtectedCollections...).Bind(&hook.Handler[*core.RecordRequestEvent]{
		Id:   "blockRestrictedArcadeUpdateRequests",
		Func: enforceArcadeWriteAllowed,
	})
	app.OnRecordDeleteRequest(arcadeWriteProtectedCollections...).Bind(&hook.Handler[*core.RecordRequestEvent]{
		Id:   "blockRestrictedArcadeDeleteRequests",
		Func: enforceArcadeWriteAllowed,
	})
}

func enforceArcadeWriteAllowed(re *core.RecordRequestEvent) error {
	if re.Auth == nil || re.Auth.Collection().Name != CollectionUser {
		return re.Next()
	}

	errText, code, err := checkArcadeWriteRestriction(re.App, re.Auth, userBanNow())
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to verify account restriction",
			"details": err.Error(),
		})
	}
	if code != "" {
		return re.JSON(http.StatusForbidden, map[string]any{
			"error": errText,
			"code":  code,
		})
	}

	return re.Next()
}

func hashNormalizedEmail(email string) string {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" {
		return ""
	}

	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func isBanActive(until string, now time.Time) bool {
	trimmed := strings.TrimSpace(until)
	if trimmed == "" {
		return true
	}

	dt, err := types.ParseDateTime(trimmed)
	if err != nil || dt.IsZero() {
		return false
	}

	return dt.Time().After(now.UTC())
}

func findBanByUserID(app core.App, userID string) (*core.Record, error) {
	if app == nil || strings.TrimSpace(userID) == "" {
		return nil, nil
	}

	rec, err := app.FindRecordById(CollectionUserBan, userID)
	if err != nil {
		if isNotFoundError(err) {
			return nil, nil
		}
		return nil, err
	}

	return rec, nil
}

func findActiveBanByHashedEmail(app core.App, hashedEmail string, now time.Time) (*core.Record, error) {
	if app == nil || strings.TrimSpace(hashedEmail) == "" {
		return nil, nil
	}

	recs, err := app.FindRecordsByFilter(
		CollectionUserBan,
		"hashed_email = {:hashed_email}",
		"-updated",
		0,
		0,
		map[string]any{"hashed_email": hashedEmail},
	)
	if err != nil {
		return nil, err
	}

	for _, rec := range recs {
		if rec != nil && isBanActive(rec.GetString("until"), now) {
			return rec, nil
		}
	}

	return nil, nil
}

func upsertUserBanByUserID(
	app core.App,
	userID string,
	hashedEmail string,
	reason string,
	until time.Time,
) (*core.Record, error) {
	if app == nil {
		return nil, fmt.Errorf("app is required")
	}
	if strings.TrimSpace(userID) == "" {
		return nil, fmt.Errorf("user id is required")
	}

	banRec, err := findBanByUserID(app, userID)
	if err != nil {
		return nil, err
	}

	if banRec == nil {
		coll, err := app.FindCollectionByNameOrId(CollectionUserBan)
		if err != nil {
			return nil, fmt.Errorf("failed to find user_ban collection: %w", err)
		}

		banRec = core.NewRecord(coll)
		banRec.Set("id", userID)
	}

	if strings.TrimSpace(hashedEmail) != "" {
		banRec.Set("hashed_email", hashedEmail)
	}

	banRec.Set("reason", strings.TrimSpace(reason))
	if until.IsZero() {
		banRec.Set("until", "")
	} else {
		banRec.Set("until", until.UTC())
	}

	if err := app.Save(banRec); err != nil {
		return nil, err
	}

	return banRec, nil
}

func checkArcadeWriteRestriction(app core.App, authRec *core.Record, now time.Time) (string, string, error) {
	if authRec == nil {
		return "", "", nil
	}

	if authRec.GetBool("withdrawn") {
		return "account withdrawn", AccountWithdrawnCode, nil
	}

	banRec, err := findBanByUserID(app, authRec.Id)
	if err != nil {
		return "", "", err
	}
	if banRec != nil && isBanActive(banRec.GetString("until"), now) {
		return userBanBlockedErrorText, AccountBannedCode, nil
	}

	return "", "", nil
}

func buildBanAuthResponse(banRec *core.Record) map[string]any {
	until := ""
	permanent := true
	reason := ""

	if banRec != nil {
		until = strings.TrimSpace(banRec.GetString("until"))
		reason = strings.TrimSpace(banRec.GetString("reason"))
		permanent = until == ""
	}

	return map[string]any{
		"error":     userBanBlockedErrorText,
		"code":      AccountBannedCode,
		"reason":    reason,
		"until":     until,
		"permanent": permanent,
	}
}
