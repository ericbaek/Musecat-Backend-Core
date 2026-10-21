package supportfeedback

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

// EnsureReplyPhotosSchema expands only the private reply attachment contract.
// Existing records and access rules are preserved, never made publicly readable.
func EnsureReplyPhotosSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		col, err := tx.FindCollectionByNameOrId(Messages)
		if err != nil {
			return err
		}
		if !col.IsBase() || col.System {
			return fmt.Errorf("incompatible %s", Messages)
		}
		message, ok := col.Fields.GetByName("message").(*core.TextField)
		if !ok || message.Max != 4000 {
			return fmt.Errorf("incompatible reply message")
		}
		field := col.Fields.GetByName("photos")
		if field == nil {
			col.Fields.Add(&core.FileField{Name: "photos", MaxSelect: 3, MaxSize: 15_000_000, Protected: true})
		} else {
			photos, ok := field.(*core.FileField)
			if !ok || photos.MaxSelect != 3 || photos.MaxSize != 15_000_000 || !photos.Protected || len(photos.MimeTypes) != 0 {
				return fmt.Errorf("incompatible reply photos")
			}
		}
		// Image-only replies are valid; the custom API requires text or attachments.
		message.Required = false
		return tx.Save(col)
	})
}
