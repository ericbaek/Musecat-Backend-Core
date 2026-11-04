package arcadechecknote

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/pocketbase/pocketbase/core"
)

const Collection = "arcade_check_note"

// EnsureSchema is shared by fresh Core bootstrap and Full's guarded forward migration.
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
		col, err := tx.FindCollectionByNameOrId(Collection)
		if errors.Is(err, sql.ErrNoRows) {
			col = core.NewBaseCollection(Collection)
		} else if err != nil {
			return err
		} else if !col.IsBase() || col.System {
			return fmt.Errorf("incompatible %s collection", Collection)
		}
		fields := []core.Field{
			&core.RelationField{Name: "arcade", CollectionId: arcade.Id, Required: true, MaxSelect: 1, CascadeDelete: true},
			&core.RelationField{Name: "author", CollectionId: user.Id, Required: true, MaxSelect: 1},
			&core.TextField{Name: "message", Required: true, Max: 1000},
			&core.TextField{Name: "answer", Max: 1000},
			&core.RelationField{Name: "answered_by", CollectionId: user.Id, MaxSelect: 1},
			&core.BoolField{Name: "completed"}, &core.DateField{Name: "answered_at"},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		}
		for _, field := range fields {
			old := col.Fields.GetByName(field.GetName())
			if old == nil {
				col.Fields.Add(field)
				continue
			}
			if old.Type() != field.Type() {
				return fmt.Errorf("incompatible %s.%s", Collection, field.GetName())
			}
			if want, ok := field.(*core.RelationField); ok {
				got := old.(*core.RelationField)
				if want.CollectionId != got.CollectionId || want.Required != got.Required || want.CascadeDelete != got.CascadeDelete || want.MaxSelect != got.MaxSelect {
					return fmt.Errorf("incompatible check note relation %s", field.GetName())
				}
			}
			if want, ok := field.(*core.TextField); ok {
				got := old.(*core.TextField)
				if want.Max != got.Max || want.Required != got.Required {
					return fmt.Errorf("incompatible check note text %s", field.GetName())
				}
			}
		}
		col.ListRule = nil
		col.ViewRule = nil
		col.CreateRule = nil
		col.UpdateRule = nil
		col.DeleteRule = nil
		col.AddIndex("idx_arcade_check_note", false, "arcade,completed,created", "")
		return tx.Save(col)
	})
}
