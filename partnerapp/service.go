package partnerapp

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/ericbaek/musecat-backend-core/connections"
	"github.com/ericbaek/musecat-backend-core/delegation"
)

var (
	ErrNotFound    = errors.New("application not found")
	ErrConflict    = errors.New("partner_id already registered")
	ErrState       = errors.New("operation not allowed in the current status")
	ErrUnavailable = errors.New("OAuth delegation is not configured")
	// ErrNoConnectionKey means MUSECAT_CONNECTION_ENCRYPTION_KEY is unset, so partner
	// credentials cannot be stored and Direction B cannot activate.
	ErrNoConnectionKey = errors.New("external connections are not configured")
)

// CallbackPath is the WebCore BFF callback for Direction B; partners register
// <web origin> + CallbackPath + "/" + partner_id at their authorization server.
const CallbackPath = "/api/user/connections/callback"

// Credentials are issued by the partner for Musecat's Direction-B client.
type Credentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// Application is the safe projection returned to owners and operators. It never
// includes secret material; HasSecret only reports that one has been generated.
type Application struct {
	ID           string    `json:"id"`
	PartnerID    string    `json:"partner_id"`
	Owner        string    `json:"owner"`
	Status       string    `json:"status"`
	Live         bool      `json:"live"`
	DisplayName  Localized `json:"display_name"`
	Description  Localized `json:"description"`
	ContactEmail string    `json:"contact_email,omitempty"`
	Submitted    *Form     `json:"submitted_form"`
	Approved     *Form     `json:"approved_form"`
	HasSecret    bool      `json:"has_secret"`
	// Direction B: declared in the approved form, credentials entered, and therefore
	// registered as a live external provider.
	DirectionBDeclared       bool   `json:"direction_b_declared"`
	HasDirectionBCredentials bool   `json:"has_direction_b_credentials"`
	DirectionBLive           bool   `json:"direction_b_live"`
	MusecatRedirectURI       string `json:"musecat_redirect_uri,omitempty"`
	// Joint: declared, partner-issued outbound key stored, Musecat-issued inbound key
	// generated, and therefore registered for one-step reciprocal connection.
	JointDeclared bool   `json:"joint_declared"`
	HasJointKey   bool   `json:"has_joint_key"`
	HasPartnerKey bool   `json:"has_partner_key"`
	JointLive     bool   `json:"joint_live"`
	ReviewNote    string `json:"review_note"`
	ApprovedAt    string `json:"approved_at,omitempty"`
	Created       string `json:"created"`
	Updated       string `json:"updated"`
}

type Event struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Actor   string `json:"actor"`
	Note    string `json:"note"`
	Created string `json:"created"`
}

// Service wraps the live delegation and connection managers so approvals activate
// immediately. A nil delegation manager keeps registration available while activation
// returns ErrUnavailable; a nil connections manager disables Direction B only.
type Service struct {
	manager     *delegation.Manager
	connections *connections.Manager
	joint       *connections.JointManager
	webOrigin   string
}

// NewService builds the Developer Center service. webOrigin is the HTTPS origin of
// the Musecat web deployment used for Direction-B callback URLs; when empty it is
// derived from the OAuth consent URL. joint may be nil (no one-step activation).
func NewService(manager *delegation.Manager, conns *connections.Manager, joint *connections.JointManager, webOrigin string) *Service {
	origin := strings.TrimRight(strings.TrimSpace(webOrigin), "/")
	if origin == "" {
		origin = manager.ConsentOrigin()
	}
	return &Service{manager: manager, connections: conns, joint: joint, webOrigin: origin}
}

func jointPurpose(id string) string { return "partner_application:" + id + ":joint" }

type jointCredentials struct {
	OutboundKey string `json:"outbound_key"`
}

func (s *Service) jointLive(r *core.Record) bool {
	f := formOf(r, "approved_form")
	return s.joint != nil && s.directionBLive(r) && f.Joint != nil && f.Joint.Enabled && r.GetString("joint_credentials") != "" && r.GetString("partner_key_hash") != ""
}

