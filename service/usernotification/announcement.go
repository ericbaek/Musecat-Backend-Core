package usernotification

import (
	"context"
	"encoding/json"

	"github.com/ericbaek/musecat-backend-core/service/announcement"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

const AnnouncementKind = "announcement"

// FanoutAnnouncementsBatch durably advances one publication by at most 100 users.
// The cursor, inbox identities and device outbox are committed together. Replaying
// a batch or publication never resets read/view state or queues a second event.
func FanoutAnnouncementsBatch(ctx context.Context, app core.App) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return app.RunInTransaction(func(tx core.App) error {
		rows, err := tx.FindRecordsByFilter(announcement.Collection, "status='published' && broadcast_done=false", "published_at,id", 1, 0)
		if err != nil || len(rows) == 0 {
			return err
		}
		publication := rows[0]
		users, err := tx.FindRecordsByFilter("user", "withdrawn=false && id>{:cursor} && created<={:published}", "id", 100, 0, dbx.Params{"cursor": publication.GetString("broadcast_cursor"), "published": publication.GetString("published_at")})
		if err != nil {
			return err
		}
		for _, user := range users {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := createAnnouncementTx(tx, user, publication); err != nil {
				return err
			}
			publication.Set("broadcast_cursor", user.Id)
		}
		if len(users) < 100 {
			publication.Set("broadcast_done", true)
		}
		return tx.Save(publication)
	})
}

func createAnnouncementTx(app core.App, user, publication *core.Record) error {
	event := "announcement:" + publication.Id
	rows, err := app.FindRecordsByFilter(Inbox, "user={:user} && event_key={:event}", "", 1, 0, dbx.Params{"user": user.Id, "event": event})
	if err != nil || len(rows) > 0 {
		return err
	}
	col, err := app.FindCollectionByNameOrId(Inbox)
	if err != nil {
		return err
	}
	row := core.NewRecord(col)
	row.Set("user", user.Id)
	row.Set("kind", AnnouncementKind)
	row.Set("event_key", event)
	row.Set("announcement", publication.Id)
	row.Set("announcement_copy", announcement.Snapshot(publication))
	if err := app.Save(row); err != nil {
		return err
	}
	return enqueueDevicesTx(app, row, user.Id)
}

func AnnouncementCopy(row *core.Record, locale string) announcement.Copy {
	localized := map[string]announcement.Copy{}
	_ = json.Unmarshal([]byte(row.GetString("announcement_copy")), &localized)
	if locale != "en" && locale != "ja" {
		locale = "ko"
	}
	return localized[locale]
}

func announcementVisible(app core.App, user, row *core.Record) bool {
	if user == nil || user.GetBool("withdrawn") || row.GetString("user") != user.Id {
		return false
	}
	publication, err := app.FindRecordById(announcement.Collection, row.GetString("announcement"))
	return err == nil && publication.GetString("status") == "published"
}

func announcementItem(row *core.Record, locale string) map[string]any {
	copy := AnnouncementCopy(row, locale)
	return map[string]any{"id": row.Id, "kind": AnnouncementKind, "read": row.GetBool("read"), "created": row.GetString("created"), "arcadeName": "", "gameName": "", "title": copy.Title, "message": copy.Summary, "reaction": "", "url": announcement.URL(row.GetString("announcement"))}
}

func ItemLocalized(app core.App, row *core.Record, locale string) map[string]any {
	if row.GetString("kind") == AnnouncementKind {
		return announcementItem(row, locale)
	}
	return Item(app, row)
}

func AnnouncementLocaleAllowed(row *core.Record, locale string) bool {
	localized := map[string]announcement.Copy{}
	_ = json.Unmarshal([]byte(row.GetString("announcement_copy")), &localized)
	if locale != "en" && locale != "ja" {
		locale = "ko"
	}
	_, allowed := localized[locale]
	return allowed
}
