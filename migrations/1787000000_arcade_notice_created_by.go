package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

func init() {
	migrations.Register(func(app core.App) error {
		notices, err := app.FindCollectionByNameOrId("arcade_notice")
		if err != nil {
			return fmt.Errorf("find arcade_notice: %w", err)
		}
		if notices.Fields.GetByName("createdBy") != nil {
			return nil
		}
		users, err := app.FindCollectionByNameOrId("user")
		if err != nil {
			return fmt.Errorf("find user: %w", err)
		}
		notices.Fields.Add(&core.RelationField{Name: "createdBy", CollectionId: users.Id, MaxSelect: 1})
		return app.Save(notices)
	}, func(app core.App) error { return nil })
}
