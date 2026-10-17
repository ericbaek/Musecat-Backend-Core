package delegation

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"time"
)

// AccountClient reports operator registration capability, not user consent.
func (m *Manager) AccountClient(clientID string) bool {
	if m == nil {
		return false
	}
	c, ok := m.clients[clientID]
	return ok && contains(c.Scopes, AccountAccess)
}

// AccountClientVersion binds server-only coordination to the live registration epoch.
func (m *Manager) AccountClientVersion(app core.App, clientID string) (string, error) {
	if !m.AccountClient(clientID) {
		return "", oauthError("invalid_grant")
	}
	return m.clientHash(app, m.clients[clientID])
}

// VerifyAccountGrant checks an already-approved generation for server-side joint
// orchestration. It cannot create consent, issue tokens or accept browser claims.
func (m *Manager) VerifyAccountGrant(app core.App, userID, clientID, id, generation string) error {
	if !m.AccountClient(clientID) {
		return oauthError("invalid_grant")
	}
	g, err := app.FindRecordById(Grants, id)
	if err != nil || g.GetString("user") != userID || generation == "" || g.GetString("generation") != generation || !contains(g.GetStringSlice("scopes"), AccountAccess) {
		return oauthError("invalid_grant")
	}
	// Require a redeemed, still-refreshable family, not merely an approved code.
	row, err := app.FindFirstRecordByFilter(Tokens,
		"grant={:grant} && generation={:generation} && refresh_used=false && refresh_expires>{:now}",
		dbx.Params{"grant": id, "generation": generation, "now": time.Now().UTC().Format("2006-01-02 15:04:05.000Z")})
	if err != nil {
		return oauthError("invalid_grant")
	}
	_, _, err = m.activeGrant(app, row, clientID)
	return err
}
