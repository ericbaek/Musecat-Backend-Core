package notifications

import (
	"github.com/ericbaek/musecat-backend-core/handlers/arcade/reporting"
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"net/http"
	"strings"
)

const inboxBatchSize = 100

func List(re *core.RequestEvent) error {
	rows, unread, next, err := listInbox(re.App, re.Auth, re.Request.URL.Query().Get("before"), re.Request.URL.Query().Get("locale"))
	if err != nil {
		return err
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, usernotification.ItemLocalized(re.App, row, re.Request.URL.Query().Get("locale")))
	}
	re.Response.Header().Set("Cache-Control", "private, no-store")
	return re.JSON(http.StatusOK, map[string]any{"items": items, "unread": unread, "next": next})
}

func listInbox(app core.App, user *core.Record, before string, locales ...string) ([]*core.Record, int, string, error) {
	locale := "ko"
	if len(locales) > 0 {
		locale = locales[0]
	}
	// Visibility depends on current source permissions, not the event's historical
	// role. Reuse decisions only within this request, with bounded cache storage.
	visibility := map[string]bool{}
	visible := func(row *core.Record) bool {
		kind, source := row.GetString("kind"), row.GetString("arcade")
		if kind == "message" {
			source = row.GetString("conversation")
		} else if kind == "feedback_reply" || kind == "feedback_created" {
			kind, source = "feedback", row.GetString("feedback")
		} else if kind == usernotification.AnnouncementKind {
			if !usernotification.AnnouncementLocaleAllowed(row, locale) {
				return false
			}
			source = row.GetString("announcement")
		} else if kind != "report_created" && kind != usernotification.ArcadeSetupKind {
			kind = "report_reaction"
		}
		key := kind + ":" + source
		if result, ok := visibility[key]; ok {
			return result
		}
		result := usernotification.Visible(app, user, row)
		if len(visibility) < 128 {
			visibility[key] = result
		}
		return result
	}

	unread := 0
	if err := scanInbox(app, user.Id, true, nil, func(row *core.Record) bool {
		if visible(row) {
			unread++
		}
		return true
	}); err != nil {
		return nil, 0, "", err
	}

	var cursor *core.Record
	if before != "" {
		rows, err := app.FindRecordsByFilter(usernotification.Inbox, "user={:user} && id={:id}", "", 1, 0, dbx.Params{"user": user.Id, "id": before})
		if err != nil {
			return nil, 0, "", err
		}
		// Preserve the empty page for missing, removed, or foreign cursors.
		if len(rows) == 0 {
			return nil, unread, "", nil
		}
		cursor = rows[0]
	}
	items := make([]*core.Record, 0, 20)
	next := ""
	err := scanInbox(app, user.Id, false, cursor, func(row *core.Record) bool {
		if !visible(row) {
			return true
		}
		if len(items) == 20 {
			next = items[len(items)-1].Id
			return false
		}
		items = append(items, row)
		return true
	})
	return items, unread, next, err
}

