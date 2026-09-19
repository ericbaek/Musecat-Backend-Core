package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Core owns the fresh-bootstrap cache field. Backend Full backfills existing
// installations with the matching guarded forward migration.
func init() {
	m.Register(addProfileAutoCountry, func(app core.App) error { return nil })
}

func addProfileAutoCountry(app core.App) error {
	collection, err := app.FindCollectionByNameOrId("user_info")
	if err != nil {
		return fmt.Errorf("find user_info: %w", err)
	}
	if collection.Name != "user_info" || collection.System || !collection.IsBase() {
		return fmt.Errorf("user_info must be a base collection")
	}
	if field := collection.Fields.GetByName("auto_primary_country"); field != nil {
		text, ok := field.(*core.TextField)
		if !ok || text.Max != 2 || text.Pattern != "^[A-Z]{2}$" {
			return fmt.Errorf("user_info.auto_primary_country must be a constrained text field")
		}
		return nil
	}
	collection.Fields.Add(&core.TextField{Name: "auto_primary_country", Max: 2, Pattern: "^[A-Z]{2}$"})
	if err := app.Save(collection); err != nil {
		return fmt.Errorf("add user_info.auto_primary_country: %w", err)
	}
	return nil
}
