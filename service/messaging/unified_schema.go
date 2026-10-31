package messaging

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

// EnsureUnifiedSchema upgrades only the known messaging contract. Full owns its
// invocation against existing databases; Core uses it on a fresh bootstrap.
func EnsureUnifiedSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		for _, name := range []string{Inquiries, Messages} {
			col, err := tx.FindCollectionByNameOrId(name)
			if err != nil {
				return err
			}
			if !col.IsBase() || col.System || col.ListRule != nil || col.ViewRule != nil || col.CreateRule != nil || col.UpdateRule != nil || col.DeleteRule != nil {
				return fmt.Errorf("incompatible %s access/type", name)
			}
			fields := []core.Field{&core.TextField{Name: "sender_guest", Max: 32}}
			if name == Inquiries {
				fields = []core.Field{&core.TextField{Name: "requester_type", Max: 16}, &core.TextField{Name: "guest_key_hash", Max: 64, Hidden: true}}
			}
			for _, f := range fields {
				if old := col.Fields.GetByName(f.GetName()); old == nil {
					col.Fields.Add(f)
				} else if !sameField(old, f) {
					return fmt.Errorf("incompatible %s.%s", name, f.GetName())
				}
			}
			if name == Messages {
				col.RemoveIndex("idx_messaging_retry")
				col.RemoveIndex("idx_messaging_initial_retry")
				col.Indexes = append(col.Indexes, "CREATE UNIQUE INDEX idx_messaging_retry ON messaging_message (conversation,sender_user,sender_guest,client_message_id) WHERE sender_user!='' OR sender_guest!=''")
				col.Indexes = append(col.Indexes, "CREATE INDEX idx_messaging_initial_retry ON messaging_message (sender_user,sender_guest,client_message_id) WHERE sequence=1")
			} else {
				col.RemoveIndex("idx_messaging_guest")
				col.Indexes = append(col.Indexes, "CREATE INDEX idx_messaging_guest ON messaging_inquiry (guest_key_hash) WHERE guest_key_hash!=''")
			}
			if err := tx.Save(col); err != nil {
				return err
			}
		}
		return EnsureSchema(tx)
	})
}
