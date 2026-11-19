// Package arcadearea stores each arcade's admin area and Passport city.
//
// The values are derived from the arcade's current basic location and its
// country through the embedded geo.Areas catalog. They live on `arcade` (not
// on each `arcade_basic` revision) so a rollback or bot edit recomputes them
// and no history row is written for a derived value. Classification never
// blocks a write: failures are logged and stored as unmapped.
//
// Each row records the dataset version it was classified with
// (area_version). When a release ships a new dataset, RecomputeStale brings
// older rows up to date in small batches after startup.
package arcadearea

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/types"

	"github.com/ericbaek/musecat-backend-core/geo"
)

const (
	collectionArcade      = "arcade"
	collectionArcadeBasic = "arcade_basic"

	FieldAdmin1    = "admin1"
	FieldAdmin2    = "admin2"
	FieldAdmin2Nm  = "admin2_name"
	FieldCity      = "city"
	FieldStatus    = "area_status"
	FieldManual    = "area_manual"
	FieldMismatch  = "area_mismatch"
	FieldVersion   = "area_version"
	FieldCheckedAt = "area_checked_at"
)

// Statuses accepted by area_status, in addition to the empty never-checked value.
var Statuses = []string{geo.AreaResolved, geo.AreaNear, geo.AreaEstimated, geo.AreaAmbiguous, geo.AreaUnmapped}

