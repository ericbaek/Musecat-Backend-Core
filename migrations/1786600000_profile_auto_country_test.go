package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestAddProfileAutoCountry(t *testing.T) {
	app := newProfileCountriesApp(t)
	if err := addProfileAutoCountry(app); err != nil {
		t.Fatalf("add auto_primary_country: %v", err)
	}
	collection, err := app.FindCollectionByNameOrId("user_info")
	if err != nil {
		t.Fatal(err)
	}
	field, ok := collection.Fields.GetByName("auto_primary_country").(*core.TextField)
	if !ok || field.Max != 2 || field.Pattern != "^[A-Z]{2}$" {
		t.Fatalf("unexpected auto_primary_country field: %#v", field)
	}
}
