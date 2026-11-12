package communitymention

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

// ArcadeIDs projects validated mentions into a stable, deduplicated relation.
func ArcadeIDs(mentions []Mention) []string {
	ids := []string{}
	seen := map[string]bool{}
	for _, mention := range mentions {
		if mention.Kind == "arcade" && !seen[mention.ID] {
			ids = append(ids, mention.ID)
			seen[mention.ID] = true
		}
	}
	return ids
}

// EnsureArcadeRelations defines the relation for a fresh Core database.
func EnsureArcadeRelations(app core.App) error {
	posts, err := app.FindCollectionByNameOrId("community_post")
	if err != nil {
		return err
	}
	arcades, err := app.FindCollectionByNameOrId("arcade")
	if err != nil {
		return err
	}
	if !posts.IsBase() || posts.System || posts.ListRule != nil || posts.ViewRule != nil || posts.CreateRule != nil || posts.UpdateRule != nil || posts.DeleteRule != nil {
		return fmt.Errorf("community_post must be a locked base collection")
	}
	if old := posts.Fields.GetByName("arcades"); old != nil {
		field, ok := old.(*core.RelationField)
		if !ok || field.CollectionId != arcades.Id || field.Required || field.CascadeDelete || field.MinSelect != 0 || field.MaxSelect != 10 {
			return fmt.Errorf("incompatible community_post.arcades relation")
		}
		return nil
	}
	posts.Fields.Add(&core.RelationField{Name: "arcades", CollectionId: arcades.Id, MaxSelect: 10})
	return app.Save(posts)
}
