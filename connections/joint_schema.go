package connections

import (
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

const (
	JointAttempts = "external_joint_connection"
	JointOutbox   = "external_joint_outbox"
	JointRequests = "external_joint_request"
	// JointManifest is the consent manifest version this server renders (spec 5.1).
	JointManifest = "musecat-joint/v1"
)

var jointStatuses = []string{"pending", "approving", "partial", "connected", "cancelled", "revoked"}

// jointFlowFields were added for the spec section 5 exchange; EnsureJointColumns upgrades
// installations created before them. Secrets (intent) are sealed and hidden.
func jointFlowFields() []core.Field {
	return []core.Field{
		&core.TextField{Name: "correlation", Max: 64},
		&core.TextField{Name: "generation", Hidden: true, Max: 64},
		&core.TextField{Name: "intent_hash", Hidden: true, Max: 64},
		&core.TextField{Name: "intent", Hidden: true, Max: 4096},
		&core.TextField{Name: "code_challenge", Hidden: true, Max: 128},
		&core.TextField{Name: "redirect_uri", Max: 2048},
		&core.TextField{Name: "partner_name", Max: 128},
		&core.TextField{Name: "manifest", Max: 64},
	}
}

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
			&core.SelectField{Name: "status", Required: true, MaxSelect: 1, Values: jointStatuses},
			&core.DateField{Name: "expires", Required: true},
		)
		c.Fields.Add(jointFlowFields()...)
		c.Fields.Add(
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		c.AddIndex("idx_joint_user_partner", false, "user, partner", "")
		c.AddIndex("idx_joint_expiry", false, "status, expires", "")
		c.AddIndex("idx_joint_partner_correlation", true, "partner, correlation", "correlation != ''")
		if err := EnsureLockedCollection(tx, c); err != nil {
			return err
		}
		if err := EnsureLockedCollection(tx, jointOutboxCollection()); err != nil {
			return err
		}
		return EnsureLockedCollection(tx, jointRequestCollection())
	})
}

func jointOutboxCollection() *core.Collection {
	c := core.NewBaseCollection(JointOutbox)
	c.Fields.Add(
		&core.TextField{Name: "partner", Required: true, Max: 128},
		&core.TextField{Name: "pair_id", Required: true, Max: 64},
		&core.TextField{Name: "generation", Required: true, Max: 64},
		&core.TextField{Name: "reason", Required: true, Max: 32},
		&core.NumberField{Name: "attempts", OnlyInt: true},
		&core.DateField{Name: "next_at", Required: true},
		&core.DateField{Name: "delivered"},
		&core.TextField{Name: "last_error", Max: 128},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	c.AddIndex("idx_joint_outbox_due", false, "delivered, next_at", "")
	return c
}

// EnsureJointColumns upgrades an installation that created the ledger before the
// exchange fields existed. Idempotent; shared by Core bootstrap and Full's forward migration.
func EnsureJointColumns(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		for _, name := range []string{Accounts, JointAttempts} {
			c, e := tx.FindCollectionByNameOrId(name)
			if e != nil {
				return e
			}
			if !c.IsBase() || c.System || c.ListRule != nil || c.ViewRule != nil || c.CreateRule != nil || c.UpdateRule != nil || c.DeleteRule != nil {
				return fmt.Errorf("%s must be locked", name)
			}
		}
		accounts, err := tx.FindCollectionByNameOrId(Accounts)
		if err != nil {
			return err
		}
		if accounts.Fields.GetByName("authorization_generation") == nil {
			accounts.Fields.Add(&core.TextField{Name: "authorization_generation", Hidden: true, Max: 64})
			if err := tx.Save(accounts); err != nil {
				return err
			}
		}
		c, err := tx.FindCollectionByNameOrId(JointAttempts)
		if err != nil {
			return err
		}
		changed := false
		for _, f := range jointFlowFields() {
			if c.Fields.GetByName(f.GetName()) == nil {
				c.Fields.Add(f)
				changed = true
			}
		}
		if status, ok := c.Fields.GetByName("status").(*core.SelectField); ok && len(status.Values) != len(jointStatuses) {
			status.Values = append([]string(nil), jointStatuses...)
			changed = true
		}
		hasIndex := false
		for _, idx := range c.Indexes {
			if strings.Contains(idx, "idx_joint_partner_correlation") {
				hasIndex = true
			}
		}
		if !hasIndex {
			c.AddIndex("idx_joint_partner_correlation", true, "partner, correlation", "correlation != ''")
			changed = true
		}
		if changed {
			if err := tx.Save(c); err != nil {
				return err
			}
		}
		if err := EnsureLockedCollection(tx, jointOutboxCollection()); err != nil {
			return err
		}
		return EnsureLockedCollection(tx, jointRequestCollection())
	})
}

func jointRequestCollection() *core.Collection {
	c := core.NewBaseCollection(JointRequests)
	c.Fields.Add(&core.TextField{Name: "partner", Required: true, Max: 128}, &core.TextField{Name: "request_id", Required: true, Max: 64}, &core.TextField{Name: "fingerprint", Required: true, Max: 64}, &core.TextField{Name: "response", Required: true, Hidden: true, Max: 65536}, &core.DateField{Name: "expires", Required: true})
	c.AddIndex("idx_joint_request_identity", true, "partner, request_id", "")
	return c
}
