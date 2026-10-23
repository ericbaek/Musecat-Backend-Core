package usernotification

import (
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/arcadereport"
	"github.com/pocketbase/pocketbase/core"
)

// EnsureConsolidatedSchema defines the canonical empty-bootstrap contract.
// Full must migrate populated legacy preferences before calling this helper.
func EnsureConsolidatedSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		device, err := tx.FindCollectionByNameOrId(Devices)
		if err != nil {
			return err
		}
		prefs, err := tx.FindCollectionByNameOrId(arcadereport.Subscriptions)
		if err != nil {
			return err
		}
		if !prefs.IsBase() || prefs.System || prefs.ListRule != nil || prefs.ViewRule != nil || prefs.CreateRule != nil || prefs.UpdateRule != nil || prefs.DeleteRule != nil {
			return fmt.Errorf("incompatible report preference access policy")
		}
		legacy := prefs.Fields.GetByName("endpoint") != nil || prefs.Fields.GetByName("subscription") != nil || prefs.Fields.GetByName("user") != nil
		if legacy {
			rows, err := tx.FindRecordsByFilter(prefs.Id, "", "", 1, 0)
			if err != nil {
				return err
			}
			if len(rows) > 0 {
				return fmt.Errorf("legacy report preferences require the Full consolidation migration")
			}
			for _, name := range []string{"user", "endpoint", "subscription"} {
				prefs.Fields.RemoveByName(name)
			}
			prefs.RemoveIndex("idx_report_subscription_identity")
		}
		if existing := prefs.Fields.GetByName("device"); existing == nil {
			prefs.Fields.Add(&core.RelationField{Name: "device", CollectionId: device.Id, Required: true, MaxSelect: 1, CascadeDelete: true})
		} else if f, ok := existing.(*core.RelationField); !ok || f.CollectionId != device.Id || !f.Required || f.MaxSelect != 1 || !f.CascadeDelete {
			return fmt.Errorf("incompatible report preference device relation")
		}
		if field := prefs.Fields.GetByName("enabled"); field == nil {
			rows, err := tx.FindRecordsByFilter(prefs.Id, "", "", 1, 0)
			if err != nil {
				return err
			}
			if len(rows) > 0 {
				return fmt.Errorf("populated preferences require enabled backfill")
			}
			prefs.Fields.Add(&core.BoolField{Name: "enabled"})
		} else if _, ok := field.(*core.BoolField); !ok {
			return fmt.Errorf("incompatible report preference enabled flag")
		}

		prefs.AddIndex("idx_report_preference_device", true, "arcade,device", "")
		if err := tx.Save(prefs); err != nil {
			return err
		}
		inbox, err := tx.FindCollectionByNameOrId(Inbox)
		if err != nil {
			return err
		}
		inbox.AddIndex("idx_notification_list", false, "user,created DESC,id DESC", "")
		if err := tx.Save(inbox); err != nil {
			return err
		}
		archive, err := tx.FindCollectionByNameOrId(arcadereport.Deliveries)
		if err != nil {
			return err
		}
		for _, name := range []string{"mapped_delivery", "mapped_notification", "mapped_device"} {
			if field := archive.Fields.GetByName(name); field == nil {
				archive.Fields.Add(&core.TextField{Name: name, Max: 15})
			} else if _, ok := field.(*core.TextField); !ok {
				return fmt.Errorf("incompatible archive mapping %s", name)
			}
		}
		if field := archive.Fields.GetByName("mapped_completed_at"); field == nil {
			archive.Fields.Add(&core.DateField{Name: "mapped_completed_at"})
		} else if _, ok := field.(*core.DateField); !ok {
			return fmt.Errorf("incompatible archive completion marker")
		}
		archive.AddIndex("idx_report_archive_mapping", false, "mapped_delivery", "")
		if err := tx.Save(archive); err != nil {
			return err
		}

		deliveries, err := tx.FindCollectionByNameOrId(Deliveries)
		if err != nil {
			return err
		}
		if field := deliveries.Fields.GetByName("completed_at"); field == nil {
			deliveries.Fields.Add(&core.DateField{Name: "completed_at"})
		} else if _, ok := field.(*core.DateField); !ok {
			return fmt.Errorf("incompatible delivery completion date")
		}
		deliveries.AddIndex("idx_notification_due", false, "next_attempt,created,id", "done=false AND attempts<5")
		deliveries.AddIndex("idx_notification_retention", false, "completed_at", "done=true")
		return tx.Save(deliveries)
	})
}
