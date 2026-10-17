package connections

import (
	"fmt"
	"github.com/pocketbase/pocketbase/core"
)

const JointAttempts = "external_joint_connection"

// EnsureJointSchema adds a locked orchestration ledger; it never grants API access.
// Core bootstraps fresh databases; Full owns the guarded existing-DB migration.
func EnsureJointSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		users, err := tx.FindCollectionByNameOrId("user")
		if err != nil {
			return err
		}
		if !users.IsAuth() {
			return fmt.Errorf("user must be an auth collection")
		}
		c := core.NewBaseCollection(JointAttempts)
		c.Fields.Add(
			&core.RelationField{Name: "user", CollectionId: users.Id, Required: true, MaxSelect: 1, CascadeDelete: true},
			&core.TextField{Name: "partner", Required: true, Max: 128},
			&core.TextField{Name: "client", Required: true, Max: 128},
			&core.TextField{Name: "provider", Required: true, Max: 128},
			&core.TextField{Name: "issuer", Required: true, Max: 2048},
			&core.TextField{Name: "partner_subject", Required: true, Hidden: true, Max: 1024},
			&core.TextField{Name: "session_hash", Required: true, Hidden: true, Max: 64},
			&core.TextField{Name: "config_hash", Required: true, Hidden: true, Max: 64},
			&core.JSONField{Name: "services", MaxSize: 16384},
			&core.TextField{Name: "grant_id", Hidden: true, Max: 15},
			&core.TextField{Name: "grant_generation", Hidden: true, Max: 64},
			&core.TextField{Name: "connection_id", Hidden: true, Max: 15},
			&core.TextField{Name: "connection_revision", Hidden: true, Max: 64},
			&core.SelectField{Name: "status", Required: true, MaxSelect: 1, Values: []string{"pending", "partial", "connected", "cancelled"}},
			&core.DateField{Name: "expires", Required: true},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		c.AddIndex("idx_joint_user_partner", false, "user, partner", "")
		c.AddIndex("idx_joint_expiry", false, "status, expires", "")
		return EnsureLockedCollection(tx, c)
	})
}