func (s *Service) redirectURI(partnerID string) string {
	if s.webOrigin == "" {
		return ""
	}
	return s.webOrigin + CallbackPath + "/" + partnerID
}

func credentialPurpose(id string) string { return "partner_application:" + id + ":direction_b" }

func (s *Service) directionBLive(r *core.Record) bool {
	f := formOf(r, "approved_form")
	return f != nil && f.DirectionB != nil && r.GetString("status") != StatusSuspended && r.GetString("direction_b_credentials") != "" && s.connections != nil && s.webOrigin != ""
}

func hashSecret(v string) string { s := sha256.Sum256([]byte(v)); return hex.EncodeToString(s[:]) }

func formOf(r *core.Record, field string) *Form {
	raw := r.GetString(field)
	if strings.TrimSpace(raw) == "" || raw == "null" {
		return nil
	}
	var f Form
	if json.Unmarshal([]byte(raw), &f) != nil {
		return nil
	}
	return &f
}

func formJSON(f *Form) string {
	b, _ := json.Marshal(f)
	return string(b)
}

func formHash(f *Form) string { return hashSecret(formJSON(f)) }

func isLive(r *core.Record) bool {
	return formOf(r, "approved_form") != nil && r.GetString("status") != StatusSuspended && r.GetString("secret_hash") != ""
}

func (s *Service) project(r *core.Record, includeContact bool) Application {
	submitted := formOf(r, "submitted_form")
	approved := formOf(r, "approved_form")
	a := Application{
		ID: r.Id, PartnerID: r.GetString("partner_id"), Owner: r.GetString("owner"), Status: r.GetString("status"),
		Live: isLive(r), Submitted: submitted, Approved: approved, HasSecret: r.GetString("secret_hash") != "",
		DirectionBDeclared:       (approved != nil && approved.DirectionB != nil) || (submitted != nil && submitted.DirectionB != nil),
		HasDirectionBCredentials: r.GetString("direction_b_credentials") != "",
		DirectionBLive:           s.directionBLive(r),
		JointDeclared:            (approved != nil && approved.Joint != nil && approved.Joint.Enabled) || (submitted != nil && submitted.Joint != nil && submitted.Joint.Enabled),
		HasJointKey:              r.GetString("joint_credentials") != "",
		HasPartnerKey:            r.GetString("partner_key_hash") != "",
		JointLive:                s.jointLive(r),
		ReviewNote:               r.GetString("review_note"), Created: r.GetDateTime("created").Time().Format(time.RFC3339), Updated: r.GetDateTime("updated").Time().Format(time.RFC3339),
	}
	if a.DirectionBDeclared {
		a.MusecatRedirectURI = s.redirectURI(a.PartnerID)
	}
	if submitted != nil {
		a.DisplayName, a.Description = submitted.DisplayName, submitted.Description
		if includeContact {
			a.ContactEmail = submitted.ContactEmail
		}
	}
	if t := r.GetDateTime("approved_at"); !t.IsZero() {
		a.ApprovedAt = t.Time().Format(time.RFC3339)
	}
	return a
}

