package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Core owns the fresh-bootstrap profile country contract. Backend Full applies
// its own guarded forward migration to existing installations.
func init() {
	m.Register(addProfileCountries, func(app core.App) error { return nil })
}

func addProfileCountries(app core.App) error {
	collection, err := app.FindCollectionByNameOrId("user_info")
	if err != nil {
		return fmt.Errorf("find user_info: %w", err)
	}
	if collection.Name != "user_info" || collection.System || !collection.IsBase() {
		return fmt.Errorf("user_info must be a base collection")
	}
	if field := collection.Fields.GetByName("countries"); field != nil {
		if _, ok := field.(*core.JSONField); !ok {
			return fmt.Errorf("user_info.countries must be a JSON field")
		}
		return nil
	}

	collection.Fields.Add(&core.JSONField{Name: "countries", MaxSize: 128})
	if err := app.Save(collection); err != nil {
		return fmt.Errorf("add user_info.countries: %w", err)
	}
	return nil
}
