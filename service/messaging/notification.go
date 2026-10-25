package messaging

import (
	"fmt"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

func conversationAudience(app core.App, c *core.Record) ([]string, error) {
	if c.GetString("kind") == "dm" {
		rows, err := app.FindRecordsByFilter(Parties, "conversation={:c} && party_type='user'", "", 3, 0, dbx.Params{"c": c.Id})
		if err != nil {
			return nil, err
		}
		ids := []string{}
		for _, r := range rows {
			ids = append(ids, r.GetString("party_id"))
		}
		return ids, nil
	}
	q, err := inquiry(app, c.Id)
	if err != nil {
		return nil, err
	}
	ids := []string{q.GetString("requester")}
	rows, err := app.FindRecordsByFilter("user", "withdrawn=false && (tags ~ 'developer' || tags ~ 'moderator' || owns ~ {:arcade})", "id", 201, 0, dbx.Params{"arcade": q.GetString("inbox_id")})
	if err != nil {
		return nil, err
	}
	if len(rows) > 200 {
		return nil, fmt.Errorf("inquiry responder audience exceeds transaction bound")
	}
	for _, u := range rows {
		if u.Id != q.GetString("requester") && Responder(app, u, q) {
			ids = append(ids, u.Id)
		}
	}
	return ids, nil
}
func notifyMessageTx(app core.App, c, m *core.Record, audience []string) error {
	recipients := audience
	if c.GetString("kind") == "inquiry" {
		q, err := inquiry(app, c.Id)
		if err != nil {
			return err
		}
		requester := q.GetString("requester")
		if m.GetString("acting_type") != "user" {
			recipients = []string{requester}
		} else {
			recipients = []string{}
			for _, id := range audience {
				if id != requester {
					recipients = append(recipients, id)
				}
			}
		}
	}

	for _, id := range recipients {
		if id == m.GetString("sender_user") {
			continue
		}
		if _, err := ActiveUser(app, id); err != nil {
			continue
		}
		n, err := record(app, "user_notification", map[string]any{"user": id, "actor": m.GetString("sender_user"), "kind": "message", "event_key": "message:" + m.Id, "conversation": c.Id, "message": m.Id})
		if err != nil {
			return err
		}
		devices, err := app.FindRecordsByFilter("user_notification_device", "user={:u}", "", 20, 0, dbx.Params{"u": id})
		if err != nil {
			return err
		}
		for _, d := range devices {
			if _, err = record(app, "user_notification_delivery", map[string]any{"notification": n.Id, "device": d.Id}); err != nil {
				return err
			}
		}
	}
	return nil
}
func NotificationVisible(app core.App, user, row *core.Record) bool {
	if user == nil || row.GetString("user") != user.Id {
		return false
	}
	_, _, err := Access(app, user.Id, row.GetString("conversation"))
	return err == nil
}
func NotificationItem(row *core.Record) map[string]any {
	return map[string]any{"id": row.Id, "kind": "message", "read": row.GetBool("read"), "created": row.GetString("created"), "arcadeName": "뮤즈캣", "gameName": "", "reaction": "", "message": "", "conversationId": row.GetString("conversation"), "url": "/messages?conversation=" + row.GetString("conversation")}
}
