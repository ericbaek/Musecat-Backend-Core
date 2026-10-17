// Package connections owns external account linking, independently of game data and partner grants.
package connections

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

const (
	Accounts = "external_account_connection"
	Services = "external_service_connection"
	Requests = "external_connection_request"
)

// EnsureSchema is shared schema only. Core bootstraps fresh databases; Full owns existing DB migrations.
// Existing incompatible collections fail closed instead of changing their rules or data.
func EnsureSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		users, err := tx.FindCollectionByNameOrId("user")
		if err != nil {
			return err
		}
		if !users.IsAuth() {
			return fmt.Errorf("user must be an auth collection")
		}
		account := core.NewBaseCollection(Accounts)
		account.Fields.Add(
			&core.RelationField{Name: "user", CollectionId: users.Id, Required: true, MaxSelect: 1, CascadeDelete: true},
			&core.TextField{Name: "provider", Required: true, Max: 128},
			&core.TextField{Name: "issuer", Required: true, Max: 2048},
			&core.TextField{Name: "subject", Required: true, Max: 1024, Hidden: true},
			&core.JSONField{Name: "scopes", MaxSize: 16384},
			&core.TextField{Name: "credentials", Hidden: true, Max: 65536},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		account.AddIndex("idx_external_account_identity", true, "issuer, subject", "")
		account.AddIndex("idx_external_account_user_issuer", true, "user, issuer", "")
		if err := EnsureLockedCollection(tx, account); err != nil {
			return err
		}
		account, err = tx.FindCollectionByNameOrId(Accounts)
		if err != nil {
			return err
		}
		service := core.NewBaseCollection(Services)
		service.Fields.Add(
			&core.RelationField{Name: "connection", CollectionId: account.Id, Required: true, MaxSelect: 1, CascadeDelete: true},
			&core.TextField{Name: "service", Required: true, Max: 128},
			&core.SelectField{Name: "visibility", Required: true, Values: []string{"private", "public"}, MaxSelect: 1},
			&core.JSONField{Name: "fields", MaxSize: 16384},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		service.AddIndex("idx_external_service_connection", true, "connection, service", "")
		if err := EnsureLockedCollection(tx, service); err != nil {
			return err
		}
		request := core.NewBaseCollection(Requests)
		request.Fields.Add(
			&core.RelationField{Name: "user", CollectionId: users.Id, Required: true, MaxSelect: 1, CascadeDelete: true},
			&core.TextField{Name: "provider", Required: true, Max: 128},
			&core.TextField{Name: "issuer", Required: true, Max: 2048},
			&core.TextField{Name: "state_hash", Required: true, Hidden: true, Max: 64},
			&core.TextField{Name: "session_hash", Required: true, Hidden: true, Max: 64},
			&core.TextField{Name: "config_hash", Required: true, Hidden: true, Max: 64},
			&core.TextField{Name: "verifier", Hidden: true, Max: 1024},
			&core.JSONField{Name: "scopes", MaxSize: 16384},
			&core.JSONField{Name: "services", MaxSize: 16384},
			&core.SelectField{Name: "status", Required: true, Values: []string{"pending", "exchanging"}, MaxSelect: 1},
			&core.DateField{Name: "expires", Required: true},
			&core.AutodateField{Name: "created", OnCreate: true},
		)
		request.AddIndex("idx_external_request_state", true, "state_hash", "")
		request.AddIndex("idx_external_request_user_issuer", true, "user, issuer", "")
		request.AddIndex("idx_external_request_expires", false, "expires", "")
		return EnsureLockedCollection(tx, request)
	})
}

// EnsureLockedCollection creates a locked base collection on a fresh database or verifies
// that an existing one is compatible (locked rules, identical fields, indexes present).
func EnsureLockedCollection(app core.App, want *core.Collection) error {
	got, err := app.FindCollectionByNameOrId(want.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return app.Save(want)
	}
	if err != nil {
		return err
	}
	if !got.IsBase() || got.System || got.ListRule != nil || got.ViewRule != nil || got.CreateRule != nil || got.UpdateRule != nil || got.DeleteRule != nil {
		return fmt.Errorf("%s must be a locked base collection", want.Name)
	}
	for _, field := range want.Fields {
		actual := got.Fields.GetByName(field.GetName())
		if actual == nil {
			return fmt.Errorf("missing %s.%s", want.Name, field.GetName())
		}
		a, _ := json.Marshal(actual)
		b, _ := json.Marshal(field)
		var left, right map[string]any
		if err := json.Unmarshal(a, &left); err != nil {
			return err
		}
		if err := json.Unmarshal(b, &right); err != nil {
			return err
		}
		delete(left, "id")
		delete(right, "id")
		if !reflect.DeepEqual(left, right) {
			return fmt.Errorf("incompatible %s.%s", want.Name, field.GetName())
		}
	}
	for _, index := range want.Indexes {
		found := false
		for _, actual := range got.Indexes {
			if actual == index {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%s index mismatch: %s", want.Name, index)
		}
	}
	return nil
}

// DeleteUserData must run in the caller's account-withdrawal transaction.
func DeleteUserData(app core.App, userID string) error {
	for _, name := range []string{JointAttempts, Requests, Accounts} {
		rows, err := app.FindRecordsByFilter(name, "user = {:user}", "", 0, 0, dbx.Params{"user": userID})
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := app.Delete(row); err != nil {
				return err
			}
		}
	}
	return nil
}
