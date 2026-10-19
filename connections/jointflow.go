package connections

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/ericbaek/musecat-backend-core/delegation"
)

// This file implements the Musecat side of the one-step reciprocal connection
// (partner-integration-spec.md sections 4–6) on top of the JointManager ledger:
// intent lookup, single approval that issues both directions, partner status
// polling, and generation-bound bilateral revocation with a durable outbox.

var (
	uuidPattern      = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-4[0-9a-fA-F]{3}-[89aAbB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
	challengePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	intentPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{43,512}$`)
)

// JointLookup is what the consent page renders. It carries no secrets or subjects.
type JointLookup struct {
	ID              string        `json:"id"`
	Partner         string        `json:"partner"`
	PartnerName     string        `json:"partner_name"`
	PartnerAccount  string        `json:"partner_account"`
	Services        []ServiceInfo `json:"services"`
	Manifest        string        `json:"manifest"`
	ExpiresAt       time.Time     `json:"expires_at"`
	PartnerRedirect string        `json:"-"`
}

type ServiceInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PartnerJointStatus is the partner-facing projection of an attempt.
type PartnerJointStatus struct {
	CorrelationID string `json:"correlation_id"`
	Status        string `json:"status"`
	Generation    string `json:"generation,omitempty"`
	MusecatAccess bool   `json:"musecat_access"`
	PartnerAccess bool   `json:"partner_access"`
}

type partnerAPIError struct {
	Status int
	Code   string
}

func (e *partnerAPIError) Error() string { return fmt.Sprintf("partner %d %s", e.Status, e.Code) }

func sha(v string) string { s := sha256.Sum256([]byte(v)); return hex.EncodeToString(s[:]) }

// BeginURL builds the partner start URL for a connection initiated on Musecat (5.5).
func (m *JointManager) BeginURL(partner, returnTo string) (string, string, error) {
	return m.BeginURLFor("", partner, returnTo)
}

func (m *JointManager) BeginURLFor(userID, partner, returnTo string) (string, string, error) {
	if m == nil || m.connections == nil {
		return "", "", ErrUnavailable
	}
	p, ok := m.partner(partner)
	provider, exists := m.connections.lookup(p.Provider)
	if !ok || !exists || !provider.AllowsUser(userID) || p.ConnectStartURL == "" {
		return "", "", ErrUnavailable
	}
	target, parseErr := url.Parse(returnTo)
	if parseErr != nil || !httpsLink(returnTo) || target.Scheme+"://"+target.Host != m.delegation.ConsentOrigin() || target.RawQuery != "" || !regexp.MustCompile(`^/[a-z]{2}(?:-[A-Za-z]{2})?/connect/`+regexp.QuoteMeta(partner)+`$`).MatchString(target.Path) {
		return "", "", ErrInvalid
	}
	correlation := newUUID()
	u, _ := url.Parse(p.ConnectStartURL)
	q := u.Query()
	q.Set("correlation_id", correlation)
	q.Set("return_to", returnTo)
	// Optional final destination, fixed to Musecat settings rather than browser input.
	locale := strings.Split(strings.TrimPrefix(target.Path, "/"), "/")[0]
	q.Set("completion_uri", m.delegation.ConsentOrigin()+"/"+locale+"/settings/connections")
	u.RawQuery = q.Encode()
	return u.String(), correlation, nil
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = io.ReadFull(rand.Reader, b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// call performs an authenticated server-to-server request to the partner (section 4).
func (m *JointManager) call(ctx context.Context, p JointPartner, path string, body any, out any) error {
	return m.callWithRequestID(ctx, p, path, body, out, newUUID())
}

func (m *JointManager) callWithRequestID(ctx context.Context, p JointPartner, path string, body any, out any, requestID string) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.JointURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.OutboundKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Request-Id", requestID)
	res, err := m.connections.client.Do(req)
	if err != nil {
		return &partnerAPIError{Status: 0, Code: "network"}
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 64<<10+1))
	if err != nil || len(raw) > 64<<10 {
		return &partnerAPIError{Status: res.StatusCode, Code: "invalid_response"}
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error == "" {
			e.Error = "upstream_error"
		}
		return &partnerAPIError{Status: res.StatusCode, Code: "upstream_error"}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return &partnerAPIError{Status: res.StatusCode, Code: "invalid_response"}
	}
	if decoder.Decode(new(any)) != io.EOF {
		return &partnerAPIError{Status: res.StatusCode, Code: "invalid_response"}
	}
	return nil
}

type intentLookupResponse struct {
	CorrelationID string   `json:"correlation_id"`
	Sub           string   `json:"sub"`
	Name          string   `json:"name"`
	Services      []string `json:"services"`
	Manifest      string   `json:"manifest"`
	CodeChallenge string   `json:"code_challenge"`
	RedirectURI   string   `json:"redirect_uri"`
	ExpiresAt     string   `json:"expires_at"`
}

// Lookup retrieves and validates a partner intent (5.3 intents/lookup), then opens a
// ledger attempt bound to the current Musecat session. Nothing is granted yet.
func (m *JointManager) Lookup(ctx context.Context, app core.App, a Actor, partner, intent string) (JointLookup, error) {
	if m == nil {
		return JointLookup{}, ErrUnavailable
	}
	p, provider, _, err := m.config(app, partner)
	if err != nil {
		return JointLookup{}, err
	}
	if !provider.AllowsUser(a.UserID) {
		return JointLookup{}, ErrUnavailable
	}
	if p.JointURL == "" {
		return JointLookup{}, ErrUnavailable
	}
	if !intentPattern.MatchString(intent) {
		return JointLookup{}, ErrInvalid
	}
	if err := jointActor(app, a); err != nil {
		return JointLookup{}, err
	}
	var res intentLookupResponse
	if err := m.call(ctx, p, "/intents/lookup", map[string]string{"intent": intent, "partner_id": p.ID}, &res); err != nil {
		var pe *partnerAPIError
		if errors.As(err, &pe) && (pe.Code == "invalid_intent" || pe.Code == "intent_expired" || pe.Code == "intent_consumed") {
			return JointLookup{}, ErrInvalid
		}
		return JointLookup{}, ErrProvider
	}
	client, ok := m.delegation.Client(p.ClientID)
	if !ok {
		return JointLookup{}, ErrUnavailable
	}
	expires, err := time.Parse(time.RFC3339, res.ExpiresAt)
	if err != nil || !expires.After(time.Now()) || expires.After(time.Now().Add(6*time.Minute)) {
		return JointLookup{}, ErrProvider
	}
	if !uuidPattern.MatchString(res.CorrelationID) || res.Sub == "" || len(res.Sub) > 256 || len(res.Name) > 64 || res.Manifest != JointManifest || !slices.Contains(p.Manifests, res.Manifest) || !challengePattern.MatchString(res.CodeChallenge) || !slices.Contains(client.RedirectURIs, res.RedirectURI) {
		return JointLookup{}, ErrProvider
	}
	if len(res.Services) == 0 || len(res.Services) > 16 {
		return JointLookup{}, ErrProvider
	}
	services := []ServiceInfo{}
	for _, id := range res.Services {
		if !slices.Contains(p.Services, id) || slices.ContainsFunc(services, func(s ServiceInfo) bool { return s.ID == id }) {
			return JointLookup{}, ErrProvider
		}
		name := id
		for _, s := range provider.Services {
			if s.ID == id {
				name = s.Name
			}
		}
		services = append(services, ServiceInfo{ID: id, Name: name})
	}
	if existing, e := m.pair(app, partner, res.CorrelationID); e == nil {
		if existing.GetString("user") != a.UserID || existing.GetString("session_hash") != digest(a.Session) || existing.GetString("partner_subject") != res.Sub || existing.GetString("intent_hash") != sha(intent) {
			return JointLookup{}, ErrInvalid
		}
		return m.Read(app, a, existing.Id)
	} else if !errors.Is(e, sql.ErrNoRows) {
		return JointLookup{}, e
	}
	started, err := m.Start(app, a, partner, res.Sub)
	if err != nil {
		return JointLookup{}, err
	}
	sealed, err := m.connections.seal(intent, "joint:"+started.ID)
	if err != nil {
		return JointLookup{}, err
	}
	ids := make([]string, 0, len(services))
	for _, s := range services {
		ids = append(ids, s.ID)
	}
	slices.Sort(ids)
	err = app.RunInTransaction(func(tx core.App) error {
		r, err := tx.FindRecordById(JointAttempts, started.ID)
		if err != nil {
			return err
		}
		r.Set("correlation", strings.ToLower(res.CorrelationID))
		r.Set("intent", sealed)
		r.Set("intent_hash", sha(intent))
		r.Set("code_challenge", res.CodeChallenge)
		r.Set("redirect_uri", res.RedirectURI)
		r.Set("partner_name", res.Name)
		r.Set("manifest", res.Manifest)
		r.Set("services", ids)
		if expires.Before(r.GetDateTime("expires").Time()) {
			r.Set("expires", expires.UTC())
		}
		return tx.Save(r)
	})
	if err != nil {
		return JointLookup{}, err
	}
	return JointLookup{ID: started.ID, Partner: p.ID, PartnerName: provider.Name, PartnerAccount: res.Name, Services: services, Manifest: res.Manifest, ExpiresAt: expires.UTC(), PartnerRedirect: res.RedirectURI}, nil
}

// Read returns the consent summary for a pending attempt owned by the session.
func (m *JointManager) Read(app core.App, a Actor, id string) (JointLookup, error) {
	r, err := m.owned(app, a, id)
	if err != nil {
		return JointLookup{}, err
	}
	p, provider, config, err := m.config(app, r.GetString("partner"))
	if err != nil || !provider.AllowsUser(a.UserID) || config != r.GetString("config_hash") || r.GetString("session_hash") != digest(a.Session) || r.GetString("status") != "pending" || !r.GetDateTime("expires").Time().After(time.Now()) {
		return JointLookup{}, ErrInvalid
	}
	services := []ServiceInfo{}
	for _, id := range r.GetStringSlice("services") {
		name := id
		for _, s := range provider.Services {
			if s.ID == id {
				name = s.Name
			}
		}
		services = append(services, ServiceInfo{ID: id, Name: name})
	}
	return JointLookup{ID: r.Id, Partner: p.ID, PartnerName: provider.Name, PartnerAccount: r.GetString("partner_name"), Services: services, Manifest: r.GetString("manifest"), ExpiresAt: r.GetDateTime("expires").Time().UTC(), PartnerRedirect: r.GetString("redirect_uri")}, nil
}

type receiptResponse struct {
	PartnerCode          string `json:"partner_code"`
	PartnerCodeExpiresAt string `json:"partner_code_expires_at"`
	Scope                string `json:"scope"`
}

func (m *JointManager) partnerRedirect(r *core.Record, values url.Values) string {
	u, err := url.Parse(r.GetString("redirect_uri"))
	if err != nil {
		return ""
	}
	q := u.Query()
	for k, v := range values {
		q.Set(k, v[0])
	}
	q.Set("state", r.GetString("correlation"))
	q.Set("iss", m.delegation.Issuer())
	u.RawQuery = q.Encode()
	return u.String()
}

// Deny cancels the attempt and returns the partner callback carrying access_denied.
func (m *JointManager) Deny(app core.App, a Actor, id string) (string, error) {
	r, err := m.owned(app, a, id)
	if err != nil {
		return "", err
	}
	if r.GetString("status") != "pending" || r.GetString("session_hash") != digest(a.Session) {
		return "", ErrInvalid
	}
	if err := m.Cancel(app, a, id); err != nil {
		return "", err
	}
	return m.partnerRedirect(r, url.Values{"error": {"access_denied"}}), nil
}

// Approve is the single user action (5.5 step 3). In order: Musecat issues its
// Direction-A code bound to the intent's callback and PKCE challenge; Musecat starts
// its Direction-B request; the partner receives the receipt and returns its code;
// Musecat redeems it and verifies the account; the ledger records the partner leg and
// binds the pending Musecat leg. The returned URL sends the browser to the partner.
func (m *JointManager) Approve(ctx context.Context, app core.App, a Actor, id string) (string, error) {
	r, err := m.owned(app, a, id)
	if err != nil {
		return "", err
	}
	p, provider, config, err := m.config(app, r.GetString("partner"))
	if err != nil || !provider.AllowsUser(a.UserID) || config != r.GetString("config_hash") || r.GetString("session_hash") != digest(a.Session) || r.GetString("status") != "pending" || !r.GetDateTime("expires").Time().After(time.Now()) || r.GetString("intent") == "" {
		return "", ErrInvalid
	}
	var intent string
	if err := m.connections.open(r.GetString("intent"), "joint:"+r.Id, &intent); err != nil {
		return "", ErrInvalid
	}
	err = app.RunInTransaction(func(tx core.App) error {
		current, err := m.owned(tx, a, id)
		if err != nil {
			return err
		}
		if current.GetString("status") != "pending" || current.GetString("session_hash") != digest(a.Session) {
			return ErrInvalid
		}
		current.Set("status", "approving")
		current.Set("intent", "")
		return tx.Save(current)
	})
	if err != nil {
		return "", err
	}
	defer func() {
		_ = app.RunInTransaction(func(tx core.App) error {
			current, e := tx.FindRecordById(JointAttempts, id)
			if e != nil {
				return e
			}
			if current.GetString("status") == "approving" {
				current.Set("status", "partial")
				return tx.Save(current)
			}
			return nil
		})
	}()
	actor := delegation.Actor{UserID: a.UserID, Session: a.Session}

	// Issue and bind the Direction-A generation atomically, before external calls.
	var callback, musecatCode string
	var grant *core.Record
	generation := newUUID()
	err = app.RunInTransaction(func(tx core.App) error {
		beginURL, err := m.delegation.Begin(tx, delegation.AuthorizationInput{ClientID: p.ClientID, RedirectURI: r.GetString("redirect_uri"), ResponseType: "code", Scope: []string{delegation.AccountAccess}, State: r.GetString("correlation"), Challenge: r.GetString("code_challenge"), ChallengeMethod: "S256"})
		if err != nil {
			return err
		}
		bu, _ := url.Parse(beginURL)
		handle := bu.Query().Get("request")
		if _, err = m.delegation.ReadConsent(tx, actor, handle); err != nil {
			return err
		}
		callback, err = m.delegation.Decide(tx, actor, delegation.Decision{Request: handle, Approve: true, Scopes: []string{delegation.AccountAccess}})
		if err != nil {
			return err
		}
		cu, _ := url.Parse(callback)
		musecatCode = cu.Query().Get("code")
		grant, err = tx.FindFirstRecordByFilter(delegation.Grants, "user={:user} && client={:client}", dbx.Params{"user": a.UserID, "client": p.ClientID})
		if err != nil {
			return err
		}
		current, err := tx.FindRecordById(JointAttempts, r.Id)
		if err != nil {
			return err
		}
		if current.GetString("status") != "approving" {
			return ErrInvalid
		}
		current.Set("grant_id", grant.Id)
		current.Set("grant_generation", grant.GetString("generation"))
		current.Set("generation", generation)
		return tx.Save(current)
	})
	if err != nil {
		return "", err
	}

	rollback := func(reason string) {
		_ = app.RunInTransaction(func(tx core.App) error {
			current, e := tx.FindRecordById(JointAttempts, r.Id)
			if e != nil {
				return e
			}
			if e = m.revokeLocal(tx, current); e != nil {
				return e
			}
			current, e = tx.FindRecordById(JointAttempts, r.Id)
			if e != nil {
				return e
			}
			current.Set("status", "partial")
			if e = tx.Save(current); e != nil {
				return e
			}
			return m.enqueueRevocation(tx, current, "security")
		})
	}

	// Direction B: Musecat's own PKCE request toward the partner.
	start, err := m.connections.Start(app, a, StartInput{Provider: p.Provider, Services: r.GetStringSlice("services")})
	if err != nil {
		rollback("start")
		return "", ErrProvider
	}
	su, _ := url.Parse(start.AuthorizationURL)
	state, challenge, redirect := su.Query().Get("state"), su.Query().Get("code_challenge"), su.Query().Get("redirect_uri")
	var receipt receiptResponse
	err = m.call(ctx, p, "/receipts", map[string]any{
		"intent": intent, "correlation_id": r.GetString("correlation"), "manifest": r.GetString("manifest"), "generation": generation,
		"musecat_code": musecatCode, "musecat_code_expires_at": time.Now().Add(90 * time.Second).UTC().Format(time.RFC3339),
		"code_challenge": challenge, "redirect_uri": redirect,
	}, &receipt)
	receiptExpiry, expiryErr := time.Parse(time.RFC3339, receipt.PartnerCodeExpiresAt)
	if err != nil || receipt.PartnerCode == "" || len(receipt.PartnerCode) > 4096 || expiryErr != nil || !receiptExpiry.After(time.Now()) || receiptExpiry.After(time.Now().Add(90*time.Second)) {
		rollback("receipt")
		return "", ErrProvider
	}
	_, err = m.connections.complete(ctx, app, a, CompleteInput{Provider: p.Provider, State: state, Code: receipt.PartnerCode, Issuer: provider.Issuer, ExpectedSubject: r.GetString("partner_subject")}, func(tx core.App, connection Connection) error {
		_, err := m.RecordVerified(tx, a, r.Id, "", "", connection.ID)
		return err
	})
	if err != nil {
		rollback("exchange")
		return "", ErrProvider
	}

	return callback, nil
}

// PartnerStatus serves GET /partner/v1/joint/{correlation_id} for an authenticated partner.
func (m *JointManager) PartnerStatus(app core.App, partner, correlation string) (PartnerJointStatus, error) {
	r, err := m.pair(app, partner, correlation)
	if err != nil {
		return PartnerJointStatus{}, err
	}
	out := PartnerJointStatus{CorrelationID: correlation, Status: r.GetString("status"), Generation: r.GetString("generation")}
	if out.Status == "cancelled" || out.Status == "revoked" {
		return out, nil
	}
	_, _, config, err := m.config(app, partner)
	if err != nil || config != r.GetString("config_hash") {
		out.Status = "invalidated"
		return out, nil
	}
	out.MusecatAccess, out.PartnerAccess = m.evidence(app, r)
	if out.Status != "connected" && !(out.MusecatAccess && out.PartnerAccess) && !r.GetDateTime("expires").Time().After(time.Now()) {
		out.Status = "expired"
		return out, nil
	}
	out.MusecatAccess, out.PartnerAccess = m.evidence(app, r)
	switch {
	case out.Status == "connected" && (!out.MusecatAccess || !out.PartnerAccess):
		out.Status = "degraded"
	case out.Status == "partial" && out.MusecatAccess && out.PartnerAccess:
		_ = app.RunInTransaction(func(tx core.App) error {
			current, err := tx.FindRecordById(JointAttempts, r.Id)
			if err != nil {
				return err
			}
			current.Set("status", "connected")
			return tx.Save(current)
		})
		out.Status = "connected"
	}
	return out, nil
}

func (m *JointManager) pair(app core.App, partner, correlation string) (*core.Record, error) {
	if !uuidPattern.MatchString(correlation) {
		return nil, sql.ErrNoRows
	}
	rows, err := app.FindRecordsByFilter(JointAttempts, "partner={:p} && correlation={:c}", "-created", 1, 0, dbx.Params{"p": partner, "c": strings.ToLower(correlation)})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, sql.ErrNoRows
	}
	return rows[0], nil
}

// AuthenticatePartner resolves the partner behind an inbound bearer key (section 4).
func (m *JointManager) AuthenticatePartner(raw string) (string, bool) {
	if m == nil || raw == "" {
		return "", false
	}
	h := []byte(sha(raw))
	m.mu.RLock()
	defer m.mu.RUnlock()
	for id, p := range m.partners {
		if (p.InboundKeyHash != "" && subtle.ConstantTimeCompare([]byte(p.InboundKeyHash), h) == 1) || (p.PreviousInboundKeyExpires.After(time.Now()) && p.PreviousInboundKeyHash != "" && subtle.ConstantTimeCompare([]byte(p.PreviousInboundKeyHash), h) == 1) {
			return id, true
		}
	}
	return "", false
}

// revokeLocal revokes exactly the grants created by this attempt and marks it revoked.
func (m *JointManager) revokeLocal(app core.App, r *core.Record) error {
	return app.RunInTransaction(func(tx core.App) error {
		current, err := tx.FindRecordById(JointAttempts, r.Id)
		if err != nil {
			return err
		}
		if current.GetString("status") == "revoked" {
			return nil
		}
		if g := current.GetString("grant_id"); g != "" {
			grant, err := tx.FindRecordById(delegation.Grants, g)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil && grant.GetString("generation") == current.GetString("grant_generation") {
				if err := delegation.RevokeGrant(tx, current.GetString("user"), g); err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			}
		}
		if c := current.GetString("connection_id"); c != "" {
			if account, err := tx.FindRecordById(Accounts, c); err == nil && account.GetString("user") == current.GetString("user") && jointConnectionRevision(account) == current.GetString("connection_revision") {
				if err := tx.Delete(account); err != nil {
					return err
				}
			}
		}
		current.Set("status", "revoked")
		return tx.Save(current)
	})
}

// ReceiveRevocation handles POST /partner/v1/revocations (section 6). Only the exact
// pair and generation are revoked; older generations and duplicates are reported.
func (m *JointManager) ReceiveRevocation(app core.App, partner, pairID, generation string) (string, error) {
	r, err := m.pair(app, partner, pairID)
	if errors.Is(err, sql.ErrNoRows) {
		return "unknown_generation", nil
	}
	if err != nil {
		return "", err
	}
	if generation == "" || r.GetString("generation") != generation {
		return "unknown_generation", nil
	}
	if r.GetString("status") == "revoked" {
		return "already_revoked", nil
	}
	if g, err := app.FindRecordById(delegation.Grants, r.GetString("grant_id")); err == nil && g.GetString("generation") != r.GetString("grant_generation") {
		return "unknown_generation", nil
	}
	if c, err := app.FindRecordById(Accounts, r.GetString("connection_id")); err == nil && jointConnectionRevision(c) != r.GetString("connection_revision") {
		return "unknown_generation", nil
	}
	if err := m.revokeLocal(app, r); err != nil {
		return "", err
	}
	return "revoked", nil
}

// DisconnectPair is the user's "disconnect both services" action: local revocation is
// immediate and a durable notification to the partner is queued.
func (m *JointManager) DisconnectPair(app core.App, a Actor, id string) error {
	r, err := m.owned(app, a, id)
	if err != nil {
		return err
	}
	if r.GetString("status") == "revoked" {
		return nil
	}
	return app.RunInTransaction(func(tx core.App) error {
		if err := m.revokeLocal(tx, r); err != nil {
			return err
		}
		if r.GetString("generation") == "" || r.GetString("correlation") == "" {
			return nil
		}
		return m.enqueueRevocation(tx, r, "user")
	})
}

// UserPairs lists the current user's joint attempts for the settings page.
func (m *JointManager) UserPairs(app core.App, a Actor) ([]JointStatus, error) {
	if m == nil {
		return []JointStatus{}, nil
	}
	if err := jointActor(app, a); err != nil {
		return nil, err
	}
	rows, err := app.FindRecordsByFilter(JointAttempts, "user={:u} && status != 'cancelled' && status != 'pending'", "-created", 50, 0, dbx.Params{"u": a.UserID})
	if err != nil {
		return nil, err
	}
	out := []JointStatus{}
	for _, r := range rows {
		s, err := m.Status(app, a, r.Id)
		if err != nil {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// DeliverRevocations sends due outbox notifications with exponential backoff. Entries
// keep retrying for at least 24 hours; a failed delivery never restores local access.
func (m *JointManager) DeliverRevocations(ctx context.Context, app core.App, limit int) error {
	if m == nil {
		return nil
	}
	now := time.Now().UTC()
	rows, err := app.FindRecordsByFilter(JointOutbox, "delivered = '' && next_at <= {:now}", "next_at", limit, 0, dbx.Params{"now": now.Format("2006-01-02 15:04:05.000Z")})
	if err != nil {
		return err
	}
	for _, o := range rows {
		p, ok := m.partner(o.GetString("partner"))
		attempts := int(o.GetFloat("attempts")) + 1
		var result struct {
			Status string `json:"status"`
		}
		var deliveryErr error
		if !ok || p.JointURL == "" {
			deliveryErr = fmt.Errorf("partner_unavailable")
		} else {
			deliveryErr = m.callWithRequestID(ctx, p, "/revocations", map[string]string{"pair_id": o.GetString("pair_id"), "generation": o.GetString("generation"), "reason": o.GetString("reason"), "revoked_at": o.GetDateTime("created").Time().UTC().Format(time.RFC3339)}, &result, o.GetString("pair_id"))
		}
		o.Set("attempts", attempts)
		if deliveryErr == nil && !slices.Contains([]string{"revoked", "already_revoked", "unknown_generation"}, result.Status) {
			deliveryErr = fmt.Errorf("invalid_response")
		}
		if deliveryErr == nil {
			o.Set("delivered", now)
			o.Set("last_error", "")
		} else {
			backoff := time.Duration(1<<min(attempts, 9)) * time.Minute
			if backoff > 6*time.Hour {
				backoff = 6 * time.Hour
			}
			o.Set("next_at", now.Add(backoff))
			o.Set("last_error", strings.TrimSpace(deliveryErr.Error()))
		}
		if err := app.Save(o); err != nil {
			return err
		}
	}
	return nil
}

// RegisterOutboxCron delivers pending revocations every minute in bounded batches.
func (m *JointManager) RegisterOutboxCron(app core.App) {
	if m == nil {
		return
	}
	app.Cron().MustAdd("external_joint_outbox", "* * * * *", func() {
		if err := m.DeliverRevocations(context.Background(), app, 20); err != nil {
			app.Logger().Error("Failed to deliver joint revocations")
		}
	})
}

// PartnerRequest atomically stores the result of an authenticated partner request.
// A repeated request ID is scoped to its partner, method, path and body for ten minutes.
func (m *JointManager) PartnerRequest(app core.App, partner, requestID, fingerprint string, run func(core.App) (any, error)) (any, error) {
	if !uuidPattern.MatchString(requestID) {
		return nil, ErrInvalid
	}
	var result any
	err := app.RunInTransaction(func(tx core.App) error {
		prior, err := tx.FindFirstRecordByFilter(JointRequests, "partner={:p} && request_id={:id}", dbx.Params{"p": partner, "id": requestID})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && prior.GetDateTime("expires").Time().After(time.Now()) {
			if prior.GetString("fingerprint") != sha(fingerprint) {
				return ErrConflict
			}
			return json.Unmarshal([]byte(prior.GetString("response")), &result)
		}
		result, err = run(tx)
		if err != nil {
			return err
		}
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if prior == nil {
			c, err := tx.FindCollectionByNameOrId(JointRequests)
			if err != nil {
				return err
			}
			prior = core.NewRecord(c)
		}
		prior.Set("partner", partner)
		prior.Set("request_id", requestID)
		prior.Set("fingerprint", sha(fingerprint))
		prior.Set("response", string(data))
		prior.Set("expires", time.Now().UTC().Add(10*time.Minute))
		return tx.Save(prior)
	})
	return result, err
}

func (m *JointManager) enqueueRevocation(app core.App, r *core.Record, reason string) error {
	return enqueueJointRevocation(app, r, reason)
}

func enqueueJointRevocation(app core.App, r *core.Record, reason string) error {
	c, err := app.FindCollectionByNameOrId(JointOutbox)
	if err != nil {
		return err
	}
	prior, err := app.FindFirstRecordByFilter(JointOutbox, "partner={:p} && pair_id={:id} && generation={:g}", dbx.Params{"p": r.GetString("partner"), "id": r.GetString("correlation"), "g": r.GetString("generation")})
	if err == nil && prior != nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	o := core.NewRecord(c)
	o.Set("partner", r.GetString("partner"))
	o.Set("pair_id", r.GetString("correlation"))
	o.Set("generation", r.GetString("generation"))
	o.Set("reason", reason)
	o.Set("next_at", time.Now().UTC())
	return app.Save(o)
}
