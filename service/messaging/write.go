package messaging

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ericbaek/musecat-backend-core/service/arcadereport"
	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
	"github.com/gabriel-vasile/mimetype"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

func record(app core.App, col string, values map[string]any) (*core.Record, error) {
	c, err := app.FindCollectionByNameOrId(col)
	if err != nil {
		return nil, err
	}
	r := core.NewRecord(c)
	for k, v := range values {
		r.Set(k, v)
	}
	return r, app.Save(r)
}
func existingRetry(app core.App, c, user, key string) (*core.Record, error) {
	return app.FindFirstRecordByFilter(Messages, "conversation={:c} && sender_user={:u} && client_message_id={:k}", dbx.Params{"c": c, "u": user, "k": key})
}
func inputHash(in *SendInput) (string, error) {
	in.Text = strings.TrimSpace(in.Text)
	if len(in.Mentions) > 16 {
		return "", apis.NewBadRequestError("Too many mentions.", nil)
	}
	for _, mention := range in.Mentions {
		if mention != "musecat" {
			return "", apis.NewBadRequestError("Invalid mention.", nil)
		}
	}
	if len(in.Mentions) > 0 {
		in.Mentions = []string{"musecat"}
	} else {
		in.Mentions = []string{}
	}
	maxText := 4000
	if in.LegacyOriginal {
		maxText = 13000
	}
	if in.ClientMessageID == "" || len(in.ClientMessageID) > 100 || utf8.RuneCountInString(in.Text) > maxText || (in.Text == "" && len(in.Photos) == 0) || len(in.Photos) > 3 {
		return "", apis.NewBadRequestError("Message requires text (up to 4000 characters) or up to three photos and a clientMessageId.", nil)
	}
	hashes := []string{}
	for _, f := range in.Photos {
		if f == nil || f.Reader == nil {
			return "", apis.NewBadRequestError("Invalid photo.", nil)
		}
		if f.Size > 15_000_000 {
			return "", apis.ErrRequestEntityTooLarge
		}
		r, err := f.Reader.Open()
		if err != nil {
			return "", err
		}
		mime, err := mimetype.DetectReader(r)
		r.Close()
		if err != nil || !slices.Contains([]string{"image/jpeg", "image/png", "image/webp", "image/gif"}, mime.String()) {
			return "", apis.NewBadRequestError("Photos must be JPEG, PNG, WebP or GIF.", nil)
		}
		r, err = f.Reader.Open()
		if err != nil {
			return "", err
		}
		h := sha256.New()
		n, copyErr := io.Copy(h, io.LimitReader(r, 15_000_001))
		err = copyErr
		if n > 15_000_000 {
			r.Close()
			return "", apis.ErrRequestEntityTooLarge
		}
		r.Close()
		if err != nil {
			return "", err
		}
		hashes = append(hashes, hex.EncodeToString(h.Sum(nil)))
	}
	raw, _ := json.Marshal(struct {
		Text   string
		Photos []string
	}{in.Text, hashes})
	// Keep the original payload encoding for retries of messages without mentions.
	if len(in.Mentions) > 0 {
		raw, _ = json.Marshal(struct {
			Text     string
			Photos   []string
			Mentions []string
		}{in.Text, hashes, in.Mentions})
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func rateLimit(app core.App, user, c string, create bool) error {
	since := time.Now().UTC().Add(-time.Minute).Format("2006-01-02 15:04:05.000Z")
	filter := "sender_user={:u} AND created>={:since}"
	params := dbx.Params{"u": user, "since": since}
	collection := Messages
	limit := 60
	if create {
		collection = Conversations
		filter = "created_by={:u} AND created>={:since}"
		limit = 10
	} else if c != "" {
		filter += " AND conversation={:c}"
		params["c"] = c
		limit = 30
	}
	n, err := app.CountRecords(collection, dbx.NewExp(filter, params))
	if err != nil {
		return err
	}
	if n >= int64(limit) {
		return router.NewApiError(429, "Please wait before sending again.", nil)
	}
	return nil
}
func Create(app core.App, user string, in CreateInput) (Summary, error) {
	summary, _, err := CreateWithReplay(app, user, in)
	return summary, err
}
func CreateWithReplay(app core.App, user string, in CreateInput) (Summary, bool, error) {
	replayed := false
	var cid string
	if in.Kind != "dm" && in.Kind != "inquiry" {
		return Summary{}, false, apis.NewBadRequestError("Invalid conversation kind.", nil)
	}
	if err := validateMentions(in.Kind, in.Inbox.Type, in.InitialMessage.Mentions); err != nil {
		return Summary{}, false, err
	}
	hash := ""
	var err error
	if in.Kind == "inquiry" {
		hash, err = inputHash(&in.InitialMessage)
		if err != nil {
			return Summary{}, false, err
		}
	}
	err = app.RunInTransaction(func(tx core.App) error {
		u, err := ActiveUser(tx, user)
		if err != nil {
			return err
		}
		if in.Kind == "dm" {
			if user == in.PeerUserID {
				return apis.NewBadRequestError("Cannot message yourself.", nil)
			}
			ids := []string{user, in.PeerUserID}
			slices.Sort(ids)
			key := strings.Join(ids, ":")
			if c, e := tx.FindFirstRecordByFilter(Conversations, "kind='dm' && pair_key={:pair}", dbx.Params{"pair": key}); e == nil {
				cid = c.Id
				replayed = true
				return nil
			}
			if !CanDM(tx, user, in.PeerUserID) {
				return apis.NewForbiddenError("Recipient is unavailable.", nil)
			}
			// Count the initiator, never users who only received new conversations.
			if err = rateLimit(tx, user, "", true); err != nil {
				return err
			}
			c, err := record(tx, Conversations, map[string]any{"kind": "dm", "created_by": user, "pair_key": key})
			if err != nil {
				return err
			}
			cid = c.Id
			for _, id := range ids {
				if _, err = record(tx, Parties, map[string]any{"conversation": cid, "party_type": "user", "party_id": id}); err != nil {
					return err
				}
			}
			afterCommit(tx, Event{Name: "conversation.changed", ID: cid, Reason: "created", Audience: ids})
			return nil
		}
		if in.Inbox.Type != "team" && in.Inbox.Type != "arcade" || in.Inbox.Type == "team" && in.Inbox.ID != "musecat" {
			return apis.NewBadRequestError("Invalid inbox.", nil)
		}
		// Initial client keys are scoped to requester across inquiry creation retries.
		prior, e := tx.FindFirstRecordByFilter(Messages, "conversation.kind='inquiry' && sender_user={:u} && client_message_id={:k} && sequence=1", dbx.Params{"u": user, "k": in.InitialMessage.ClientMessageID})
		if e == nil {
			q, e := inquiry(tx, prior.GetString("conversation"))
			if e != nil || q.GetString("requester") != user || q.GetString("inbox_type") != in.Inbox.Type || q.GetString("inbox_id") != in.Inbox.ID || prior.GetString("payload_hash") != hash {
				return router.NewApiError(409, "clientMessageId was already used for different content.", nil)
			}
			cid = prior.GetString("conversation")
			replayed = true
			return nil
		}
		if in.Inbox.Type == "arcade" {
			arcade, e := tx.FindRecordById("arcade", in.Inbox.ID)
			if e != nil || (!arcade.GetBool("public") && arcade.GetString("createdBy") != u.Id && !arcadereport.CanManage(u, arcade)) {
				return apis.NewNotFoundError("Arcade not found.", nil)
			}
			if supportfeedback.HasOfficialAccount(tx, arcade.Id) && supportfeedback.InquirySettings(arcade).Mode != "internal" {
				code := "INQUIRY_DISABLED"
				if supportfeedback.InquirySettings(arcade).Mode == "external" {
					code = "INQUIRY_EXTERNAL"
				}
				return router.NewApiError(409, code, nil)
			}
		}
		if err = rateLimit(tx, user, "", true); err != nil {
			return err
		}
		c, err := record(tx, Conversations, map[string]any{"kind": "inquiry", "created_by": user})
		if err != nil {
			return err
		}
		cid = c.Id
		_, err = record(tx, Inquiries, map[string]any{"conversation": cid, "requester": user, "inbox_type": in.Inbox.Type, "inbox_id": in.Inbox.ID, "status": "waiting", "legacy_feedback_id": cid})
		if err != nil {
			return err
		}
		for _, p := range []Identity{{"user", user}, in.Inbox} {
			if _, err = record(tx, Parties, map[string]any{"conversation": cid, "party_type": p.Type, "party_id": p.ID}); err != nil {
				return err
			}
		}
		_, err = saveMessage(tx, u, c, in.InitialMessage, Identity{"user", user}, hash)
		return err
	})
	if err != nil {
		return Summary{}, false, err
	}
	summary, err := Get(app, user, cid)
	return summary, replayed, err
}
func Send(app core.App, user, cid string, in SendInput) (Message, bool, error) {
	hash, err := inputHash(&in)
	if err != nil {
		return Message{}, false, err
	}
	var saved *core.Record
	replayed := false
	err = app.RunInTransaction(func(tx core.App) error {
		c, u, err := Access(tx, user, cid)
		if err != nil {
			return err
		}
		inboxType := ""
		if c.GetString("kind") == "inquiry" {
			q, e := inquiry(tx, cid)
			if e != nil {
				return e
			}
			inboxType = q.GetString("inbox_type")
		}
		if err := validateMentions(c.GetString("kind"), inboxType, in.Mentions); err != nil {
			return err
		}
		// A committed retry is readable even after receive/block policy changes.
		if prior, e := existingRetry(tx, cid, user, in.ClientMessageID); e == nil {
			if prior.GetString("payload_hash") != hash || (in.ActingAs != nil && (prior.GetString("acting_type") != in.ActingAs.Type || prior.GetString("acting_id") != in.ActingAs.ID)) {
				return router.NewApiError(409, "clientMessageId was already used for different content.", nil)
			}
			saved = prior
			replayed = true
			return nil
		}
		acting, err := actor(tx, u, c, in.ActingAs)
		if err != nil {
			return err
		}
		if err = rateLimit(tx, user, "", false); err != nil {
			return err
		}
		if err = rateLimit(tx, user, cid, false); err != nil {
			return err
		}
		saved, err = saveMessage(tx, u, c, in, acting, hash)
		return err
	})
	if err != nil {
		return Message{}, false, err
	}
	return messageDTO(app, saved, nil), replayed, nil
}
func saveMessage(tx core.App, u, c *core.Record, in SendInput, acting Identity, hash string) (*core.Record, error) {
	if len(in.Photos) > 0 {
		var used int
		err := tx.DB().NewQuery("SELECT COALESCE(sum(json_array_length(photos)),0) FROM messaging_message WHERE sender_user={:u} AND created>={:since}").Bind(dbx.Params{"u": u.Id, "since": time.Now().UTC().Add(-time.Minute).Format("2006-01-02 15:04:05.000Z")}).Row(&used)
		if err != nil {
			return nil, err
		}
		if used+len(in.Photos) > 12 {
			return nil, router.NewApiError(429, "Please wait before uploading more photos.", nil)
		}
	}
	parties, err := loadParties(tx, []Identity{{"user", u.Id}})
	if err != nil {
		return nil, err
	}
	sender := parties["user:"+u.Id]
	roles := append([]string{}, u.GetStringSlice("tags")...)
	for _, tag := range u.GetStringSlice("tag") {
		if !slices.Contains(roles, tag) {
			roles = append(roles, tag)
		}
	}
	seq := c.GetInt("last_sequence") + 1
	m, err := record(tx, Messages, map[string]any{"conversation": c.Id, "sequence": seq, "sender_user": u.Id, "acting_type": acting.Type, "acting_id": acting.ID, "sender_name": sender.Name, "sender_avatar": sender.AvatarURL, "sender_roles": roles, "client_message_id": in.ClientMessageID, "payload_hash": hash, "text": in.Text, "mentions": in.Mentions, "photos": in.Photos})
	if err != nil {
		return nil, err
	}
	c.Set("last_sequence", seq)
	c.Set("last_message", m.Id)
	c.Set("last_message_at", m.GetString("created"))
	if err = tx.Save(c); err != nil {
		return nil, err
	}
	if c.GetString("kind") == "inquiry" {
		q, e := inquiry(tx, c.Id)
		if e != nil {
			return nil, e
		}
		status := "recognised"
		if acting.Type == "user" {
			status = "waiting"
		}
		q.Set("status", status)
		if err = tx.Save(q); err != nil {
			return nil, err
		}
	}
	audience, err := conversationAudience(tx, c)
	if err != nil {
		return nil, err
	}
	if err = notifyMessageTx(tx, c, m, audience); err != nil {
		return nil, err
	}
	afterCommit(tx, Event{Name: "conversation.changed", ID: c.Id, LatestSequence: seq, Reason: "message", Audience: audience})
	return m, nil
}
func SetRead(app core.App, user, id string, through int) (map[string]any, error) {
	last := 0
	err := app.RunInTransaction(func(tx core.App) error {
		c, _, err := Access(tx, user, id)
		if err != nil {
			return err
		}
		if through < 0 || through > c.GetInt("last_sequence") {
			return apis.NewBadRequestError("Invalid read sequence.", nil)
		}
		row, e := tx.FindFirstRecordByFilter(Reads, "conversation={:c} && user={:u}", dbx.Params{"c": id, "u": user})
		if e != nil {
			row, err = record(tx, Reads, map[string]any{"conversation": id, "user": user, "last_read_sequence": through})
		} else if through > row.GetInt("last_read_sequence") {
			row.Set("last_read_sequence", through)
			err = tx.Save(row)
		}
		if err != nil {
			return err
		}
		last = row.GetInt("last_read_sequence")
		afterCommit(tx, Event{Name: "conversation.read", ID: id, UserID: user, LastReadSequence: last})
		return nil
	})
	if err != nil {
		return nil, err
	}
	s, err := Get(app, user, id)
	return map[string]any{"conversationId": id, "lastReadSequence": last, "unreadCount": s.UnreadCount}, err
}
func SetStatus(app core.App, user, id, status string) (Summary, error) {
	err := app.RunInTransaction(func(tx core.App) error {
		c, u, err := Access(tx, user, id)
		if err != nil {
			return err
		}
		if c.GetString("kind") != "inquiry" {
			return apis.NewBadRequestError("DM has no inquiry status.", nil)
		}
		q, err := inquiry(tx, id)
		if err != nil {
			return err
		}
		if !slices.Contains(inquiryPolicy(u, q, Responder(tx, u, q)).statuses, status) {
			return apis.NewBadRequestError("Invalid inquiry status.", nil)
		}
		q.Set("status", status)
		if err = tx.Save(q); err != nil {
			return err
		}
		audience, err := conversationAudience(tx, c)
		if err != nil {
			return err
		}
		afterCommit(tx, Event{Name: "conversation.changed", ID: id, LatestSequence: c.GetInt("last_sequence"), Reason: "status", Audience: audience})
		return nil
	})
	if err != nil {
		return Summary{}, err
	}
	return Get(app, user, id)
}
func Preference(app core.App, user, receive string) error {
	if receive != "nobody" && receive != "everyone" {
		return apis.NewBadRequestError("Invalid receive preference.", nil)
	}
	return app.RunInTransaction(func(tx core.App) error {
		if _, err := ActiveUser(tx, user); err != nil {
			return err
		}
		r, e := tx.FindFirstRecordByFilter(Preferences, "user={:u}", dbx.Params{"u": user})
		if e != nil {
			_, e = record(tx, Preferences, map[string]any{"user": user, "receive": receive})
		} else {
			r.Set("receive", receive)
			e = tx.Save(r)
		}
		if e == nil {
			afterCommit(tx, Event{Name: "resync"})
		}
		return e
	})
}
func Block(app core.App, user, other string, remove bool) error {
	if user == other {
		return apis.NewBadRequestError("Cannot block yourself.", nil)
	}
	return app.RunInTransaction(func(tx core.App) error {
		if _, err := ActiveUser(tx, user); err != nil {
			return err
		}
		r, e := tx.FindFirstRecordByFilter(Blocks, "user={:u} && blocked_user={:b}", dbx.Params{"u": user, "b": other})
		if remove {
			if e == nil {
				e = tx.Delete(r)
			} else {
				e = nil
			}
		} else if e != nil {
			if _, e = tx.FindRecordById("user", other); e != nil {
				return apis.NewNotFoundError("User not found.", nil)
			}
			var n int64
			n, e = tx.CountRecords(Blocks, dbx.HashExp{"user": user})
			if e != nil {
				return e
			}
			if n >= 100 {
				return router.NewApiError(429, "Block limit reached.", nil)
			}
			_, e = record(tx, Blocks, map[string]any{"user": user, "blocked_user": other})
		}
		if e == nil {
			afterCommit(tx, Event{Name: "resync"})
		}
		return e
	})
}
func Report(app core.App, user, id, reason string) (string, bool, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > 2000 {
		return "", false, apis.NewBadRequestError("A reason of up to 2000 characters is required.", nil)
	}
	out := ""
	exists := false
	err := app.RunInTransaction(func(tx core.App) error {
		m, e := tx.FindRecordById(Messages, id)
		if e != nil {
			return apis.NewNotFoundError("Message not found.", nil)
		}
		if _, _, e = Access(tx, user, m.GetString("conversation")); e != nil {
			return e
		}
		if r, e := tx.FindFirstRecordByFilter(Reports, "reporter={:u} && message={:m}", dbx.Params{"u": user, "m": id}); e == nil {
			out = r.Id
			exists = true
			return nil
		}
		n, e := tx.CountRecords(Reports, dbx.NewExp("reporter={:u} AND created>={:since}", dbx.Params{"u": user, "since": time.Now().UTC().Add(-time.Minute).Format("2006-01-02 15:04:05.000Z")}))
		if e != nil {
			return e
		}
		if n >= 20 {
			return router.NewApiError(429, "Please wait before reporting again.", nil)
		}
		r, e := record(tx, Reports, map[string]any{"reporter": user, "message": id, "reason": reason, "evidence": map[string]any{"message": messageDTO(tx, m, nil), "legacyCollection": m.GetString("legacy_collection"), "legacyRecord": m.GetString("legacy_record"), "legacyPhotos": m.GetStringSlice("legacy_photos")}})
		if e == nil {
			out = r.Id
		}
		return e
	})
	return out, exists, err
}

func validateMentions(kind, inboxType string, mentions []string) error {
	if len(mentions) > 0 && (kind != "inquiry" || inboxType != "arcade") {
		return apis.NewBadRequestError("Mentions are only supported for arcade inquiries.", nil)
	}
	return nil
}
