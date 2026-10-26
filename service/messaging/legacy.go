package messaging

import (
	"encoding/json"
	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"net/url"
	"slices"
)

func LegacyMessage(m Message) map[string]any {
	names := []string{}
	for _, p := range m.Photos {
		names = append(names, p.Name)
	}
	staff := slices.Contains(m.Sender.RoleTags, "staff") || slices.Contains(m.Sender.RoleTags, "moderator") || slices.Contains(m.Sender.RoleTags, "developer")
	return map[string]any{"id": m.ID, "message": m.Text, "author": m.SenderUserID, "staff": staff, "official": !staff && m.ActingAs.Type == "arcade", "created": m.CreatedAt, "photos": names}
}
func LegacyItem(app core.App, s Summary) (map[string]any, error) {
	first, err := app.FindFirstRecordByFilter(Messages, "conversation={:c} && sequence=1", dbx.Params{"c": s.ID})
	if err != nil {
		return nil, err
	}
	item := legacyItem(s, first)
	if s.Inbox != nil && s.Inbox.Type == "arcade" {
		item["arcadeHasOfficial"] = supportfeedback.HasOfficialAccount(app, s.Inbox.ID)
	}
	return item, nil
}
func legacyItem(s Summary, first *core.Record) map[string]any {
	arcade := ""
	if s.Inbox != nil && s.Inbox.Type == "arcade" {
		arcade = s.Inbox.ID
	}
	id := s.ID
	if s.LegacyFeedbackID != nil {
		id = *s.LegacyFeedbackID
	}
	photos := append(append([]string{}, first.GetStringSlice("photos")...), first.GetStringSlice("legacy_photos")...)
	status := "waiting"
	if s.Status != nil {
		status = *s.Status
	}
	requester := ""
	if s.Requester != nil {
		if s.Requester.Type == "user" {
			requester = s.Requester.ID
		}
	}
	return map[string]any{"id": id, "conversationId": s.ID, "guestChat": s.GuestChat, "message": first.GetString("text"), "status": status, "createdBy": requester, "arcade": arcade, "arcadeId": arcade, "arcadeName": s.Inbox.Name, "arcadeHasOfficial": s.Inbox.Type == "arcade", "photos": photos, "created": first.GetString("created"), "updated": s.LastMessageAt, "lastMessageAt": s.LastMessageAt}
}
func LegacyThread(app core.App, user, cid, beforeID string) (map[string]any, error) {
	return LegacyThreadFor(app, Principal{UserID: user}, cid, beforeID)
}
func LegacyThreadFor(app core.App, viewer Principal, cid, beforeID string) (map[string]any, error) {
	s, err := GetFor(app, viewer, cid)
	if err != nil {
		return nil, err
	}
	feedback, err := LegacyItem(app, s)
	if err != nil {
		return nil, err
	}
	before := 0
	if beforeID != "" {
		m, e := app.FindFirstRecordByFilter(Messages, "conversation={:c} && (id={:id} || legacy_message_id={:id})", dbx.Params{"c": cid, "id": beforeID})
		if e != nil {
			return nil, apis.NewBadRequestError("Invalid cursor.", nil)
		}
		before = m.GetInt("sequence")
	}
	page, err := HistoryFor(app, viewer, cid, before, -1, 50)
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	next := ""
	for _, m := range page.Items {
		if m.Sequence > 1 {
			items = append(items, LegacyMessage(m))
		}
	}
	if page.NextBeforeSequence != nil && len(items) > 0 {
		next = items[0]["id"].(string)
	}
	return map[string]any{"conversationId": cid, "feedback": feedback, "items": items, "next": next}, nil
}
func LegacyList(app core.App, user, createdBy, arcade, status string) ([]map[string]any, error) {
	return LegacyListFor(app, Principal{UserID: user}, createdBy, arcade, status)
}
func LegacyListFor(app core.App, viewer Principal, createdBy, arcade, status string) ([]map[string]any, error) {
	q := url.Values{"kind": {"inquiry"}, "limit": {"50"}}
	if arcade != "" {
		q.Set("inbox", "arcade:"+arcade)
	}
	out := []map[string]any{}
	for {
		page, err := ListFor(app, viewer, q)
		if err != nil {
			return nil, err
		}
		ids := []string{}
		for _, s := range page.Items {
			if status != "" && (s.Status == nil || *s.Status != status) {
				continue
			}
			if createdBy != "" && s.Requester.ID != createdBy && !(viewer.UserID == createdBy && s.Counterpart.ID == s.Requester.ID) {
				continue
			}
			ids = append(ids, s.ID)
		}
		if len(ids) > 0 {
			col, err := app.FindCollectionByNameOrId(Messages)
			if err != nil {
				return nil, err
			}
			raw, _ := json.Marshal(ids)
			rows := []*core.Record{}
			err = app.RecordQuery(col).AndWhere(dbx.NewExp("sequence=1 AND conversation IN (SELECT value FROM json_each({:ids}))", dbx.Params{"ids": string(raw)})).All(&rows)
			if err != nil {
				return nil, err
			}
			first := map[string]*core.Record{}
			for _, r := range rows {
				first[r.GetString("conversation")] = r
			}
			for _, s := range page.Items {
				if m := first[s.ID]; m != nil {
					out = append(out, legacyItem(s, m))
				}
			}
		}
		if page.NextCursor == nil {
			break
		}
		q.Set("cursor", *page.NextCursor)
	}
	return out, nil
}

func LegacySender(app core.App, user, cid string) *Identity {
	q, err := inquiry(app, cid)
	if err == nil && q.GetString("requester") == user {
		return &Identity{Type: "user", ID: user}
	}
	return nil
}
