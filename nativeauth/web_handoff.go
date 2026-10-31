package nativeauth

import (
	"time"

	user "github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

const WebCodeLifetime = 60
const WebSessionLifetime = 900

// WebClient identifies the confidential BFF that may exchange a native grant.
// Secret never belongs in an application bundle or a browser response.
type WebClient struct{ ID, Issuer, Secret string }
type WebCode struct {
	Code      string `json:"code"`
	UserID    string `json:"user_id"`
	ExpiresIn int    `json:"expires_in"`
}
type WebCredentials struct {
	Token     string `json:"token"`
	UserID    string `json:"user_id"`
	ExpiresIn int    `json:"expires_in"`
}

// InstallWebHandoffSchema is shared by fresh Core and guarded Full migrations.
// It changes neither existing session rows nor user/profile/domain data.
func InstallWebHandoffSchema(app core.App) error {
	if _, err := app.DB().NewQuery("SELECT id, client FROM native_auth_session LIMIT 0").Execute(); err != nil {
		return err
	}
	return app.RunInTransaction(func(tx core.App) error {
		for _, statement := range []string{
			`CREATE TABLE IF NOT EXISTS native_auth_web_code (hash TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES native_auth_session(id) ON DELETE CASCADE, user_id TEXT NOT NULL, client TEXT NOT NULL, issuer TEXT NOT NULL, challenge TEXT NOT NULL, expires INTEGER NOT NULL, consumed INTEGER NOT NULL DEFAULT 0)`,
			`CREATE INDEX IF NOT EXISTS idx_native_web_code_expiry ON native_auth_web_code(expires)`,
		} {
			if _, err := tx.DB().NewQuery(statement).Execute(); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s Service) webClient(id, issuer string) (WebClient, bool) {
	for _, client := range s.WebClients {
		if client.ID == id && client.Issuer == issuer && s.registered(id) && challengePattern.MatchString(client.Secret) {
			return client, true
		}
	}
	return WebClient{}, false
}

func (s Service) IssueWebCode(app core.App, access, client, issuer, challenge string) (WebCode, error) {
	var out WebCode
	if _, allowed := s.webClient(client, issuer); !allowed || !challengePattern.MatchString(challenge) {
		return out, ErrInvalid
	}
	err := app.RunInTransaction(func(tx core.App) error {
		u, session, err := s.AuthenticateSession(tx, access)
		if err != nil {
			return err
		}
		var owner struct {
			Client string `db:"client"`
		}
		if err := tx.DB().NewQuery("SELECT client FROM native_auth_session WHERE id={:id}").Bind(dbx.Params{"id": session}).One(&owner); err != nil {
			return err
		}
		if owner.Client != client {
			return ErrInvalid
		}
		if err := user.CheckActiveUser(tx, u); err != nil {
			return err
		}
		code, err := secret()
		if err != nil {
			return err
		}
		// Expired tickets carry no replay authority and need not accumulate forever.
		if _, err := tx.DB().NewQuery("DELETE FROM native_auth_web_code WHERE expires < {:now}").Bind(dbx.Params{"now": s.now()}).Execute(); err != nil {
			return err
		}
		_, err = tx.DB().NewQuery(`INSERT INTO native_auth_web_code (hash,session_id,user_id,client,issuer,challenge,expires) VALUES ({:hash},{:session},{:user},{:client},{:issuer},{:challenge},{:expires})`).Bind(dbx.Params{
			"hash": hash(code), "session": session, "user": u.Id, "client": client, "issuer": issuer, "challenge": challenge, "expires": s.now() + WebCodeLifetime,
		}).Execute()
		out = WebCode{code, u.Id, WebCodeLifetime}
		return err
	})
	if err != nil {
		return WebCode{}, err
	}
	return out, nil
}

// ExchangeWebCode is a confidential server-to-server exchange. The BFF sets an
// HttpOnly cookie; it must not forward this result in JSON, URLs or JavaScript.
func (s Service) ExchangeWebCode(app core.App, code, verifier, client, issuer, serverSecret string) (WebCredentials, error) {
	var out WebCredentials
	configured, allowed := s.webClient(client, issuer)
	if !allowed || !equal(hash(configured.Secret), hash(serverSecret)) || !verifierPattern.MatchString(verifier) || !challengePattern.MatchString(code) {
		return out, ErrInvalid
	}
	err := app.RunInTransaction(func(tx core.App) error {
		var ticket struct {
			Session, User, Client, Issuer, Challenge string
			Expires                                  int64
			Consumed                                 int
		}
		if err := tx.DB().NewQuery(`SELECT session_id AS session,user_id AS user,client,issuer,challenge,expires,consumed FROM native_auth_web_code WHERE hash={:hash}`).Bind(dbx.Params{"hash": hash(code)}).One(&ticket); err != nil {
			return ErrInvalid
		}
		if ticket.Consumed != 0 || ticket.Expires <= s.now() || ticket.Client != client || ticket.Issuer != issuer || !equal(ticket.Challenge, hash(verifier)) {
			return ErrInvalid
		}
		var family sessionRow
		if err := tx.DB().NewQuery("SELECT * FROM native_auth_session WHERE id={:id}").Bind(dbx.Params{"id": ticket.Session}).One(&family); err != nil {
			return ErrInvalid
		}
		if family.UserID != ticket.User || family.Client != client || family.Revoked != 0 || family.Expires <= s.now() || !s.registered(family.Client) {
			return ErrInvalid
		}
		u, err := account(tx, family.UserID, family.AccountKey)
		if err != nil {
			return err
		}
		if err := user.CheckActiveUser(tx, u); err != nil {
			return err
		}
		token, err := u.NewStaticAuthToken(time.Duration(WebSessionLifetime) * time.Second)
		if err != nil {
			return err
		}
		result, err := tx.DB().NewQuery("UPDATE native_auth_web_code SET consumed=1 WHERE hash={:hash} AND consumed=0").Bind(dbx.Params{"hash": hash(code)}).Execute()
		if err != nil {
			return err
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			return ErrInvalid
		}
		out = WebCredentials{token, u.Id, WebSessionLifetime}
		return nil
	})
	if err != nil {
		return WebCredentials{}, err
	}
	return out, nil
}
