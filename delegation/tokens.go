package delegation

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"strings"
	"time"
)

type TokenInput struct {
	GrantType    string
	Code         string
	RedirectURI  string
	Verifier     string
	RefreshToken string
}
type TokenResponse struct {
	IDToken      string `json:"id_token,omitempty"`
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

func (m *Manager) issue(app core.App, grant *core.Record, scopes []string, until time.Time) (TokenResponse, error) {
	at, rt := "mcat_at_"+random(), "mcat_rt_"+random()
	r, e := newRecord(app, Tokens)
	if e != nil {
		return TokenResponse{}, e
	}
	expiry := time.Now().UTC().Add(AccessSeconds * time.Second)
	if until.Before(expiry) {
		expiry = until
	}
	r.Set("grant", grant.Id)
	r.Set("generation", grant.GetString("generation"))
	r.Set("scopes", scopes)
	r.Set("access_hash", hash(at))
	r.Set("refresh_hash", hash(rt))
	r.Set("access_expires", expiry)
	r.Set("refresh_expires", until)
	if e = app.Save(r); e != nil {
		return TokenResponse{}, e
	}
	return TokenResponse{AccessToken: at, TokenType: "Bearer", ExpiresIn: int(time.Until(expiry).Seconds()), RefreshToken: rt, Scope: strings.Join(scopes, " ")}, nil
}
func (m *Manager) Exchange(app core.App, clientID string, in TokenInput) (TokenResponse, error) {
	var out TokenResponse
	var rejected error
	if m == nil {
		return out, &OAuthError{Code: "temporarily_unavailable", Status: 503}
	}
	e := app.RunInTransaction(func(tx core.App) error {
		var row *core.Record
		var err error
		switch in.GrantType {
		case "authorization_code":
			if !pkce.MatchString(in.Code) || !verifierPattern.MatchString(in.Verifier) || in.RefreshToken != "" {
				return oauthError("invalid_grant")
			}
			row, err = find(tx, Codes, "code_hash={:hash}", dbx.Params{"hash": hash(in.Code)})
		case "refresh_token":
			if !strings.HasPrefix(in.RefreshToken, "mcat_rt_") || len(in.RefreshToken) != 51 || in.Code != "" || in.Verifier != "" || in.RedirectURI != "" {
				return oauthError("invalid_grant")
			}
			row, err = find(tx, Tokens, "refresh_hash={:hash}", dbx.Params{"hash": hash(in.RefreshToken)})
		default:
			return oauthError("unsupported_grant_type")
		}
		if err != nil {
			return oauthError("invalid_grant")
		}
		grant, _, err := m.activeGrant(tx, row, clientID)
		if err != nil {
			return err
		}
		if row.GetBool("consumed") || row.GetBool("refresh_used") {
			grant.Set("revoked", true)
			if err = tx.Save(grant); err != nil {
				return err
			}
			rejected = oauthError("invalid_grant")
			return nil
		}
		until := time.Now().UTC().Add(30 * 24 * time.Hour)
		if in.GrantType == "authorization_code" {
			sum := sha256.Sum256([]byte(in.Verifier))
			challenge := base64.RawURLEncoding.EncodeToString(sum[:])
			if !row.GetDateTime("expires").Time().After(time.Now()) || row.GetString("redirect_uri") != in.RedirectURI || !same(challenge, row.GetString("challenge")) {
				return oauthError("invalid_grant")
			}
			row.Set("consumed", true)
		} else {
			until = row.GetDateTime("refresh_expires").Time()
			if !until.After(time.Now()) {
				return oauthError("invalid_grant")
			}
			row.Set("refresh_used", true)
		}
		if err = tx.Save(row); err != nil {
			return err
		}
		out, err = m.issue(tx, grant, row.GetStringSlice("scopes"), until)
		if err == nil && in.GrantType == "authorization_code" && contains(row.GetStringSlice("scopes"), OpenID) {
			out.IDToken, err = m.signIDToken(grant, row.GetString("nonce"), out.ExpiresIn)
		}
		return err
	})
	if e != nil {
		return TokenResponse{}, e
	}
	if rejected != nil {
		return TokenResponse{}, rejected
	}
	return out, nil
}

// ResolveAccess accepts only opaque delegation tokens, never Musecat session tokens.
func (m *Manager) ResolveAccess(app core.App, raw, scope string) (*core.Record, string, error) {
	if m == nil {
		return nil, "", &OAuthError{Code: "temporarily_unavailable", Status: 503}
	}
	invalid := &OAuthError{Code: "invalid_token", Status: 401}
	if !strings.HasPrefix(raw, "mcat_at_") || len(raw) != 51 {
		return nil, "", invalid
	}
	row, e := find(app, Tokens, "access_hash={:hash}", dbx.Params{"hash": hash(raw)})
	if e != nil || !row.GetDateTime("access_expires").Time().After(time.Now()) {
		return nil, "", invalid
	}
	grant, u, e := m.activeGrant(app, row, "")
	if e != nil {
		return nil, "", invalid
	}
	scopes := row.GetStringSlice("scopes")
	allowed := contains(scopes, scope) || (scope == "profile:read" && contains(scopes, Profile)) || (scope != OpenID && scope != Profile && contains(scopes, AccountAccess))
	if !allowed {
		return nil, "", &OAuthError{Code: "insufficient_scope", Status: 403}
	}
	return u, grant.GetString("subject"), nil
}
func (m *Manager) RevokeToken(app core.App, clientID, raw string) error {
	if m == nil {
		return &OAuthError{Code: "temporarily_unavailable", Status: 503}
	}
	return app.RunInTransaction(func(tx core.App) error {
		row, e := find(tx, Tokens, "access_hash={:hash} || refresh_hash={:hash}", dbx.Params{"hash": hash(raw)})
		if e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return nil
			}
			return e
		}
		grant, e := tx.FindRecordById(Grants, row.GetString("grant"))
		if e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return nil
			}
			return e
		}
		if grant.GetString("client") != clientID || row.GetString("generation") != grant.GetString("generation") {
			return nil
		}
		grant.Set("revoked", true)
		return tx.Save(grant)
	})
}

