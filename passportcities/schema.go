// Package passportcities owns the reusable city contract, never deployment migrations.
package passportcities

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/pocketbase/pocketbase/core"
)

// EnsureSchema adds only schema. Existing incompatible fields fail closed.
func EnsureSchema(app core.App) error {
	c, err := app.FindCollectionByNameOrId("passport_city")
	if errors.Is(err, sql.ErrNoRows) {
		c = core.NewBaseCollection("passport_city")
		c.Fields.Add(&core.TextField{Name: "source_id", Required: true, Pattern: `^[0-9]+$`}, &core.TextField{Name: "name", Required: true}, &core.TextField{Name: "country", Required: true, Pattern: `^[A-Z]{2}$`}, &core.TextField{Name: "admin1", Required: true}, &core.JSONField{Name: "aliases", MaxSize: 100000}, &core.GeoPointField{Name: "location"})
		c.AddIndex("idx_passport_city_source", true, "source_id", "")
		if err = app.Save(c); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if c.ListRule != nil || c.ViewRule != nil || c.CreateRule != nil || c.UpdateRule != nil || c.DeleteRule != nil {
		return fmt.Errorf("passport_city must have locked rules")
	}
	for _, name := range []string{"source_id", "name", "country", "admin1"} {
		if _, ok := c.Fields.GetByName(name).(*core.TextField); !ok {
			return fmt.Errorf("incompatible passport_city.%s", name)
		}
	}
	if _, ok := c.Fields.GetByName("location").(*core.GeoPointField); !ok {
		return fmt.Errorf("incompatible passport_city.location")
	}
	if _, ok := c.Fields.GetByName("aliases").(*core.JSONField); !ok {
		return fmt.Errorf("incompatible passport_city.aliases")
	}
	if c.GetIndex("idx_passport_city_source") == "" {
		return fmt.Errorf("passport_city source index missing")
	}
	b, err := app.FindCollectionByNameOrId("arcade_basic")
	if err != nil {
		return err
	}
	if field := b.Fields.GetByName("city_id"); field != nil {
		r, ok := field.(*core.RelationField)
		if !ok || r.CollectionId != c.Id || r.MaxSelect != 1 || r.Required || r.CascadeDelete {
			return fmt.Errorf("incompatible arcade_basic.city_id")
		}
		return nil
	}
	b.Fields.Add(&core.RelationField{Name: "city_id", CollectionId: c.Id, MaxSelect: 1})
	return app.Save(b)
}
