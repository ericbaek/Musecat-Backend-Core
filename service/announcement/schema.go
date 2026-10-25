package announcement

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

const Collection = "announcement"
const Views = "announcement_view"
const MaxSlides = 12
const MaxImageBytes = 5 << 20
const MaxBodyBytes = 3*MaxSlides*MaxImageBytes + (1 << 20)

// EnsureSchema is shared schema validation, never a production migration runner.
func EnsureSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		user, err := tx.FindCollectionByNameOrId("user")
		if err != nil {
			return err
		}
		col, err := ensureCollection(tx, Collection, []core.Field{
			&core.SelectField{Name: "status", Values: []string{"draft", "published", "archived"}, Required: true, MaxSelect: 1},
			&core.JSONField{Name: "localizations", MaxSize: 524288},
			&core.SelectField{Name: "locales", Values: []string{"ko", "en", "ja"}, MaxSelect: 3},
			&core.FileField{Name: "images", MaxSelect: 3 * MaxSlides, MaxSize: MaxImageBytes, MimeTypes: []string{"image/png", "image/jpeg", "image/webp"}, Protected: true},
			&core.DateField{Name: "published_at"},
			&core.TextField{Name: "broadcast_cursor", Max: 15},
			&core.BoolField{Name: "broadcast_done"},
			&core.RelationField{Name: "created_by", CollectionId: user.Id, MaxSelect: 1},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		})
		if err != nil {
			return err
		}
		col.AddIndex("idx_announcement_published", false, "status,published_at,id", "")
		if err := tx.Save(col); err != nil {
			return err
		}
		views, err := ensureCollection(tx, Views, []core.Field{
			&core.RelationField{Name: "user", CollectionId: user.Id, Required: true, MaxSelect: 1, CascadeDelete: true},
			&core.RelationField{Name: "announcement", CollectionId: col.Id, Required: true, MaxSelect: 1, CascadeDelete: true},
			&core.AutodateField{Name: "created", OnCreate: true},
		})
		if err != nil {
			return err
		}
		views.AddIndex("idx_announcement_view", true, "user,announcement", "")
		if err := tx.Save(views); err != nil {
			return err
		}
		inbox, err := tx.FindCollectionByNameOrId("user_notification")
		if err != nil {
			return err
		}
		for _, field := range []core.Field{
			&core.RelationField{Name: "announcement", CollectionId: col.Id, MaxSelect: 1, CascadeDelete: true},
			&core.JSONField{Name: "announcement_copy", MaxSize: 16384},
		} {
			if err := ensureField(inbox, field); err != nil {
				return err
			}
		}
		return tx.Save(inbox)
	})
}

func ensureCollection(app core.App, name string, fields []core.Field) (*core.Collection, error) {
	col, err := app.FindCollectionByNameOrId(name)
	if errors.Is(err, sql.ErrNoRows) {
		col = core.NewBaseCollection(name)
	} else if err != nil {
		return nil, err
	}
	if !col.IsBase() || col.System || col.ListRule != nil || col.ViewRule != nil || col.CreateRule != nil || col.UpdateRule != nil || col.DeleteRule != nil {
		return nil, fmt.Errorf("incompatible or unlocked %s collection", name)
	}
	for _, field := range fields {
		if err := ensureField(col, field); err != nil {
			return nil, err
		}
	}
	return col, nil
}

func ensureField(col *core.Collection, field core.Field) error {
	old := col.Fields.GetByName(field.GetName())
	if old == nil {
		col.Fields.Add(field)
		return nil
	}
	if old.Type() != field.Type() {
		return fmt.Errorf("incompatible %s.%s", col.Name, field.GetName())
	}
	valid := true
	switch want := field.(type) {
	case *core.RelationField:
		got := old.(*core.RelationField)
		valid = got.CollectionId == want.CollectionId && got.Required == want.Required && got.MaxSelect == want.MaxSelect && got.CascadeDelete == want.CascadeDelete
	case *core.JSONField:
		valid = old.(*core.JSONField).MaxSize == want.MaxSize
	case *core.FileField:
		got := old.(*core.FileField)
		valid = got.Protected && got.MaxSelect == want.MaxSelect && got.MaxSize == want.MaxSize && fmt.Sprint(got.MimeTypes) == fmt.Sprint(want.MimeTypes)
	case *core.SelectField:
		got := old.(*core.SelectField)
		valid = got.Required == want.Required && got.MaxSelect == want.MaxSelect && fmt.Sprint(got.Values) == fmt.Sprint(want.Values)
	case *core.TextField:
		valid = old.(*core.TextField).Max == want.Max
	case *core.AutodateField:
		got := old.(*core.AutodateField)
		valid = got.OnCreate == want.OnCreate && got.OnUpdate == want.OnUpdate
	}
	if !valid {
		return fmt.Errorf("incompatible %s.%s contract", col.Name, field.GetName())
	}
	return nil
}
