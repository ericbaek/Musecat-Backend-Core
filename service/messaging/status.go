package messaging

import (
	"fmt"
	"github.com/pocketbase/pocketbase/core"
)

// Store the transition on its triggering message, or append a dedicated history
// entry for an explicit status action. The caller owns the transaction.
func changeInquiryStatus(tx core.App, u *participant, c, q *core.Record, status string, message *core.Record) error {
	previous := q.GetString("status")
	if previous == status {
		return nil
	}
	if message == nil {
		parties, err := loadParties(tx, []Identity{{Type: u.Kind, ID: u.ID}})
		if err != nil {
			return err
		}
		sender := parties[u.Kind+":"+u.ID]
		senderUser, senderGuest := "", ""
		if u.Kind == "user" {
			senderUser = u.ID
		} else {
			senderGuest = u.ID
		}
		acting := Identity{Type: u.Kind, ID: u.ID}
		if Responder(tx, u.User, q) {
			acting = Identity{Type: q.GetString("inbox_type"), ID: q.GetString("inbox_id")}
		}
		sequence := c.GetInt("last_sequence") + 1
		message, err = record(tx, Messages, map[string]any{
			"conversation": c.Id, "sequence": sequence, "sender_user": senderUser, "sender_guest": senderGuest,
			"sender_name": sender.Name, "sender_avatar": sender.AvatarURL, "sender_roles": sender.RoleTags,
			"acting_type": acting.Type, "acting_id": acting.ID,
			"client_message_id": fmt.Sprintf("status:%s:%d", c.Id, sequence), "mentions": []string{},
		})
		if err != nil {
			return err
		}
		c.Set("last_sequence", sequence)
		if err = tx.Save(c); err != nil {
			return err
		}
	}
	message.Set("status_change", InquiryStatusChange{From: previous, To: status})
	if err := tx.Save(message); err != nil {
		return err
	}
	q.Set("status", status)
	return tx.Save(q)
}

// Fresh Core bootstrap only; Full owns its independent forward migration.
func EnsureStatusHistorySchema(app core.App) error {
	col, err := app.FindCollectionByNameOrId(Messages)
	if err != nil {
		return err
	}
	wanted := &core.JSONField{Name: "status_change", MaxSize: 256}
	if old := col.Fields.GetByName(wanted.Name); old != nil {
		if !sameField(old, wanted) {
			return fmt.Errorf("incompatible messaging status history field")
		}
		return nil
	}
	col.Fields.Add(wanted)
	return app.Save(col)
}
