package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestEnsureArcadeFavoriteSortOrder(t *testing.T) {
	app := newArcadeFavoriteSortOrderApp(t)

	for i := 0; i < 2; i++ {
		if err := ensureArcadeFavoriteSortOrder(app); err != nil {
			t.Fatalf("apply %d: %v", i+1, err)
		}
	}

	col, err := app.FindCollectionByNameOrId("arcade_favorite")
	if err != nil {
		t.Fatalf("find arcade_favorite: %v", err)
	}

	field := col.Fields.GetByName("sort_order")
	if field == nil {
		t.Fatal("expected sort_order field on arcade_favorite")
	}
	numField, ok := field.(*core.NumberField)
	if !ok || !numField.OnlyInt {
		t.Fatalf("expected OnlyInt NumberField, got %#v", field)
	}

	if col.GetIndex("idx_arcade_favorite_user_sort_order") == "" {
		t.Fatal("missing idx_arcade_favorite_user_sort_order index")
	}
}

func newArcadeFavoriteSortOrderApp(t *testing.T) core.App {
	t.Helper()
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir(), EncryptionEnv: "arcade_favorite_sort_order"})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("bootstrap app: %v", err)
	}
	t.Cleanup(func() { _ = app.ResetBootstrapState() })

	col := core.NewBaseCollection("arcade_favorite")
	col.Fields.Add(&core.TextField{Name: "user"})
	col.Fields.Add(&core.TextField{Name: "arcade"})
	col.Fields.Add(&core.TextField{Name: "created"})
	if err := app.Save(col); err != nil {
		t.Fatalf("create arcade_favorite: %v", err)
	}
	return app
}
