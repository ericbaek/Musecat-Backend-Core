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
