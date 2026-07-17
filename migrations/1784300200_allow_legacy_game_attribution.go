package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Legacy molecules predate per-record attribution. Keep that absence explicit
// rather than fabricating a system editor, then retry only untouched arcades.
func init() {
	m.Register(func(app core.App) error {
		for _, name := range []string{"arcade_game_entry", "arcade_game_revision_batch"} {
			collection, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				return fmt.Errorf("find %s: %w", name, err)
			}
			field, ok := collection.Fields.GetByName("created_by").(*core.RelationField)
			if !ok {
				return fmt.Errorf("%s.created_by must be a relation", name)
			}
			field.Required = false
			if err := app.Save(collection); err != nil {
				return err
			}
		}
		return app.RunInTransaction(func(tx core.App) error {
			arcades, err := tx.FindRecordsByFilter("arcade", "game_state='' && game!=''", "", 0, 0)
			if err != nil {
				return err
			}
			for _, arcade := range arcades {
				if err := backfillCurrentGameState(tx, arcade, true); err != nil {
					return err
				}
			}
			return nil
		})
	}, func(app core.App) error { return nil })
}
