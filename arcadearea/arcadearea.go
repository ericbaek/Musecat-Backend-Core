// Package arcadearea stores each arcade's admin area and Passport city.
//
// The values are derived from the arcade's current basic location and its
// country through the embedded geo.Areas catalog. They live on `arcade` (not
// on each `arcade_basic` revision) so a rollback or bot edit recomputes them
// and no history row is written for a derived value. Classification never
// blocks a write: failures are logged and stored as unmapped.
package arcadearea

import (
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
	switch c.Fields.GetByName(FieldManual).(type) {
	case nil:
		c.Fields.Add(&core.BoolField{Name: FieldManual})
		changed = true
	case *core.BoolField:
	default:
		return fmt.Errorf("incompatible arcade.%s", FieldManual)
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
}

// FromRecord reads the stored assignment.
func FromRecord(arcade *core.Record) Assignment {
	return Assignment{
		Admin1:     arcade.GetString(FieldAdmin1),
		Admin2:     arcade.GetString(FieldAdmin2),
		Admin2Name: arcade.GetString(FieldAdmin2Nm),
		City:       arcade.GetString(FieldCity),
		Status:     arcade.GetString(FieldStatus),
	}
}

// Classify computes the assignment for a country and coordinate.
func Classify(country string, lat, lon float64) (Assignment, error) {
	areas, err := geo.EmbeddedAreas()
	if err != nil {
		return Assignment{Status: geo.AreaUnmapped}, err
	}
	r := areas.Classify(country, lat, lon)
	return Assignment{Admin1: r.Admin1, Admin2: r.Admin2, Admin2Name: r.Admin2Name, City: r.City, Status: r.Status}, nil
}

// Apply sets the assignment on an arcade record without saving it.
func Apply(arcade *core.Record, a Assignment, now time.Time) {
	arcade.Set(FieldAdmin1, a.Admin1)
	arcade.Set(FieldAdmin2, a.Admin2)
	arcade.Set(FieldAdmin2Nm, a.Admin2Name)
	arcade.Set(FieldCity, a.City)
	arcade.Set(FieldStatus, a.Status)
	checked, _ := types.ParseDateTime(now.UTC())
	arcade.Set(FieldCheckedAt, checked)
}

// Recompute classifies an arcade from its current basic location and sets the
// fields on the record without saving. Manual assignments are kept. A missing
// basic or location yields unmapped; only a broken catalog returns an error,
// and the record still gets the unmapped status.
func Recompute(app core.App, arcade *core.Record, now time.Time) error {
	if arcade.GetBool(FieldManual) {
		return nil
	}
	basicID := strings.TrimSpace(arcade.GetString("basic"))
	if basicID == "" {
		Apply(arcade, Assignment{Status: geo.AreaUnmapped}, now)
		return nil
	}
	basic, err := app.FindRecordById(collectionArcadeBasic, basicID)
	if errors.Is(err, sql.ErrNoRows) {
		Apply(arcade, Assignment{Status: geo.AreaUnmapped}, now)
		return nil
	}
	if err != nil {
		Apply(arcade, Assignment{Status: geo.AreaUnmapped}, now)
		return err
	}
	point := basic.GetGeoPoint("location")
	if point.Lat == 0 && point.Lon == 0 {
		Apply(arcade, Assignment{Status: geo.AreaUnmapped}, now)
		return nil
	}
	a, err := Classify(arcade.GetString("country"), point.Lat, point.Lon)
	Apply(arcade, a, now)
	return err
}

// needsRecompute reports whether a save moves the arcade to another location
// revision or country.
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

// Plan classifies every arcade (all visibilities) from its current basic
// location without writing. Manual assignments plan to keep their values.
func Plan(app core.App) ([]Change, error) {
	areas, err := geo.EmbeddedAreas()
	if err != nil {
		return nil, err
	}
	type row struct {
		ID      string  `db:"id"`
		Country string  `db:"country"`
		Public  bool    `db:"public"`
		Manual  bool    `db:"area_manual"`
		Admin1  string  `db:"admin1"`
		Admin2  string  `db:"admin2"`
		Admin2N string  `db:"admin2_name"`
		City    string  `db:"city"`
		Status  string  `db:"area_status"`
		Lat     float64 `db:"lat"`
		Lon     float64 `db:"lon"`
		HasLoc  bool    `db:"has_loc"`
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
	var rows []row
	err = app.DB().NewQuery(`SELECT a.id, COALESCE(a.country,'') AS country, COALESCE(a.public,0) AS public, ` +
		column(FieldManual, "0") + ", " + column(FieldAdmin1, "''") + ", " + column(FieldAdmin2, "''") + ", " +
		column(FieldAdmin2Nm, "''") + ", " + column(FieldCity, "''") + ", " + column(FieldStatus, "''") + `,
 COALESCE(json_extract(b.location,'$.lat'),0) AS lat, COALESCE(json_extract(b.location,'$.lon'),0) AS lon,
 (b.id IS NOT NULL) AS has_loc
 FROM arcade a LEFT JOIN arcade_basic b ON b.id=a.basic ORDER BY a.id`).All(&rows)
	if err != nil {
		return nil, err
	}
	out := make([]Change, 0, len(rows))
	for _, r := range rows {
		current := Assignment{Admin1: r.Admin1, Admin2: r.Admin2, Admin2Name: r.Admin2N, City: r.City, Status: r.Status}
		next := current
		if !r.Manual {
			if !r.HasLoc || (r.Lat == 0 && r.Lon == 0) {
				next = Assignment{Status: geo.AreaUnmapped}
			} else {
				res := areas.Classify(r.Country, r.Lat, r.Lon)
				next = Assignment{Admin1: res.Admin1, Admin2: res.Admin2, Admin2Name: res.Admin2Name, City: res.City, Status: res.Status}
			}
		}
		out = append(out, Change{Arcade: r.ID, Country: r.Country, Public: r.Public, Manual: r.Manual, Lat: r.Lat, Lon: r.Lon, Current: current, Next: next})
	}
	return out, nil
}

// ApplyPlan writes planned values that differ from the stored ones. It updates
// only the derived columns, so `updated`, history and XP are untouched, and it
// is idempotent. Callers own the transaction and cache invalidation.
func ApplyPlan(app core.App, changes []Change, now time.Time) (int, error) {
	checked := types.NowDateTime()
	if !now.IsZero() {
		checked, _ = types.ParseDateTime(now.UTC())
	}
	written := 0
	for _, c := range changes {
		if !c.Changed() || c.Manual {
			continue
		}
		_, err := app.DB().Update(collectionArcade, map[string]any{
			FieldAdmin1: c.Next.Admin1, FieldAdmin2: c.Next.Admin2, FieldAdmin2Nm: c.Next.Admin2Name,
			FieldCity: c.Next.City, FieldStatus: c.Next.Status, FieldCheckedAt: checked.String(),
		}, dbx.HashExp{"id": c.Arcade}).Execute()
		if err != nil {
			return written, fmt.Errorf("arcade %s: %w", c.Arcade, err)
		}
		written++
	}
	return written, nil
}
