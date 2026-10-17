package connections

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/ericbaek/musecat-backend-core/delegation"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// JointPartner is operator-supplied. It is not decoded from a browser request.
type JointPartner struct {
	ID       string   `json:"id"`
	ClientID string   `json:"client_id"`
	Provider string   `json:"provider"`
	Services []string `json:"services"`
}

// JointManager correlates already verified grants; it does not perform OAuth or
// trust a partner receipt. HTTP/consent wiring stays disabled until that bilateral
// protocol is agreed and its receipt verification adapter exists.
type JointManager struct {
	connections *Manager
	delegation  *delegation.Manager
	partners    map[string]JointPartner
}

type JointStatus struct {
	ID            string    `json:"id"`
	Partner       string    `json:"partner"`
	Status        string    `json:"status"`
	MusecatAccess bool      `json:"musecat_access"`
	PartnerAccess bool      `json:"partner_access"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// ParseJointConfig decodes the operator's partner registry. An empty value keeps joint
// coordination disabled; a registry without configured providers or OAuth clients fails
// startup rather than silently inferring either side. Errors never echo the raw value.
func ParseJointConfig(raw string, c *Manager, d *delegation.Manager) (*JointManager, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	if c == nil || d == nil {
		return nil, fmt.Errorf("joint partners require external providers and OAuth clients")
	}
	var partners []JointPartner
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&partners); err != nil {
		return nil, fmt.Errorf("invalid joint partner configuration")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("invalid joint partner configuration")
	}
	if len(partners) == 0 {
		return nil, nil
	}
	m, err := NewJointManager(c, d, partners)
	if err != nil {
		return nil, fmt.Errorf("invalid joint partner configuration")
	}
	return m, nil
}

func NewJointManager(c *Manager, d *delegation.Manager, partners []JointPartner) (*JointManager, error) {
	if c == nil || d == nil {
		return nil, ErrUnavailable
	}
	m := &JointManager{c, d, map[string]JointPartner{}}
	for _, p := range partners {
		provider, ok := c.lookup(p.Provider)
		if !ok || !identifier.MatchString(p.ID) || !d.AccountClient(p.ClientID) || len(p.Services) == 0 || len(p.Services) > 16 {
			return nil, ErrInvalid
		}
		if _, exists := m.partners[p.ID]; exists {
			return nil, ErrInvalid
		}
		seen := map[string]bool{}
		for _, id := range p.Services {
			if seen[id] || !slices.ContainsFunc(provider.Services, func(s Service) bool { return s.ID == id }) {
				return nil, ErrInvalid
			}
			seen[id] = true
		}
		p.Services = slices.Clone(p.Services)
		slices.Sort(p.Services)
		m.partners[p.ID] = p
	}
	return m, nil
}

func jointActor(app core.App, a Actor) error {
	if err := a.check(app); err != nil {
		return err
	}
	u, err := app.FindAuthRecordByToken(a.Session, core.TokenTypeAuth)
	if err != nil || u.Collection().Name != "user" || u.Id != a.UserID {
		return ErrInvalid
	}
	return nil
}

func (m *JointManager) config(app core.App, partner string) (JointPartner, Provider, string, error) {
	if m == nil {
		return JointPartner{}, Provider{}, "", ErrUnavailable
	}
	p, ok := m.partners[partner]
	provider, exists := m.connections.lookup(p.Provider)
	if !ok || !exists || !m.delegation.AccountClient(p.ClientID) {
		return p, provider, "", ErrUnavailable
	}
	version, err := m.delegation.AccountClientVersion(app, p.ClientID)
	if err != nil {
		return p, provider, "", ErrUnavailable
	}
	return p, provider, digest(p.ID + "\n" + p.ClientID + "\n" + version + "\n" + fingerprint(provider) + "\n" + strings.Join(p.Services, "\n")), nil
}

// Start requires the subject obtained by the trusted provider/receipt adapter,
// never a user-supplied UID. No partner token or Musecat grant is created here.
func (m *JointManager) Start(app core.App, a Actor, partner, verifiedPartnerSubject string) (JointStatus, error) {
	p, provider, config, err := m.config(app, partner)
	if err != nil {
		return JointStatus{}, err
	}
	if verifiedPartnerSubject == "" || len(verifiedPartnerSubject) > 1024 {
		return JointStatus{}, ErrInvalid
	}
	var out JointStatus
	err = app.RunInTransaction(func(tx core.App) error {
		if err := jointActor(tx, a); err != nil {
			return err
		}
		old, err := tx.FindRecordsByFilter(JointAttempts, "user={:user} && partner={:partner} && (status='pending' || status='partial')", "", 0, 0, dbx.Params{"user": a.UserID, "partner": partner})
		if err != nil {
			return err
		}
		for _, r := range old {
			r.Set("status", "cancelled")
			if err := tx.Save(r); err != nil {
				return err
			}
		}
		c, err := tx.FindCollectionByNameOrId(JointAttempts)
		if err != nil {
			return err
		}
		r := core.NewRecord(c)
		r.Set("user", a.UserID)
		r.Set("partner", partner)
		r.Set("client", p.ClientID)
		r.Set("provider", p.Provider)
		r.Set("issuer", provider.Issuer)
		r.Set("partner_subject", verifiedPartnerSubject)
		r.Set("services", p.Services)
		r.Set("config_hash", config)
		r.Set("session_hash", digest(a.Session))
		r.Set("status", "pending")
		r.Set("expires", time.Now().UTC().Add(5*time.Minute))
		if err := tx.Save(r); err != nil {
			return err
		}
		out = jointProjection(r)
		return nil
	})
	return out, err
}

func jointProjection(r *core.Record) JointStatus {
	return JointStatus{ID: r.Id, Partner: r.GetString("partner"), Status: r.GetString("status"), ExpiresAt: r.GetDateTime("expires").Time()}
}

func (m *JointManager) owned(app core.App, a Actor, id string) (*core.Record, error) {
	if err := jointActor(app, a); err != nil {
		return nil, err
	}
	r, err := app.FindRecordById(JointAttempts, id)
	if err != nil {
		return nil, err
	}
	if r.GetString("user") != a.UserID {
		return nil, sql.ErrNoRows
	}
	return r, nil
}

func (m *JointManager) accountValid(app core.App, r *core.Record, id string) bool {
	p, provider, _, err := m.config(app, r.GetString("partner"))
	if err != nil {
		return false
	}
	c, err := app.FindRecordById(Accounts, id)
	if err != nil || c.GetString("user") != r.GetString("user") || c.GetString("provider") != p.Provider || c.GetString("issuer") != provider.Issuer || c.GetString("subject") != r.GetString("partner_subject") || c.GetString("credentials") == "" {
		return false
	}
	var tokens credentials
	if err := m.connections.open(c.GetString("credentials"), "account:"+c.Id, &tokens); err != nil || tokens.AccessToken == "" {
		return false
	}
	if !tokens.Expiry.IsZero() && !tokens.Expiry.After(time.Now()) && tokens.RefreshToken == "" {
		return false
	}
	for _, scope := range provider.Scopes {
		if !slices.Contains(c.GetStringSlice("scopes"), scope) {
			return false
		}
	}
	for _, id := range p.Services {
		service, err := app.FindFirstRecordByFilter(Services, "connection={:connection} && service={:service}", dbx.Params{"connection": c.Id, "service": id})
		if err != nil || service.GetString("visibility") != "private" || len(service.GetStringSlice("fields")) != 0 {
			return false
		}
		for _, s := range provider.Services {
			if s.ID == id {
				for _, scope := range s.Scopes {
					if !slices.Contains(c.GetStringSlice("scopes"), scope) {
						return false
					}
				}
			}
		}
	}
	return true
}

// Credentials use randomized authenticated encryption, so reauthorization changes
// this revision even when two saves share the same millisecond timestamp.
func jointConnectionRevision(c *core.Record) string {
	return digest(c.GetString("credentials") + "\n" + c.GetString("updated") + "\n" + strings.Join(c.GetStringSlice("scopes"), "\n"))
}

func (m *JointManager) evidence(app core.App, r *core.Record) (bool, bool) {
	grant := m.delegation.VerifyAccountGrant(app, r.GetString("user"), r.GetString("client"), r.GetString("grant_id"), r.GetString("grant_generation")) == nil
	account := m.accountValid(app, r, r.GetString("connection_id"))
	if account {
		c, err := app.FindRecordById(Accounts, r.GetString("connection_id"))
		account = err == nil && jointConnectionRevision(c) == r.GetString("connection_revision")
	}
	return grant, account
}

// RecordVerified accepts only persisted evidence identifiers from server-owned
// callbacks AFTER receipt/session/PKCE validation. Empty legs remain pending.
// Repeated identical confirmations are idempotent; evidence cannot be replaced.
func (m *JointManager) RecordVerified(app core.App, a Actor, id, grantID, generation, connectionID string) (JointStatus, error) {
	var out JointStatus
	err := app.RunInTransaction(func(tx core.App) error {
		r, err := m.owned(tx, a, id)
		if err != nil {
			return err
		}
		_, _, config, err := m.config(tx, r.GetString("partner"))
		if err != nil {
			return err
		}
		if config != r.GetString("config_hash") || r.GetString("session_hash") != digest(a.Session) || r.GetString("status") == "cancelled" || !r.GetDateTime("expires").Time().After(time.Now()) {
			return ErrInvalid
		}
		if grantID == "" && connectionID == "" {
			return ErrInvalid
		}
		if grantID != "" {
			if err := m.delegation.VerifyAccountGrant(tx, a.UserID, r.GetString("client"), grantID, generation); err != nil {
				return ErrInvalid
			}
			if r.GetString("grant_id") != "" && (r.GetString("grant_id") != grantID || r.GetString("grant_generation") != generation) {
				return ErrConflict
			}
			r.Set("grant_id", grantID)
			r.Set("grant_generation", generation)
		}
		if connectionID != "" {
			if !m.accountValid(tx, r, connectionID) {
				return ErrInvalid
			}
			c, err := tx.FindRecordById(Accounts, connectionID)
			if err != nil {
				return err
			}
			if r.GetString("connection_id") != "" && (r.GetString("connection_id") != connectionID || r.GetString("connection_revision") != jointConnectionRevision(c)) {
				return ErrConflict
			}
			r.Set("connection_id", connectionID)
			r.Set("connection_revision", jointConnectionRevision(c))
		}
		g, c := m.evidence(tx, r)
		status := "partial"
		if g && c {
			status = "connected"
		}
		r.Set("status", status)
		if err := tx.Save(r); err != nil {
			return err
		}
		out = jointProjection(r)
		out.MusecatAccess = g
		out.PartnerAccess = c
		return nil
	})
	return out, err
}

// Status rechecks both ledgers. A cached connected label never authorizes a call.
func (m *JointManager) Status(app core.App, a Actor, id string) (JointStatus, error) {
	var out JointStatus
	err := app.RunInTransaction(func(tx core.App) error {
		r, err := m.owned(tx, a, id)
		if err != nil {
			return err
		}
		out = jointProjection(r)
		if out.Status == "cancelled" {
			return nil
		}
		_, _, config, err := m.config(tx, out.Partner)
		if err != nil || config != r.GetString("config_hash") {
			out.Status = "invalidated"
			return nil
		}
		if out.Status != "connected" && !out.ExpiresAt.After(time.Now()) {
			out.Status = "expired"
			return nil
		}
		out.MusecatAccess, out.PartnerAccess = m.evidence(tx, r)
		if out.Status == "connected" && (!out.MusecatAccess || !out.PartnerAccess) {
			out.Status = "degraded"
		}
		return nil
	})
	return out, err
}

// Cancel cancels coordination only. It cannot silently revoke independently
// approved or pre-existing grants. Bilateral disconnect needs the partner adapter.
func (m *JointManager) Cancel(app core.App, a Actor, id string) error {
	return app.RunInTransaction(func(tx core.App) error {
		r, err := m.owned(tx, a, id)
		if err != nil {
			return err
		}
		r.Set("status", "cancelled")
		return tx.Save(r)
	})
}

// PurgeJointAttempts retains live connected metadata and removes abandoned
// attempts after their five-minute window. Executables opt in when wiring joint flows.
func PurgeJointAttempts(app core.App, now time.Time) error {
	rows, err := app.FindRecordsByFilter(JointAttempts, "expires <= {:now} && status != 'connected'", "expires,id", 100, 0, dbx.Params{"now": now.UTC().Format("2006-01-02 15:04:05.000Z")})
	if err != nil {
		return err
	}
	return app.RunInTransaction(func(tx core.App) error {
		for _, r := range rows {
			current, err := tx.FindRecordById(JointAttempts, r.Id)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if current.GetString("status") != "connected" && !current.GetDateTime("expires").Time().After(now) {
				if err := tx.Delete(current); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// RegisterJointCleanupCron is called by executables that wire a JointManager, never by
// coreapp.Configure. With no joint partners configured the table simply stays empty.
func RegisterJointCleanupCron(app core.App) {
	app.Cron().MustAdd("external_joint_connection_cleanup", "*/5 * * * *", func() {
		if err := PurgeJointAttempts(app, time.Now()); err != nil {
			app.Logger().Error("Failed to purge expired joint connection attempts")
		}
	})
}
