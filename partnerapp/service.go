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

	"github.com/ericbaek/musecat-backend-core/delegation"
)

var (
	ErrNotFound    = errors.New("application not found")
	ErrConflict    = errors.New("partner_id already registered")
	ErrState       = errors.New("operation not allowed in the current status")
	ErrUnavailable = errors.New("OAuth delegation is not configured")
)

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
	ReviewNote   string    `json:"review_note"`
	ApprovedAt   string    `json:"approved_at,omitempty"`
	Created      string    `json:"created"`
	Updated      string    `json:"updated"`
}

type Event struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Actor   string `json:"actor"`
	Note    string `json:"note"`
	Created string `json:"created"`
}

// Service wraps the live delegation manager so approvals activate immediately.
// A nil manager keeps registration available while activation returns ErrUnavailable.
type Service struct {
	manager *delegation.Manager
}

func NewService(manager *delegation.Manager) *Service { return &Service{manager: manager} }

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

func project(r *core.Record, includeContact bool) Application {
	submitted := formOf(r, "submitted_form")
	a := Application{
		ID: r.Id, PartnerID: r.GetString("partner_id"), Owner: r.GetString("owner"), Status: r.GetString("status"),
		Live: isLive(r), Submitted: submitted, Approved: formOf(r, "approved_form"), HasSecret: r.GetString("secret_hash") != "",
		ReviewNote: r.GetString("review_note"), Created: r.GetDateTime("created").Time().Format(time.RFC3339), Updated: r.GetDateTime("updated").Time().Format(time.RFC3339),
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
		out = project(r, true)
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
		out = append(out, project(r, true))
	}
	return out, nil
}

func (s *Service) Get(app core.App, ownerID, id string) (Application, error) {
	r, err := owned(app, ownerID, id)
	if err != nil {
		return Application{}, err
	}
	return project(r, true), nil
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
		out = project(r, true)
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
		out = project(r, true)
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
		out = append(out, project(r, true))
	}
	return out, nil
}

func (s *Service) Inspect(app core.App, id string) (Application, error) {
	r, err := record(app, id)
	if err != nil {
		return Application{}, err
	}
	return project(r, true), nil
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
		if err := logEvent(tx, r.Id, operatorID, "approved", note); err != nil {
			return err
		}
		out = project(r, true)
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
		out = project(r, true)
		return nil
	})
	if err != nil {
		return Application{}, err
	}
	if s.manager != nil {
		err = s.Sync(app)
	}
	return out, err
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

// Version changes whenever the live registry would change; cheap enough to poll.
func (s *Service) Version(app core.App) (string, error) {
	rows, err := liveRows(app)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, r := range rows {
		h.Write([]byte(r.GetString("partner_id") + "|" + r.GetString("approved_hash") + "|" + r.GetString("secret_hash") + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Sync reloads the delegation manager from the live registry.
func (s *Service) Sync(app core.App) error {
	if s.manager == nil {
		return nil
	}
	clients, err := s.Clients(app)
	if err != nil {
		return err
	}
	return s.manager.Reload(app, clients)
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
