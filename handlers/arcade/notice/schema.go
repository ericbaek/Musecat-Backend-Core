package notice

import "github.com/pocketbase/pocketbase/core"

// EnsurePolicySchema defines reusable fields only; Full owns existing-data backfills.
func EnsurePolicySchema(app core.App) error {
	collection, err := app.FindCollectionByNameOrId("arcade_notice")
	if err != nil {
		return err
	}
	if collection.Fields.GetByName("title") == nil {
		collection.Fields.Add(&core.TextField{Name: "title", Max: 5000})
	}
	if collection.Fields.GetByName("display_mode") == nil {
		collection.Fields.Add(&core.SelectField{Name: "display_mode", MaxSelect: 1, Values: []string{"normal", "pinned", "required"}})
	}
	return app.Save(collection)
}