type GrantInfo struct {
	ID       string   `json:"id"`
	ClientID string   `json:"client_id"`
	Name     string   `json:"name"`
	Scopes   []string `json:"scopes"`
	Created  string   `json:"created"`
	Updated  string   `json:"updated"`
}

func (m *Manager) ListGrants(app core.App, userID string) ([]GrantInfo, error) {
	rows, e := app.FindRecordsByFilter(Grants, "user={:user} && revoked=false", "-updated", 0, 0, dbx.Params{"user": userID})
	if e != nil {
		return nil, e
	}
	out := []GrantInfo{}
	for _, r := range rows {
		name := r.GetString("client")
		if m != nil {
			if c, ok := m.lookup(name); ok {
				name = c.Name
			}
		}
		out = append(out, GrantInfo{ID: r.Id, ClientID: r.GetString("client"), Name: name, Scopes: r.GetStringSlice("scopes"), Created: r.GetDateTime("created").Time().Format(time.RFC3339), Updated: r.GetDateTime("updated").Time().Format(time.RFC3339)})
	}
	return out, nil
}
func RevokeGrant(app core.App, userID, id string) error {
	return app.RunInTransaction(func(tx core.App) error {
		r, e := find(tx, Grants, "id={:id} && user={:user}", dbx.Params{"id": id, "user": userID})
		if e != nil {
			return e
		}
		r.Set("revoked", true)
		if e = tx.Save(r); e != nil {
			return e
		}
		pending, e := tx.FindRecordsByFilter(Requests, "user={:user} && client={:client}", "", 0, 0, dbx.Params{"user": userID, "client": r.GetString("client")})
		if e != nil {
			return e
		}
		for _, p := range pending {
			if e = tx.Delete(p); e != nil {
				return e
			}
		}
		return nil
	})
}
func DeleteUserData(app core.App, userID string) error {
	for _, name := range []string{Requests, Grants} {
		rows, e := app.FindRecordsByFilter(name, "user={:user}", "", 0, 0, dbx.Params{"user": userID})
		if e != nil {
			return e
		}
		for _, r := range rows {
			if e = app.Delete(r); e != nil {
				return e
			}
		}
	}
	return nil
}
