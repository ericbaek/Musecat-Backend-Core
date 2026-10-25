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
func FeedbackReplyTx(app core.App, feedback, message *core.Record) error {
	recipients := map[string]bool{}
	actor := message.GetString("author")
	if message.GetString("author") != feedback.GetString("createdBy") && (message.GetBool("staff") || message.GetBool("official")) {
		recipients[feedback.GetString("createdBy")] = true
	} else {
		users, err := supportfeedback.Responders(app, feedback)
		if err != nil {
			return err
		}
		for _, user := range users {
			if supportfeedback.IsStaff(user) || supportfeedback.IsOfficial(app, user, feedback) {
				recipients[user.Id] = true
			}
		}
	}
	for id := range recipients {
		if id == "" || id == actor {
			continue
		}
		user, err := app.FindRecordById("user", id)
		if err != nil || !supportfeedback.CanAccess(app, user, feedback) {
			continue
		}
		col, err := app.FindCollectionByNameOrId(Inbox)
		if err != nil {
			return err
		}
		row := core.NewRecord(col)
		row.Set("user", id)
		row.Set("actor", actor)
		row.Set("kind", "feedback_reply")
		row.Set("event_key", "feedback:"+message.Id)
		row.Set("feedback", feedback.Id)
		row.Set("feedback_message", message.Id)
		if err := app.Save(row); err != nil {
			return err
		}
		if err := enqueueDevicesTx(app, row, id); err != nil {
			return err
		}
	}
	return nil
}
func feedbackVisible(app core.App, user, row *core.Record) bool {
	if user == nil || row.GetString("user") != user.Id {
		return false
	}
	feedback, err := app.FindRecordById("support_feedback", row.GetString("feedback"))
	return err == nil && supportfeedback.CanAccess(app, user, feedback)
}
func feedbackItem(app core.App, row *core.Record) map[string]any {
	message := ""
	if reply, err := app.FindRecordById(supportfeedback.Messages, row.GetString("feedback_message")); err == nil {
		r := []rune(reply.GetString("message"))
		if len(r) > 160 {
			r = r[:160]
		}
		message = string(r)
	}
	arcadeName := "MUSECAT"
	if feedback, err := app.FindRecordById("support_feedback", row.GetString("feedback")); err == nil && feedback.GetString("arcade") != "" {
		if arcade, err := app.FindRecordById("arcade", feedback.GetString("arcade")); err == nil {
			if basic, err := app.FindRecordById("arcade_basic", arcade.GetString("basic")); err == nil {
				arcadeName = basic.GetString("name")
			}
		}
	}
	return map[string]any{"id": row.Id, "kind": row.GetString("kind"), "read": row.GetBool("read"), "created": row.GetString("created"), "arcadeName": arcadeName, "gameName": "", "reaction": "", "message": message, "url": "/updates/feedback?thread=" + row.GetString("feedback")}
}

func FeedbackCreatedTx(app core.App, feedback *core.Record) error {
	if feedback.GetString("arcade") == "" {
		return nil
	}
	users, err := supportfeedback.Responders(app, feedback)
	if err != nil {
		return err
	}
	for _, user := range users {
		if user.Id == feedback.GetString("createdBy") || (!supportfeedback.IsStaff(user) && !supportfeedback.IsOfficial(app, user, feedback)) {
			continue
		}
		col, err := app.FindCollectionByNameOrId(Inbox)
		if err != nil {
			return err
		}
		row := core.NewRecord(col)
		row.Set("user", user.Id)
		row.Set("actor", feedback.GetString("createdBy"))
		row.Set("kind", "feedback_created")
		row.Set("event_key", "feedback-created:"+feedback.Id)
		row.Set("feedback", feedback.Id)
		if err := app.Save(row); err != nil {
			return err
		}
		if err := enqueueDevicesTx(app, row, user.Id); err != nil {
			return err
		}
	}
	return nil
}
