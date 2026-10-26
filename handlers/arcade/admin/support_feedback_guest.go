package admin

import (
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

// Guest endpoints require a credential even for an empty inbox.
func RequireGuestFeedback() *hook.Handler[*core.RequestEvent] {
	return &hook.Handler[*core.RequestEvent]{Func: func(re *core.RequestEvent) error {
		if supportfeedback.GuestKeyHash(re.Request.Header.Get(supportfeedback.GuestHeader)) == "" {
			return re.UnauthorizedError("guest inquiry key required", nil)
		}
		// A guest credential cannot borrow account permissions.
		re.Auth = nil
		return re.Next()
	}}
}
func ListGuestSupportFeedback(re *core.RequestEvent) error {
	items, err := messaging.LegacyListFor(re.App, feedbackPrincipal(re), "", re.Request.URL.Query().Get("arcade"), "")
	if err != nil {
		return err
	}
	re.Response.Header().Set("Cache-Control", "private, no-store")
	return re.JSON(200, map[string]any{"items": items, "total": len(items)})
}