func record(app core.App, id string) (*core.Record, error) {
	r, err := app.FindRecordById(Applications, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func owned(app core.App, ownerID, id string) (*core.Record, error) {
	r, err := record(app, id)
	if err != nil {
		return nil, err
	}
	if r.GetString("owner") != ownerID {
		return nil, ErrNotFound
	}
	return r, nil
}

func logEvent(app core.App, applicationID, actorID, kind, note string) error {
	c, err := app.FindCollectionByNameOrId(Events)
	if err != nil {
		return err
	}
	e := core.NewRecord(c)
	e.Set("application", applicationID)
	e.Set("actor", actorID)
	e.Set("kind", kind)
	e.Set("note", note)
	return app.Save(e)
}

// partnerIDTaken reports whether another application or an executable-configured
// client already owns the ID. A static client can never be replaced dynamically.
func (s *Service) partnerIDTaken(app core.App, partnerID, exceptID string) (bool, error) {
	rows, err := app.FindRecordsByFilter(Applications, "partner_id = {:p} && id != {:id}", "", 1, 0, dbx.Params{"p": partnerID, "id": exceptID})
	if err != nil {
		return true, err
	}
	if len(rows) > 0 {
		return true, nil
	}
	if _, ok := s.manager.Client(partnerID); ok {
		own, err := app.FindRecordsByFilter(Applications, "partner_id = {:p}", "", 1, 0, dbx.Params{"p": partnerID})
		if err != nil {
			return true, err
		}
		return len(own) == 0, nil
	}
	return false, nil
}

// Create stores a draft. Owners may hold at most 8 applications.
func (s *Service) Create(app core.App, ownerID string, form Form) (Application, error) {
	if err := form.Validate(); err != nil {
		return Application{}, err
	}
	var out Application
	err := app.RunInTransaction(func(tx core.App) error {
		mine, err := tx.FindRecordsByFilter(Applications, "owner = {:o}", "", 9, 0, dbx.Params{"o": ownerID})
		if err != nil {
			return err
		}
		if len(mine) >= 8 {
			return ErrState
		}
		taken, err := s.partnerIDTaken(tx, form.PartnerID, "")
		if err != nil {
			return err
		}
		if taken {
			return ErrConflict
		}
		c, err := tx.FindCollectionByNameOrId(Applications)
		if err != nil {
			return err
		}
		r := core.NewRecord(c)
		r.Set("partner_id", form.PartnerID)
		r.Set("owner", ownerID)
		r.Set("status", StatusDraft)
		r.Set("submitted_form", formJSON(&form))
		if err := tx.Save(r); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return ErrConflict
			}
			return err
		}
		if err := logEvent(tx, r.Id, ownerID, "created", ""); err != nil {
			return err
		}
		out = s.project(r, true)
		return nil
	})
	return out, err
}

func (s *Service) List(app core.App, ownerID string) ([]Application, error) {
	rows, err := app.FindRecordsByFilter(Applications, "owner = {:o}", "-updated", 50, 0, dbx.Params{"o": ownerID})
	if err != nil {
		return nil, err
	}
	out := make([]Application, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.project(r, true))
	}
	return out, nil
}

func (s *Service) Get(app core.App, ownerID, id string) (Application, error) {
	r, err := owned(app, ownerID, id)
	if err != nil {
		return Application{}, err
	}
	return s.project(r, true), nil
}

// Update replaces the submitted form. partner_id is immutable once approved. An
// approved application keeps serving its approved form until the change is approved.
func (s *Service) Update(app core.App, ownerID, id string, form Form) (Application, error) {
	if err := form.Validate(); err != nil {
		return Application{}, err
	}
	var out Application
	err := app.RunInTransaction(func(tx core.App) error {
		r, err := owned(tx, ownerID, id)
		if err != nil {
			return err
		}
		if r.GetString("status") == StatusSuspended {
			return ErrState
		}
		if formOf(r, "approved_form") != nil && form.PartnerID != r.GetString("partner_id") {
			return ErrState
		}
		if form.PartnerID != r.GetString("partner_id") {
			taken, err := s.partnerIDTaken(tx, form.PartnerID, r.Id)
			if err != nil {
				return err
			}
			if taken {
				return ErrConflict
			}
			r.Set("partner_id", form.PartnerID)
		}
		r.Set("submitted_form", formJSON(&form))
		// An approved application stays live; the edit becomes a new draft for review.
		r.Set("status", StatusDraft)
		if err := tx.Save(r); err != nil {
			return err
		}
		if err := logEvent(tx, r.Id, ownerID, "updated", ""); err != nil {
			return err
		}
		out = s.project(r, true)
		return nil
	})
	return out, err
}

