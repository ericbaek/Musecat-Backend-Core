package usernotification

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

const AimeKind = "aime_recovery"
const AimeState = "aime_status"
const AimeProbes = "aime_probe"
const AimeSubscriptions = "aime_recovery_subscription"
const aimeStateID = "aimestatus00001"

// EnsureAimeSchema is used by fresh Core bootstrap and Full's forward migration.
func EnsureAimeSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		user, err := tx.FindCollectionByNameOrId("user")
		if err != nil {
			return err
		}
		definitions := []struct {
			name   string
			fields []core.Field
		}{
			{AimeProbes, []core.Field{&core.DateField{Name: "checked_at", Required: true}, &core.TextField{Name: "status", Required: true, Max: 20}, &core.NumberField{Name: "http_status", OnlyInt: true}}},
			{AimeState, []core.Field{&core.TextField{Name: "status", Max: 20}, &core.NumberField{Name: "http_status", OnlyInt: true}, &core.DateField{Name: "checked_at"}, &core.TextField{Name: "incident", Max: 40}, &core.BoolField{Name: "recovered"}, &core.NumberField{Name: "confirmations", OnlyInt: true}, &core.DateField{Name: "confirmation_at"}}},
			{AimeSubscriptions, []core.Field{&core.RelationField{Name: "user", CollectionId: user.Id, Required: true, MaxSelect: 1, CascadeDelete: true}, &core.TextField{Name: "incident", Required: true, Max: 40}}},
		}
		for _, d := range definitions {
			col, err := tx.FindCollectionByNameOrId(d.name)
			if errors.Is(err, sql.ErrNoRows) {
				col = core.NewBaseCollection(d.name)
			} else if err != nil {
				return err
			} else if !col.IsBase() || col.System {
				return fmt.Errorf("incompatible %s", d.name)
			}
			for _, field := range d.fields {
				old := col.Fields.GetByName(field.GetName())
				if old == nil {
					col.Fields.Add(field)
				} else if old.Type() != field.Type() {
					return fmt.Errorf("incompatible %s.%s", d.name, field.GetName())
				}
				if want, ok := field.(*core.RelationField); ok && old != nil {
					got := old.(*core.RelationField)
					if got.CollectionId != want.CollectionId || !got.Required || !got.CascadeDelete || got.MaxSelect != 1 {
						return fmt.Errorf("incompatible %s.user", d.name)
					}
				}
			}
			col.ListRule = nil
			col.ViewRule = nil
			col.CreateRule = nil
			col.UpdateRule = nil
			col.DeleteRule = nil
			if d.name == AimeProbes {
				col.AddIndex("idx_aime_probe_checked_at", false, "checked_at", "")
			}
			if d.name == AimeSubscriptions {
				col.AddIndex("idx_aime_subscriber", true, "user", "")
			}
			if err := tx.Save(col); err != nil {
				return err
			}
		}
		return nil
	})
}

type AimeProbe struct {
	CheckedAt  string `json:"checkedAt"`
	Status     string `json:"status"`
	HTTPStatus int    `json:"httpStatus"`
}

type AimeStatus struct {
	History       []AimeProbe `json:"history"`
	Status        string      `json:"status"`
	HTTPStatus    int         `json:"httpStatus"`
	CheckedAt     string      `json:"checkedAt"`
	Subscribed    bool        `json:"subscribed"`
	Completed     bool        `json:"completed"`
	Confirmations int         `json:"confirmations"`
}

func GetAimeStatus(app core.App, userID string) (AimeStatus, error) {
	result := AimeStatus{Status: "unknown", History: []AimeProbe{}}
	row, err := app.FindRecordById(AimeState, aimeStateID)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.Status = row.GetString("status")
	result.HTTPStatus = row.GetInt("http_status")
	result.CheckedAt = row.GetString("checked_at")
	result.Completed = row.GetBool("recovered")
	result.Confirmations = row.GetInt("confirmations")
	probes, err := app.FindRecordsByFilter(AimeProbes, "checked_at>={:cutoff}", "-checked_at,-id", 288, 0, dbx.Params{"cutoff": time.Now().UTC().Add(-24 * time.Hour).Format("2006-01-02 15:04:05.000Z")})
	if err != nil {
		return result, err
	}
	for i := len(probes) - 1; i >= 0; i-- {
		probe := probes[i]
		result.History = append(result.History, AimeProbe{CheckedAt: probe.GetString("checked_at"), Status: probe.GetString("status"), HTTPStatus: probe.GetInt("http_status")})
	}
	if userID != "" {
		rows, err := app.FindRecordsByFilter(AimeSubscriptions, "user={:user} && incident={:incident}", "", 1, 0, dbx.Params{"user": userID, "incident": row.GetString("incident")})
		if err != nil {
			return result, err
		}
		result.Subscribed = len(rows) > 0
	}
	return result, nil
}

var ErrAimeNotBlocked = errors.New("Aime is not currently blocked")

// A subscription is explicit, account scoped and consumed after this recovery.
func SetAimeSubscription(app core.App, userID string, enabled bool) error {
	return app.RunInTransaction(func(tx core.App) error {
		rows, err := tx.FindRecordsByFilter(AimeSubscriptions, "user={:user}", "", 1, 0, dbx.Params{"user": userID})
		if err != nil {
			return err
		}
		if !enabled {
			if len(rows) > 0 {
				return tx.Delete(rows[0])
			}
			return nil
		}
		state, err := tx.FindRecordById(AimeState, aimeStateID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAimeNotBlocked
		}
		if err != nil {
			return err
		}
		if state.GetString("incident") == "" || state.GetBool("recovered") {
			return ErrAimeNotBlocked
		}
		col, err := tx.FindCollectionByNameOrId(AimeSubscriptions)
		if err != nil {
			return err
		}
		row := core.NewRecord(col)
		if len(rows) > 0 {
			row = rows[0]
		}
		row.Set("user", userID)
		row.Set("incident", state.GetString("incident"))
		return tx.Save(row)
	})
}

