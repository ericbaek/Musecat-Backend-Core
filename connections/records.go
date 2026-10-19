package connections

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/oauth2"
)

// Records is the cache of partner game-record envelopes (spec section 3.4).
const Records = "external_service_record"

// RecordSpec is the only envelope version Musecat accepts.
const RecordSpec = "musecat-records/v1"

const (
	RecordOK          = "ok"          // envelope cached; partner visibility public or private is in the payload
	RecordPrivate     = "private"     // partner reported records_private; nothing is displayed
	RecordUnavailable = "unavailable" // fetch failed; a previous payload may remain but is not displayed
	RecordUnsupported = "unsupported" // provider has no records_url
)

const (
	recordMaxBytes   = 256 << 10
	recordFreshFor   = 24 * time.Hour
	recordMinRefresh = time.Minute
)

var recordFieldTypes = []string{"integer", "decimal", "string", "percent", "datetime", "date", "rank"}

type RecordLabel struct {
	Ko string `json:"ko,omitempty"`
	En string `json:"en"`
	Ja string `json:"ja,omitempty"`
}
type RecordField struct {
	Key       string      `json:"key"`
	Label     RecordLabel `json:"label"`
	Type      string      `json:"type"`
	Value     any         `json:"value"`
	Precision *int        `json:"precision,omitempty"`
}
type RecordColumn struct {
	Key   string      `json:"key"`
	Label RecordLabel `json:"label"`
	Type  string      `json:"type"`
}
type RecordSection struct {
	Key     string         `json:"key"`
	Label   RecordLabel    `json:"label"`
	Columns []RecordColumn `json:"columns"`
	Rows    [][]any        `json:"rows"`
	Total   *int           `json:"total,omitempty"`
	MoreURL string         `json:"more_url,omitempty"`
}
type RecordPlayer struct {
	Name  string `json:"name,omitempty"`
	Title string `json:"title,omitempty"`
	Level *int   `json:"level,omitempty"`
	Team  string `json:"team,omitempty"`
}
type RecordSource struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// RecordPlay uses stable provider-local IDs; metrics retain their game-specific units.
type RecordPlay struct {
	ID       string        `json:"id,omitempty"`
	PlayedAt time.Time     `json:"played_at"`
	Song     RecordSong    `json:"song"`
	Chart    RecordChart   `json:"chart"`
	Fields   []RecordField `json:"fields"`
}
type RecordSong struct {
	ID     string `json:"id,omitempty"`
	Title  string `json:"title"`
	Artist string `json:"artist,omitempty"`
}
type RecordChart struct {
	ID         string `json:"id,omitempty"`
	Difficulty string `json:"difficulty"`
	Level      string `json:"level,omitempty"`
	Mode       string `json:"mode,omitempty"`
}
type RecordRecentPlays struct {
	Plays   []RecordPlay `json:"plays"`
	Total   *int         `json:"total,omitempty"`
	MoreURL string       `json:"more_url,omitempty"`
}

// RecordSelection explains omitted groups or individual fields without implying zero.
type RecordSelection struct {
	Key    string `json:"key"`
	Status string `json:"status"`
}

// RecordEnvelope is the validated, size-bounded projection of a partner response.
type RecordEnvelope struct {
	Spec        string             `json:"spec"`
	GameSchema  string             `json:"game_schema,omitempty"`
	RecentPlays *RecordRecentPlays `json:"recent_plays,omitempty"`
	Selection   []RecordSelection  `json:"selection,omitempty"`
	Service     string             `json:"service"`
	Visibility  string             `json:"visibility"`
	UpdatedAt   time.Time          `json:"updated_at"`
	Source      RecordSource       `json:"source"`
	Player      *RecordPlayer      `json:"player,omitempty"`
	Fields      []RecordField      `json:"fields"`
	Sections    []RecordSection    `json:"sections"`
}

// RecordView is what owners and public readers receive. Sub is never included.
type RecordView struct {
	Connection string          `json:"connection"`
	Provider   string          `json:"provider"`
	Service    string          `json:"service"`
	Status     string          `json:"status"`
	Error      string          `json:"error,omitempty"`
	FetchedAt  *time.Time      `json:"fetched_at"`
	UpdatedAt  *time.Time      `json:"updated_at"`
	Payload    *RecordEnvelope `json:"payload"`
}