func (s *Service) Submit(app core.App, ownerID, id string) (Application, error) {
	var out Application
	err := app.RunInTransaction(func(tx core.App) error {
		r, err := owned(tx, ownerID, id)
		if err != nil {
			return err
		}
		if st := r.GetString("status"); st != StatusDraft && st != StatusRejected {
			return ErrState
		}
		form := formOf(r, "submitted_form")
		if form == nil {
			return ErrState
		}
		if err := form.Validate(); err != nil {
			return err
		}
		r.Set("status", StatusSubmitted)
		r.Set("review_note", "")
		if err := tx.Save(r); err != nil {
			return err
		}
		if err := logEvent(tx, r.Id, ownerID, "submitted", ""); err != nil {
			return err
		}
		out = s.project(r, true)
		return nil
	})
	return out, err
}

// GenerateSecret issues a new client secret for an approved application and
// returns the plaintext exactly once. Rotation changes the registration
// fingerprint, so existing grants for the client are invalidated.
func (s *Service) GenerateSecret(app core.App, ownerID, id string) (clientID, secret string, err error) {
	if s.manager == nil {
		return "", "", ErrUnavailable
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	secret = base64.RawURLEncoding.EncodeToString(buf)
	err = app.RunInTransaction(func(tx core.App) error {
		r, err := owned(tx, ownerID, id)
		if err != nil {
			return err
		}
		if formOf(r, "approved_form") == nil || r.GetString("status") == StatusSuspended {
			return ErrState
		}
		clientID = r.GetString("partner_id")
		r.Set("secret_hash", hashSecret(secret))
		if err := tx.Save(r); err != nil {
			return err
		}
		return logEvent(tx, r.Id, ownerID, "secret_generated", "")
	})
	if err != nil {
		return "", "", err
	}
	return clientID, secret, s.Sync(app)
}

func (s *Service) EventsFor(app core.App, id string) ([]Event, error) {
	rows, err := app.FindRecordsByFilter(Events, "application = {:a}", "-created", 100, 0, dbx.Params{"a": id})
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, Event{ID: r.Id, Kind: r.GetString("kind"), Actor: r.GetString("actor"), Note: r.GetString("note"), Created: r.GetDateTime("created").Time().Format(time.RFC3339)})
	}
	return out, nil
}

func (s *Service) OwnerEvents(app core.App, ownerID, id string) ([]Event, error) {
	if _, err := owned(app, ownerID, id); err != nil {
		return nil, err
	}
	return s.EventsFor(app, id)
}

// SetCredentials stores the partner-issued Musecat client credentials for Direction B.
// They are sealed with the connection key and bound to this application; the plaintext
// is never returned. Direction B activates immediately when the approved form declares it.
func (s *Service) SetCredentials(app core.App, ownerID, id string, c Credentials) (Application, error) {
	if s.connections == nil {
		return Application{}, ErrNoConnectionKey
	}
	c.ClientID, c.ClientSecret = strings.TrimSpace(c.ClientID), strings.TrimSpace(c.ClientSecret)
	if c.ClientID == "" || len(c.ClientID) > 256 || len(c.ClientSecret) < 16 || len(c.ClientSecret) > 512 || strings.ContainsAny(c.ClientID+c.ClientSecret, " \t\r\n") {
		return Application{}, FieldErrors{"credentials": "invalid"}
	}
	var out Application
	err := app.RunInTransaction(func(tx core.App) error {
		r, err := owned(tx, ownerID, id)
		if err != nil {
			return err
		}
		if r.GetString("status") == StatusSuspended {
			return ErrState
		}
		approved, submitted := formOf(r, "approved_form"), formOf(r, "submitted_form")
		if (approved == nil || approved.DirectionB == nil) && (submitted == nil || submitted.DirectionB == nil) {
			return ErrState
		}
		sealed, err := s.connections.Seal(c, credentialPurpose(r.Id))
		if err != nil {
			return err
		}
		r.Set("direction_b_credentials", sealed)
		if err := tx.Save(r); err != nil {
			return err
		}
		if approved != nil && approved.DirectionB != nil {
			providers, err := s.Providers(tx)
			if err != nil {
				return err
			}
			if err := s.connections.Check(providers); err != nil {
				return ErrConflict
			}
		}
		if err := logEvent(tx, r.Id, ownerID, "credentials_set", ""); err != nil {
			return err
		}
		out = s.project(r, true)
		return nil
	})
	if err != nil {
		return Application{}, err
	}
	return out, s.Sync(app)
}

