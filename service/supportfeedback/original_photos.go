package supportfeedback

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

// EnsureOriginalPhotosSchema protects existing inquiry files without moving them
// or changing upload limits. Custom attachment reads enforce participant access.
func EnsureOriginalPhotosSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		col, err := tx.FindCollectionByNameOrId("support_feedback")
		if err != nil {
			return err
		}
		if !col.IsBase() || col.System || col.ListRule != nil || col.ViewRule != nil {
			return fmt.Errorf("incompatible support_feedback access policy")
		}
		photos, ok := col.Fields.GetByName("photos").(*core.FileField)
		if !ok {
			return fmt.Errorf("incompatible support_feedback.photos")
		}
		photos.Protected = true
		return tx.Save(col)
	})
}
