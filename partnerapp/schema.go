// Package partnerapp implements the Developer Center: self-service partner
// application registration, operator approval and hot activation of the
// resulting OAuth client registration. It never stores plaintext secrets.
package partnerapp

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"

	"github.com/ericbaek/musecat-backend-core/connections"
)

const (
	Applications = "partner_application"
	Events       = "partner_application_event"

	StatusDraft     = "draft"
	StatusSubmitted = "submitted"
	StatusApproved  = "approved"
	StatusRejected  = "rejected"
	StatusSuspended = "suspended"
)

var statuses = []string{StatusDraft, StatusSubmitted, StatusApproved, StatusRejected, StatusSuspended}

var eventKinds = []string{"created", "updated", "submitted", "approved", "rejected", "suspended", "reinstated", "secret_generated", "credentials_set", "credentials_cleared"}

// directionBCredentialsField stores the partner-issued Musecat client credentials,
// AES-GCM sealed with the connection key. Never projected.
func directionBCredentialsField() core.Field {
	return &core.TextField{Name: "direction_b_credentials", Hidden: true, Max: 4096}
}

// EnsureDirectionBColumn adds the credentials field and new event kinds to an
// installation created before Direction B activation existed. Idempotent; shared by
// Core's fresh bootstrap and Full's guarded forward migration.
func EnsureDirectionBColumn(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		apps, err := tx.FindCollectionByNameOrId(Applications)
		if err != nil {
			return err
		}
		if apps.Fields.GetByName("direction_b_credentials") == nil {
			apps.Fields.Add(directionBCredentialsField())
			if err := tx.Save(apps); err != nil {
				return err
			}
		}
		events, err := tx.FindCollectionByNameOrId(Events)
		if err != nil {
			return err
		}
		kind, ok := events.Fields.GetByName("kind").(*core.SelectField)
		if !ok {
			return fmt.Errorf("%s.kind must be a select field", Events)
		}
		if len(kind.Values) != len(eventKinds) {
			kind.Values = append([]string(nil), eventKinds...)
			return tx.Save(events)
		}
		return nil
	})
}

// EnsureSchema creates the locked Developer Center collections on a fresh
// database or verifies compatibility. Full owns the guarded forward migration
// for existing installations and must not import Core's migration registry.
func EnsureSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		users, err := tx.FindCollectionByNameOrId("user")
		if err != nil {
			return err
		}
		if !users.IsAuth() {
			return fmt.Errorf("user must be an auth collection")
		}
		apps := core.NewBaseCollection(Applications)
		apps.Fields.Add(
			&core.TextField{Name: "partner_id", Required: true, Max: 128},
			&core.RelationField{Name: "owner", CollectionId: users.Id, Required: true, MaxSelect: 1, CascadeDelete: false},
			&core.SelectField{Name: "status", Required: true, MaxSelect: 1, Values: statuses},
			&core.JSONField{Name: "submitted_form", MaxSize: 65536},
			&core.JSONField{Name: "approved_form", MaxSize: 65536},
			&core.TextField{Name: "approved_hash", Max: 64},
			&core.RelationField{Name: "approved_by", CollectionId: users.Id, MaxSelect: 1},
			&core.DateField{Name: "approved_at"},
			&core.TextField{Name: "review_note", Max: 2000},
			&core.TextField{Name: "secret_hash", Hidden: true, Max: 64},
			directionBCredentialsField(),
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		apps.AddIndex("idx_partner_application_partner", true, "partner_id", "")
		apps.AddIndex("idx_partner_application_owner", false, "owner, status", "")
		apps.AddIndex("idx_partner_application_status", false, "status, updated", "")
		if err := connections.EnsureLockedCollection(tx, apps); err != nil {
			return err
		}
		saved, err := tx.FindCollectionByNameOrId(Applications)
		if err != nil {
			return err
		}
		events := core.NewBaseCollection(Events)
		events.Fields.Add(
			&core.RelationField{Name: "application", CollectionId: saved.Id, Required: true, MaxSelect: 1, CascadeDelete: true},
			&core.RelationField{Name: "actor", CollectionId: users.Id, MaxSelect: 1},
			&core.SelectField{Name: "kind", Required: true, MaxSelect: 1, Values: eventKinds},
			&core.TextField{Name: "note", Max: 2000},
			&core.AutodateField{Name: "created", OnCreate: true},
		)
		events.AddIndex("idx_partner_application_event_app", false, "application, created", "")
		return connections.EnsureLockedCollection(tx, events)
	})
}