// ClearCredentials removes the stored credentials and deactivates Direction B.
func (s *Service) ClearCredentials(app core.App, ownerID, id string) (Application, error) {
	var out Application
	err := app.RunInTransaction(func(tx core.App) error {
		r, err := owned(tx, ownerID, id)
		if err != nil {
			return err
		}
		r.Set("direction_b_credentials", "")
		if err := tx.Save(r); err != nil {
			return err
		}
		if err := logEvent(tx, r.Id, ownerID, "credentials_cleared", ""); err != nil {
			return err
		}
		out = s.project(r, true)
		return nil
	})
	if err != nil {
		return Application{}, err
	}
	return out, s.Sync(app)
}

// SetJointKey stores the partner-issued key Musecat presents on calls to the partner's
// joint endpoints (spec section 4). Sealed; never returned.
func (s *Service) SetJointKey(app core.App, ownerID, id, key string) (Application, error) {
	if s.connections == nil {
		return Application{}, ErrNoConnectionKey
	}
	key = strings.TrimSpace(key)
	if len(key) < 43 || len(key) > 512 || strings.ContainsAny(key, " \t\r\n") {
		return Application{}, FieldErrors{"joint_key": "invalid"}
	}
	var out Application
	err := app.RunInTransaction(func(tx core.App) error {
		r, err := owned(tx, ownerID, id)
		if err != nil {
			return err
		}
		approved, submitted := formOf(r, "approved_form"), formOf(r, "submitted_form")
		declared := func(f *Form) bool { return f != nil && f.Joint != nil && f.Joint.Enabled }
		if r.GetString("status") == StatusSuspended || (!declared(approved) && !declared(submitted)) {
			return ErrState
		}
		sealed, err := s.connections.Seal(jointCredentials{OutboundKey: key}, jointPurpose(r.Id))
		if err != nil {
			return err
		}
		r.Set("joint_credentials", sealed)
		if err := tx.Save(r); err != nil {
			return err
		}
		if err := logEvent(tx, r.Id, ownerID, "joint_key_set", ""); err != nil {
			return err
		}
		out = s.project(r, true)
		return nil
	})
	if err != nil {
		return Application{}, err
	}
	return out, s.Sync(app)
}

// GeneratePartnerKey issues the key the partner must present on /partner/v1/* calls.
// Only the hash is stored; the plaintext is returned once. Regeneration rotates it.
func (s *Service) GeneratePartnerKey(app core.App, ownerID, id string) (partnerID, key string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	key = base64.RawURLEncoding.EncodeToString(buf)
	err = app.RunInTransaction(func(tx core.App) error {
		r, err := owned(tx, ownerID, id)
		if err != nil {
			return err
		}
		approved, submitted := formOf(r, "approved_form"), formOf(r, "submitted_form")
		declared := func(f *Form) bool { return f != nil && f.Joint != nil && f.Joint.Enabled }
		if r.GetString("status") == StatusSuspended || (!declared(approved) && !declared(submitted)) {
			return ErrState
		}
		partnerID = r.GetString("partner_id")
		hours := 24
		if approved != nil && approved.Joint != nil {
			hours = approved.Joint.KeyRotationWindowHours
		}
		r.Set("previous_partner_key_hash", r.GetString("partner_key_hash"))
		r.Set("previous_partner_key_expires", time.Now().UTC().Add(time.Duration(hours)*time.Hour))
		r.Set("partner_key_hash", hashSecret(key))
		if err := tx.Save(r); err != nil {
			return err
		}
		return logEvent(tx, r.Id, ownerID, "partner_key_generated", "")
	})
	if err != nil {
		return "", "", err
	}
	return partnerID, key, s.Sync(app)
}

