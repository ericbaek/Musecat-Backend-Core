package migrations

import (
	"fmt"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"reflect"
)

func init() { m.Register(applyCommunityCommentMedia, func(core.App) error { return nil }) }
func applyCommunityCommentMedia(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		collection, err := tx.FindCollectionByNameOrId("community_comment")
		if err != nil {
			return err
		}
		if collection.Type != core.CollectionTypeBase || collection.ListRule != nil || collection.ViewRule != nil || collection.CreateRule != nil || collection.UpdateRule != nil || collection.DeleteRule != nil {
			return fmt.Errorf("community_comment must be a locked base collection")
		}
		expected := &core.FileField{Name: "photos", MaxSelect: 3, MaxSize: 10 << 20, MimeTypes: []string{"image/jpeg", "image/png", "image/webp", "image/gif"}, Thumbs: []string{"600x600", "1200x0"}}
		if field := collection.Fields.GetByName("photos"); field == nil {
			collection.Fields.Add(expected)
		} else {
			actual, ok := field.(*core.FileField)
			if !ok || actual.MaxSelect != expected.MaxSelect || actual.MaxSize != expected.MaxSize || actual.Required || actual.Protected || !reflect.DeepEqual(actual.MimeTypes, expected.MimeTypes) || !reflect.DeepEqual(actual.Thumbs, expected.Thumbs) {
				return fmt.Errorf("community_comment.photos is incompatible")
			}
		}
		return tx.Save(collection)
	})
}
