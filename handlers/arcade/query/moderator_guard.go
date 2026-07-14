package query

import (
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

var moderatorAccessTags = map[string]struct{}{
	"developer":         {},
	"moderator":         {},
	"supporter":         {},
	"founding_supporter": {},
}

// RequireModeratorAccess allows only authenticated users whose tag set contains an allowed role.
func RequireModeratorAccess() *hook.Handler[*core.RequestEvent] {
	return &hook.Handler[*core.RequestEvent]{
		Id: "requireModeratorAccess",
		Func: func(re *core.RequestEvent) error {
			if re.Auth == nil {
				return re.UnauthorizedError("The request requires valid record authorization token.", nil)
			}

			if !hasAnyModeratorAccessTag(re.Auth) {
				return re.JSON(http.StatusForbidden, map[string]any{
					"error": "moderator access required",
				})
			}

			return re.Next()
		},
	}
}

func hasAnyModeratorAccessTag(auth *core.Record) bool {
	if auth == nil {
		return false
	}

	for _, tag := range auth.GetStringSlice("tag") {
		if _, ok := moderatorAccessTags[strings.ToLower(strings.TrimSpace(tag))]; ok {
			return true
		}
	}

	for _, tag := range auth.GetStringSlice("tags") {
		if _, ok := moderatorAccessTags[strings.ToLower(strings.TrimSpace(tag))]; ok {
			return true
		}
	}

	return false
}
