package botaccess

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/contribution"
	"github.com/pocketbase/pocketbase/core"
)

const Keys = "user_bot_key"

// EnsureSchema defines the reusable contract. Full invokes it only in its own migration.
func EnsureSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		users, err := tx.FindCollectionByNameOrId("user")
		if err != nil {
			return err
		}
		collection, err := tx.FindCollectionByNameOrId(Keys)
		if errors.Is(err, sql.ErrNoRows) {
			collection = core.NewBaseCollection(Keys)
		} else if err != nil {
			return err
		}
		collection.ListRule, collection.ViewRule, collection.CreateRule, collection.UpdateRule, collection.DeleteRule = nil, nil, nil, nil, nil
		if !collection.IsBase() {
			return fmt.Errorf("bot key collection must be a base collection")
		}
		fields := []core.Field{
			&core.RelationField{Name: "owner", CollectionId: users.Id, Required: true, MaxSelect: 1},
			&core.TextField{Name: "name", Required: true, Max: 60},
			&core.TextField{Name: "key_hash", Required: true, Hidden: true, Min: 64, Max: 64},
			&core.TextField{Name: "owner_key_hash", Required: true, Hidden: true, Min: 64, Max: 64},
			&core.TextField{Name: "prefix", Required: true, Max: 24},
			&core.DateField{Name: "expires"},
			&core.DateField{Name: "revoked"},
			&core.DateField{Name: "last_used"},
			&core.DateField{Name: "window_start"},
			&core.NumberField{Name: "window_count", OnlyInt: true},
			&core.AutodateField{Name: "created", OnCreate: true},
		}
		for _, field := range fields {
			if existing := collection.Fields.GetByName(field.GetName()); existing != nil {
				if existing.Type() != field.Type() {
					return fmt.Errorf("incompatible bot key field %s", field.GetName())
				}
				field.SetId(existing.GetId())
			}
			collection.Fields.Add(field)
		}
		collection.AddIndex("idx_bot_key_hash", true, "key_hash", "")
		collection.AddIndex("idx_bot_key_owner", false, "owner,created", "")
		if err := tx.Save(collection); err != nil {
			return err
		}
		for _, name := range []string{"arcade", "arcade_changelog", "arcade_photo_atoms"} {
			collection, err := tx.FindCollectionByNameOrId(name)
			if err != nil {
				return err
			}
			if existing := collection.Fields.GetByName(contribution.Field); existing != nil && existing.Type() != "json" {
				return fmt.Errorf("incompatible contribution source in %s", name)
			}
			if collection.Fields.GetByName(contribution.Field) == nil {
				collection.Fields.Add(&core.JSONField{Name: contribution.Field, MaxSize: 1024})
				if err := tx.Save(collection); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
