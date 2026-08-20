package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestCommunityPostSchemaIsGuardedAndIdempotent(t *testing.T) {
	app := core.NewBaseApp(core.BaseAppConfig{
		DataDir:       t.TempDir(),
		EncryptionEnv: "musecat_core_community_translation_migration_test",
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("bootstrap test app: %v", err)
	}
	t.Cleanup(func() {
		if err := app.ResetBootstrapState(); err != nil {
			t.Errorf("reset test app: %v", err)
		}
	})

	users := core.NewAuthCollection("user")
	series := core.NewBaseCollection("game_series")
	for _, collection := range []*core.Collection{users, series} {
		if err := app.Save(collection); err != nil {
			t.Fatalf("create %s: %v", collection.Name, err)
		}
	}

	if err := applyCommunityPostSchema(app); err != nil {
		t.Fatalf("apply community post schema: %v", err)
	}
	if err := applyCommunityPostSchema(app); err != nil {
		t.Fatalf("reapply community post schema: %v", err)
	}

	collection, err := app.FindCollectionByNameOrId(communityPostCollection)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCommunityPostCollection(collection, users, series); err != nil {
		t.Fatal(err)
	}

	collection.Fields.RemoveByName("translations")
	if err := app.Save(collection); err != nil {
		t.Fatalf("corrupt community schema for guard test: %v", err)
	}
	if err := applyCommunityPostSchema(app); err == nil {
		t.Fatal("expected incompatible community schema to be rejected")
	}
}
