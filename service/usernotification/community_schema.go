package usernotification

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/pocketbase/pocketbase/core"
)

const CommunityViews = "community_post_view"

// EnsureCommunitySchema adds optional community references to the existing inbox.
// It is idempotent and rejects incompatible fields without rewriting existing data.
func EnsureCommunitySchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		inbox, err := tx.FindCollectionByNameOrId(Inbox)
		if err != nil {
			return err
		}
		if !inbox.IsBase() || inbox.System || inbox.ListRule != nil || inbox.ViewRule != nil || inbox.CreateRule != nil || inbox.UpdateRule != nil || inbox.DeleteRule != nil {
			return fmt.Errorf("incompatible notification inbox")
		}
		targets := map[string]string{}
		for _, name := range []string{"user", "community_post", "community_comment"} {
			col, err := tx.FindCollectionByNameOrId(name)
			if err != nil {
				return err
			}
			targets[name] = col.Id
		}
		for _, spec := range []struct {
			name, target string
			cascade      bool
		}{{"community_post", "community_post", true}, {"community_comment", "community_comment", true}, {"community_thread", "community_comment", false}} {
			if old := inbox.Fields.GetByName(spec.name); old != nil {
				f, ok := old.(*core.RelationField)
				if !ok || f.CollectionId != targets[spec.target] || f.MaxSelect != 1 || f.Required || f.CascadeDelete != spec.cascade {
					return fmt.Errorf("incompatible notification %s", spec.name)
				}
			} else {
				inbox.Fields.Add(&core.RelationField{Name: spec.name, CollectionId: targets[spec.target], MaxSelect: 1, CascadeDelete: spec.cascade})
			}
		}
		inbox.AddIndex("idx_notification_community_group", false, "user,kind,community_post,community_thread,created DESC,id DESC", "community_post!=''")
		if err := tx.Save(inbox); err != nil {
			return err
		}
		views, err := tx.FindCollectionByNameOrId(CommunityViews)
		if errors.Is(err, sql.ErrNoRows) {
			views = core.NewBaseCollection(CommunityViews)
		} else if err != nil {
			return err
		}
		if !views.IsBase() || views.System || views.ListRule != nil || views.ViewRule != nil || views.CreateRule != nil || views.UpdateRule != nil || views.DeleteRule != nil {
			return fmt.Errorf("incompatible community views")
		}
		fields := []core.Field{&core.RelationField{Name: "user", CollectionId: targets["user"], Required: true, MaxSelect: 1, CascadeDelete: true}, &core.RelationField{Name: "post", CollectionId: targets["community_post"], Required: true, MaxSelect: 1, CascadeDelete: true}, &core.TextField{Name: "session", Required: true, Max: 64}, &core.DateField{Name: "expires", Required: true}}
		for _, field := range fields {
			old := views.Fields.GetByName(field.GetName())
			if old == nil {
				views.Fields.Add(field)
				continue
			}
			if old.Type() != field.Type() {
				return fmt.Errorf("incompatible community view %s", field.GetName())
			}
			if want, ok := field.(*core.TextField); ok {
				got := old.(*core.TextField)
				if got.Required != want.Required || got.Max != want.Max {
					return fmt.Errorf("incompatible community view session")
				}
			}
			if want, ok := field.(*core.DateField); ok {
				got := old.(*core.DateField)
				if got.Required != want.Required {
					return fmt.Errorf("incompatible community view expiry")
				}
			}
			if want, ok := field.(*core.RelationField); ok {
				got := old.(*core.RelationField)
				if got.CollectionId != want.CollectionId || got.MaxSelect != 1 || !got.Required || !got.CascadeDelete {
					return fmt.Errorf("incompatible community view relation")
				}
			}
		}
		views.ListRule = nil
		views.ViewRule = nil
		views.CreateRule = nil
		views.UpdateRule = nil
		views.DeleteRule = nil
		views.AddIndex("idx_community_view_session", true, "user,post,session", "")
		views.AddIndex("idx_community_view_active", false, "user,post,expires", "")
		return tx.Save(views)
	})
}
