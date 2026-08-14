package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// This is a schema-only Core migration for existing Core bootstrap databases.
// Backend-Full owns a separate migration for existing Full installations.
func init() {
	m.Register(addATMToGTKAtoms, func(app core.App) error { return nil })
}

func addATMToGTKAtoms(app core.App) error {
	collection, err := app.FindCollectionByNameOrId("arcade_gtk_atoms")
	if err != nil {
		return fmt.Errorf("find arcade_gtk_atoms: %w", err)
	}
	if collection.Name != "arcade_gtk_atoms" || collection.System || !collection.IsBase() {
		return fmt.Errorf("arcade_gtk_atoms must be a base collection")
	}

	field, ok := collection.Fields.GetByName("type").(*core.SelectField)
	if !ok {
		return fmt.Errorf("arcade_gtk_atoms.type must be a select field")
	}
	if field.MaxSelect != 1 || field.Required || field.System || field.Hidden || field.Presentable {
		return fmt.Errorf("arcade_gtk_atoms.type has incompatible select settings")
	}
	if containsGTKAtomType(field.Values, "ATM") {
		return nil
	}

	field.Values = append(field.Values, "ATM")
	if err := app.Save(collection); err != nil {
		return fmt.Errorf("add ATM to arcade_gtk_atoms.type: %w", err)
	}
	return nil
}

func containsGTKAtomType(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
