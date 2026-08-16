package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Core owns the fresh-bootstrap profile presentation contract. Backend Full
// applies its own guarded forward migration to existing installations.
func init() {
	m.Register(addProfileBackgroundPosition, func(app core.App) error { return nil })
}

func addProfileBackgroundPosition(app core.App) error {
	collection, err := app.FindCollectionByNameOrId("user_info")
	if err != nil {
		return fmt.Errorf("find user_info: %w", err)
	}
	if collection.Name != "user_info" || collection.System || !collection.IsBase() {
		return fmt.Errorf("user_info must be a base collection")
	}
	if field := collection.Fields.GetByName("background_position"); field != nil {
		if _, ok := field.(*core.JSONField); !ok {
			return fmt.Errorf("user_info.background_position must be a JSON field")
		}
		return nil
	}

	collection.Fields.Add(&core.JSONField{Name: "background_position", MaxSize: 128})
	if err := app.Save(collection); err != nil {
		return fmt.Errorf("add user_info.background_position: %w", err)
	}
	return nil
}
