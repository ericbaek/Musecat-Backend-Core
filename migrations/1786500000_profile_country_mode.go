package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Core owns the fresh-bootstrap profile country mode field. Missing legacy
// values are interpreted as auto by the profile API.
func init() {
	m.Register(addProfileCountryMode, func(app core.App) error { return nil })
}

func addProfileCountryMode(app core.App) error {
	collection, err := app.FindCollectionByNameOrId("user_info")
	if err != nil {
		return fmt.Errorf("find user_info: %w", err)
	}
	if collection.Name != "user_info" || collection.System || !collection.IsBase() {
		return fmt.Errorf("user_info must be a base collection")
	}
	if field := collection.Fields.GetByName("country_mode"); field != nil {
		text, ok := field.(*core.TextField)
		if !ok || text.Pattern != "^(auto|manual|off)$" {
			return fmt.Errorf("user_info.country_mode must be a constrained text field")
		}
		return nil
	}
	collection.Fields.Add(&core.TextField{Name: "country_mode", Max: 6, Pattern: "^(auto|manual|off)$"})
	if err := app.Save(collection); err != nil {
		return fmt.Errorf("add user_info.country_mode: %w", err)
	}
	return nil
}