// ProbeAime makes a single shared, unauthenticated GET; no user credentials are used.
func ProbeAime(ctx context.Context, client *http.Client) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://my-aime.net/", nil)
	if err != nil {
		return 0, err
	}
	response, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	return response.StatusCode, nil
}

// RecordAimeProbe persists observations before queuing bounded, resumable fan-out.
// This one-off incident completes after three consecutive 2xx confirmations,
// separated by at least five minutes. Any failed observation resets the run.
func RecordAimeProbe(app core.App, status int, now time.Time) error {
	return app.RunInTransaction(func(tx core.App) error {
		row, err := tx.FindRecordById(AimeState, aimeStateID)
		if errors.Is(err, sql.ErrNoRows) {
			col, e := tx.FindCollectionByNameOrId(AimeState)
			if e != nil {
				return e
			}
			row = core.NewRecord(col)
			row.Id = aimeStateID
		} else if err != nil {
			return err
		}
		observed := "unknown"
		if status == 403 {
			observed = "blocked"
		} else if status >= 200 && status < 300 {
			observed = "available"
		}
		if !row.GetBool("recovered") {
			if status == 403 && row.GetString("incident") == "" {
				row.Set("incident", now.UTC().Format("20060102T150405.000000000"))
			}
			if observed != "available" {
				row.Set("confirmations", 0)
				row.Set("confirmation_at", now.UTC())
			} else if row.GetString("incident") != "" {
				last := row.GetDateTime("confirmation_at").Time()
				// A restarted process cannot turn rapid startup probes into
				// three confirmations. Each confirmation covers five minutes.
				if !last.IsZero() && !now.Before(last.Add(5*time.Minute)) {
					count := row.GetInt("confirmations")
					// An unobserved gap does not establish continuous recovery.
					if now.Sub(last) > 10*time.Minute {
						count = 0
					}
					count++
					row.Set("confirmations", count)
					row.Set("confirmation_at", now.UTC())
					if count >= 3 {
						row.Set("recovered", true)
					}
				}
			}
		}
		row.Set("status", observed)
		row.Set("http_status", status)
		row.Set("checked_at", now.UTC())
		if err := tx.Save(row); err != nil {
			return err
		}
		col, err := tx.FindCollectionByNameOrId(AimeProbes)
		if err != nil {
			return err
		}
		probe := core.NewRecord(col)
		probe.Set("checked_at", now.UTC())
		probe.Set("status", observed)
		probe.Set("http_status", status)
		if err := tx.Save(probe); err != nil {
			return err
		}
		expired, err := tx.FindRecordsByFilter(AimeProbes, "checked_at<{:cutoff}", "checked_at", 100, 0, dbx.Params{"cutoff": now.UTC().Add(-24 * time.Hour).Format("2006-01-02 15:04:05.000Z")})
		if err != nil {
			return err
		}
		for _, old := range expired {
			if err := tx.Delete(old); err != nil {
				return err
			}
		}
		return nil
	})
}

// DrainAimeRecoveries is resumable after this incident completes. Later outages
// never reopen the campaign or create another notification identity.
func DrainAimeRecoveries(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		state, err := tx.FindRecordById(AimeState, aimeStateID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if !state.GetBool("recovered") {
			return nil
		}
		rows, err := tx.FindRecordsByFilter(AimeSubscriptions, "", "id", 100, 0)
		if err != nil {
			return err
		}
		for _, sub := range rows {
			user, err := tx.FindRecordById("user", sub.GetString("user"))
			if err != nil {
				return err
			}
			if !user.GetBool("withdrawn") {
				event := "aime:" + sub.GetString("incident")
				existing, err := tx.FindRecordsByFilter(Inbox, "user={:user} && event_key={:event}", "", 1, 0, dbx.Params{"user": user.Id, "event": event})
				if err != nil {
					return err
				}
				if len(existing) == 0 {
					col, err := tx.FindCollectionByNameOrId(Inbox)
					if err != nil {
						return err
					}
					notice := core.NewRecord(col)
					notice.Set("user", user.Id)
					notice.Set("kind", AimeKind)
					notice.Set("event_key", event)
					if err := tx.Save(notice); err != nil {
						return err
					}
					if err := enqueueDevicesTx(tx, notice, user.Id); err != nil {
						return err
					}
				}
			}
			if err := tx.Delete(sub); err != nil {
				return err
			}
		}
		return nil
	})
}

func AimeItem(row *core.Record, locale string) map[string]any {
	title, body := "Aime 접속이 복구되었습니다", "my-aime.net에 다시 접속할 수 있습니다."
	if locale == "en" {
		title, body = "Aime access is restored", "my-aime.net is available again."
	} else if locale == "ja" {
		title, body = "Aimeへの接続が復旧しました", "my-aime.netに再びアクセスできます。"
	}
	return map[string]any{"id": row.Id, "kind": AimeKind, "read": row.GetBool("read"), "created": row.GetString("created"), "title": title, "message": body, "arcadeName": "", "gameName": "", "reaction": "", "url": "/"}
}
