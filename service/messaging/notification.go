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
	"net/url"
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
	ids := []string{}
	if requesterType(q) == "user" {
		ids = append(ids, q.GetString("requester"))
	}
	staffAccess := q.GetString("inbox_type") == "team"
	if !staffAccess {
		invited, err := mentionedConversations(app, []string{c.Id})
		if err != nil {
			return nil, err
		}
		staffAccess = invited[c.Id]
	}
	rows, err := app.FindRecordsByFilter("user", "withdrawn=false && (({:staff}=true && (tags ~ 'developer' || tags ~ 'moderator')) || owns ~ {:arcade})", "id", 201, 0, dbx.Params{"arcade": q.GetString("inbox_id"), "staff": staffAccess})
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
		if u.Id != q.GetString("requester") && ((staffAccess && supportfeedback.IsStaff(u)) || officialResponder(u, arcade)) {
			ids = append(ids, u.Id)
		}
	}
	return ids, nil
}

// Personal SSE and notification inboxes include requesters and official responders.
// Platform staff join after an explicit mention; dashboard management is separate.
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
	if row.GetBool("read") {
		return false
	}
	read, err := app.FindRecordsByFilter(Reads, "conversation={:c} && user={:u} && last_read_sequence>={:s}", "", 1, 0, dbx.Params{"c": c.Id, "u": user.Id, "s": m.GetInt("sequence")})
	return err == nil && len(read) == 0 && notificationRecipient(current, q, arcade, m)
}
func notificationRecipient(u, q, arcade, m *core.Record) bool {
	if u == nil || u.Id == m.GetString("sender_user") {
		return false
	}
	if q == nil {
		return true
	}
	notify := false
	if m.GetString("acting_type") != "user" && m.GetString("acting_type") != "guest" {
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
	c, _, err := Access(app, user.Id, row.GetString("conversation"))
	if err != nil {
		return false
	}
	if c.GetString("kind") != "inquiry" {
		return true
	}
	q, err := inquiry(app, c.Id)
	if err != nil {
		return false
	}
	if q.GetString("inbox_type") != "arcade" || q.GetString("requester") == user.Id {
		return true
	}
	arcade, _ := app.FindRecordById("arcade", q.GetString("inbox_id"))
	if officialResponder(user, arcade) {
		return true
	}
	invited, err := mentionedConversations(app, []string{c.Id})
	return err == nil && invited[c.Id]
}
func NotificationItem(app core.App, row *core.Record) map[string]any {
	return NotificationItemLocalized(app, row, "ko")
}

func NotificationItemLocalized(app core.App, row *core.Record, locale string) map[string]any {
	target := "/messages?conversation=" + row.GetString("conversation")
	if q, err := inquiry(app, row.GetString("conversation")); err == nil && q.GetString("inbox_type") == "arcade" && q.GetString("requester") != row.GetString("user") {
		invited, err := mentionedConversations(app, []string{q.GetString("conversation")})
		if err != nil || !invited[q.GetString("conversation")] {
			target = "/arcade/" + url.PathEscape(q.GetString("inbox_id"))
		}
	}
	name, preview := "", ""
	if m, err := app.FindRecordById(Messages, row.GetString("message")); err == nil && m.GetString("conversation") == row.GetString("conversation") {
		name = messageDTO(app, m, nil).ActingAs.Name
		if m.GetString("acting_type") == "team" {
			if locale == "en" {
				name = "Musecat Team"
			} else if locale == "ja" {
				name = "Musecatチーム"
			}
		}
		runes := []rune(strings.Join(strings.Fields(m.GetString("text")), " "))
		if len(runes) > 160 {
			runes = append(runes[:160], '…')
		}
		preview = string(runes)
		if preview == "" && (len(m.GetStringSlice("photos")) > 0 || len(m.GetStringSlice("legacy_photos")) > 0) {
			preview = "사진을 보냈습니다"
			if locale == "en" {
				preview = "Sent a photo"
			} else if locale == "ja" {
				preview = "写真を送信しました"
			}
		}
	}
	return map[string]any{"id": row.Id, "kind": "message", "read": row.GetBool("read"), "created": row.GetString("created"), "arcadeName": name, "title": name, "gameName": "", "reaction": "", "message": preview, "conversationId": row.GetString("conversation"), "url": target}
}
