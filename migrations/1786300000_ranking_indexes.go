package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(ensureRankingIndexes, func(app core.App) error { return nil })
}

func ensureRankingIndexes(app core.App) error {
	for _, spec := range []struct {
		collection string
		name       string
		fields     string
		where      string
	}{
		{collection: "arcade_visit", name: "idx_arcade_visit_visited_at_user_arcade", fields: "visited_at, user, arcade"},
		{collection: "arcade_visit", name: "idx_arcade_visit_user_visited_at_id", fields: "user, visited_at, id"},
		{collection: "user_level_log", name: "idx_user_level_log_created_user", fields: "created, user"},
		{collection: "arcade_photo_atoms", name: "idx_arcade_photo_atoms_created_author", fields: "created, createdBy", where: "public = 1"},
	} {
		collection, err := app.FindCollectionByNameOrId(spec.collection)
		if err != nil {
			return fmt.Errorf("find %s: %w", spec.collection, err)
		}
		if collection.GetIndex(spec.name) != "" {
			continue
		}
		collection.AddIndex(spec.name, false, spec.fields, spec.where)
		if err := app.Save(collection); err != nil {
			return fmt.Errorf("add %s to %s: %w", spec.name, spec.collection, err)
		}
	}
	return nil
}