// PublicRecord is a record shown on a user's public profile.
type PublicRecord struct {
	Provider     string         `json:"provider"`
	ProviderName string         `json:"provider_name"`
	Service      string         `json:"service"`
	ServiceName  string         `json:"service_name"`
	FetchedAt    time.Time      `json:"fetched_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	Payload      RecordEnvelope `json:"payload"`
}

var errEnvelope = errors.New("invalid record envelope")

func httpsLink(raw string) bool {
	if raw == "" {
		return true
	}
	if len(raw) > 2048 {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil
}

func validLabel(l RecordLabel) bool {
	return l.En != "" && len(l.En) <= 64 && len(l.Ko) <= 64 && len(l.Ja) <= 64
}

func validKey(k string) bool {
	return k != "" && len(k) <= 64 && !strings.ContainsAny(k, " \t\r\n")
}

func validValue(kind string, v any) bool {
	if v == nil {
		return true
	}
	switch kind {
	case "integer":
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		_, err := n.Int64()
		return err == nil
	case "decimal", "percent":
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		_, err := n.Float64()
		return err == nil
	case "string":
		s, ok := v.(string)
		return ok && len(s) <= 256
	case "datetime":
		s, ok := v.(string)
		if !ok {
			return false
		}
		_, err := time.Parse(time.RFC3339, s)
		return err == nil
	case "date":
		s, ok := v.(string)
		if !ok {
			return false
		}
		_, err := time.Parse("2006-01-02", s)
		return err == nil
	case "rank":
		switch t := v.(type) {
		case string:
			return len(t) <= 32
		case json.Number:
			_, err := t.Int64()
			return err == nil
		}
		return false
	}
	return false
}

func validRecordFields(fields []RecordField, limit int) bool {
	if len(fields) > limit {
		return false
	}
	keys := map[string]bool{}
	for _, f := range fields {
		if !validKey(f.Key) || keys[f.Key] || !validLabel(f.Label) || !slices.Contains(recordFieldTypes, f.Type) || !validValue(f.Type, f.Value) || (f.Precision != nil && (*f.Precision < 0 || *f.Precision > 6)) {
			return false
		}
		keys[f.Key] = true
	}
	return true
}

func validRecordExtensions(e RecordEnvelope) bool {
	if e.GameSchema != "" && !slices.Contains([]string{"musecat-game/maimai/v1", "musecat-game/chunithm/v1", "musecat-game/sdvx/v1", "musecat-game/iidx/v1", "musecat-game/nostalgia/v1"}, e.GameSchema) {
		return false
	}
	if e.GameSchema != "" && e.GameSchema != "musecat-game/"+e.Service+"/v1" {
		return false
	}
	if r := e.RecentPlays; r != nil {
		if len(r.Plays) > 50 || !httpsLink(r.MoreURL) || (r.Total != nil && *r.Total < len(r.Plays)) {
			return false
		}
		seen := map[string]bool{}
		var previous time.Time
		for _, p := range r.Plays {
			if len(p.ID) > 128 || (p.ID != "" && seen[p.ID]) || p.PlayedAt.IsZero() || p.PlayedAt.After(time.Now().Add(time.Hour)) || (!previous.IsZero() && p.PlayedAt.After(previous)) {
				return false
			}
			if p.ID != "" {
				seen[p.ID] = true
			}
			previous = p.PlayedAt
			if len(p.Song.ID) > 128 || p.Song.Title == "" || len(p.Song.Title) > 256 || len(p.Song.Artist) > 256 || len(p.Chart.ID) > 128 || p.Chart.Difficulty == "" || len(p.Chart.Difficulty) > 64 || len(p.Chart.Level) > 32 || len(p.Chart.Mode) > 64 || !validRecordFields(p.Fields, 16) {
				return false
			}
		}
	}
	if len(e.Selection) > 64 {
		return false
	}
	seen := map[string]bool{}
	for _, item := range e.Selection {
		if seen[item.Key] || !slices.Contains([]string{"denied", "no_data", "unsupported"}, item.Status) {
			return false
		}
		seen[item.Key] = true
		switch item.Key {
		case "player":
			if e.Player != nil {
				return false
			}
		case "fields":
			if len(e.Fields) > 0 {
				return false
			}
		case "sections":
			if len(e.Sections) > 0 {
				return false
			}
		case "recent_plays":
			if e.RecentPlays != nil {
				return false
			}
		default:
			group, key, ok := strings.Cut(item.Key, ".")
			if !ok || !validKey(key) {
				return false
			}
			switch group {
			case "fields":
				for _, f := range e.Fields {
					if f.Key == key {
						return false
					}
				}
			case "sections":
				for _, section := range e.Sections {
					if section.Key == key {
						return false
					}
				}
			default:
				return false
			}
		}
	}
	return true
}

// ParseEnvelope validates a partner response against the spec. The service must match
// the requested one and sub must equal the connection's verified subject; sub is then
// dropped so it never reaches storage or clients.
func ParseEnvelope(data []byte, service, subject string) (RecordEnvelope, error) {
	if len(data) > recordMaxBytes {
		return RecordEnvelope{}, errEnvelope
	}
	var raw struct {
		RecordEnvelope
		Sub       string `json:"sub"`
		UpdatedAt string `json:"updated_at"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return RecordEnvelope{}, errEnvelope
	}
	if decoder.Decode(new(any)) != io.EOF {
		return RecordEnvelope{}, errEnvelope
	}
	e := raw.RecordEnvelope
	if raw.Spec != RecordSpec || raw.Service != service || raw.Sub != subject || (raw.Visibility != "public" && raw.Visibility != "private") {
		return RecordEnvelope{}, errEnvelope
	}
	updated, err := time.Parse(time.RFC3339, raw.UpdatedAt)
	if err != nil || updated.After(time.Now().Add(time.Hour)) {
		return RecordEnvelope{}, errEnvelope
	}
	e.UpdatedAt = updated.UTC()
	if e.Source.Name == "" || len(e.Source.Name) > 128 || !httpsLink(e.Source.URL) {
		return RecordEnvelope{}, errEnvelope
	}
	if e.Player != nil {
		if len(e.Player.Name) > 64 || len(e.Player.Title) > 128 || len(e.Player.Team) > 64 || (e.Player.Level != nil && (*e.Player.Level < 0 || *e.Player.Level > 1_000_000)) {
			return RecordEnvelope{}, errEnvelope
		}
	}
	if !validRecordFields(e.Fields, 24) || len(e.Sections) > 4 || !validRecordExtensions(e) {
		return RecordEnvelope{}, errEnvelope
	}
	sections := map[string]bool{}
	for _, s := range e.Sections {
		if !validKey(s.Key) || sections[s.Key] || !validLabel(s.Label) || len(s.Columns) == 0 || len(s.Columns) > 8 || len(s.Rows) > 50 || !httpsLink(s.MoreURL) || (s.Total != nil && *s.Total < 0) {
			return RecordEnvelope{}, errEnvelope
		}
		sections[s.Key] = true
		cols := map[string]bool{}
		for _, c := range s.Columns {
			if !validKey(c.Key) || cols[c.Key] || !validLabel(c.Label) || !slices.Contains(recordFieldTypes, c.Type) {
				return RecordEnvelope{}, errEnvelope
			}
			cols[c.Key] = true
		}
		for _, row := range s.Rows {
			if len(row) != len(s.Columns) {
				return RecordEnvelope{}, errEnvelope
			}
			for i, cell := range row {
				if !validValue(s.Columns[i].Type, cell) {
					return RecordEnvelope{}, errEnvelope
				}
			}
		}
	}
	if e.RecentPlays != nil {
		if e.RecentPlays.Plays == nil {
			e.RecentPlays.Plays = []RecordPlay{}
		}
		for i := range e.RecentPlays.Plays {
			if e.RecentPlays.Plays[i].Fields == nil {
				e.RecentPlays.Plays[i].Fields = []RecordField{}
			}
		}
	}
	if e.Fields == nil {
		e.Fields = []RecordField{}
	}
	if e.Sections == nil {
		e.Sections = []RecordSection{}
	}
	return e, nil
}

