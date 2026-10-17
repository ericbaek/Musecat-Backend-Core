package delegation

import (
	"database/sql"
	"errors"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"net/url"
	"time"
)

type Actor struct {
	UserID  string
	Session string
}
type AuthorizationInput struct {
	ClientID        string
	RedirectURI     string
	ResponseType    string
	Scope           []string
	State           string
	Challenge       string
	ChallengeMethod string
}
type ConsentScope struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}
type Consent struct {
	ClientID    string         `json:"client_id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Scopes      []ConsentScope `json:"requested_scopes"`
	Expires     string         `json:"expires_at"`
}
type Decision struct {
	Request string   `json:"request"`
	Approve bool     `json:"approve"`
	Scopes  []string `json:"scopes"`
}

func newRecord(app core.App, name string) (*core.Record, error) {
	c, e := app.FindCollectionByNameOrId(name)
	if e != nil {
		return nil, e
	}
	return core.NewRecord(c), nil
}
func find(app core.App, name, filter string, params dbx.Params) (*core.Record, error) {
	return app.FindFirstRecordByFilter(name, filter, params)
}
func (m *Manager) actor(app core.App, a Actor) (*core.Record, error) {
	if m == nil {
		return nil, &OAuthError{Code: "temporarily_unavailable", Status: 503}
	}
	u, e := app.FindAuthRecordByToken(a.Session, core.TokenTypeAuth)
	if e != nil || u.Collection().Name != "user" || u.Id != a.UserID {
		return nil, &OAuthError{Code: "access_denied", Status: 401}
	}
	if e = m.checkUser(app, u); e != nil {
		return nil, &OAuthError{Code: "access_denied", Status: 403}
	}
	return u, nil
}
func (m *Manager) Begin(app core.App, in AuthorizationInput) (string, error) {
	if m == nil {
		return "", &OAuthError{Code: "temporarily_unavailable", Status: 503}
	}
	c, ok := m.clients[in.ClientID]
	if !ok || !contains(c.RedirectURIs, in.RedirectURI) {
		return "", oauthError("invalid_request")
	}
	fingerprint, e := m.clientHash(app, c)
	if e != nil {
		return "", e
	}
	reject := func(err error) (string, error) {
		// A malformed state remains a local error; never reflect an unbounded value.
		if len(in.State) == 0 || len(in.State) > 512 {
			return "", err
		}
		target, _ := url.Parse(in.RedirectURI)
		q := target.Query()
		q.Set("state", in.State)
		q.Set("iss", m.issuer)
		q.Set("error", err.(*OAuthError).Code)
		target.RawQuery = q.Encode()
		return target.String(), err
	}
	if in.ResponseType != "code" {
		return reject(oauthError("unsupported_response_type"))
	}
	if len(in.State) == 0 || len(in.State) > 512 || !pkce.MatchString(in.Challenge) || in.ChallengeMethod != "S256" {
		return reject(oauthError("invalid_request"))
	}
	if len(in.Scope) == 0 && contains(c.Scopes, AccountAccess) {
		in.Scope = []string{AccountAccess}
	}
	scopes, e := uniqueScopes(in.Scope, c.Scopes)
	if e != nil {
		return reject(e)
	}
	handle := random()
	r, e := newRecord(app, Requests)
	if e != nil {
		return "", e
	}
	r.Set("handle_hash", hash(handle))
	r.Set("client", c.ID)
	r.Set("config_hash", fingerprint)
	r.Set("redirect_uri", in.RedirectURI)
	r.Set("state", in.State)
	r.Set("challenge", in.Challenge)
	r.Set("scopes", scopes)
	r.Set("expires", time.Now().UTC().Add(5*time.Minute))
	if e = app.Save(r); e != nil {
		return "", e
	}
	target, _ := url.Parse(m.consentURL)
	q := target.Query()
	q.Set("request", handle)
	target.RawQuery = q.Encode()
	return target.String(), nil
}
func (m *Manager) pending(app core.App, handle string) (*core.Record, registeredClient, error) {
	if m == nil {
		return nil, registeredClient{}, &OAuthError{Code: "temporarily_unavailable", Status: 503}
	}
	if !pkce.MatchString(handle) {
		return nil, registeredClient{}, oauthError("invalid_request")
	}
	r, e := find(app, Requests, "handle_hash={:hash}", dbx.Params{"hash": hash(handle)})
	if e != nil {
		if !errors.Is(e, sql.ErrNoRows) {
			return nil, registeredClient{}, e
		}
		return nil, registeredClient{}, oauthError("invalid_request")
	}
	c, ok := m.clients[r.GetString("client")]
	fingerprint, e := m.clientHash(app, c)
	if e != nil {
		return nil, registeredClient{}, e
	}
	if !ok || fingerprint != r.GetString("config_hash") || !r.GetDateTime("expires").Time().After(time.Now()) {
		return nil, registeredClient{}, oauthError("invalid_request")
	}
	return r, c, nil
}
func (m *Manager) ReadConsent(app core.App, a Actor, handle string) (Consent, error) {
	var out Consent
	e := app.RunInTransaction(func(tx core.App) error {
		if _, e := m.actor(tx, a); e != nil {
			return e
		}
		r, c, e := m.pending(tx, handle)
		if e != nil {
			return e
		}
		if r.GetString("user") != "" && (r.GetString("user") != a.UserID || r.GetString("session_hash") != hash(a.Session)) {
			return &OAuthError{Code: "access_denied", Status: 403}
		}
		r.Set("user", a.UserID)
		r.Set("session_hash", hash(a.Session))
		if e := tx.Save(r); e != nil {
			return e
		}
		out = Consent{ClientID: c.ID, Name: c.Name, Description: c.Description, Scopes: []ConsentScope{}, Expires: r.GetDateTime("expires").Time().Format(time.RFC3339)}
		for _, s := range r.GetStringSlice("scopes") {
			out.Scopes = append(out.Scopes, ConsentScope{ID: s, Description: scopeLabels[s]})
		}
		return nil
	})
	return out, e
}
func (m *Manager) Decide(app core.App, a Actor, in Decision) (string, error) {
	var target string
	e := app.RunInTransaction(func(tx core.App) error {
		u, e := m.actor(tx, a)
		if e != nil {
			return e
		}
		r, c, e := m.pending(tx, in.Request)
		if e != nil {
			return e
		}
		if r.GetString("user") != a.UserID || r.GetString("session_hash") != hash(a.Session) {
			return &OAuthError{Code: "access_denied", Status: 403}
		}
		callback, _ := url.Parse(r.GetString("redirect_uri"))
		q := callback.Query()
		q.Set("state", r.GetString("state"))
		q.Set("iss", m.issuer)
		if !in.Approve {
			q.Set("error", "access_denied")
		} else {
			selected, e := uniqueScopes(in.Scopes, r.GetStringSlice("scopes"))
			if e != nil {
				return e
			}
			grant, e := find(tx, Grants, "user={:user} && client={:client}", dbx.Params{"user": u.Id, "client": c.ID})
			if errors.Is(e, sql.ErrNoRows) {
				grant, e = newRecord(tx, Grants)
				if e != nil {
					return e
				}
				grant.Set("user", u.Id)
				grant.Set("client", c.ID)
				grant.Set("subject", random())
			} else if e != nil {
				return e
			}
			grant.Set("generation", random())
			grant.Set("config_hash", r.GetString("config_hash"))
			grant.Set("account_key", hash(u.TokenKey()))
			grant.Set("scopes", selected)
			grant.Set("revoked", false)
			if e = tx.Save(grant); e != nil {
				return e
			}
			raw := random()
			code, e := newRecord(tx, Codes)
			if e != nil {
				return e
			}
			code.Set("grant", grant.Id)
			code.Set("generation", grant.GetString("generation"))
			code.Set("scopes", selected)
			code.Set("code_hash", hash(raw))
			code.Set("redirect_uri", r.GetString("redirect_uri"))
			code.Set("challenge", r.GetString("challenge"))
			code.Set("expires", time.Now().UTC().Add(90*time.Second))
			if e = tx.Save(code); e != nil {
				return e
			}
			q.Set("code", raw)
		}
		callback.RawQuery = q.Encode()
		target = callback.String()
		return tx.Delete(r)
	})
	return target, e
}
func (m *Manager) activeGrant(app core.App, row *core.Record, clientID string) (*core.Record, *core.Record, error) {
	grant, e := app.FindRecordById(Grants, row.GetString("grant"))
	if e != nil {
		return nil, nil, oauthError("invalid_grant")
	}
	c, ok := m.clients[grant.GetString("client")]
	fingerprint, e := m.clientHash(app, c)
	if e != nil {
		return nil, nil, oauthError("invalid_grant")
	}
	if !ok || (clientID != "" && c.ID != clientID) || grant.GetBool("revoked") || grant.GetString("config_hash") != fingerprint || row.GetString("generation") != grant.GetString("generation") {
		return nil, nil, oauthError("invalid_grant")
	}
	u, e := app.FindRecordById("user", grant.GetString("user"))
	if e != nil || hash(u.TokenKey()) != grant.GetString("account_key") {
		return nil, nil, oauthError("invalid_grant")
	}
	if e = m.checkUser(app, u); e != nil {
		return nil, nil, oauthError("invalid_grant")
	}
	for _, s := range row.GetStringSlice("scopes") {
		if !contains(grant.GetStringSlice("scopes"), s) || !contains(c.Scopes, s) {
			return nil, nil, oauthError("invalid_grant")
		}
	}
	return grant, u, nil
}
