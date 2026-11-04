package connections_test

import (
	"github.com/ericbaek/musecat-backend-core/connections"
	"github.com/ericbaek/musecat-backend-core/testutil"
	"github.com/pocketbase/pocketbase/core"
	"testing"
)

func TestSchemaIsIdempotentAndRejectsIncompatibleExistingFields(t *testing.T) {
	app := testutil.NewTestApp(t)
	for range 2 {
		if err := connections.EnsureSchema(app); err != nil {
			t.Fatal(err)
		}
	}
	collection, err := app.FindCollectionByNameOrId(connections.Accounts)
	if err != nil {
		t.Fatal(err)
	}
	field := collection.Fields.GetByName("credentials").(*core.TextField)
	field.Hidden = false
	if err := app.Save(collection); err != nil {
		t.Fatal(err)
	}
	if err := connections.EnsureSchema(app); err == nil {
		t.Fatal("accepted a visible credential field")
	}
}

func TestSchemaRollsBackWhenAnExistingCollectionIsIncompatible(t *testing.T) {
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir()})
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	defer app.ResetBootstrapState()
	if err := app.Save(core.NewAuthCollection("user")); err != nil {
		t.Fatal(err)
	}
	malformed := core.NewBaseCollection(connections.Services)
	if err := app.Save(malformed); err != nil {
		t.Fatal(err)
	}
	if err := connections.EnsureSchema(app); err == nil {
		t.Fatal("accepted incompatible service collection")
	}
	if _, err := app.FindCollectionByNameOrId(connections.Accounts); err == nil {
		t.Fatal("schema partially committed")
	}
	if _, err := app.FindCollectionByNameOrId(connections.Requests); err == nil {
		t.Fatal("schema partially committed")
	}
}

func TestConnectionUniquenessIsEnforcedByDatabase(t *testing.T) {
	f := newFixture(t)
	id := f.complete(f.start(f.token, "rhythm", "chunithm"))
	_, other := f.newUser()
	collection, err := f.app.FindCollectionByNameOrId(connections.Accounts)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ user, subject string }{{other.Id, "verified-subject"}, {f.user.Id, "different-subject"}} {
		record := core.NewRecord(collection)
		record.Set("user", test.user)
		record.Set("provider", "rhythm")
		record.Set("issuer", "https://rhythm.example")
		record.Set("subject", test.subject)
		if err := f.app.Save(record); err == nil {
			t.Fatal("duplicate connection bypassed database uniqueness")
		}
	}
	serviceCollection, err := f.app.FindCollectionByNameOrId(connections.Services)
	if err != nil {
		t.Fatal(err)
	}
	service := core.NewRecord(serviceCollection)
	service.Set("connection", id)
	service.Set("service", "chunithm")
	service.Set("visibility", "private")
	if err := f.app.Save(service); err == nil {
		t.Fatal("duplicate service grant bypassed database uniqueness")
	}
}
