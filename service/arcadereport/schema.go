package arcadereport

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

const Subscriptions = "arcade_report_subscription"
const Deliveries = "arcade_report_delivery"

// EnsureSchema is shared by fresh Core bootstrap and Full's forward migration.
func EnsureSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		arcade, err := tx.FindCollectionByNameOrId("arcade")
		if err != nil {
			return err
		}
		user, err := tx.FindCollectionByNameOrId("user")
		if err != nil {
			return err
		}
		flag, err := tx.FindCollectionByNameOrId("arcade_flag")
		if err != nil {
			return err
		}
		if f := arcade.Fields.GetByName("report_settings"); f == nil {
			arcade.Fields.Add(&core.JSONField{Name: "report_settings", MaxSize: SettingsMaxSize})
		} else if field, ok := f.(*core.JSONField); !ok || (field.MaxSize != LegacySettingsMaxSize && field.MaxSize != SettingsMaxSize) {
			return fmt.Errorf("incompatible arcade.report_settings")
		}
		if err := tx.Save(arcade); err != nil {
			return err
		}
		// A consolidated collection already has canonical device preferences.
		if pref, err := tx.FindCollectionByNameOrId(Subscriptions); err == nil && pref.Fields.GetByName("device") != nil && pref.Fields.GetByName("endpoint") == nil {
			return nil
		}

		definitions := []struct {
			name    string
			fields  []core.Field
			index   string
			columns string
		}{
			{Subscriptions, []core.Field{
				&core.RelationField{Name: "arcade", CollectionId: arcade.Id, Required: true, MaxSelect: 1, CascadeDelete: true},
				&core.RelationField{Name: "user", CollectionId: user.Id, Required: true, MaxSelect: 1, CascadeDelete: true},
				&core.TextField{Name: "endpoint", Required: true, Max: 2048},
				&core.JSONField{Name: "subscription", Required: true, MaxSize: 8192},
				&core.AutodateField{Name: "created", OnCreate: true},
			}, "idx_report_subscription_identity", "arcade, user, endpoint"},
			{Deliveries, []core.Field{
				&core.RelationField{Name: "flag", CollectionId: flag.Id, Required: true, MaxSelect: 1, CascadeDelete: true},
				&core.RelationField{Name: "subscription", CollectionId: "", Required: true, MaxSelect: 1, CascadeDelete: true},
				&core.NumberField{Name: "attempts", OnlyInt: true},
				&core.DateField{Name: "next_attempt"},
				&core.BoolField{Name: "done"},
				&core.AutodateField{Name: "created", OnCreate: true},
			}, "idx_report_delivery_identity", "flag, subscription"},
		}
		for _, def := range definitions {
			col, err := tx.FindCollectionByNameOrId(def.name)
			if err != nil {
				if !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				col = core.NewBaseCollection(def.name)
			} else if !col.IsBase() || col.System {
				return fmt.Errorf("incompatible %s", def.name)
			}
			for _, f := range def.fields {
				if r, ok := f.(*core.RelationField); ok && r.Name == "subscription" {
					sub, err := tx.FindCollectionByNameOrId(Subscriptions)
					if err != nil {
						return err
					}
					r.CollectionId = sub.Id
				}
				existing := col.Fields.GetByName(f.GetName())
				if existing == nil {
					col.Fields.Add(f)
				} else if existing.Type() != f.Type() {
					return fmt.Errorf("incompatible %s.%s", def.name, f.GetName())
				}
				if expected, ok := f.(*core.RelationField); ok && existing != nil {
					actual := existing.(*core.RelationField)
					if actual.CollectionId != expected.CollectionId || actual.Required != expected.Required || actual.MaxSelect != expected.MaxSelect || actual.CascadeDelete != expected.CascadeDelete {
						return fmt.Errorf("incompatible relation %s.%s", def.name, f.GetName())
					}
				}
			}
			col.ListRule = nil
			col.ViewRule = nil
			col.CreateRule = nil
			col.UpdateRule = nil
			col.DeleteRule = nil
			col.AddIndex(def.index, true, def.columns, "")
			if err := tx.Save(col); err != nil {
				return err
			}
		}
		return nil
	})
}
