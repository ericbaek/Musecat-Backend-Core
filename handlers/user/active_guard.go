package user

import (
	"github.com/pocketbase/pocketbase/apis"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

// RequireActiveUser blocks requests from withdrawn or banned users.
func RequireActiveUser() *hook.Handler[*core.RequestEvent] {
	return &hook.Handler[*core.RequestEvent]{
		Id: "requireActiveUser",
		Func: func(re *core.RequestEvent) error {
			if re.Auth == nil {
				return re.UnauthorizedError("The request requires valid record authorization token.", nil)
			}

			banRec, code, err := checkAccountRestrictionWithBan(re.App, re.Auth, userBanNow())
			if err != nil {
				return re.JSON(http.StatusBadGateway, map[string]any{
					"error":   "failed to verify account restriction",
					"details": err.Error(),
				})
			}
			if code == AccountBannedCode {
				return re.JSON(http.StatusForbidden, buildBanAuthResponse(banRec))
			}
			if code != "" {
				return re.JSON(http.StatusForbidden, map[string]any{
					"error": "account withdrawn",
					"code":  code,
				})
			}

			return re.Next()
		},
	}
}

var staffAccessTags = map[string]struct{}{
	"developer": {},
	"moderator": {},
}

func HasStaffAccess(auth *core.Record) bool {
	if auth == nil {
		return false
	}
	for _, tags := range [][]string{auth.GetStringSlice("tag"), auth.GetStringSlice("tags")} {
		for _, tag := range tags {
			if _, ok := staffAccessTags[strings.ToLower(strings.TrimSpace(tag))]; ok {
				return true
			}
		}
	}
	return false
}

func hasActiveStaffAccess(app core.App, auth *core.Record) (bool, error) {
	if !HasStaffAccess(auth) || auth.Collection().Name != CollectionUser {
		return false, nil
	}
	_, code, err := checkAccountRestrictionWithBan(app, auth, userBanNow())
	return err == nil && code == "", err
}

// CheckActiveUser is the transaction-safe account restriction check used after external I/O.
func CheckActiveUser(app core.App, auth *core.Record) error {
	if auth == nil {
		return apis.NewUnauthorizedError("Authentication required.", nil)
	}
	_, code, err := checkAccountRestrictionWithBan(app, auth, userBanNow())
	if err != nil {
		return err
	}
	if code != "" {
		return apis.NewForbiddenError("Account is restricted.", map[string]any{"code": code})
	}
	return nil
}
