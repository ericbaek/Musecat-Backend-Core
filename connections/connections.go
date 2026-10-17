package connections

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/security"
	"golang.org/x/oauth2"
)

var (
	ErrInvalid     = errors.New("invalid or expired connection request")
	ErrConflict    = errors.New("external account is already connected")
	ErrUnavailable = errors.New("external provider is unavailable")
	ErrProvider    = errors.New("external provider could not verify this account")
)

// Actor is created by the authenticated HTTP boundary, never decoded from a body.
// Check revalidates the live session and account restrictions inside each transaction.
type Actor struct {
	UserID, Session string
	Check           func(core.App) error
}

func (a Actor) check(app core.App) error {
	if a.UserID == "" || a.Session == "" || a.Check == nil {
		return ErrInvalid
	}
	return a.Check(app)
}

type StartInput struct {
	Provider string   `json:"provider"`
	Services []string `json:"services"`
}
type StartResult struct {
	AuthorizationURL string    `json:"authorization_url"`
	ExpiresAt        time.Time `json:"expires_at"`
}
type CompleteInput struct {
	Provider string `json:"provider"`
	State    string `json:"state"`
	Code     string `json:"code"`
	Issuer   string `json:"iss,omitempty"`
	Error    string `json:"error,omitempty"`
}
type ServiceConnection struct {
	Service    string   `json:"service"`
	Visibility string   `json:"visibility"`
	Fields     []string `json:"fields"`
}
type Connection struct {
	ID       string              `json:"id"`
	Provider string              `json:"provider"`
	Issuer   string              `json:"issuer"`
	Scopes   []string            `json:"scopes"`
	Services []ServiceConnection `json:"services"`
	Created  time.Time           `json:"created"`
	Updated  time.Time           `json:"updated"`
}

func (m *Manager) Start(app core.App, actor Actor, input StartInput) (StartResult, error) {
	if m == nil {
		return StartResult{}, ErrUnavailable
	}
	p, ok := m.lookup(input.Provider)
	if !ok {
		return StartResult{}, ErrUnavailable
	}
	if len(input.Services) == 0 || len(input.Services) > 16 {
		return StartResult{}, ErrInvalid
	}
	scopes := slices.Clone(p.Scopes)
	seen := map[string]bool{}
	for _, id := range input.Services {
		if seen[id] {
			return StartResult{}, ErrInvalid
		}
		seen[id] = true
		found := false
		for _, service := range p.Services {
			if service.ID == id {
				scopes = append(scopes, service.Scopes...)
				found = true
			}
		}
		if !found {
			return StartResult{}, ErrInvalid
		}
	}
	slices.Sort(scopes)
	scopes = slices.Compact(scopes)
	state, verifier := randomToken(), oauth2.GenerateVerifier()
	expiry := time.Now().UTC().Add(5 * time.Minute)
	err := app.RunInTransaction(func(tx core.App) error {
		if err := actor.check(tx); err != nil {
			return err
		}
		// A newer attempt replaces pending or in-flight work for the same issuer.
		if err := deleteRequests(tx, actor.UserID, p.Issuer); err != nil {
			return err
		}
		collection, err := tx.FindCollectionByNameOrId(Requests)
		if err != nil {
			return err
		}
		request := core.NewRecord(collection)
		request.Id = security.RandomStringWithAlphabet(15, "abcdefghijklmnopqrstuvwxyz0123456789")
		encrypted, err := m.seal(verifier, "request:"+request.Id)
		if err != nil {
			return err
		}
		request.Set("user", actor.UserID)
		request.Set("provider", p.ID)
		request.Set("issuer", p.Issuer)
		request.Set("state_hash", digest(state))
		request.Set("session_hash", digest(actor.Session))
		request.Set("config_hash", fingerprint(p))
		request.Set("verifier", encrypted)
		request.Set("scopes", scopes)
		request.Set("services", input.Services)
		request.Set("expires", expiry)
		request.Set("status", "pending")
		return tx.Save(request)
	})
	if err != nil {
		return StartResult{}, err
	}
	return StartResult{p.oauth(scopes).AuthCodeURL(state, oauth2.S256ChallengeOption(verifier)), expiry}, nil
}

