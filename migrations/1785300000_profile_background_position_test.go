package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestAddProfileBackgroundPosition(t *testing.T) {
	app := newProfileBackgroundPositionApp(t)
	if err := addProfileBackgroundPosition(app); err != nil {
		t.Fatalf("add background position: %v", err)
	}
	if err := addProfileBackgroundPosition(app); err != nil {
		t.Fatalf("reapply background position: %v", err)
	}
	collection, err := app.FindCollectionByNameOrId("user_info")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := collection.Fields.GetByName("background_position").(*core.JSONField); !ok {
		t.Fatal("expected user_info.background_position JSON field")
	}
}

func TestAddProfileBackgroundPositionRejectsIncompatibleField(t *testing.T) {
	app := newProfileBackgroundPositionApp(t)
	collection, err := app.FindCollectionByNameOrId("user_info")
	if err != nil {
		t.Fatal(err)
	}
	collection.Fields.Add(&core.TextField{Name: "background_position"})
	if err := app.Save(collection); err != nil {
		t.Fatal(err)
	}
	if err := addProfileBackgroundPosition(app); err == nil {
		t.Fatal("expected incompatible field to fail")
	}
}

func newProfileBackgroundPositionApp(t *testing.T) core.App {
	t.Helper()
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir(), EncryptionEnv: "profile_background_position"})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("bootstrap app: %v", err)
	}
	t.Cleanup(func() { _ = app.ResetBootstrapState() })
	collection := core.NewBaseCollection("user_info")
	if err := app.Save(collection); err != nil {
		t.Fatalf("create user_info: %v", err)
	}
	return app
}