// EnsureSchema adds the derived area fields to `arcade`. Existing incompatible
// fields fail closed. It changes schema only; values come from Recompute.
func EnsureSchema(app core.App) error {
	c, err := app.FindCollectionByNameOrId(collectionArcade)
	if err != nil {
		return err
	}
	changed := false
	text := func(name, pattern string, max int) error {
		switch field := c.Fields.GetByName(name).(type) {
		case nil:
			c.Fields.Add(&core.TextField{Name: name, Pattern: pattern, Max: max})
			changed = true
		case *core.TextField:
			if field.Required || field.Pattern != pattern {
				return fmt.Errorf("incompatible arcade.%s", name)
			}
		default:
			return fmt.Errorf("incompatible arcade.%s", name)
		}
		return nil
	}
	for _, spec := range []struct {
		name, pattern string
		max           int
	}{
		{FieldAdmin1, `^[A-Z]{2}(-[A-Za-z0-9-]{1,12})?$`, 16},
		{FieldAdmin2, "", 64},
		{FieldAdmin2Nm, "", 200},
		{FieldCity, "", 200},
		{FieldVersion, "", 64},
	} {
		if err := text(spec.name, spec.pattern, spec.max); err != nil {
			return err
		}
	}
	switch field := c.Fields.GetByName(FieldStatus).(type) {
	case nil:
		c.Fields.Add(&core.SelectField{Name: FieldStatus, Values: Statuses, MaxSelect: 1})
		changed = true
	case *core.SelectField:
		for _, value := range Statuses {
			if !containsString(field.Values, value) {
				return fmt.Errorf("incompatible arcade.%s", FieldStatus)
			}
		}
	default:
		return fmt.Errorf("incompatible arcade.%s", FieldStatus)
	}
	for _, name := range []string{FieldManual, FieldMismatch} {
		switch c.Fields.GetByName(name).(type) {
		case nil:
			c.Fields.Add(&core.BoolField{Name: name})
			changed = true
		case *core.BoolField:
		default:
			return fmt.Errorf("incompatible arcade.%s", name)
		}
	}
	switch c.Fields.GetByName(FieldCheckedAt).(type) {
	case nil:
		c.Fields.Add(&core.DateField{Name: FieldCheckedAt})
		changed = true
	case *core.DateField:
	default:
		return fmt.Errorf("incompatible arcade.%s", FieldCheckedAt)
	}
	for _, index := range []struct{ name, columns string }{
		{"idx_arcade_admin1", "`" + FieldAdmin1 + "`"},
		{"idx_arcade_city", "`" + FieldCity + "`"},
		{"idx_arcade_area_status", "`" + FieldStatus + "`"},
		{"idx_arcade_area_version", "`" + FieldVersion + "`"},
	} {
		if c.GetIndex(index.name) == "" {
			c.AddIndex(index.name, false, index.columns, "")
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return app.Save(c)
}

func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// Assignment is the stored area of one arcade.
type Assignment struct {
	Admin1     string `json:"admin1"`
	Admin2     string `json:"admin2"`
	Admin2Name string `json:"admin2_name"`
	City       string `json:"city"`
	Status     string `json:"area_status"`
	// Mismatch flags an address that names another admin-1 of the country.
	Mismatch bool `json:"area_mismatch"`
	// Version is the dataset the values came from.
	Version string `json:"-"`
}

// FromRecord reads the stored assignment.
func FromRecord(arcade *core.Record) Assignment {
	return Assignment{
		Admin1:     arcade.GetString(FieldAdmin1),
		Admin2:     arcade.GetString(FieldAdmin2),
		Admin2Name: arcade.GetString(FieldAdmin2Nm),
		City:       arcade.GetString(FieldCity),
		Status:     arcade.GetString(FieldStatus),
		Mismatch:   arcade.GetBool(FieldMismatch),
		Version:    arcade.GetString(FieldVersion),
	}
}

// Location is what an assignment is derived from.
type Location struct {
	Country string
	Lat     float64
	Lon     float64
	Valid   bool // a basic revision with a non-zero coordinate
	Address string
}

// Derive computes the assignment for a location. A manual assignment keeps
// its admin-1 and city (a renamed city key follows the dataset) and only
// refreshes the address check and version.
func Derive(areas *geo.Areas, loc Location, manual *Assignment) Assignment {
	var next Assignment
	switch {
	case manual != nil:
		next = *manual
		if next.City != "" {
			if key, ok := areas.CurrentCityKey(next.City); ok {
				next.City = key
			}
		}
	case !loc.Valid:
		next = Assignment{Status: geo.AreaUnmapped}
	default:
		r := areas.Classify(loc.Country, loc.Lat, loc.Lon)
		next = Assignment{Admin1: r.Admin1, Admin2: r.Admin2, Admin2Name: r.Admin2Name, City: r.City, Status: r.Status}
	}
	next.Mismatch = areas.AddressMismatch(loc.Country, next.Admin1, loc.Address)
	next.Version = areas.Version()
	return next
}

// Classify computes the assignment for a country and coordinate.
func Classify(country string, lat, lon float64) (Assignment, error) {
	areas, err := geo.EmbeddedAreas()
	if err != nil {
		return Assignment{Status: geo.AreaUnmapped}, err
	}
	return Derive(areas, Location{Country: country, Lat: lat, Lon: lon, Valid: true}, nil), nil
}

// Apply sets the assignment on an arcade record without saving it.
func Apply(arcade *core.Record, a Assignment, now time.Time) {
	arcade.Set(FieldAdmin1, a.Admin1)
	arcade.Set(FieldAdmin2, a.Admin2)
	arcade.Set(FieldAdmin2Nm, a.Admin2Name)
	arcade.Set(FieldCity, a.City)
	arcade.Set(FieldStatus, a.Status)
	arcade.Set(FieldMismatch, a.Mismatch)
	arcade.Set(FieldVersion, a.Version)
	checked, _ := types.ParseDateTime(now.UTC())
	arcade.Set(FieldCheckedAt, checked)
}

// Recompute derives an arcade's assignment from its current basic revision
// and sets the fields on the record without saving. A missing basic or
// location yields unmapped; only a broken catalog returns an error, and the
// record still gets the unmapped status.
func Recompute(app core.App, arcade *core.Record, now time.Time) error {
	areas, err := geo.EmbeddedAreas()
	if err != nil {
		if !arcade.GetBool(FieldManual) {
			Apply(arcade, Assignment{Status: geo.AreaUnmapped}, now)
		}
		return err
	}
	loc := Location{Country: arcade.GetString("country")}
	if basicID := strings.TrimSpace(arcade.GetString("basic")); basicID != "" {
		basic, err := app.FindRecordById(collectionArcadeBasic, basicID)
		switch {
		case err == nil:
			point := basic.GetGeoPoint("location")
			loc.Lat, loc.Lon, loc.Valid = point.Lat, point.Lon, point.Lat != 0 || point.Lon != 0
			loc.Address = basic.GetString("address")
		case !errors.Is(err, sql.ErrNoRows):
			if !arcade.GetBool(FieldManual) {
				Apply(arcade, Assignment{Status: geo.AreaUnmapped}, now)
			}
			return err
		}
	}
	var manual *Assignment
	if arcade.GetBool(FieldManual) {
		current := FromRecord(arcade)
		manual = &current
	}
	Apply(arcade, Derive(areas, loc, manual), now)
	return nil
}

// needsRecompute reports whether a save moves the arcade to another location
// revision or country, or releases a manual assignment.
func needsRecompute(e *core.RecordEvent) bool {
	if e.Record.IsNew() {
		return true
	}
	original := e.Record.Original()
	return original.GetString("basic") != e.Record.GetString("basic") ||
		original.GetString("country") != e.Record.GetString("country") ||
		(original.GetBool(FieldManual) && !e.Record.GetBool(FieldManual))
}

// RegisterHooks recomputes the stored area in the same save whenever an
// arcade's basic revision or country changes: creation, user and bot edits,
// rollbacks and city corrections all go through `arcade.basic`.
func RegisterHooks(app core.App) {
	handler := func(e *core.RecordEvent) error {
		if needsRecompute(e) {
			if err := Recompute(e.App, e.Record, time.Now()); err != nil {
				e.App.Logger().Warn("arcade area classification failed", "arcade", e.Record.Id, "error", err)
			}
		}
		return e.Next()
	}
	app.OnRecordCreate(collectionArcade).Bind(&hook.Handler[*core.RecordEvent]{Id: "__arcadeAreaCreate__", Func: handler})
	app.OnRecordUpdate(collectionArcade).Bind(&hook.Handler[*core.RecordEvent]{Id: "__arcadeAreaUpdate__", Func: handler})
}

// Change is the planned area of one arcade next to what it stores now.
type Change struct {
	Arcade  string
	Basic   string
	Country string
	Public  bool
	Manual  bool
	Lat     float64
	Lon     float64
	Current Assignment
	Next    Assignment
}

// Changed reports whether applying the plan would alter stored values.
func (c Change) Changed() bool { return c.Current != c.Next }

// Plan derives every arcade (all visibilities) from its current basic
// revision without writing.
func Plan(app core.App) ([]Change, error) {
	return plan(app, 0)
}

// StalePlan derives up to limit arcades whose stored dataset version is not
// the embedded one.
func StalePlan(app core.App, limit int) ([]Change, error) {
	return plan(app, limit)
}

func plan(app core.App, staleLimit int) ([]Change, error) {
	areas, err := geo.EmbeddedAreas()
	if err != nil {
		return nil, err
	}
	type row struct {
		ID       string  `db:"id"`
		Basic    string  `db:"basic"`
		Country  string  `db:"country"`
		Public   bool    `db:"public"`
		Manual   bool    `db:"area_manual"`
		Mismatch bool    `db:"area_mismatch"`
		Admin1   string  `db:"admin1"`
		Admin2   string  `db:"admin2"`
		Admin2N  string  `db:"admin2_name"`
		City     string  `db:"city"`
		Status   string  `db:"area_status"`
		Version  string  `db:"area_version"`
		Lat      float64 `db:"lat"`
		Lon      float64 `db:"lon"`
		Address  string  `db:"address"`
		HasLoc   bool    `db:"has_loc"`
	}
	collection, err := app.FindCollectionByNameOrId(collectionArcade)
	if err != nil {
		return nil, err
	}
	// Before the schema migration (report on a backup copy) the area columns
	// do not exist yet; read them as empty.
	column := func(name, empty string) string {
		if collection.Fields.GetByName(name) == nil {
			return empty + " AS " + name
		}
		return "COALESCE(a." + name + "," + empty + ") AS " + name
	}
	where, limit, params := "", "", dbx.Params{}
	if staleLimit > 0 {
		if collection.Fields.GetByName(FieldVersion) == nil {
			return nil, fmt.Errorf("arcade.%s is missing; run the schema migration first", FieldVersion)
		}
		where, limit = " WHERE COALESCE(a."+FieldVersion+",'') != {:version}", " LIMIT {:limit}"
		params["version"], params["limit"] = areas.Version(), staleLimit
	}
	var rows []row
	err = app.DB().NewQuery(`SELECT a.id, COALESCE(a.basic,'') AS basic, COALESCE(a.country,'') AS country, COALESCE(a.public,0) AS public, ` +
		column(FieldManual, "0") + ", " + column(FieldMismatch, "0") + ", " + column(FieldAdmin1, "''") + ", " + column(FieldAdmin2, "''") + ", " +
		column(FieldAdmin2Nm, "''") + ", " + column(FieldCity, "''") + ", " + column(FieldStatus, "''") + ", " + column(FieldVersion, "''") + `,
 COALESCE(json_extract(b.location,'$.lat'),0) AS lat, COALESCE(json_extract(b.location,'$.lon'),0) AS lon,
 COALESCE(b.address,'') AS address, (b.id IS NOT NULL) AS has_loc
 FROM arcade a LEFT JOIN arcade_basic b ON b.id=a.basic` + where + ` ORDER BY a.id` + limit).Bind(params).All(&rows)
	if err != nil {
		return nil, err
	}
	out := make([]Change, 0, len(rows))
	for _, r := range rows {
		current := Assignment{Admin1: r.Admin1, Admin2: r.Admin2, Admin2Name: r.Admin2N, City: r.City, Status: r.Status, Mismatch: r.Mismatch, Version: r.Version}
		loc := Location{Country: r.Country, Lat: r.Lat, Lon: r.Lon, Valid: r.HasLoc && (r.Lat != 0 || r.Lon != 0), Address: r.Address}
		var manual *Assignment
		if r.Manual {
			manual = &current
		}
		next := Derive(areas, loc, manual)
		out = append(out, Change{Arcade: r.ID, Basic: r.Basic, Country: r.Country, Public: r.Public, Manual: r.Manual, Lat: r.Lat, Lon: r.Lon, Current: current, Next: next})
	}
	return out, nil
}

// ApplyPlan writes planned values that differ from the stored ones. It
// updates only the derived columns, so `updated`, history and XP are
// untouched, and it is idempotent. A row is written only while it still has
// the basic revision, country and manual flag it was planned from, so a
// concurrent edit (which recomputes in its own save) is never overwritten.
// Callers own the transaction and cache invalidation.
func ApplyPlan(app core.App, changes []Change, now time.Time) (int, error) {
	checked := types.NowDateTime()
	if !now.IsZero() {
		checked, _ = types.ParseDateTime(now.UTC())
	}
	written := 0
	for _, c := range changes {
		if !c.Changed() {
			continue
		}
		result, err := app.DB().Update(collectionArcade, map[string]any{
			FieldAdmin1: c.Next.Admin1, FieldAdmin2: c.Next.Admin2, FieldAdmin2Nm: c.Next.Admin2Name,
			FieldCity: c.Next.City, FieldStatus: c.Next.Status, FieldMismatch: c.Next.Mismatch,
			FieldVersion: c.Next.Version, FieldCheckedAt: checked.String(),
		}, dbx.And(
			dbx.HashExp{"id": c.Arcade},
			dbx.NewExp("COALESCE(country,'')={:country}", dbx.Params{"country": c.Country}),
			dbx.NewExp("COALESCE(basic,'')={:basic}", dbx.Params{"basic": c.Basic}),
			dbx.NewExp("COALESCE("+FieldManual+",0)={:manual}", dbx.Params{"manual": c.Manual}),
		)).Execute()
		if err != nil {
			return written, fmt.Errorf("arcade %s: %w", c.Arcade, err)
		}
		if n, _ := result.RowsAffected(); n > 0 {
			written++
		}
	}
	return written, nil
}

// RecomputeStale brings rows classified with an older dataset up to date,
// batch by batch, each batch in its own short write transaction so ordinary
// writes interleave. It stops when no stale row is left, ctx ends, or a batch
// writes nothing (rows that changed underneath are picked up next start).
// afterBatch, when set, runs after each committed batch (cache invalidation).
func RecomputeStale(ctx context.Context, app core.App, batch int, pause time.Duration, afterBatch func()) (int, error) {
	if batch <= 0 {
		batch = 100
	}
	total := 0
	for ctx.Err() == nil {
		changes, err := StalePlan(app, batch)
		if err != nil || len(changes) == 0 {
			return total, err
		}
		var written int
		err = app.RunInTransaction(func(tx core.App) error {
			var err error
			written, err = ApplyPlan(tx, changes, time.Now())
			return err
		})
		if err != nil {
			return total, err
		}
		total += written
		if afterBatch != nil && written > 0 {
			afterBatch()
		}
		if written == 0 {
			return total, nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(pause):
		}
	}
	return total, ctx.Err()
}
