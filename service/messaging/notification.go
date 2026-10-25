package messaging

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/arcadereport"
	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
	"slices"
	"strings"
	"time"
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
	var arcade *core.Record
	if q.GetString("inbox_type") == "arcade" {
		arcade, _ = app.FindRecordById("arcade", q.GetString("inbox_id"))
	}
	for _, u := range rows {
		if u.Id != q.GetString("requester") && (supportfeedback.IsStaff(u) || officialResponder(u, arcade)) {
			ids = append(ids, u.Id)
		}
	}
	return ids, nil
}

// Access and SSE always use conversationAudience. Notification preferences never
// narrow the set of users who can see a shared inquiry or receive its invalidation.
func notifyMessageTx(app core.App, c, m *core.Record, audience []string) error {
	users, err := activeRecipients(app, audience)
	if err != nil {
		return err
	}
	recipients := []string{}
	var q, arcade *core.Record
	if c.GetString("kind") == "inquiry" {
		q, err = inquiry(app, c.Id)
		if err != nil {
			return err
		}
		if q.GetString("inbox_type") == "arcade" {
			arcade, _ = app.FindRecordById("arcade", q.GetString("inbox_id"))
		}
	}
	for _, id := range audience {
		u := users[id]
		if u == nil || id == m.GetString("sender_user") || slices.Contains(recipients, id) {
			continue
		}
		notify := notificationRecipient(u, q, arcade, m)
		if notify {
			recipients = append(recipients, id)
		}
	}
	if len(recipients) == 0 {
		return nil
	}
	raw, _ := json.Marshal(recipients)
	collection, err := app.FindCollectionByNameOrId("user_notification_device")
	if err != nil {
		return err
	}
	devices := []*core.Record{}
	// Keep the existing maximum of 20 devices per recipient in one bounded query.
	err = app.RecordQuery(collection).AndWhere(dbx.NewExp(`id IN (SELECT id FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY user ORDER BY id) position FROM user_notification_device WHERE user IN (SELECT value FROM json_each({:ids}))) WHERE position<=20)`, dbx.Params{"ids": string(raw)})).All(&devices)
	if err != nil {
		return err
	}
	byUser := map[string][]*core.Record{}
	for _, d := range devices {
		byUser[d.GetString("user")] = append(byUser[d.GetString("user")], d)
	}
	for _, id := range recipients {
		n, err := record(app, "user_notification", map[string]any{"user": id, "actor": m.GetString("sender_user"), "kind": "message", "event_key": "message:" + m.Id, "conversation": c.Id, "message": m.Id})
		if err != nil {
			return err
		}
		for _, d := range byUser[id] {
			if _, err = record(app, "user_notification_delivery", map[string]any{"notification": n.Id, "device": d.Id}); err != nil {
				return err
			}
		}
	}
	return nil
}

// NotificationDeliverable rechecks the current recipient policy for queued pushes.
// NotificationVisible deliberately remains access-only for existing inbox history.
func NotificationDeliverable(app core.App, user, row *core.Record) bool {
	if user == nil || row.GetString("user") != user.Id {
		return false
	}
	c, current, err := Access(app, user.Id, row.GetString("conversation"))
	if err != nil {
		return false
	}
	m, err := app.FindRecordById(Messages, row.GetString("message"))
	if err != nil || m.GetString("conversation") != c.Id {
		return false
	}
	var q, arcade *core.Record
	if c.GetString("kind") == "inquiry" {
		q, err = inquiry(app, c.Id)
		if err != nil {
			return false
		}
		if q.GetString("inbox_type") == "arcade" {
			arcade, _ = app.FindRecordById("arcade", q.GetString("inbox_id"))
		}
	}
	return notificationRecipient(current, q, arcade, m)
}
func notificationRecipient(u, q, arcade, m *core.Record) bool {
	if u == nil || u.Id == m.GetString("sender_user") {
		return false
	}
	if q == nil {
		return true
	}
	notify := false
	if m.GetString("acting_type") != "user" {
		notify = u.Id == q.GetString("requester")
	} else if u.Id != q.GetString("requester") {
		notify = q.GetString("inbox_type") == "team" || officialResponder(u, arcade)
	}
	return notify || (q.GetString("inbox_type") == "arcade" && slices.Contains(m.GetStringSlice("mentions"), "musecat") && supportfeedback.IsStaff(u))
}

func officialResponder(u, arcade *core.Record) bool {
	return arcade != nil && slices.Contains(u.GetStringSlice("owns"), arcade.Id) && arcadereport.CanManage(u, arcade)
}

// Resolve account and email bans together, never once per notification recipient.
func activeRecipients(app core.App, ids []string) (map[string]*core.Record, error) {
	out := map[string]*core.Record{}
	if len(ids) == 0 {
		return out, nil
	}
	users, err := app.FindRecordsByIds("user", ids)
	if err != nil {
		return nil, err
	}
	hashes := []string{}
	byHash := map[string]string{}
	for _, u := range users {
		sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(u.Email()))))
		hash := hex.EncodeToString(sum[:])
		hashes = append(hashes, hash)
		byHash[u.Id] = hash
	}
	rawIDs, _ := json.Marshal(ids)
	rawHashes, _ := json.Marshal(hashes)
	collection, err := app.FindCollectionByNameOrId("user_ban")
	if err != nil {
		return nil, err
	}
	bans := []*core.Record{}
	err = app.RecordQuery(collection).AndWhere(dbx.NewExp(`id IN (SELECT value FROM json_each({:ids})) OR hashed_email IN (SELECT value FROM json_each({:hashes}))`, dbx.Params{"ids": string(rawIDs), "hashes": string(rawHashes)})).All(&bans)
	if err != nil {
		return nil, err
	}
	blockedIDs, blockedHashes := map[string]bool{}, map[string]bool{}
	for _, ban := range bans {
		until := strings.TrimSpace(ban.GetString("until"))
		dt, e := types.ParseDateTime(until)
		if until == "" || (e == nil && !dt.IsZero() && dt.Time().After(time.Now().UTC())) {
			blockedIDs[ban.Id] = true
			blockedHashes[ban.GetString("hashed_email")] = true
		}
	}
	for _, u := range users {
		if !u.GetBool("withdrawn") && !blockedIDs[u.Id] && !blockedHashes[byHash[u.Id]] {
			out[u.Id] = u
		}
	}
	return out, nil
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
