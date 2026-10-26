package messaging

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/arcadereport"
	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

func loadParties(app core.App, ids []Identity) (map[string]Party, error) {
	out := map[string]Party{}
	users, arcades := []string{}, []string{}
	for _, p := range ids {
		out[p.Type+":"+p.ID] = Party{Type: p.Type, ID: p.ID, Name: "", RoleTags: []string{}}
		if p.Type == "user" && p.ID != "" {
			users = append(users, p.ID)
		} else if p.Type == "guest" {
			out[p.Type+":"+p.ID] = Party{Type: "guest", ID: p.ID, Name: "비회원", RoleTags: []string{}}
		} else if p.Type == "arcade" {
			arcades = append(arcades, p.ID)
		} else if p.Type == "team" {
			out[p.Type+":"+p.ID] = Party{Type: p.Type, ID: p.ID, Name: "뮤즈캣 팀", RoleTags: []string{"staff"}}
		}
	}
	if len(users) > 0 {
		rows, err := app.FindRecordsByIds("user", users)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			p := out["user:"+r.Id]
			p.Name = r.GetString("username")
			if r.GetBool("withdrawn") {
				p.Name = "탈퇴한 사용자"
			} else {
				p.RoleTags = append([]string{}, r.GetStringSlice("tags")...)
				for _, tag := range r.GetStringSlice("tag") {
					if !slices.Contains(p.RoleTags, tag) {
						p.RoleTags = append(p.RoleTags, tag)
					}
				}
			}
			out["user:"+r.Id] = p
		}
		infos, err := app.FindRecordsByIds("user_info", users)
		if err != nil {
			return nil, err
		}
		for _, r := range infos {
			p := out["user:"+r.Id]
			if p.Name == "탈퇴한 사용자" {
				continue
			}
			if name := r.GetString("nickname"); name != "" {
				p.Name = name
			}
			if avatar := r.GetString("avatar"); avatar != "" {
				p.AvatarURL = ptr("/api/files/" + r.Collection().Id + "/" + r.Id + "/" + url.PathEscape(avatar))
			}
			out["user:"+r.Id] = p
		}
	}
	if len(arcades) > 0 {
		rows, err := app.FindRecordsByIds("arcade", arcades)
		if err != nil {
			return nil, err
		}
		basicIDs := []string{}
		for _, r := range rows {
			basicIDs = append(basicIDs, r.GetString("basic"))
		}
		basics, err := app.FindRecordsByIds("arcade_basic", basicIDs)
		if err != nil {
			return nil, err
		}
		names := map[string]string{}
		for _, b := range basics {
			names[b.Id] = b.GetString("name")
		}
		for _, r := range rows {
			out["arcade:"+r.Id] = Party{Type: "arcade", ID: r.Id, Name: names[r.GetString("basic")], RoleTags: []string{"official"}}
		}
	}
	return out, nil
}
func messageDTO(app core.App, r *core.Record, parties map[string]Party) Message {
	sender := Party{Type: "user", ID: r.GetString("sender_user"), Name: r.GetString("sender_name"), RoleTags: append([]string{}, r.GetStringSlice("sender_roles")...)}
	if avatar := r.GetString("sender_avatar"); avatar != "" {
		sender.AvatarURL = ptr(avatar)
	}
	if guest := r.GetString("sender_guest"); guest != "" {
		sender = Party{Type: "guest", ID: guest, Name: "비회원", RoleTags: []string{}}
	}
	if parties == nil {
		parties, _ = loadParties(app, []Identity{{sender.Type, sender.ID}, {r.GetString("acting_type"), r.GetString("acting_id")}})
	}
	if current, ok := parties[sender.Type+":"+sender.ID]; ok && current.Name != "" {
		sender = current
	}
	acting := sender
	if r.GetString("acting_type") != "user" {
		key := r.GetString("acting_type") + ":" + r.GetString("acting_id")
		if parties == nil {
			parties, _ = loadParties(app, []Identity{{r.GetString("acting_type"), r.GetString("acting_id")}})
		}
		acting = parties[key]
	}
	photos := []Attachment{}
	names := append(append([]string{}, r.GetStringSlice("photos")...), r.GetStringSlice("legacy_photos")...)
	for _, f := range names {
		photos = append(photos, Attachment{Name: f, URL: "/user/conversations/" + r.GetString("conversation") + "/attachments/" + r.Id + "/" + url.PathEscape(f)})
	}
	return Message{ID: r.Id, ConversationID: r.GetString("conversation"), Sequence: r.GetInt("sequence"), SenderUserID: r.GetString("sender_user"), Sender: sender, ActingAs: acting, ClientMessageID: r.GetString("client_message_id"), Text: r.GetString("text"), Mentions: append([]string{}, r.GetStringSlice("mentions")...), Photos: photos, CreatedAt: r.GetString("created")}
}
func LegacyConversation(app core.App, legacy string) (string, error) {
	q, err := app.FindFirstRecordByFilter(Inquiries, "legacy_feedback_id={:id}", dbx.Params{"id": legacy})
	if err != nil {
		return "", err
	}
	return q.GetString("conversation"), nil
}
func Get(app core.App, user, id string) (Summary, error) {
	return GetFor(app, Principal{UserID: user}, id)
}
func GetFor(app core.App, viewer Principal, id string) (Summary, error) {
	c, u, err := access(app, viewer, id)
	if err != nil {
		return Summary{}, err
	}
	items, err := summaries(app, u, []*core.Record{c})
	if err != nil {
		return Summary{}, err
	}
	return items[0], nil
}
func allowedSQL(p *participant) (string, dbx.Params) {
	if p.Kind == "guest" {
		return "c.kind='inquiry' AND EXISTS(SELECT 1 FROM messaging_inquiry i WHERE i.conversation=c.id AND i.requester_type='guest' AND i.requester={:user} AND i.guest_key_hash={:guest})", dbx.Params{"user": p.ID, "guest": p.guestHash}
	}
	u := p.User
	owns := []string{}
	if slices.Contains(u.GetStringSlice("tags"), "arcade_owner") || slices.Contains(u.GetStringSlice("tag"), "arcade_owner") {
		owns = u.GetStringSlice("owns")
	}
	raw, _ := json.Marshal(owns)
	return `(c.kind='dm' AND EXISTS(SELECT 1 FROM messaging_party p WHERE p.conversation=c.id AND p.party_type='user' AND p.party_id={:user})) OR (c.kind='inquiry' AND EXISTS(SELECT 1 FROM messaging_inquiry i WHERE i.conversation=c.id AND (i.requester={:user} OR {:staff}=1 OR (i.inbox_type='arcade' AND i.inbox_id IN (SELECT value FROM json_each({:owns})) AND EXISTS(SELECT 1 FROM arcade a WHERE a.id=i.inbox_id)))))`, dbx.Params{"user": u.Id, "staff": supportfeedback.IsStaff(u), "owns": string(raw)}
}
func ListConversations(app core.App, user string, q url.Values) (List, error) {
	return ListFor(app, Principal{UserID: user}, q)
}
func ListFor(app core.App, viewer Principal, q url.Values) (List, error) {
	out := List{Items: []Summary{}}
	u, err := viewer.resolve(app)
	if err != nil {
		return out, err
	}
	limit := 30
	if q.Get("limit") != "" {
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil || limit < 1 || limit > 50 {
			return out, apis.NewBadRequestError("Invalid limit.", nil)
		}
	}
	acl, params := allowedSQL(u)
	where := "(" + acl + ")"
	if kind := q.Get("kind"); kind != "" {
		if kind != "dm" && kind != "inquiry" {
			return out, apis.NewBadRequestError("Invalid kind.", nil)
		}
		where += " AND c.kind={:kind}"
		params["kind"] = kind
	}
	if inbox := q.Get("inbox"); inbox != "" {
		parts := strings.SplitN(inbox, ":", 2)
		if len(parts) != 2 {
			return out, apis.NewBadRequestError("Invalid inbox.", nil)
		}
		where += " AND EXISTS(SELECT 1 FROM messaging_inquiry i WHERE i.conversation=c.id AND i.inbox_type={:it} AND i.inbox_id={:ii})"
		params["it"] = parts[0]
		params["ii"] = parts[1]
	}
	if search := strings.TrimSpace(q.Get("q")); search != "" {
		if len([]rune(search)) > 100 {
			return out, apis.NewBadRequestError("Search is too long.", nil)
		}
		where += ` AND (EXISTS(SELECT 1 FROM messaging_message m WHERE m.id=c.last_message AND m.text LIKE {:q}) OR EXISTS(SELECT 1 FROM messaging_party p LEFT JOIN user u ON p.party_type='user' AND u.id=p.party_id LEFT JOIN user_info ui ON ui.id=u.id LEFT JOIN arcade a ON p.party_type='arcade' AND a.id=p.party_id LEFT JOIN arcade_basic b ON b.id=a.basic WHERE p.conversation=c.id AND (u.username LIKE {:q} OR ui.nickname LIKE {:q} OR b.name LIKE {:q})))`
		params["q"] = "%" + search + "%"
	}
	if cursor := q.Get("cursor"); cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(cursor)
		var tuple []string
		if e != nil || json.Unmarshal(raw, &tuple) != nil || len(tuple) != 3 {
			return out, apis.NewBadRequestError("Invalid cursor.", nil)
		}
		where += " AND (COALESCE(NULLIF(c.last_message_at,''),c.created),c.created,c.id)<({:at},{:created},{:id})"
		params["at"], params["created"], params["id"] = tuple[0], tuple[1], tuple[2]
	}
	var ids []struct {
		ID string `db:"id"`
	}
	err = app.DB().NewQuery("SELECT c.id FROM messaging_conversation c WHERE " + where + " ORDER BY COALESCE(NULLIF(c.last_message_at,''),c.created) DESC,c.created DESC,c.id DESC LIMIT " + strconv.Itoa(limit+1)).Bind(params).All(&ids)
	if err != nil {
		return out, err
	}
	hasMore := len(ids) > limit
	if hasMore {
		ids = ids[:limit]
	}
	if len(ids) == 0 {
		return out, nil
	}
	keys := []string{}
	for _, id := range ids {
		keys = append(keys, id.ID)
	}
	rows, err := app.FindRecordsByIds(Conversations, keys)
	if err != nil {
		return out, err
	}
	byID := map[string]*core.Record{}
	for _, r := range rows {
		byID[r.Id] = r
	}
	ordered := []*core.Record{}
	for _, id := range keys {
		ordered = append(ordered, byID[id])
	}
	out.Items, err = summaries(app, u, ordered)
	if err != nil {
		return out, err
	}
	if hasMore {
		last := ordered[len(ordered)-1]
		at := last.GetString("last_message_at")
		if at == "" {
			at = last.GetString("created")
		}
		raw, _ := json.Marshal([]string{at, last.GetString("created"), last.Id})
		out.NextCursor = ptr(base64.RawURLEncoding.EncodeToString(raw))
	}
	return out, nil
}
func summaries(app core.App, u *participant, rows []*core.Record) ([]Summary, error) {
	ids, latestIDs := []string{}, []string{}
	for _, r := range rows {
		ids = append(ids, r.Id)
		if m := r.GetString("last_message"); m != "" {
			latestIDs = append(latestIDs, m)
		}
	}
	raw, _ := json.Marshal(ids)
	params := dbx.Params{"ids": string(raw), "u": u.ID}
	queryRecords := func(col string) ([]*core.Record, error) {
		collection, e := app.FindCollectionByNameOrId(col)
		if e != nil {
			return nil, e
		}
		result := []*core.Record{}
		query := app.RecordQuery(collection).AndWhere(dbx.NewExp("conversation IN (SELECT value FROM json_each({:ids}))", params))
		if col == Reads {
			query.AndWhere(dbx.HashExp{"user": u.ID})
		}
		e = query.All(&result)
		return result, e
	}
	ps, err := queryRecords(Parties)
	if err != nil {
		return nil, err
	}
	qs, err := queryRecords(Inquiries)
	if err != nil {
		return nil, err
	}
	rs, err := queryRecords(Reads)
	if err != nil {
		return nil, err
	}
	identities := []Identity{}
	peerBy := map[string]string{}
	for _, p := range ps {
		identities = append(identities, Identity{p.GetString("party_type"), p.GetString("party_id")})
		if p.GetString("party_type") == "user" && p.GetString("party_id") != u.ID {
			peerBy[p.GetString("conversation")] = p.GetString("party_id")
		}
	}
	latest := map[string]*core.Record{}
	if len(latestIDs) > 0 {
		ms, e := app.FindRecordsByIds(Messages, latestIDs)
		if e != nil {
			return nil, e
		}
		for _, m := range ms {
			latest[m.Id] = m
		}
	}
	for _, m := range latest {
		identities = append(identities, Identity{"user", m.GetString("sender_user")}, Identity{m.GetString("acting_type"), m.GetString("acting_id")})
	}
	parties, err := loadParties(app, identities)
	if err != nil {
		return nil, err
	}
	cases := map[string]*core.Record{}
	for _, q := range qs {
		cases[q.GetString("conversation")] = q
	}
	reads := map[string]int{}
	for _, r := range rs {
		if r.GetString("user") == u.ID {
			reads[r.GetString("conversation")] = r.GetInt("last_read_sequence")
		}
	}
	var counts []struct {
		ID    string `db:"id"`
		Count int    `db:"count"`
	}
	err = app.DB().NewQuery(`SELECT m.conversation id,count(*) count FROM messaging_message m JOIN messaging_conversation c ON c.id=m.conversation LEFT JOIN messaging_inquiry i ON i.conversation=c.id LEFT JOIN messaging_read r ON r.conversation=c.id AND r.user={:u} WHERE c.id IN (SELECT value FROM json_each({:ids})) AND m.sequence>COALESCE(r.last_read_sequence,0) AND m.sender_user!={:u} AND ((c.kind='dm' AND m.sender_user!={:u}) OR (c.kind='inquiry' AND ((i.requester={:u} AND m.acting_type NOT IN ('user','guest')) OR (i.requester!={:u} AND m.acting_type IN ('user','guest'))))) GROUP BY m.conversation`).Bind(params).All(&counts)
	if err != nil {
		return nil, err
	}
	unread := map[string]int{}
	for _, c := range counts {
		unread[c.ID] = c.Count
	}
	// Batch receive/block/ban checks for all DM peers. No per-row policy query.
	peers := []string{}
	for _, id := range peerBy {
		peers = append(peers, id)
	}
	peerRaw, _ := json.Marshal(peers)
	var eligible []struct {
		ID    string `db:"id"`
		Email string `db:"email"`
	}
	err = app.DB().NewQuery(`SELECT u.id,u.email FROM user u LEFT JOIN messaging_preference p ON p.user=u.id WHERE (p.id IS NULL OR p.receive='everyone') AND u.id IN (SELECT value FROM json_each({:peers})) AND u.withdrawn=false AND NOT EXISTS(SELECT 1 FROM messaging_block b WHERE (b.user={:u} AND b.blocked_user=u.id) OR (b.user=u.id AND b.blocked_user={:u})) AND NOT EXISTS(SELECT 1 FROM user_ban b WHERE b.id=u.id AND (b.until='' OR b.until>strftime('%Y-%m-%d %H:%M:%fZ','now')))`).Bind(dbx.Params{"peers": string(peerRaw), "u": u.ID}).All(&eligible)
	if err != nil {
		return nil, err
	}
	can := map[string]bool{}
	hashes := []string{}
	userHash := map[string]string{}
	for _, p := range eligible {
		sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(p.Email))))
		hash := hex.EncodeToString(sum[:])
		hashes = append(hashes, hash)
		userHash[p.ID] = hash
	}
	hashJSON, _ := json.Marshal(hashes)
	var banned []struct {
		Hash string `db:"hashed_email"`
	}
	err = app.DB().NewQuery("SELECT hashed_email FROM user_ban WHERE hashed_email IN (SELECT value FROM json_each({:hashes})) AND (until='' OR until>strftime('%Y-%m-%d %H:%M:%fZ','now'))").Bind(dbx.Params{"hashes": string(hashJSON)}).All(&banned)
	if err != nil {
		return nil, err
	}
	blockedHashes := map[string]bool{}
	for _, b := range banned {
		blockedHashes[b.Hash] = true
	}
	for _, p := range eligible {
		can[p.ID] = !blockedHashes[userHash[p.ID]]
	}

	// Resolve arcade existence once for official-owner policy, including dual-role requesters.
	arcadeIDs := []string{}
	for _, q := range qs {
		if q.GetString("inbox_type") == "arcade" {
			arcadeIDs = append(arcadeIDs, q.GetString("inbox_id"))
		}
	}
	arcades := map[string]*core.Record{}
	if len(arcadeIDs) > 0 {
		current, e := app.FindRecordsByIds("arcade", arcadeIDs)
		if e != nil {
			return nil, e
		}
		for _, a := range current {
			arcades[a.Id] = a
		}
	}
	out := []Summary{}
	for _, c := range rows {
		s := Summary{ID: c.Id, Kind: c.GetString("kind"), LatestSequence: c.GetInt("last_sequence"), CreatedAt: c.GetString("created"), UnreadCount: unread[c.Id], LastReadSequence: reads[c.Id], AllowedSenderIdentities: []Identity{}, AllowedStatusTransitions: []string{}}
		if at := c.GetString("last_message_at"); at != "" {
			s.LastMessageAt = ptr(at)
		}
		if m := latest[c.GetString("last_message")]; m != nil {
			dto := messageDTO(app, m, parties)
			s.LastMessage = &dto
		}
		if s.Kind == "dm" {
			peer := peerBy[c.Id]
			s.Counterpart = parties["user:"+peer]
			s.CanSend = can[peer]
			if s.CanSend {
				s.AllowedSenderIdentities = []Identity{{"user", u.ID}}
			}
		} else {
			q := cases[c.Id]
			if q == nil {
				return nil, fmt.Errorf("inquiry missing for %s", c.Id)
			}
			s.Requester = ptr(parties[requesterType(q)+":"+q.GetString("requester")])
			s.Inbox = ptr(parties[q.GetString("inbox_type")+":"+q.GetString("inbox_id")])
			s.Counterpart = *s.Inbox
			if q.GetString("requester") != u.ID {
				s.Counterpart = *s.Requester
			}
			s.GuestChat = requesterType(q) == "guest" && q.GetString("guest_key_hash") != ""
			s.Status = ptr(q.GetString("status"))
			if id := q.GetString("legacy_feedback_id"); id != "" {
				s.LegacyFeedbackID = ptr(id)
			}
			permissions := inquiryPolicy(u, q, supportfeedback.IsStaff(u.User) || (q.GetString("inbox_type") == "arcade" && arcadereport.CanManage(u.User, arcades[q.GetString("inbox_id")])))
			s.AllowedSenderIdentities = permissions.identities
			s.AllowedStatusTransitions = permissions.statuses
			s.CanSend = len(permissions.identities) > 0
			s.CanManageStatus = len(permissions.statuses) > 0
		}
		out = append(out, s)
	}
	return out, nil
}
func History(app core.App, user, id string, before, after, limit int) (MessagePage, error) {
	return HistoryFor(app, Principal{UserID: user}, id, before, after, limit)
}
func HistoryFor(app core.App, viewer Principal, id string, before, after, limit int) (MessagePage, error) {
	out := MessagePage{Items: []Message{}}
	c, _, err := access(app, viewer, id)
	if err != nil {
		return out, err
	}
	if before < 0 || after < -1 || (before > 0 && after >= 0) || limit < 1 || limit > 200 || (after < 0 && limit > 100) {
		return out, apis.NewBadRequestError("Invalid message pagination.", nil)
	}
	filter := "conversation={:c}"
	params := dbx.Params{"c": id}
	sort := "-sequence"
	if before > 0 {
		filter += " && sequence < {:s}"
		params["s"] = before
	}
	if after >= 0 {
		filter += " && sequence > {:s}"
		params["s"] = after
		sort = "sequence"
	}
	rows, err := app.FindRecordsByFilter(Messages, filter, sort, limit+1, 0, params)
	if err != nil {
		return out, err
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	if after < 0 {
		slices.Reverse(rows)
	}
	identities := []Identity{}
	for _, m := range rows {
		identities = append(identities, Identity{m.GetString("acting_type"), m.GetString("acting_id")}, Identity{"user", m.GetString("sender_user")})
	}
	parties, err := loadParties(app, identities)
	if err != nil {
		return out, err
	}
	for _, m := range rows {
		out.Items = append(out.Items, messageDTO(app, m, parties))
	}
	out.LatestSequence = c.GetInt("last_sequence")
	if more && len(rows) > 0 {
		if after >= 0 {
			out.NextAfterSequence = ptr(rows[len(rows)-1].GetInt("sequence"))
		} else {
			out.NextBeforeSequence = ptr(rows[0].GetInt("sequence"))
		}
	}
	return out, nil
}
func BlockList(app core.App, user string) ([]Party, error) {
	rows, err := app.FindRecordsByFilter(Blocks, "user={:u}", "-created", 100, 0, dbx.Params{"u": user})
	if err != nil {
		return nil, err
	}
	ids := []Identity{}
	for _, r := range rows {
		ids = append(ids, Identity{"user", r.GetString("blocked_user")})
	}
	parties, err := loadParties(app, ids)
	out := []Party{}
	for _, p := range ids {
		out = append(out, parties[p.Type+":"+p.ID])
	}
	return out, err
}
func Recipients(app core.App, user, q string, limit int) ([]Party, error) {
	q = strings.TrimSpace(q)
	if len([]rune(q)) < 2 || len([]rune(q)) > 100 || limit < 1 || limit > 20 {
		return nil, apis.NewBadRequestError("Search requires 2 to 100 characters and limit 1 to 20.", nil)
	}
	var rows []struct {
		ID string `db:"id"`
	}
	err := app.DB().NewQuery("SELECT u.id FROM user u LEFT JOIN user_info ui ON ui.id=u.id WHERE u.id!={:u} AND u.withdrawn=false AND (u.username LIKE {:q} OR ui.nickname LIKE {:q}) ORDER BY u.username,u.id LIMIT " + strconv.Itoa(limit)).Bind(dbx.Params{"u": user, "q": "%" + q + "%"}).All(&rows)
	if err != nil {
		return nil, err
	}
	ids := []Identity{}
	for _, r := range rows {
		ids = append(ids, Identity{"user", r.ID})
	}
	parties, err := loadParties(app, ids)
	out := []Party{}
	for _, id := range ids {
		out = append(out, parties["user:"+id.ID])
	}
	return out, err
}