func (m *Manager) Complete(ctx context.Context, app core.App, actor Actor, input CompleteInput) (Connection, error) {
	if m == nil {
		return Connection{}, ErrUnavailable
	}
	p, ok := m.lookup(input.Provider)
	if !ok {
		return Connection{}, ErrUnavailable
	}
	if len(input.State) != 43 || len(input.Code) > 4096 || len(input.Error) > 256 || ((input.Code == "") == (input.Error == "")) {
		return Connection{}, ErrInvalid
	}
	if (input.Issuer != "" || p.RequireIssuer) && input.Issuer != p.Issuer {
		return Connection{}, ErrInvalid
	}
	var pending *core.Record
	var verifier string
	var requested, serviceIDs []string
	err := app.RunInTransaction(func(tx core.App) error {
		if err := actor.check(tx); err != nil {
			return err
		}
		r, err := tx.FindFirstRecordByFilter(Requests, "state_hash = {:state} && user = {:user}", dbx.Params{"state": digest(input.State), "user": actor.UserID})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalid
		}
		if err != nil {
			return err
		}
		if r.GetString("provider") != p.ID || r.GetString("issuer") != p.Issuer || r.GetString("session_hash") != digest(actor.Session) || r.GetString("config_hash") != fingerprint(p) || r.GetString("status") != "pending" || !r.GetDateTime("expires").Time().After(time.Now().UTC()) {
			return ErrInvalid
		}
		pending = r
		if input.Error != "" {
			return tx.Delete(r)
		}
		if err := m.open(r.GetString("verifier"), "request:"+r.Id, &verifier); err != nil {
			return ErrUnavailable
		}
		if err := r.UnmarshalJSONField("scopes", &requested); err != nil {
			return err
		}
		if err := r.UnmarshalJSONField("services", &serviceIDs); err != nil {
			return err
		}
		r.Set("status", "exchanging")
		r.Set("verifier", "")
		return tx.Save(r)
	})
	if err != nil {
		return Connection{}, err
	}
	if input.Error != "" {
		return Connection{}, ErrInvalid
	}
	// Every claimed attempt is terminal, including network failures. A fresh start is required.
	defer func() {
		_ = app.RunInTransaction(func(tx core.App) error {
			r, err := tx.FindRecordById(Requests, pending.Id)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			return tx.Delete(r)
		})
	}()
	subject, tokens, granted, err := m.exchange(ctx, p, input.Code, verifier, requested)
	if err != nil {
		return Connection{}, ErrProvider
	}
	var result Connection
	err = app.RunInTransaction(func(tx core.App) error {
		if err := actor.check(tx); err != nil {
			return err
		}
		r, err := tx.FindRecordById(Requests, pending.Id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalid
		}
		if err != nil {
			return err
		}
		if r.GetString("status") != "exchanging" || !r.GetDateTime("expires").Time().After(time.Now().UTC()) {
			return ErrInvalid
		}
		owner, err := tx.FindFirstRecordByFilter(Accounts, "issuer = {:issuer} && subject = {:subject}", dbx.Params{"issuer": p.Issuer, "subject": subject})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if owner != nil && owner.GetString("user") != actor.UserID {
			return ErrConflict
		}
		existing, err := tx.FindFirstRecordByFilter(Accounts, "user = {:user} && issuer = {:issuer}", dbx.Params{"user": actor.UserID, "issuer": p.Issuer})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if existing != nil && existing.GetString("subject") != subject {
			return ErrConflict
		}
		account := existing
		if account == nil {
			collection, err := tx.FindCollectionByNameOrId(Accounts)
			if err != nil {
				return err
			}
			account = core.NewRecord(collection)
			account.Id = security.RandomStringWithAlphabet(15, "abcdefghijklmnopqrstuvwxyz0123456789")
		}
		encrypted, err := m.seal(tokens, "account:"+account.Id)
		if err != nil {
			return err
		}
		account.Set("user", actor.UserID)
		account.Set("provider", p.ID)
		account.Set("issuer", p.Issuer)
		account.Set("subject", subject)
		account.Set("scopes", granted)
		account.Set("credentials", encrypted)
		if err := tx.Save(account); err != nil {
			return err
		}
		// Reauthorization replaces game grants with exactly the requested, actually granted set.
		old, err := tx.FindRecordsByFilter(Services, "connection = {:id}", "", 0, 0, dbx.Params{"id": account.Id})
		if err != nil {
			return err
		}
		for _, service := range old {
			if err := tx.Delete(service); err != nil {
				return err
			}
		}
		collection, err := tx.FindCollectionByNameOrId(Services)
		if err != nil {
			return err
		}
		for _, service := range p.Services {
			if !slices.Contains(serviceIDs, service.ID) {
				continue
			}
			allowed := true
			for _, scope := range service.Scopes {
				if !slices.Contains(granted, scope) {
					allowed = false
				}
			}
			if !allowed {
				continue
			}
			record := core.NewRecord(collection)
			record.Set("connection", account.Id)
			record.Set("service", service.ID)
			record.Set("visibility", "private")
			record.Set("fields", []string{})
			if err := tx.Save(record); err != nil {
				return err
			}
		}
		if err := tx.Delete(r); err != nil {
			return err
		}
		result, err = project(tx, account)
		return err
	})
	return result, err
}

