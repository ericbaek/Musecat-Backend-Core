package usernotification

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/pocketbase/pocketbase/core"
)

const Inbox = "user_notification"
const Devices = "user_notification_device"
const Deliveries = "user_notification_delivery"

func EnsureSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		user, err := tx.FindCollectionByNameOrId("user")
		if err != nil {
			return err
		}
		arcade, err := tx.FindCollectionByNameOrId("arcade")
		if err != nil {
			return err
		}
		flag, err := tx.FindCollectionByNameOrId("arcade_flag")
		if err != nil {
			return err
		}
		reaction, err := tx.FindCollectionByNameOrId("arcade_flag_reaction")
		if err != nil {
			return err
		}
		relation := func(name, id string, required, cascade bool) core.Field {
			return &core.RelationField{Name: name, CollectionId: id, Required: required, MaxSelect: 1, CascadeDelete: cascade}
		}
		defs := []struct {
			name, index, columns string
			fields               []core.Field
		}{
			{Devices, "idx_notification_device_endpoint", "endpoint", []core.Field{relation("user", user.Id, true, true), &core.TextField{Name: "endpoint", Required: true, Max: 2048}, &core.JSONField{Name: "subscription", Required: true, MaxSize: 8192}, &core.TextField{Name: "locale", Max: 8}}},
			{Inbox, "idx_notification_event", "user,event_key", []core.Field{relation("user", user.Id, true, true), relation("actor", user.Id, false, false), relation("arcade", arcade.Id, false, true), relation("flag", flag.Id, false, true), relation("reaction_record", reaction.Id, false, true), &core.TextField{Name: "event_key", Required: true, Max: 100}, &core.TextField{Name: "kind", Required: true, Max: 40}, &core.BoolField{Name: "read"}}},
		}
		for _, def := range defs {
			col, err := tx.FindCollectionByNameOrId(def.name)
			if errors.Is(err, sql.ErrNoRows) {
				col = core.NewBaseCollection(def.name)
			} else if err != nil {
				return err
			} else if !col.IsBase() || col.System {
				return fmt.Errorf("incompatible %s", def.name)
			}
			for _, f := range append(def.fields, &core.AutodateField{Name: "created", OnCreate: true}) {
				existing := col.Fields.GetByName(f.GetName())
				if existing == nil {
					col.Fields.Add(f)
				} else if existing.Type() != f.Type() {
					return fmt.Errorf("incompatible %s.%s", def.name, f.GetName())
				}
				if want, ok := f.(*core.RelationField); ok && existing != nil {
					got := existing.(*core.RelationField)
					if def.name == Inbox && (f.GetName() == "arcade" || f.GetName() == "flag") && got.CollectionId == want.CollectionId && got.MaxSelect == 1 && got.CascadeDelete {
						got.Required = false
					}
					if want.CollectionId != got.CollectionId || want.CascadeDelete != got.CascadeDelete || want.Required != got.Required || want.MaxSelect != got.MaxSelect {
						return fmt.Errorf("incompatible relation %s.%s", def.name, f.GetName())
					}
				}
			}
			col.ListRule = nil
			col.ViewRule = nil
			col.CreateRule = nil
			col.UpdateRule = nil
			col.DeleteRule = nil
			col.AddIndex(def.index, true, def.columns, "")
			if def.name == Inbox {
				col.AddIndex("idx_notification_inbox", false, "user,read,created", "")
			}
			if err := tx.Save(col); err != nil {
				return err
			}
		}
		inbox, err := tx.FindCollectionByNameOrId(Inbox)
		if err != nil {
			return err
		}
		device, err := tx.FindCollectionByNameOrId(Devices)
		if err != nil {
			return err
		}
		col, err := tx.FindCollectionByNameOrId(Deliveries)
		if errors.Is(err, sql.ErrNoRows) {
			col = core.NewBaseCollection(Deliveries)
		} else if err != nil {
			return err
		} else if !col.IsBase() || col.System {
			return fmt.Errorf("incompatible %s", Deliveries)
		}
		fields := []core.Field{relation("notification", inbox.Id, true, true), relation("device", device.Id, true, true), &core.NumberField{Name: "attempts", OnlyInt: true}, &core.DateField{Name: "next_attempt"}, &core.DateField{Name: "sent_at"}, &core.BoolField{Name: "done"}, &core.AutodateField{Name: "created", OnCreate: true}}
		for _, f := range fields {
			if old := col.Fields.GetByName(f.GetName()); old == nil {
				col.Fields.Add(f)
			} else if old.Type() != f.Type() {
				return fmt.Errorf("incompatible %s.%s", Deliveries, f.GetName())
			}
			if want, ok := f.(*core.RelationField); ok {
				if got, ok := col.Fields.GetByName(f.GetName()).(*core.RelationField); !ok || got.CollectionId != want.CollectionId || got.Required != want.Required || got.MaxSelect != want.MaxSelect || got.CascadeDelete != want.CascadeDelete {
					return fmt.Errorf("incompatible relation %s.%s", Deliveries, f.GetName())
				}
			}

		}
		col.ListRule = nil
		col.ViewRule = nil
		col.CreateRule = nil
		col.UpdateRule = nil
		col.DeleteRule = nil
		col.AddIndex("idx_notification_delivery", true, "notification,device", "")
		if err := tx.Save(col); err != nil {
			return err
		}
		return nil
	})
}
