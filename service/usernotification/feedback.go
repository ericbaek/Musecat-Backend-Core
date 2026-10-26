package usernotification

import (
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
	"github.com/pocketbase/pocketbase/core"
)

func EnsureFeedbackSchema(app core.App) error {
	if err := EnsureSchema(app); err != nil {
		return err
	}
	if err := supportfeedback.EnsureSchema(app); err != nil {
		return err
	}
	return app.RunInTransaction(func(tx core.App) error {
		inbox, err := tx.FindCollectionByNameOrId(Inbox)
		if err != nil {
			return err
		}
		for _, name := range []string{"support_feedback", supportfeedback.Messages} {
			target, err := tx.FindCollectionByNameOrId(name)
			if err != nil {
				return err
			}
			field := "feedback"
			if name == supportfeedback.Messages {
				field = "feedback_message"
			}
			if old := inbox.Fields.GetByName(field); old != nil {
				got, ok := old.(*core.RelationField)
				if !ok || got.CollectionId != target.Id || got.MaxSelect != 1 || !got.CascadeDelete || got.Required {
					return fmt.Errorf("incompatible notification %s", field)
				}
			} else {
				inbox.Fields.Add(&core.RelationField{Name: field, CollectionId: target.Id, MaxSelect: 1, CascadeDelete: true})
			}
		}
		return tx.Save(inbox)
	})
}

// Pre-cutover queue entries are not delivered. Full converts every retained
// event to canonical conversation/message references before application startup.
func FeedbackNotificationDeliverable(app core.App, user, row *core.Record) bool { return false }
