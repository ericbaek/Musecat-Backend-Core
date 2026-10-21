package arcadereport

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

const LegacySettingsMaxSize = 8192

// SettingsMaxSize accommodates the rune limits even with JSON's six-byte escapes.
const SettingsMaxSize = 32768

// EnsureSettingsCapacitySchema changes only the existing destination settings.
// Full uses this independently of the legacy report notification schema.
func EnsureSettingsCapacitySchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		arcade, err := tx.FindCollectionByNameOrId("arcade")
		if err != nil {
			return err
		}
		if !arcade.IsBase() || arcade.System {
			return fmt.Errorf("incompatible arcade collection")
		}
		for _, name := range []string{"report_settings", "inquiry_settings"} {
			field, ok := arcade.Fields.GetByName(name).(*core.JSONField)
			if !ok || (field.MaxSize != LegacySettingsMaxSize && field.MaxSize != SettingsMaxSize) {
				return fmt.Errorf("incompatible arcade.%s", name)
			}
			field.MaxSize = SettingsMaxSize
		}
		return tx.Save(arcade)
	})
}