// --- Operator operations ---

func (s *Service) Queue(app core.App, status string) ([]Application, error) {
	filter, params := "status = {:s}", dbx.Params{"s": status}
	if status == "" {
		filter, params = "status != {:d}", dbx.Params{"d": StatusDraft}
	}
	rows, err := app.FindRecordsByFilter(Applications, filter, "-updated", 200, 0, params)
	if err != nil {
		return nil, err
	}
	out := make([]Application, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.project(r, true))
	}
	return out, nil
}

func (s *Service) Inspect(app core.App, id string) (Application, error) {
	r, err := record(app, id)
	if err != nil {
		return Application{}, err
	}
	return s.project(r, true), nil
}

// Approve makes the submitted form live. The merged registry is validated before
// anything is written so a duplicate or invalid client cannot be approved.
func (s *Service) Approve(app core.App, operatorID, id, note string) (Application, error) {
	if s.manager == nil {
		return Application{}, ErrUnavailable
	}
	var out Application
	err := app.RunInTransaction(func(tx core.App) error {
		r, err := record(tx, id)
		if err != nil {
			return err
		}
		if r.GetString("status") != StatusSubmitted {
			return ErrState
		}
		form := formOf(r, "submitted_form")
		if form == nil {
			return ErrState
		}
		if err := form.Validate(); err != nil {
			return err
		}
		if form.PartnerID != r.GetString("partner_id") {
			return ErrState
		}
		if _, ok := s.manager.Client(form.PartnerID); ok {
			if approved := formOf(r, "approved_form"); approved == nil || !isLive(r) {
				return ErrConflict
			}
		}
		r.Set("approved_form", formJSON(form))
		r.Set("approved_hash", formHash(form))
		r.Set("approved_by", operatorID)
		r.Set("approved_at", time.Now().UTC())
		r.Set("status", StatusApproved)
		r.Set("review_note", note)
		if err := tx.Save(r); err != nil {
			return err
		}
		clients, err := s.Clients(tx)
		if err != nil {
			return err
		}
		if err := s.manager.Check(clients); err != nil {
			return ErrConflict
		}
		if s.connections != nil {
			providers, err := s.Providers(tx)
			if err != nil {
				return err
			}
			if err := s.connections.Check(providers); err != nil {
				return ErrConflict
			}
		}
		if err := logEvent(tx, r.Id, operatorID, "approved", note); err != nil {
			return err
		}
		out = s.project(r, true)
		return nil
	})
	if err != nil {
		return Application{}, err
	}
	return out, s.Sync(app)
}

func (s *Service) transition(app core.App, operatorID, id, note, from, to, kind string, alsoFrom ...string) (Application, error) {
	var out Application
	err := app.RunInTransaction(func(tx core.App) error {
		r, err := record(tx, id)
		if err != nil {
			return err
		}
		st := r.GetString("status")
		allowed := st == from
		for _, f := range alsoFrom {
			allowed = allowed || st == f
		}
		if !allowed {
			return ErrState
		}
		r.Set("status", to)
		r.Set("review_note", note)
		if err := tx.Save(r); err != nil {
			return err
		}
		if err := logEvent(tx, r.Id, operatorID, kind, note); err != nil {
			return err
		}
		out = s.project(r, true)
		return nil
	})
	if err != nil {
		return Application{}, err
	}
	return out, s.Sync(app)
}

