package delegation

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// SynchronizeClients runs at startup after migrations. Persistent registration epochs
// prevent configuration removal/change and later restoration from resurrecting tokens.
// Work is bounded by the maximum of 128 enabled clients; secrets are never persisted.
func SynchronizeClients(app core.App, m *Manager) error {
	return app.RunInTransaction(func(tx core.App) error {
		active, e := tx.FindRecordsByFilter(Registrations, "enabled=true", "", 129, 0)
		if e != nil {
			return e
		}
		if len(active) > 128 {
			return fmt.Errorf("too many active OAuth registrations")
		}
		for _, r := range active {
			ok := false
			if m != nil {
				_, ok = m.clients[r.GetString("client")]
			}
			if !ok {
				r.Set("enabled", false)
				r.Set("epoch", random())
				if e = tx.Save(r); e != nil {
					return e
				}
			}
		}
		if m == nil {
			return nil
		}
		for _, c := range m.clients {
			r, e := find(tx, Registrations, "client={:client}", dbx.Params{"client": c.ID})
			if errors.Is(e, sql.ErrNoRows) {
				r, e = newRecord(tx, Registrations)
				if e != nil {
					return e
				}
				r.Set("client", c.ID)
			} else if e != nil {
				return e
			}
			if r.GetBool("enabled") && r.GetString("config_hash") == c.fingerprint {
				continue
			}
			r.Set("enabled", true)
			r.Set("config_hash", c.fingerprint)
			r.Set("epoch", random())
			if e = tx.Save(r); e != nil {
				return e
			}
		}
		return nil
	})
}
func (m *Manager) clientHash(app core.App, c registeredClient) (string, error) {
	r, e := find(app, Registrations, "client={:client}", dbx.Params{"client": c.ID})
	if e != nil || !r.GetBool("enabled") || r.GetString("config_hash") != c.fingerprint {
		return "", &OAuthError{Code: "temporarily_unavailable", Status: 503}
	}
	return hash(c.fingerprint + r.GetString("epoch")), nil
}
