package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestEnsureUserActivityIndexes(t *testing.T) {
	app := newUserActivityIndexesApp(t)

	for i := 0; i < 2; i++ {
		if err := ensureUserActivityIndexes(app); err != nil {
			t.Fatalf("apply %d: %v", i+1, err)
		}
	}

	for _, spec := range []struct {
		collection string
		indexName  string
	}{
		{collection: "arcade_changelog", indexName: "idx_arcade_changelog_by_created"},
		{collection: "arcade_flag", indexName: "idx_arcade_flag_created_by_created"},
		{collection: "arcade_flag_reaction", indexName: "idx_arcade_flag_reaction_created_by_created"},
	} {
		col, err := app.FindCollectionByNameOrId(spec.collection)
		if err != nil {
			t.Fatalf("find %s: %v", spec.collection, err)
		}
		if col.GetIndex(spec.indexName) == "" {
			t.Fatalf("collection %s missing index %s", spec.collection, spec.indexName)
		}
	}
}

func newUserActivityIndexesApp(t *testing.T) core.App {
	t.Helper()
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir(), EncryptionEnv: "user_activity_indexes"})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("bootstrap app: %v", err)
	}
	t.Cleanup(func() { _ = app.ResetBootstrapState() })

	for _, name := range []string{"arcade_changelog", "arcade_flag", "arcade_flag_reaction"} {
		col := core.NewBaseCollection(name)
		col.Fields.Add(&core.TextField{Name: "by"})
		col.Fields.Add(&core.TextField{Name: "createdBy"})
		col.Fields.Add(&core.TextField{Name: "created"})
		if err := app.Save(col); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	return app
}