func (s *Service) Reject(app core.App, operatorID, id, note string) (Application, error) {
	return s.transition(app, operatorID, id, note, StatusSubmitted, StatusRejected, "rejected")
}

// Suspend stops the live registration immediately; every token for the client fails.
func (s *Service) Suspend(app core.App, operatorID, id, note string) (Application, error) {
	return s.transition(app, operatorID, id, note, StatusApproved, StatusSuspended, "suspended", StatusSubmitted, StatusRejected, StatusDraft)
}

// Reinstate restores the previously approved form without issuing a new secret. If the
// owner edited the form while suspended, the edit returns to draft for review.
func (s *Service) Reinstate(app core.App, operatorID, id, note string) (Application, error) {
	r, err := record(app, id)
	if err != nil {
		return Application{}, err
	}
	to := StatusApproved
	if submitted := formOf(r, "submitted_form"); submitted != nil && formHash(submitted) != r.GetString("approved_hash") {
		to = StatusDraft
	}
	return s.transition(app, operatorID, id, note, StatusSuspended, to, "reinstated")
}

// --- Registry ---

func liveRows(app core.App) ([]*core.Record, error) {
	return app.FindRecordsByFilter(Applications, "approved_form != '' && status != {:s} && secret_hash != ''", "partner_id", 129, 0, dbx.Params{"s": StatusSuspended})
}

// Clients maps live applications to OAuth client registrations.
func (s *Service) Clients(app core.App) ([]delegation.Client, error) {
	rows, err := liveRows(app)
	if err != nil {
		return nil, err
	}
	if len(rows) > 128 {
		return nil, fmt.Errorf("too many live partner applications")
	}
	out := make([]delegation.Client, 0, len(rows))
	for _, r := range rows {
		f := formOf(r, "approved_form")
		if f == nil || !isLive(r) {
			continue
		}
		out = append(out, delegation.Client{
			ID: f.PartnerID, Name: f.DisplayName.En, Description: f.Description.En,
			RedirectURIs: f.DirectionA.RedirectURIs, Scopes: []string{delegation.AccountAccess},
			SecretHash: r.GetString("secret_hash"),
		})
	}
	return out, nil
}

// Providers maps live Direction-B applications to external provider registrations.
func (s *Service) Providers(app core.App) ([]connections.Provider, error) {
	if s.connections == nil || s.webOrigin == "" {
		return nil, nil
	}
	rows, err := app.FindRecordsByFilter(Applications, "approved_form != '' && status != {:s} && direction_b_credentials != ''", "partner_id", 129, 0, dbx.Params{"s": StatusSuspended})
	if err != nil {
		return nil, err
	}
	out := make([]connections.Provider, 0, len(rows))
	for _, r := range rows {
		f := formOf(r, "approved_form")
		if f == nil || f.DirectionB == nil {
			continue
		}
		var c Credentials
		if err := s.connections.Open(r.GetString("direction_b_credentials"), credentialPurpose(r.Id), &c); err != nil {
			// A key rotation leaves stale ciphertext; skip rather than fail every partner.
			continue
		}
		services := make([]connections.Service, 0, len(f.DirectionB.Services))
		for _, svc := range f.DirectionB.Services {
			services = append(services, connections.Service{ID: svc.ID, Name: ServiceName(svc.ID), Scopes: []string{svc.Scope}})
		}
		out = append(out, connections.Provider{
			ID: f.PartnerID, Name: f.DisplayName.En, Issuer: f.DirectionB.Issuer,
			AuthorizationURL: f.DirectionB.AuthorizationURL, TokenURL: f.DirectionB.TokenURL, UserInfoURL: f.DirectionB.UserInfoURL,
			SubjectField: "sub", ClientID: c.ClientID, ClientSecret: c.ClientSecret, RedirectURI: s.redirectURI(f.PartnerID),
			ClientAuthMethod: "client_secret_basic", RequireIssuer: true, Scopes: []string{f.DirectionB.IdentityScope}, Services: services,
			RecordsURL: f.DirectionB.RecordsURL,
		})
	}
	return out, nil
}

