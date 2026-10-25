package messaging

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/pocketbase/pocketbase/core"
)

const (
	Conversations = "messaging_conversation"
	Parties       = "messaging_party"
	Inquiries     = "messaging_inquiry"
	Messages      = "messaging_message"
	Reads         = "messaging_read"
	Preferences   = "messaging_preference"
	Blocks        = "messaging_block"
	Reports       = "messaging_report"
)

// EnsureSchema is a reusable schema contract, not a data migration or runtime hook.
// Full may call it only from its own guarded forward migration.
func EnsureSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		text := func(n string, max int) core.Field { return &core.TextField{Name: n, Max: max} }
		integer := func(n string) core.Field { return &core.NumberField{Name: n, OnlyInt: true} }
		created := func() core.Field { return &core.AutodateField{Name: "created", OnCreate: true} }
		relation := func(n, target string) core.Field {
			col, _ := tx.FindCollectionByNameOrId(target)
			id := ""
			if col != nil {
				id = col.Id
			}
			return &core.RelationField{Name: n, CollectionId: id, Required: true, MaxSelect: 1}
		}
		specs := []struct {
			name    string
			fields  func() []core.Field
			indexes []string
		}{
			{Conversations, func() []core.Field {
				return []core.Field{text("kind", 16), text("created_by", 32), text("pair_key", 100), integer("last_sequence"), text("last_message", 32), &core.DateField{Name: "last_message_at"}, created()}
			}, []string{"CREATE UNIQUE INDEX idx_messaging_pair ON messaging_conversation (pair_key) WHERE kind='dm'", "CREATE INDEX idx_messaging_latest ON messaging_conversation (last_message_at,created,id)", "CREATE INDEX idx_messaging_creator_time ON messaging_conversation (created_by,created)"}},
			{Parties, func() []core.Field {
				return []core.Field{relation("conversation", Conversations), text("party_type", 16), text("party_id", 100)}
			}, []string{"CREATE UNIQUE INDEX idx_messaging_party ON messaging_party (conversation,party_type,party_id)", "CREATE INDEX idx_messaging_party_actor ON messaging_party (party_type,party_id,conversation)"}},
			{Inquiries, func() []core.Field {
				return []core.Field{relation("conversation", Conversations), text("requester", 32), text("inbox_type", 16), text("inbox_id", 100), text("status", 16), text("legacy_feedback_id", 32)}
			}, []string{"CREATE UNIQUE INDEX idx_messaging_case ON messaging_inquiry (conversation)", "CREATE UNIQUE INDEX idx_messaging_legacy ON messaging_inquiry (legacy_feedback_id) WHERE legacy_feedback_id!=''", "CREATE INDEX idx_messaging_inbox ON messaging_inquiry (inbox_type,inbox_id,requester)"}},
			{Messages, func() []core.Field {
				return []core.Field{relation("conversation", Conversations), integer("sequence"), text("sender_user", 32), text("acting_type", 16), text("acting_id", 100), text("sender_name", 200), text("sender_avatar", 2048), &core.JSONField{Name: "sender_roles", MaxSize: 2048}, text("client_message_id", 100), text("payload_hash", 64), text("text", 13000), &core.FileField{Name: "photos", MaxSelect: 3, MaxSize: 15_000_000, Protected: true}, text("legacy_collection", 64), text("legacy_record", 32), text("legacy_message_id", 32), &core.JSONField{Name: "legacy_photos", MaxSize: 4096}, created()}
			}, []string{"CREATE UNIQUE INDEX idx_messaging_sequence ON messaging_message (conversation,sequence)", "CREATE UNIQUE INDEX idx_messaging_retry ON messaging_message (conversation,sender_user,client_message_id)", "CREATE INDEX idx_messaging_sender_time ON messaging_message (sender_user,created)", "CREATE INDEX idx_messaging_initial_retry ON messaging_message (sender_user,client_message_id) WHERE sequence=1"}},
			{Reads, func() []core.Field {
				return []core.Field{relation("conversation", Conversations), text("user", 32), integer("last_read_sequence")}
			}, []string{"CREATE UNIQUE INDEX idx_messaging_read ON messaging_read (conversation,user)"}},
			{Preferences, func() []core.Field { return []core.Field{text("user", 32), text("receive", 16)} }, []string{"CREATE UNIQUE INDEX idx_messaging_preference ON messaging_preference (user)"}},
			{Blocks, func() []core.Field { return []core.Field{text("user", 32), text("blocked_user", 32), created()} }, []string{"CREATE UNIQUE INDEX idx_messaging_block ON messaging_block (user,blocked_user)"}},
			{Reports, func() []core.Field {
				return []core.Field{text("reporter", 32), relation("message", Messages), text("reason", 2000), &core.JSONField{Name: "evidence", MaxSize: 65536}, created()}
			}, []string{"CREATE UNIQUE INDEX idx_messaging_report ON messaging_report (reporter,message)", "CREATE INDEX idx_messaging_report_time ON messaging_report (created,id)", "CREATE INDEX idx_messaging_reporter_time ON messaging_report (reporter,created)"}},
		}
		for _, spec := range specs {
			wanted := core.NewBaseCollection(spec.name)
			wanted.Fields.Add(spec.fields()...)
			wanted.Indexes = spec.indexes
			got, err := tx.FindCollectionByNameOrId(spec.name)
			if errors.Is(err, sql.ErrNoRows) {
				if err = tx.Save(wanted); err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}
			if !got.IsBase() || got.System || got.ListRule != nil || got.ViewRule != nil || got.CreateRule != nil || got.UpdateRule != nil || got.DeleteRule != nil {
				return fmt.Errorf("incompatible %s access/type", spec.name)
			}
			for _, field := range wanted.Fields {
				if field.GetName() == "id" {
					continue
				}
				old := got.Fields.GetByName(field.GetName())
				if old == nil || !sameField(old, field) {
					return fmt.Errorf("incompatible %s.%s", spec.name, field.GetName())
				}
			}
			a, b := append([]string{}, got.Indexes...), append([]string{}, wanted.Indexes...)
			slices.Sort(a)
			slices.Sort(b)
			if !reflect.DeepEqual(a, b) {
				return fmt.Errorf("incompatible %s indexes", spec.name)
			}
		}
		inbox, err := tx.FindCollectionByNameOrId("user_notification")
		if err != nil {
			return err
		}
		changed := false
		for _, pair := range [][2]string{{"conversation", Conversations}, {"message", Messages}} {
			col, err := tx.FindCollectionByNameOrId(pair[1])
			if err != nil {
				return err
			}
			wanted := &core.RelationField{Name: pair[0], CollectionId: col.Id, MaxSelect: 1}
			old := inbox.Fields.GetByName(pair[0])
			if old == nil {
				inbox.Fields.Add(wanted)
				changed = true
			} else if !sameField(old, wanted) {
				return fmt.Errorf("incompatible notification %s", pair[0])
			}
		}
		if changed {
			return tx.Save(inbox)
		}
		return nil
	})
}
func sameField(a, b core.Field) bool {
	decode := func(f core.Field) map[string]any {
		raw, _ := json.Marshal(f)
		v := map[string]any{}
		_ = json.Unmarshal(raw, &v)
		delete(v, "id")
		return v
	}
	return reflect.DeepEqual(decode(a), decode(b))
}
