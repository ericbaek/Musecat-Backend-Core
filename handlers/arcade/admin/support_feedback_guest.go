package admin

import (
	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
	"github.com/pocketbase/dbx"
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
func canAccessFeedback(re *core.RequestEvent, app core.App, user, feedback *core.Record) bool {
	return supportfeedback.CanAccess(app, user, feedback) || (user == nil && supportfeedback.CanAccessGuest(feedback, re.Request.Header.Get(supportfeedback.GuestHeader)))
}
func ListGuestSupportFeedback(re *core.RequestEvent) error {
	filter := "guest_key_hash={:hash} && createdBy=''"
	params := dbx.Params{"hash": supportfeedback.GuestKeyHash(re.Request.Header.Get(supportfeedback.GuestHeader))}
	if id := re.Request.URL.Query().Get("arcade"); id != "" {
		filter += " && arcade={:arcade}"
		params["arcade"] = id
	}
	rows, err := re.App.FindRecordsByFilter("support_feedback", filter, "-updated,-created,-id", 0, 0, params)
	if err != nil {
		return err
	}
	items := []map[string]any{}
	for _, row := range rows {
		items = append(items, map[string]any{"id": row.Id, "message": row.GetString("message"), "status": row.GetString("status"), "createdBy": "", "arcade": row.GetString("arcade"), "photos": row.GetStringSlice("photos"), "created": row.GetString("created"), "updated": row.GetString("updated")})
	}
	re.Response.Header().Set("Cache-Control", "private, no-store")
	return re.JSON(200, map[string]any{"items": items, "total": len(items)})
}
