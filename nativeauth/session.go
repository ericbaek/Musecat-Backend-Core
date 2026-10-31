// Package nativeauth owns first-party device sessions, separate from web and partner credentials.
package nativeauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

var ErrInvalid = errors.New("invalid native credential")
var verifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
var challengePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type Client struct{ ID, Callback string }
type Service struct {
	Clients []Client
	Now     func() time.Time
}
type Credentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	UserID       string `json:"user_id"`
}

func (s Service) now() int64 {
	if s.Now != nil {
		return s.Now().Unix()
	}
	return time.Now().Unix()
}
func hash(v string) string {
	b := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func secret() (string, error) {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), e
}
func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func (s Service) allowed(id, callback string) bool {
	for _, c := range s.Clients {
		if c.ID == id && c.Callback == callback {
			return true
		}
	}
	return false
}
func (s Service) registered(id string) bool {
	for _, client := range s.Clients {
		if client.ID == id {
			return true
		}
	}
	return false
}

// InstallSchema is reused by independent Core bootstrap and Full forward migrations.
// These private SQL tables have no PocketBase raw-collection API.
func InstallSchema(app core.App) error {
	users, err := app.FindCollectionByNameOrId("user")
	if err != nil {
		return err
	}
	if !users.IsAuth() {
		return fmt.Errorf("native sessions require the user auth collection")
	}
	return app.RunInTransaction(func(tx core.App) error {
		for _, q := range []string{
			`CREATE TABLE IF NOT EXISTS native_auth_code (hash TEXT PRIMARY KEY, user_id TEXT NOT NULL, account_key TEXT NOT NULL, client TEXT NOT NULL, callback TEXT NOT NULL, challenge TEXT NOT NULL, expires INTEGER NOT NULL, consumed INTEGER NOT NULL DEFAULT 0)`,
			`CREATE TABLE IF NOT EXISTS native_auth_session (id TEXT PRIMARY KEY, user_id TEXT NOT NULL, account_key TEXT NOT NULL, client TEXT NOT NULL, expires INTEGER NOT NULL, revoked INTEGER NOT NULL DEFAULT 0, access_hash TEXT NOT NULL UNIQUE, access_expires INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS native_auth_refresh (hash TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES native_auth_session(id) ON DELETE CASCADE, consumed INTEGER NOT NULL DEFAULT 0)`,
			`CREATE INDEX IF NOT EXISTS idx_native_session_user ON native_auth_session(user_id)`,
		} {
			if _, e := tx.DB().NewQuery(q).Execute(); e != nil {
				return e
			}
		}
		return nil
	})
}
func (s Service) IssueCode(app core.App, u *core.Record, client, callback, challenge string) (string, error) {
	if u == nil || u.Collection().Name != "user" || !s.allowed(client, callback) || !challengePattern.MatchString(challenge) {
		return "", ErrInvalid
	}
	code, e := secret()
	if e != nil {
		return "", e
	}
	_, e = app.DB().NewQuery(`INSERT INTO native_auth_code (hash,user_id,account_key,client,callback,challenge,expires) VALUES ({:hash},{:user},{:key},{:client},{:callback},{:challenge},{:expires})`).Bind(dbx.Params{"hash": hash(code), "user": u.Id, "key": hash(u.TokenKey()), "client": client, "callback": callback, "challenge": challenge, "expires": s.now() + 120}).Execute()
	return code, e
}

type codeRow struct {
	UserID     string `db:"user_id"`
	AccountKey string `db:"account_key"`
	Client     string `db:"client"`
	Callback   string `db:"callback"`
	Challenge  string `db:"challenge"`
	Expires    int64  `db:"expires"`
	Consumed   int    `db:"consumed"`
}
type sessionRow struct {
	ID            string `db:"id"`
	UserID        string `db:"user_id"`
	AccountKey    string `db:"account_key"`
	Client        string `db:"client"`
	Expires       int64  `db:"expires"`
	Revoked       int    `db:"revoked"`
	AccessExpires int64  `db:"access_expires"`
}

