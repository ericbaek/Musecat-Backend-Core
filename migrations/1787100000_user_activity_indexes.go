package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(ensureUserActivityIndexes, func(app core.App) error { return nil })
}

func ensureUserActivityIndexes(app core.App) error {
	for _, spec := range []struct {
		collection string
		name       string
		fields     string
		where      string
	}{
		{collection: "arcade_changelog", name: "idx_arcade_changelog_by_created", fields: "`by`, created"},
		{collection: "arcade_flag", name: "idx_arcade_flag_created_by_created", fields: "createdBy, created"},
		{collection: "arcade_flag_reaction", name: "idx_arcade_flag_reaction_created_by_created", fields: "createdBy, created"},
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