// scanInbox never retains the full history. The created/id keyset preserves
// newest-first ordering even when several events share the same timestamp.
func scanInbox(app core.App, userID string, unreadOnly bool, cursor *core.Record, visit func(*core.Record) bool) error {
	for {
		filter := "user_notification.user={:user}"
		if unreadOnly {
			filter += " AND user_notification.read=false"
		}
		params := dbx.Params{"user": userID}
		if cursor != nil {
			filter += " AND (user_notification.created<{:created} OR (user_notification.created={:created} AND user_notification.id<{:id}))"
			params["created"], params["id"] = cursor.GetString("created"), cursor.Id
		}
		// Apply grouping before the cursor so older events never reappear on later pages.
		filter += ` AND (user_notification.kind!='message' OR NOT EXISTS (SELECT 1 FROM user_notification newer WHERE newer.user=user_notification.user AND newer.kind='message' AND newer.conversation=user_notification.conversation AND ((SELECT sequence FROM messaging_message WHERE id=newer.message)>(SELECT sequence FROM messaging_message WHERE id=user_notification.message) OR (newer.message=user_notification.message AND newer.id>user_notification.id))))`
		rows := []*core.Record{}
		err := app.RecordQuery(usernotification.Inbox).AndWhere(dbx.NewExp(filter, params)).OrderBy("user_notification.created DESC", "user_notification.id DESC").Limit(inboxBatchSize).All(&rows)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if !visit(row) {
				return nil
			}
		}
		if len(rows) < inboxBatchSize {
			return nil
		}
		cursor = rows[len(rows)-1]
	}
}
func Read(re *core.RequestEvent) error {
	var body struct {
		ID  string `json:"id"`
		All bool   `json:"all"`
	}
	if err := re.BindBody(&body); err != nil || (!body.All && body.ID == "") {
		return re.BadRequestError("notification id is required", nil)
	}
	err := re.App.RunInTransaction(func(tx core.App) error {
		filter := "user={:user} && read=false"
		params := dbx.Params{"user": re.Auth.Id}
		if !body.All {
			selected, err := tx.FindRecordsByFilter(usernotification.Inbox, "user={:user} && id={:id}", "", 1, 0, dbx.Params{"user": re.Auth.Id, "id": body.ID})
			if err != nil {
				return err
			}
			if len(selected) == 0 {
				return nil
			}
			row := selected[0]
			if row.GetString("kind") == "message" {
				message, err := tx.FindRecordById("messaging_message", row.GetString("message"))
				if err != nil {
					return err
				}
				filter += " && kind='message' && conversation={:c} && message.sequence<={:s}"
				params["c"], params["s"] = row.GetString("conversation"), message.GetInt("sequence")
			} else {
				filter += " && id={:id}"
			}
			params["id"] = body.ID
		}
		rows, err := tx.FindRecordsByFilter(usernotification.Inbox, filter, "", 0, 0, params)
		if err != nil {
			return err
		}
		for _, row := range rows {
			row.Set("read", true)
			if err := tx.Save(row); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return re.JSON(http.StatusOK, map[string]bool{"ok": true})
}
func DeviceStatus(re *core.RequestEvent) error {
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := re.BindBody(&body); err != nil || len(body.Endpoint) > 2048 || !reporting.PushEndpointAllowed(body.Endpoint) {
		return re.BadRequestError("invalid endpoint", nil)
	}
	rows, err := re.App.FindRecordsByFilter(usernotification.Devices, "user={:user} && endpoint={:endpoint}", "", 1, 0, dbx.Params{"user": re.Auth.Id, "endpoint": body.Endpoint})
	if err != nil {
		return err
	}
	re.Response.Header().Set("Cache-Control", "private, no-store")
	return re.JSON(http.StatusOK, map[string]bool{"enabled": len(rows) > 0})
}
func SaveDevice(re *core.RequestEvent) error {
	var body struct {
		Subscription reporting.PushSubscription `json:"subscription"`
		Locale       string                     `json:"locale"`
	}
	if err := re.BindBody(&body); err != nil || !reporting.ValidPushSubscription(body.Subscription) {
		return re.BadRequestError("invalid subscription", nil)
	}
	if body.Locale != "ja" && body.Locale != "en" {
		body.Locale = "ko"
	}
	err := re.App.RunInTransaction(func(tx core.App) error {
		_, err := usernotification.UpsertDeviceTx(tx, re.Auth.Id, body.Subscription.Endpoint, body.Subscription, body.Locale)
		return err
	})
	if err != nil {
		return err
	}
	return re.JSON(http.StatusOK, map[string]bool{"enabled": true})
}
func DeleteDevice(re *core.RequestEvent) error {
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := re.BindBody(&body); err != nil || strings.TrimSpace(body.Endpoint) == "" || len(body.Endpoint) > 2048 {
		return re.BadRequestError("invalid endpoint", nil)
	}
	err := re.App.RunInTransaction(func(tx core.App) error { return usernotification.RemoveDeviceTx(tx, re.Auth.Id, body.Endpoint) })
	if err != nil {
		return err
	}
	return re.JSON(http.StatusOK, map[string]bool{"enabled": false})
}

// SyncDeviceLocale never registers or transfers an endpoint; it updates this account's existing device only.
func SyncDeviceLocale(re *core.RequestEvent) error {
	var body struct {
		Endpoint string `json:"endpoint"`
		Locale   string `json:"locale"`
	}
	if err := re.BindBody(&body); err != nil || len(body.Endpoint) > 2048 || !reporting.PushEndpointAllowed(body.Endpoint) || (body.Locale != "ko" && body.Locale != "en" && body.Locale != "ja") {
		return re.BadRequestError("invalid device locale", nil)
	}
	enabled := false
	err := re.App.RunInTransaction(func(tx core.App) error {
		records, err := tx.FindRecordsByFilter(usernotification.Devices, "user={:user} && endpoint={:endpoint}", "", 1, 0, dbx.Params{"user": re.Auth.Id, "endpoint": body.Endpoint})
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return nil
		}
		enabled = true
		if records[0].GetString("locale") == body.Locale {
			return nil
		}
		records[0].Set("locale", body.Locale)
		return tx.Save(records[0])
	})
	if err != nil {
		return err
	}
	return re.JSON(200, map[string]bool{"enabled": enabled})
}