// EnsureRecordSchema adds the locked record cache. Core bootstraps fresh databases;
// Full owns the guarded existing-database migration.
func EnsureRecordSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		account, err := tx.FindCollectionByNameOrId(Accounts)
		if err != nil {
			return err
		}
		c := core.NewBaseCollection(Records)
		c.Fields.Add(
			&core.RelationField{Name: "connection", CollectionId: account.Id, Required: true, MaxSelect: 1, CascadeDelete: true},
			&core.TextField{Name: "service", Required: true, Max: 128},
			&core.SelectField{Name: "status", Required: true, MaxSelect: 1, Values: []string{RecordOK, RecordPrivate, RecordUnavailable, RecordUnsupported}},
			&core.TextField{Name: "error", Max: 64},
			&core.JSONField{Name: "payload", MaxSize: recordMaxBytes},
			&core.DateField{Name: "source_updated"},
			&core.DateField{Name: "fetched", Required: true},
			&core.DateField{Name: "retry_at"},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		c.AddIndex("idx_external_record_connection_service", true, "connection, service", "")
		c.AddIndex("idx_external_record_fetched", false, "status, fetched", "")
		return EnsureLockedCollection(tx, c)
	})
}

func recordRow(app core.App, connectionID, service string) (*core.Record, error) {
	r, err := app.FindFirstRecordByFilter(Records, "connection = {:c} && service = {:s}", dbx.Params{"c": connectionID, "s": service})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

func viewOf(account, r *core.Record, service string) RecordView {
	v := RecordView{Connection: account.Id, Provider: account.GetString("provider"), Service: service, Status: RecordUnavailable, Error: "never_fetched"}
	if r == nil {
		return v
	}
	v.Status, v.Error = r.GetString("status"), r.GetString("error")
	if t := r.GetDateTime("fetched").Time(); !t.IsZero() {
		t = t.UTC()
		v.FetchedAt = &t
	}
	if t := r.GetDateTime("source_updated").Time(); !t.IsZero() {
		t = t.UTC()
		v.UpdatedAt = &t
	}
	if v.Status == RecordOK {
		var e RecordEnvelope
		if r.UnmarshalJSONField("payload", &e) == nil && e.Spec == RecordSpec {
			v.Payload = &e
		}
	}
	return v
}

// ReadRecord returns the owner's cached view, fetching when the cache is missing or
// older than 24 hours. refresh forces a fetch unless one ran within the last minute.
func (m *Manager) ReadRecord(ctx context.Context, app core.App, actor Actor, connectionID, service string, refresh bool) (RecordView, error) {
	if err := actor.check(app); err != nil {
		return RecordView{}, err
	}
	account, err := app.FindRecordById(Accounts, connectionID)
	if err != nil || account.GetString("user") != actor.UserID {
		return RecordView{}, sql.ErrNoRows
	}
	if _, err := app.FindFirstRecordByFilter(Services, "connection = {:c} && service = {:s}", dbx.Params{"c": account.Id, "s": service}); err != nil {
		return RecordView{}, sql.ErrNoRows
	}
	existing, err := recordRow(app, account.Id, service)
	if err != nil {
		return RecordView{}, err
	}
	invalidated := existing != nil && existing.GetString("error") == "privacy_invalidated"
	stale := invalidated || existing == nil || time.Since(existing.GetDateTime("fetched").Time()) > recordFreshFor
	recent := !invalidated && existing != nil && time.Since(existing.GetDateTime("fetched").Time()) < recordMinRefresh
	backoff := existing != nil && existing.GetDateTime("retry_at").Time().After(time.Now())
	if !backoff && (stale || (refresh && !recent)) {
		if m == nil {
			return viewOf(account, existing, service), nil
		}
		row, err := m.fetch(ctx, app, account, service)
		if err != nil {
			return RecordView{}, err
		}
		return viewOf(account, row, service), nil
	}
	return viewOf(account, existing, service), nil
}

// SetServiceVisibility is the Musecat-side publication switch for one game service.
// It never changes partner data; public display additionally requires the partner's
// envelope to report public visibility.
func SetServiceVisibility(app core.App, actor Actor, connectionID, service, visibility string) (ServiceConnection, error) {
	if visibility != "public" && visibility != "private" {
		return ServiceConnection{}, ErrInvalid
	}
	var out ServiceConnection
	err := app.RunInTransaction(func(tx core.App) error {
		if err := actor.check(tx); err != nil {
			return err
		}
		account, err := tx.FindRecordById(Accounts, connectionID)
		if err != nil || account.GetString("user") != actor.UserID {
			return sql.ErrNoRows
		}
		grant, err := tx.FindFirstRecordByFilter(Services, "connection = {:c} && service = {:s}", dbx.Params{"c": account.Id, "s": service})
		if err != nil {
			return sql.ErrNoRows
		}
		grant.Set("visibility", visibility)
		if err := tx.Save(grant); err != nil {
			return err
		}
		out = ServiceConnection{Service: service, Visibility: visibility, Fields: []string{}}
		_ = grant.UnmarshalJSONField("fields", &out.Fields)
		return nil
	})
	return out, err
}

// PublicRecords lists records a user has published and the partner reports as public.
func (m *Manager) PublicRecords(app core.App, userID string) ([]PublicRecord, error) {
	accounts, err := app.FindRecordsByFilter(Accounts, "user = {:u}", "created,id", 0, 0, dbx.Params{"u": userID})
	if err != nil {
		return nil, err
	}
	out := []PublicRecord{}
	for _, account := range accounts {
		grants, err := app.FindRecordsByFilter(Services, "connection = {:c} && visibility = 'public'", "service", 0, 0, dbx.Params{"c": account.Id})
		if err != nil {
			return nil, err
		}
		providerName, serviceNames := account.GetString("provider"), map[string]string{}
		if m == nil {
			continue
		}
		current, enabled := m.lookup(account.GetString("provider"))
		if !enabled || current.Issuer != account.GetString("issuer") {
			continue
		}
		providerName = current.Name
		for _, service := range current.Services {
			serviceNames[service.ID] = service.Name
		}

		for _, grant := range grants {
			service := grant.GetString("service")
			if !slices.ContainsFunc(current.Services, func(s Service) bool { return s.ID == service }) {
				continue
			}
			row, err := recordRow(app, account.Id, service)
			if err != nil {
				return nil, err
			}
			if row == nil || row.GetString("status") != RecordOK || time.Since(row.GetDateTime("fetched").Time()) >= recordFreshFor {
				continue
			}
			var e RecordEnvelope
			if row.UnmarshalJSONField("payload", &e) != nil || e.Spec != RecordSpec || e.Visibility != "public" {
				continue
			}
			name := serviceNames[service]
			if name == "" {
				name = service
			}
			out = append(out, PublicRecord{Provider: account.GetString("provider"), ProviderName: providerName, Service: service, ServiceName: name, FetchedAt: row.GetDateTime("fetched").Time().UTC(), UpdatedAt: row.GetDateTime("source_updated").Time().UTC(), Payload: e})
		}
	}
	return out, nil
}

// RefreshStalePublicRecords re-fetches up to limit published records older than 24 hours.
// Executables schedule it; failures mark the row unavailable without removing the grant.
func (m *Manager) RefreshStalePublicRecords(ctx context.Context, app core.App, limit int) error {
	if m == nil {
		return nil
	}

	limit = min(max(limit, 1), 100)
	now := time.Now().UTC()
	var rows []struct {
		Connection string `db:"connection"`
		Service    string `db:"service"`
	}
	err := app.DB().NewQuery(`SELECT s.connection, s.service
 FROM external_service_connection s
 LEFT JOIN external_service_record r ON r.connection=s.connection AND r.service=s.service
 WHERE s.visibility='public' AND (r.id IS NULL OR r.error='privacy_invalidated' OR
 (r.fetched <= {:cutoff} AND r.status != 'unsupported' AND (r.retry_at='' OR r.retry_at <= {:now})))
 ORDER BY COALESCE(r.fetched, ''), s.id LIMIT {:limit}`).Bind(dbx.Params{
		"cutoff": now.Add(-recordFreshFor).Format("2006-01-02 15:04:05.000Z"),
		"now":    now.Format("2006-01-02 15:04:05.000Z"), "limit": limit,
	}).All(&rows)
	if err != nil {
		return err
	}
	for _, row := range rows {
		account, err := app.FindRecordById(Accounts, row.Connection)
		if err != nil {
			continue
		}
		_, _ = m.fetch(ctx, app, account, row.Service)
	}

	return nil
}

func (m *Manager) store(app core.App, account *core.Record, service, status, errCode string, payload *RecordEnvelope, started time.Time, retryAt ...time.Time) (*core.Record, error) {
	var out *core.Record
	err := app.RunInTransaction(func(tx core.App) error {
		current, err := tx.FindRecordById(Accounts, account.Id)
		if err != nil {
			return err
		}
		if current.GetString("authorization_generation") != account.GetString("authorization_generation") || current.GetString("credentials") != account.GetString("credentials") {
			return ErrInvalid
		}
		if _, err := tx.FindFirstRecordByFilter(Services, "connection={:c} && service={:s}", dbx.Params{"c": account.Id, "s": service}); err != nil {
			return err
		}
		row, err := recordRow(tx, account.Id, service)
		if err != nil {
			return err
		}
		// A privacy notification received during an upstream fetch wins over that stale response.
		if row != nil && !row.GetDateTime("fetched").Time().Before(started) {
			out = row
			return nil
		}
		if row == nil {
			c, err := tx.FindCollectionByNameOrId(Records)
			if err != nil {
				return err
			}
			row = core.NewRecord(c)
			row.Set("connection", account.Id)
			row.Set("service", service)
		}
		row.Set("status", status)
		row.Set("error", errCode)
		row.Set("fetched", time.Now().UTC())
		row.Set("retry_at", "")
		if len(retryAt) > 0 {
			row.Set("retry_at", retryAt[0])
		}
		if payload != nil {
			row.Set("payload", payload)
			row.Set("source_updated", payload.UpdatedAt)
		} else if status == RecordPrivate {
			row.Set("payload", nil)
		}
		if err := tx.Save(row); err != nil {
			return err
		}
		out = row
		return nil
	})
	return out, err
}

// InvalidateRecords removes previously shared data for this provider's verified subject.
// Unknown subjects are deliberately indistinguishable. The next read fetches a fresh selection.
func (m *Manager) InvalidateRecords(app core.App, provider, subject, service string) error {
	if m == nil {
		return ErrUnavailable
	}
	p, ok := m.lookup(provider)
	if !ok || subject == "" || len(subject) > 256 || !slices.ContainsFunc(p.Services, func(s Service) bool { return s.ID == service }) {
		return ErrInvalid
	}
	return app.RunInTransaction(func(tx core.App) error {
		accounts, err := tx.FindRecordsByFilter(Accounts, "provider={:p} && issuer={:i} && subject={:s}", "", 0, 0, dbx.Params{"p": provider, "i": p.Issuer, "s": subject})
		if err != nil {
			return err
		}
		for _, account := range accounts {
			if _, err := tx.FindFirstRecordByFilter(Services, "connection={:c} && service={:s}", dbx.Params{"c": account.Id, "s": service}); errors.Is(err, sql.ErrNoRows) {
				continue
			} else if err != nil {
				return err
			}
			row, err := recordRow(tx, account.Id, service)
			if err != nil {
				return err
			}
			if row == nil {
				c, err := tx.FindCollectionByNameOrId(Records)
				if err != nil {
					return err
				}
				row = core.NewRecord(c)
				row.Set("connection", account.Id)
				row.Set("service", service)
			}
			row.Set("status", RecordPrivate)
			row.Set("error", "privacy_invalidated")
			row.Set("payload", nil)
			row.Set("source_updated", "")
			row.Set("retry_at", "")
			row.Set("fetched", time.Now().UTC())
			if err := tx.Save(row); err != nil {
				return err
			}
		}
		return nil
	})
}

// fetch performs the partner request outside any transaction and persists the outcome.
// The access token is refreshed once when expired; a refreshed family is re-sealed.
func (m *Manager) fetch(ctx context.Context, app core.App, account *core.Record, service string) (*core.Record, error) {
	started := time.Now().UTC().Truncate(time.Millisecond)
	p, ok := m.lookup(account.GetString("provider"))
	if !ok || p.Issuer != account.GetString("issuer") {
		return m.store(app, account, service, RecordUnavailable, "provider_disabled", nil, started)
	}
	if p.RecordsURL == "" {
		return m.store(app, account, service, RecordUnsupported, "", nil, started)
	}
	if !slices.ContainsFunc(p.Services, func(s Service) bool { return s.ID == service }) {
		return m.store(app, account, service, RecordUnavailable, "unknown_service", nil, started)
	}
	var tokens credentials
	if err := m.open(account.GetString("credentials"), "account:"+account.Id, &tokens); err != nil || tokens.AccessToken == "" {
		return m.store(app, account, service, RecordUnavailable, "credentials", nil, started)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, oauth2.HTTPClient, m.client)
	refreshed := false
	refresh := func() bool {
		if tokens.RefreshToken == "" || refreshed {
			return false
		}
		refreshed = true
		next, err := p.oauth(nil).TokenSource(ctx, &oauth2.Token{AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken, Expiry: time.Now().Add(-time.Minute)}).Token()
		if err != nil || next.AccessToken == "" {
			return false
		}
		tokens = credentials{next.AccessToken, next.RefreshToken, next.Expiry}
		sealed, err := m.seal(tokens, "account:"+account.Id)
		if err != nil {
			return false
		}
		if err := app.RunInTransaction(func(tx core.App) error {
			current, err := tx.FindRecordById(Accounts, account.Id)
			if err != nil {
				return err
			}
			if current.GetString("credentials") != account.GetString("credentials") || current.GetString("authorization_generation") != account.GetString("authorization_generation") {
				return ErrInvalid
			}
			current.Set("credentials", sealed)
			return tx.Save(current)
		}); err != nil {
			return false
		}
		account.Set("credentials", sealed)
		return true
	}
	if !tokens.Expiry.IsZero() && !tokens.Expiry.After(time.Now().Add(30*time.Second)) && !refresh() {
		return m.store(app, account, service, RecordUnavailable, "token_refresh", nil, started)
	}
	for attempt := 0; attempt < 2; attempt++ {
		status, body, retryAfter, err := m.getRecords(ctx, p.RecordsURL+"/"+url.PathEscape(service), tokens.AccessToken)
		if err != nil {
			return m.store(app, account, service, RecordUnavailable, "network", nil, started)
		}
		switch {
		case status == http.StatusOK:
			envelope, err := ParseEnvelope(body, service, account.GetString("subject"))
			if err != nil {
				return m.store(app, account, service, RecordUnavailable, "invalid_envelope", nil, started)
			}
			if envelope.Visibility == "private" {
				return m.store(app, account, service, RecordPrivate, "", nil, started)
			}
			return m.store(app, account, service, RecordOK, "", &envelope, started)
		case status == http.StatusUnauthorized && attempt == 0 && refresh():
			continue
		case status == http.StatusUnauthorized:
			return m.store(app, account, service, RecordUnavailable, "unauthorized", nil, started)
		case status == http.StatusForbidden:
			var payload struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal(body, &payload)
			if payload.Error == "records_private" {
				return m.store(app, account, service, RecordPrivate, "", nil, started)
			}
			return m.store(app, account, service, RecordUnavailable, "forbidden", nil, started)
		case status == http.StatusNotFound:
			return m.store(app, account, service, RecordUnavailable, "not_found", nil, started)
		case status == http.StatusTooManyRequests:
			return m.store(app, account, service, RecordUnavailable, "rate_limited", nil, started, recordRetryAt(retryAfter, time.Now()))
		default:
			return m.store(app, account, service, RecordUnavailable, fmt.Sprintf("upstream_%d", status), nil, started)
		}
	}
	return m.store(app, account, service, RecordUnavailable, "unauthorized", nil, started)
}

func (m *Manager) getRecords(ctx context.Context, target, token string) (int, []byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	res, err := m.client.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, recordMaxBytes+1))
	if err != nil {
		return 0, nil, "", err
	}
	return res.StatusCode, body, res.Header.Get("Retry-After"), nil
}