// JointPartners maps live applications with one-step capability to the joint registry.
// Registration happens after the provider and client are live so Check passes.
func (s *Service) JointPartners(app core.App) ([]connections.JointPartner, error) {
	if s.joint == nil || s.connections == nil {
		return nil, nil
	}
	rows, err := app.FindRecordsByFilter(Applications, "approved_form != '' && status != {:s} && direction_b_credentials != '' && joint_credentials != '' && partner_key_hash != '' && secret_hash != ''", "partner_id", 129, 0, dbx.Params{"s": StatusSuspended})
	if err != nil {
		return nil, err
	}
	out := make([]connections.JointPartner, 0, len(rows))
	for _, r := range rows {
		f := formOf(r, "approved_form")
		if f == nil || f.DirectionB == nil || f.Joint == nil || !f.Joint.Enabled {
			continue
		}
		var jc jointCredentials
		if err := s.connections.Open(r.GetString("joint_credentials"), jointPurpose(r.Id), &jc); err != nil {
			continue
		}
		services := make([]string, 0, len(f.DirectionB.Services))
		for _, svc := range f.DirectionB.Services {
			services = append(services, svc.ID)
		}
		out = append(out, connections.JointPartner{
			ID: f.PartnerID, ClientID: f.PartnerID, Provider: f.PartnerID, Services: services,
			JointURL: f.Joint.JointURL, ConnectStartURL: f.Joint.ConnectStartURL, Manifests: f.Joint.Manifests,
			OutboundKey: jc.OutboundKey, InboundKeyHash: r.GetString("partner_key_hash"),
			PreviousInboundKeyHash: r.GetString("previous_partner_key_hash"), PreviousInboundKeyExpires: r.GetDateTime("previous_partner_key_expires").Time(),
		})
	}
	return out, nil
}

// Version changes whenever the live registry would change; cheap enough to poll.
func (s *Service) Version(app core.App) (string, error) {
	rows, err := app.FindRecordsByFilter(Applications, "approved_form != '' && status != {:s}", "partner_id", 300, 0, dbx.Params{"s": StatusSuspended})
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, r := range rows {
		h.Write([]byte(r.GetString("partner_id") + "|" + r.GetString("approved_hash") + "|" + r.GetString("secret_hash") + "|" + hashSecret(r.GetString("direction_b_credentials")) + "|" + hashSecret(r.GetString("joint_credentials")) + "|" + r.GetString("partner_key_hash") + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Sync reloads both live registries from the database.
func (s *Service) Sync(app core.App) error {
	if s.manager != nil {
		clients, err := s.Clients(app)
		if err != nil {
			return err
		}
		if err := s.manager.Reload(app, clients); err != nil {
			return err
		}
	}
	if s.connections != nil {
		providers, err := s.Providers(app)
		if err != nil {
			return err
		}
		if err := s.connections.Reload(providers); err != nil {
			return err
		}
	}
	if s.joint != nil {
		partners, err := s.JointPartners(app)
		if err != nil {
			return err
		}
		// A partner whose provider/client just went away is dropped rather than failing
		// every other partner; Check at approval time guards the common path.
		if err := s.joint.Reload(partners); err != nil {
			_ = s.joint.Reload(nil)
			return err
		}
	}
	return nil
}

// RegisterSyncCron keeps every instance within one minute of an approval made on
// another instance. Executables opt in; Core never starts jobs itself.
func (s *Service) RegisterSyncCron(app core.App) {
	last := ""
	app.Cron().MustAdd("partner_application_sync", "* * * * *", func() {
		v, err := s.Version(app)
		if err != nil || v == last {
			return
		}
		if err := s.Sync(app); err != nil {
			app.Logger().Error("Failed to synchronize partner applications")
			return
		}
		last = v
	})
}
