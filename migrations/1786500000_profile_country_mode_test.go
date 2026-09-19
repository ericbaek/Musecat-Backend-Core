package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestAddProfileCountryMode(t *testing.T) {
	app := newProfileCountriesApp(t)
	if err := addProfileCountryMode(app); err != nil {
		t.Fatalf("add country_mode: %v", err)
	}
	collection, err := app.FindCollectionByNameOrId("user_info")
	if err != nil {
		t.Fatal(err)
	}
	field, ok := collection.Fields.GetByName("country_mode").(*core.TextField)
	if !ok || field.Pattern != "^(auto|manual|off)$" {
		t.Fatalf("unexpected country_mode field: %#v", field)
	}
}