func List(app core.App, userID string) ([]Connection, error) {
	records, err := app.FindRecordsByFilter(Accounts, "user = {:user}", "created,id", 0, 0, dbx.Params{"user": userID})
	if err != nil {
		return nil, err
	}
	result := []Connection{}
	for _, record := range records {
		item, err := project(app, record)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func project(app core.App, r *core.Record) (Connection, error) {
	result := Connection{ID: r.Id, Provider: r.GetString("provider"), Issuer: r.GetString("issuer"), Scopes: []string{}, Services: []ServiceConnection{}, Created: r.GetDateTime("created").Time(), Updated: r.GetDateTime("updated").Time()}
	if err := r.UnmarshalJSONField("scopes", &result.Scopes); err != nil {
		return result, err
	}
	services, err := app.FindRecordsByFilter(Services, "connection = {:id}", "service", 0, 0, dbx.Params{"id": r.Id})
	if err != nil {
		return result, err
	}
	for _, r := range services {
		item := ServiceConnection{Service: r.GetString("service"), Visibility: r.GetString("visibility"), Fields: []string{}}
		if err := r.UnmarshalJSONField("fields", &item.Fields); err != nil {
			return result, err
		}
		result.Services = append(result.Services, item)
	}
	return result, nil
}

// Disconnect works even when a provider is disabled or its credentials have changed.
// The local deletion commits without contacting the provider. No remote revocation is implied.
func Disconnect(app core.App, actor Actor, id string) error {
	return app.RunInTransaction(func(tx core.App) error {
		if err := actor.check(tx); err != nil {
			return err
		}
		account, err := tx.FindRecordById(Accounts, id)
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		if err != nil {
			return err
		}
		if account.GetString("user") != actor.UserID {
			return sql.ErrNoRows
		}
		if err := deleteRequests(tx, actor.UserID, account.GetString("issuer")); err != nil {
			return err
		}
		return tx.Delete(account)
	})
}

func deleteRequests(app core.App, userID, issuer string) error {
	rows, err := app.FindRecordsByFilter(Requests, "user = {:user} && issuer = {:issuer}", "", 0, 0, dbx.Params{"user": userID, "issuer": issuer})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := app.Delete(row); err != nil {
			return err
		}
	}
	return nil
}