// RegisterRecordRefreshCron refreshes published stale records hourly in bounded batches.
func (m *Manager) RegisterRecordRefreshCron(app core.App) {
	if m == nil {
		return
	}
	app.Cron().MustAdd("external_service_record_refresh", "17 * * * *", func() {
		if err := m.RefreshStalePublicRecords(context.Background(), app, 50); err != nil {
			app.Logger().Error("Failed to refresh external service records")
		}
	})
}

// Invalid Retry-After values use the normal minimum interval. Valid seconds are
// bounded to avoid duration overflow; requests never sleep while holding a handler.
func recordRetryAt(value string, now time.Time) time.Time {
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds < 0 {
		seconds = 60
	}
	if seconds > 31536000 {
		seconds = 31536000
	}
	return now.UTC().Add(time.Duration(max(seconds, 60)) * time.Second)
}

// EnsureRecordRetryColumn adds durable upstream backoff on a locked record cache.
func EnsureRecordRetryColumn(app core.App) error {
	c, err := app.FindCollectionByNameOrId(Records)
	if err != nil {
		return err
	}
	if !c.IsBase() || c.System || c.ListRule != nil || c.ViewRule != nil || c.CreateRule != nil || c.UpdateRule != nil || c.DeleteRule != nil {
		return fmt.Errorf("%s must be locked", Records)
	}
	if f := c.Fields.GetByName("retry_at"); f != nil {
		if _, ok := f.(*core.DateField); !ok {
			return fmt.Errorf("retry_at must be a date")
		}
		return nil
	}
	c.Fields.Add(&core.DateField{Name: "retry_at"})
	return app.Save(c)
}
