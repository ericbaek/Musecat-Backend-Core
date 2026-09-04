package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestAddProfileCountries(t *testing.T) {
	app := newProfileCountriesApp(t)
	if err := addProfileCountries(app); err != nil {
		t.Fatalf("add countries: %v", err)
	}
	if err := addProfileCountries(app); err != nil {
		t.Fatalf("reapply countries: %v", err)
	}
	collection, err := app.FindCollectionByNameOrId("user_info")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := collection.Fields.GetByName("countries").(*core.JSONField); !ok {
		t.Fatal("expected user_info.countries JSON field")
	}
}

func TestAddProfileCountriesRejectsIncompatibleField(t *testing.T) {
	app := newProfileCountriesApp(t)
	collection, err := app.FindCollectionByNameOrId("user_info")
	if err != nil {
		t.Fatal(err)
	}
	collection.Fields.Add(&core.TextField{Name: "countries"})
	if err := app.Save(collection); err != nil {
		t.Fatal(err)
	}
	if err := addProfileCountries(app); err == nil {
		t.Fatal("expected incompatible field to fail")
	}
}

func newProfileCountriesApp(t *testing.T) core.App {
	t.Helper()
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir(), EncryptionEnv: "profile_countries"})
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
