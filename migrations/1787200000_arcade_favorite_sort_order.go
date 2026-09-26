package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(ensureArcadeFavoriteSortOrder, func(app core.App) error { return nil })
}

func ensureArcadeFavoriteSortOrder(app core.App) error {
	collection, err := app.FindCollectionByNameOrId("arcade_favorite")
	if err != nil {
		return fmt.Errorf("find arcade_favorite: %w", err)
	}

	changed := false
	if field := collection.Fields.GetByName("sort_order"); field == nil {
		collection.Fields.Add(&core.NumberField{
			Name:    "sort_order",
			OnlyInt: true,
		})
		changed = true
	}

	const indexName = "idx_arcade_favorite_user_sort_order"
	if collection.GetIndex(indexName) == "" {
		collection.AddIndex(indexName, false, "user, sort_order", "")
		changed = true
	}

	if changed {
		if err := app.Save(collection); err != nil {
			return fmt.Errorf("save arcade_favorite sort_order schema: %w", err)
		}
	}

	return nil
}