func account(app core.App, id, key string) (*core.Record, error) {
	u, e := app.FindRecordById("user", id)
	if e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return nil, ErrInvalid
		}
		return nil, e
	}
	if u.GetBool("withdrawn") || !equal(hash(u.TokenKey()), key) {
		return nil, ErrInvalid
	}
	return u, nil
}
func (s Service) Exchange(app core.App, code, verifier, client, callback string) (Credentials, error) {
	var out Credentials
	if !s.allowed(client, callback) || !verifierPattern.MatchString(verifier) || !challengePattern.MatchString(code) {
		return out, ErrInvalid
	}
	e := app.RunInTransaction(func(tx core.App) error {
		var c codeRow
		if e := tx.DB().NewQuery(`SELECT * FROM native_auth_code WHERE hash={:hash}`).Bind(dbx.Params{"hash": hash(code)}).One(&c); e != nil {
			return ErrInvalid
		}
		if c.Consumed != 0 || c.Expires <= s.now() || c.Client != client || c.Callback != callback || !equal(c.Challenge, hash(verifier)) {
			return ErrInvalid
		}
		if _, e := account(tx, c.UserID, c.AccountKey); e != nil {
			return e
		}
		if _, e := tx.DB().NewQuery(`UPDATE native_auth_code SET consumed=1 WHERE hash={:hash}`).Bind(dbx.Params{"hash": hash(code)}).Execute(); e != nil {
			return e
		}
		id, e := secret()
		if e != nil {
			return e
		}
		access, e := secret()
		if e != nil {
			return e
		}
		refresh, e := secret()
		if e != nil {
			return e
		}
		_, e = tx.DB().NewQuery(`INSERT INTO native_auth_session (id,user_id,account_key,client,expires,access_hash,access_expires) VALUES ({:id},{:user},{:key},{:client},{:expires},{:access},{:accessExpires})`).Bind(dbx.Params{"id": id, "user": c.UserID, "key": c.AccountKey, "client": client, "expires": s.now() + 30*86400, "access": hash(access), "accessExpires": s.now() + 600}).Execute()
		if e != nil {
			return e
		}
		if _, e = tx.DB().NewQuery(`INSERT INTO native_auth_refresh (hash,session_id) VALUES ({:hash},{:id})`).Bind(dbx.Params{"hash": hash(refresh), "id": id}).Execute(); e != nil {
			return e
		}
		out = Credentials{access, refresh, 600, c.UserID}
		return nil
	})
	return out, e
}
func (s Service) Refresh(app core.App, token string) (Credentials, error) {
	var out Credentials
	invalid := false
	if !challengePattern.MatchString(token) {
		return out, ErrInvalid
	}
	e := app.RunInTransaction(func(tx core.App) error {
		var r struct {
			SessionID string `db:"session_id"`
			Consumed  int    `db:"consumed"`
		}
		if e := tx.DB().NewQuery(`SELECT session_id,consumed FROM native_auth_refresh WHERE hash={:hash}`).Bind(dbx.Params{"hash": hash(token)}).One(&r); e != nil {
			return ErrInvalid
		}
		var f sessionRow
		if e := tx.DB().NewQuery(`SELECT * FROM native_auth_session WHERE id={:id}`).Bind(dbx.Params{"id": r.SessionID}).One(&f); e != nil {
			return ErrInvalid
		}
		if r.Consumed != 0 || !s.registered(f.Client) {
			invalid = true
			_, e := tx.DB().NewQuery(`UPDATE native_auth_session SET revoked=1 WHERE id={:id}`).Bind(dbx.Params{"id": f.ID}).Execute()
			return e
		}
		if f.Revoked != 0 || f.Expires <= s.now() {
			return ErrInvalid
		}
		if _, e := account(tx, f.UserID, f.AccountKey); e != nil {
			return e
		}
		access, e := secret()
		if e != nil {
			return e
		}
		refresh, e := secret()
		if e != nil {
			return e
		}
		if _, e = tx.DB().NewQuery(`UPDATE native_auth_refresh SET consumed=1 WHERE hash={:hash}`).Bind(dbx.Params{"hash": hash(token)}).Execute(); e != nil {
			return e
		}
		if _, e = tx.DB().NewQuery(`INSERT INTO native_auth_refresh (hash,session_id) VALUES ({:hash},{:id})`).Bind(dbx.Params{"hash": hash(refresh), "id": f.ID}).Execute(); e != nil {
			return e
		}
		if _, e = tx.DB().NewQuery(`UPDATE native_auth_session SET access_hash={:access},access_expires={:expires} WHERE id={:id}`).Bind(dbx.Params{"access": hash(access), "expires": s.now() + 600, "id": f.ID}).Execute(); e != nil {
			return e
		}
		out = Credentials{access, refresh, 600, f.UserID}
		return nil
	})
	if invalid {
		return Credentials{}, ErrInvalid
	}
	return out, e
}
func (s Service) Authenticate(app core.App, token string) (*core.Record, error) {
	u, _, err := s.AuthenticateSession(app, token)
	return u, err
}

// AuthenticateSession additionally returns the server-owned session family ID.
// It is never a client-supplied identifier or an access credential.
func (s Service) AuthenticateSession(app core.App, token string) (*core.Record, string, error) {
	if !challengePattern.MatchString(token) {
		return nil, "", ErrInvalid
	}
	var f sessionRow
	if e := app.DB().NewQuery(`SELECT * FROM native_auth_session WHERE access_hash={:hash}`).Bind(dbx.Params{"hash": hash(token)}).One(&f); e != nil {
		return nil, "", ErrInvalid
	}
	if f.Revoked != 0 || !s.registered(f.Client) || f.Expires <= s.now() || f.AccessExpires <= s.now() {
		return nil, "", ErrInvalid
	}
	u, err := account(app, f.UserID, f.AccountKey)
	return u, f.ID, err
}

func (s Service) Revoke(app core.App, refresh string) error {
	if !challengePattern.MatchString(refresh) {
		return ErrInvalid
	}
	_, e := app.DB().NewQuery(`UPDATE native_auth_session SET revoked=1 WHERE id IN (SELECT session_id FROM native_auth_refresh WHERE hash={:hash})`).Bind(dbx.Params{"hash": hash(refresh)}).Execute()
	return e
}
