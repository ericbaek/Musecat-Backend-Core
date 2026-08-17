package migrations

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

const arcadeMemoCollection = "arcade_memo"

func init() {
	m.Register(applyArcadeMemoSchema, func(app core.App) error { return nil })
}

func applyArcadeMemoSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		arcades, err := tx.FindCollectionByNameOrId("arcade")
		if err != nil {
			return fmt.Errorf("find arcade: %w", err)
		}
		users, err := tx.FindCollectionByNameOrId("user")
		if err != nil {
			return fmt.Errorf("find user: %w", err)
		}

		memo, err := tx.FindCollectionByNameOrId(arcadeMemoCollection)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("find %s: %w", arcadeMemoCollection, err)
			}
			memo = core.NewBaseCollection(arcadeMemoCollection)
			memo.Fields.Add(
				&core.RelationField{Name: "arcade", CollectionId: arcades.Id, CascadeDelete: true, Required: true, MaxSelect: 1},
				&core.JSONField{Name: "document", Required: true},
				&core.RelationField{Name: "by", CollectionId: users.Id, CascadeDelete: false, Required: true, MaxSelect: 1},
				&core.AutodateField{Name: "created", OnCreate: true},
			)
			memo.AddIndex("idx_arcade_memo_arcade_created", false, "arcade, created", "")
			memo.ListRule = nil
			memo.ViewRule = nil
			memo.CreateRule = nil
			memo.UpdateRule = nil
			memo.DeleteRule = nil
			if err := tx.Save(memo); err != nil {
				return fmt.Errorf("create %s: %w", arcadeMemoCollection, err)
			}
		} else {
			if memo.Name != arcadeMemoCollection || memo.System || !memo.IsBase() {
				return fmt.Errorf("%s must be a base collection", arcadeMemoCollection)
			}
			if err := requireArcadeMemoFieldShape(memo, arcades.Id, users.Id); err != nil {
				return err
			}
			memo.ListRule = nil
			memo.ViewRule = nil
			memo.CreateRule = nil
			memo.UpdateRule = nil
			memo.DeleteRule = nil
			if err := tx.Save(memo); err != nil {
				return fmt.Errorf("lock %s raw REST rules: %w", arcadeMemoCollection, err)
			}
		}

		field := arcades.Fields.GetByName("memo")
		if field == nil {
			arcades.Fields.Add(&core.RelationField{Name: "memo", CollectionId: memo.Id, CascadeDelete: false, MaxSelect: 1})
		} else {
			relation, ok := field.(*core.RelationField)
			if !ok || relation.CollectionId != memo.Id || relation.Required || relation.CascadeDelete || relation.MinSelect != 0 || relation.MaxSelect != 1 {
				return fmt.Errorf("arcade.memo has incompatible relation settings")
			}
		}
		if err := tx.Save(arcades); err != nil {
			return fmt.Errorf("add arcade.memo: %w", err)
		}
		return nil
	})
}

func requireArcadeMemoFieldShape(collection *core.Collection, arcadeID, userID string) error {
	relation, ok := collection.Fields.GetByName("arcade").(*core.RelationField)
	if !ok || relation.CollectionId != arcadeID || !relation.Required || !relation.CascadeDelete || relation.MinSelect != 0 || relation.MaxSelect != 1 {
		return fmt.Errorf("%s.arcade has incompatible relation settings", collection.Name)
	}
	document, ok := collection.Fields.GetByName("document").(*core.JSONField)
	if !ok || !document.Required || document.MaxSize != 0 {
		return fmt.Errorf("%s.document has incompatible JSON settings", collection.Name)
	}
	by, ok := collection.Fields.GetByName("by").(*core.RelationField)
	if !ok || by.CollectionId != userID || !by.Required || by.CascadeDelete || by.MinSelect != 0 || by.MaxSelect != 1 {
		return fmt.Errorf("%s.by has incompatible relation settings", collection.Name)
	}
	created, ok := collection.Fields.GetByName("created").(*core.AutodateField)
	if !ok || !created.OnCreate || created.OnUpdate {
		return fmt.Errorf("%s.created has incompatible autodate settings", collection.Name)
	}
	if collection.GetIndex("idx_arcade_memo_arcade_created") == "" {
		collection.AddIndex("idx_arcade_memo_arcade_created", false, "arcade, created", "")
	}
	return nil
}
