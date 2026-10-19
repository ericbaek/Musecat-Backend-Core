package delegation

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/pocketbase/pocketbase/core"
	"reflect"
)

func EnsureSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		users, err := tx.FindCollectionByNameOrId("user")
		if err != nil {
			return err
		}
		if !users.IsAuth() {
			return fmt.Errorf("user must be an auth collection")
		}
		registration := core.NewBaseCollection(Registrations)
		registration.Fields.Add(&core.TextField{Name: "client", Required: true, Max: 128}, &core.TextField{Name: "config_hash", Required: true, Hidden: true, Max: 64}, &core.TextField{Name: "epoch", Required: true, Hidden: true, Max: 64}, &core.BoolField{Name: "enabled"})
		registration.AddIndex("idx_oauth_registration_client", true, "client", "")
		if err := ensureCollection(tx, registration); err != nil {
			return err
		}
		grant := core.NewBaseCollection(Grants)
		grant.Fields.Add(&core.RelationField{Name: "user", CollectionId: users.Id, Required: true, MaxSelect: 1, CascadeDelete: true}, &core.TextField{Name: "client", Required: true, Max: 128}, &core.TextField{Name: "subject", Required: true, Hidden: true, Max: 64}, &core.TextField{Name: "generation", Required: true, Hidden: true, Max: 64}, &core.TextField{Name: "config_hash", Required: true, Hidden: true, Max: 64}, &core.TextField{Name: "account_key", Required: true, Hidden: true, Max: 64}, &core.JSONField{Name: "scopes", MaxSize: 4096}, &core.BoolField{Name: "revoked"}, &core.AutodateField{Name: "created", OnCreate: true}, &core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true})
		grant.AddIndex("idx_oauth_grant_owner", true, "user, client", "")
		if err := ensureCollection(tx, grant); err != nil {
			return err
		}
		grant, err = tx.FindCollectionByNameOrId(Grants)
		if err != nil {
			return err
		}
		request := core.NewBaseCollection(Requests)
		request.Fields.Add(&core.TextField{Name: "handle_hash", Required: true, Hidden: true, Max: 64}, &core.TextField{Name: "client", Required: true, Max: 128}, &core.TextField{Name: "config_hash", Required: true, Hidden: true, Max: 64}, &core.TextField{Name: "redirect_uri", Required: true, Max: 2048}, &core.TextField{Name: "state", Required: true, Hidden: true, Max: 512}, &core.TextField{Name: "challenge", Required: true, Hidden: true, Max: 128}, &core.JSONField{Name: "scopes", MaxSize: 4096}, &core.RelationField{Name: "user", CollectionId: users.Id, MaxSelect: 1, CascadeDelete: true}, &core.TextField{Name: "session_hash", Hidden: true, Max: 64}, &core.DateField{Name: "expires", Required: true})
		request.Fields.Add(&core.TextField{Name: "nonce", Hidden: true, Max: 512})
		request.AddIndex("idx_oauth_request_handle", true, "handle_hash", "")
		request.AddIndex("idx_oauth_request_expires", false, "expires", "")
		if err := ensureCollection(tx, request); err != nil {
			return err
		}
		for _, name := range []string{Codes, Tokens} {
			c := core.NewBaseCollection(name)
			c.Fields.Add(&core.RelationField{Name: "grant", CollectionId: grant.Id, Required: true, MaxSelect: 1, CascadeDelete: true}, &core.TextField{Name: "generation", Required: true, Hidden: true, Max: 64}, &core.JSONField{Name: "scopes", MaxSize: 4096})
			if name == Codes {
				c.Fields.Add(&core.TextField{Name: "nonce", Hidden: true, Max: 512})
				c.Fields.Add(&core.TextField{Name: "code_hash", Required: true, Hidden: true, Max: 64}, &core.TextField{Name: "redirect_uri", Required: true, Max: 2048}, &core.TextField{Name: "challenge", Required: true, Hidden: true, Max: 128}, &core.DateField{Name: "expires", Required: true}, &core.BoolField{Name: "consumed"})
				c.AddIndex("idx_oauth_code_hash", true, "code_hash", "")
			} else {
				c.Fields.Add(&core.TextField{Name: "access_hash", Required: true, Hidden: true, Max: 64}, &core.TextField{Name: "refresh_hash", Required: true, Hidden: true, Max: 64}, &core.DateField{Name: "access_expires", Required: true}, &core.DateField{Name: "refresh_expires", Required: true}, &core.BoolField{Name: "refresh_used"})
				c.AddIndex("idx_oauth_access_hash", true, "access_hash", "")
				c.AddIndex("idx_oauth_refresh_hash", true, "refresh_hash", "")
			}
			if err := ensureCollection(tx, c); err != nil {
				return err
			}
		}
		return nil
	})
}
func ensureCollection(app core.App, want *core.Collection) error {
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

// EnsureOIDCSchema adds only hidden nonce storage to the locked OAuth ledgers.
func EnsureOIDCSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		for _, name := range []string{Requests, Codes} {
			c, err := tx.FindCollectionByNameOrId(name)
			if err != nil {
				return err
			}
			if !c.IsBase() || c.System || c.ListRule != nil || c.ViewRule != nil || c.CreateRule != nil || c.UpdateRule != nil || c.DeleteRule != nil {
				return fmt.Errorf("%s must be locked", name)
			}
			if f := c.Fields.GetByName("nonce"); f != nil {
				if t, ok := f.(*core.TextField); !ok || !t.Hidden || t.Max != 512 || t.Required {
					return fmt.Errorf("incompatible %s.nonce", name)
				}
				continue
			}
			c.Fields.Add(&core.TextField{Name: "nonce", Hidden: true, Max: 512})
			if err := tx.Save(c); err != nil {
				return err
			}
		}
		return nil
	})
}
