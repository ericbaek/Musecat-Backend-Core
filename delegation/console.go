package delegation

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"time"
)

type ConnectedAccount struct {
	Subject   string   `json:"subject"`
	Name      string   `json:"name,omitempty"`
	Scopes    []string `json:"scopes"`
	Connected string   `json:"connected"`
}

// The app sees only its pairwise subject; profile names require a profile grant.
func (m *Manager) ConnectedAccounts(app core.App, clientID string, page int) ([]ConnectedAccount, int, error) {
	out := []ConnectedAccount{}
	if m == nil {
		return out, 0, nil
	}
	c, ok := m.lookup(clientID)
	if !ok {
		return out, 0, nil
	}
	fingerprint, err := m.clientHash(app, c)
	if err != nil {
		return out, 0, err
	}
	params := dbx.Params{"client": clientID, "fingerprint": fingerprint, "now": time.Now().UTC().Format("2006-01-02 15:04:05.000Z")}
	// Count only grants with an unexpired, current token family. Never expose user IDs.
	where := `g.client={:client} AND g.config_hash={:fingerprint} AND g.revoked=false AND EXISTS(SELECT 1 FROM oauth_delegated_token t WHERE t."grant"=g.id AND t.generation=g.generation AND t.refresh_used=false AND t.refresh_expires>{:now})`
	var total int
	if err = app.DB().NewQuery("SELECT count(*) FROM oauth_grant g WHERE " + where).Bind(params).Row(&total); err != nil {
		return out, 0, err
	}
	var ids []struct {
		ID string `db:"id"`
	}
	params["offset"] = (page - 1) * 25
	if err = app.DB().NewQuery("SELECT g.id FROM oauth_grant g WHERE " + where + " ORDER BY g.updated DESC,g.id LIMIT 25 OFFSET {:offset}").Bind(params).All(&ids); err != nil {
		return out, 0, err
	}
	for _, id := range ids {
		tokens, e := app.FindRecordsByFilter(Tokens, "grant={:id} && refresh_used=false && refresh_expires>{:now}", "", 1, 0, dbx.Params{"id": id.ID, "now": params["now"]})
		if e != nil {
			return out, 0, e
		}
		if len(tokens) == 0 {
			continue
		}
		grant, u, e := m.activeGrant(app, tokens[0], clientID)
		if e != nil {
			continue
		}
		account := ConnectedAccount{Subject: grant.GetString("subject"), Scopes: grant.GetStringSlice("scopes"), Connected: grant.GetDateTime("created").Time().Format(time.RFC3339)}
		if contains(account.Scopes, Profile) || contains(account.Scopes, "profile:read") || contains(account.Scopes, AccountAccess) {
			account.Name = u.GetString("username")
		}
		out = append(out, account)
	}
	return out, total, nil
}
