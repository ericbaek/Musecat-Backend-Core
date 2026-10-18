package partnerapp

import (
	"fmt"
	"github.com/ericbaek/musecat-backend-core/connections"
	"github.com/pocketbase/pocketbase/core"
)

const CheckRuns = "partner_conformance_run"

// EnsureCheckSchema is a fresh schema contract. Full owns the independent upgrade.
func EnsureCheckSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		apps, err := tx.FindCollectionByNameOrId(Applications)
		if err != nil {
			return err
		}
		users, err := tx.FindCollectionByNameOrId("user")
		if err != nil {
			return err
		}
		if !users.IsAuth() {
			return fmt.Errorf("user must be auth")
		}
		c := core.NewBaseCollection(CheckRuns)
		c.Fields.Add(
			&core.RelationField{Name: "application", CollectionId: apps.Id, Required: true, MaxSelect: 1, CascadeDelete: true},
			&core.RelationField{Name: "actor", CollectionId: users.Id, Required: true, MaxSelect: 1},
			&core.TextField{Name: "revision", Required: true, Max: 64},
			&core.JSONField{Name: "form", Hidden: true, MaxSize: 65536},
			&core.TextField{Name: "fixtures", Hidden: true, Max: 32768},
			&core.SelectField{Name: "status", Required: true, MaxSelect: 1, Values: []string{"queued", "running", "passed", "failed", "incomplete", "cancelled"}},
			&core.JSONField{Name: "cases", MaxSize: 65536},
			&core.DateField{Name: "started"}, &core.DateField{Name: "finished"},
			&core.AutodateField{Name: "created", OnCreate: true},
		)
		c.AddIndex("idx_conformance_application", false, "application, created", "")
		c.AddIndex("idx_conformance_active", true, "application", "status = 'queued' OR status = 'running'")
		return connections.EnsureLockedCollection(tx, c)
	})
}
