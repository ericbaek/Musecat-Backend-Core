package messaging

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
	"strings"
	"time"
)

// ActiveUser refreshes identity for transactions, streams and protected files;
// HTTP entry points additionally use the existing RequireActiveUser middleware.
func ActiveUser(app core.App, id string) (*core.Record, error) {
	u, err := app.FindRecordById("user", id)
	if err != nil || u.GetBool("withdrawn") {
		return nil, apis.NewForbiddenError("Account is restricted.", nil)
	}
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(u.Email()))))
	hash := hex.EncodeToString(sum[:])
	rows, err := app.FindRecordsByFilter("user_ban", "id={:id} || hashed_email={:hash}", "", 0, 0, dbx.Params{"id": id, "hash": hash})
	if err != nil {
		return nil, err
	}
	for _, b := range rows {
		until := strings.TrimSpace(b.GetString("until"))
		dt, e := types.ParseDateTime(until)
		if until == "" || (e == nil && !dt.IsZero() && dt.Time().After(time.Now().UTC())) {
			return nil, apis.NewForbiddenError("Account is restricted.", nil)
		}
	}
	return u, nil
}
func inquiry(app core.App, id string) (*core.Record, error) {
	return app.FindFirstRecordByFilter(Inquiries, "conversation={:id}", dbx.Params{"id": id})
}

// Persisted mention metadata surfaces arcade inquiries in personal chat lists
// and staff realtime feeds. Dashboard management permissions remain separate.
const musecatMentionSQL = `EXISTS(SELECT 1 FROM messaging_message m, json_each(m.mentions) mention WHERE m.conversation=c.id AND mention.value='musecat')`

func mentionedConversations(app core.App, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	raw, _ := json.Marshal(ids)
	var rows []struct {
		ID string `db:"id"`
	}
	err := app.DB().NewQuery("SELECT c.id FROM messaging_conversation c WHERE c.id IN (SELECT value FROM json_each({:ids})) AND " + musecatMentionSQL).Bind(dbx.Params{"ids": string(raw)}).All(&rows)
	for _, row := range rows {
		out[row.ID] = true
	}
	return out, err
}

func Responder(app core.App, u, c *core.Record) bool {
	if supportfeedback.IsStaff(u) {
		return true
	}
	if c.GetString("inbox_type") != "arcade" {
		return false
	}
	f := core.NewRecord(&core.Collection{})
	f.Set("arcade", c.GetString("inbox_id"))
	return supportfeedback.IsOfficial(app, u, f)
}
func Access(app core.App, userID, id string) (*core.Record, *core.Record, error) {
	c, p, err := access(app, Principal{UserID: userID}, id)
	if err != nil {
		return nil, nil, err
	}
	return c, p.User, nil
}
func access(app core.App, viewer Principal, id string) (*core.Record, *participant, error) {
	p, err := viewer.resolve(app)
	if err != nil {
		return nil, nil, err
	}
	c, err := app.FindRecordById(Conversations, id)
	if err != nil {
		return nil, nil, apis.NewNotFoundError("Conversation not found.", nil)
	}
	allowed := false
	if c.GetString("kind") == "dm" && p.Kind == "user" {
		_, err = app.FindFirstRecordByFilter(Parties, "conversation={:c} && party_type='user' && party_id={:u}", dbx.Params{"c": id, "u": p.ID})
		allowed = err == nil
	} else if c.GetString("kind") == "inquiry" {
		q, e := inquiry(app, id)
		allowed = e == nil && (p.requests(q) || Responder(app, p.User, q))
	}
	if !allowed {
		return nil, nil, apis.NewNotFoundError("Conversation not found.", nil)
	}
	return c, p, nil
}
func Receive(app core.App, userID string) string {
	row, err := app.FindFirstRecordByFilter(Preferences, "user={:u}", dbx.Params{"u": userID})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && row.GetString("receive") == "everyone") {
		return "everyone"
	}
	return "nobody"
}
func CanDM(app core.App, from, to string) bool {
	if from == to || Receive(app, to) != "everyone" {
		return false
	}
	if _, err := ActiveUser(app, to); err != nil {
		return false
	}
	n, err := app.CountRecords(Blocks, dbx.NewExp("(user={:a} AND blocked_user={:b}) OR (user={:b} AND blocked_user={:a})", dbx.Params{"a": from, "b": to}))
	return err == nil && n == 0
}
func Peer(app core.App, id, user string) (string, error) {
	row, err := app.FindFirstRecordByFilter(Parties, "conversation={:c} && party_type='user' && party_id!={:u}", dbx.Params{"c": id, "u": user})
	if err != nil {
		return "", err
	}
	return row.GetString("party_id"), nil
}
func actor(app core.App, u *participant, c *core.Record, requested *Identity) (Identity, error) {
	own := Identity{Type: u.Kind, ID: u.ID}
	if c.GetString("kind") == "dm" {
		if requested != nil && *requested != own {
			return own, apis.NewForbiddenError("Invalid sender identity.", nil)
		}
		peer, err := Peer(app, c.Id, u.ID)
		if err != nil || !CanDM(app, u.ID, peer) {
			return own, apis.NewForbiddenError("Recipient is unavailable.", nil)
		}
		return own, nil
	}
	q, err := inquiry(app, c.Id)
	if err != nil {
		return own, err
	}
	permissions := inquiryPolicy(u, q, Responder(app, u.User, q))
	if requested == nil {
		if len(permissions.identities) > 1 {
			return own, apis.NewBadRequestError("Choose the sender identity.", nil)
		}
		if len(permissions.identities) == 1 {
			return permissions.identities[0], nil
		}
	}
	for _, identity := range permissions.identities {
		if requested != nil && *requested == identity {
			return identity, nil
		}
	}
	return own, apis.NewForbiddenError("Invalid sender identity.", nil)
}

// Shared by summaries and mutation validation; callers resolve current access first.
type inquiryPermissions struct {
	identities []Identity
	statuses   []string
}

func inquiryPolicy(u *participant, q *core.Record, responder bool) inquiryPermissions {
	p := inquiryPermissions{identities: []Identity{}, statuses: []string{}}
	if u.requests(q) {
		p.identities = append(p.identities, Identity{u.Kind, u.ID})
	}
	if responder {
		p.identities = append(p.identities, Identity{q.GetString("inbox_type"), q.GetString("inbox_id")})
		p.statuses = []string{"waiting", "recognised", "solved"}
	}
	return p
}
