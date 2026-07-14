package user

import (
	"net/http"

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
		},
	}
}
