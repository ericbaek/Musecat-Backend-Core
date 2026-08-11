package migrations

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestApplyGameCabinetSchemaFreshAndIdempotent(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("create fresh Core app: %v", err)
	}
	t.Cleanup(app.Cleanup)
	for _, name := range []string{"arcade_game", "arcade_game_atoms", "arcade_game_legacy_map", "arcade_game_migration_issue"} {
		if _, err := app.FindCollectionByNameOrId(name); err == nil {
			t.Fatalf("fresh Core unexpectedly contains legacy collection %s", name)
		}
	}
	arcades, err := app.FindCollectionByNameOrId("arcade")
	if err != nil {
		t.Fatalf("find arcade: %v", err)
	}
	if arcades.Fields.GetByName("game") != nil {
		t.Fatal("fresh Core unexpectedly contains arcade.game")
	}
	if arcades.Fields.GetByName("game_v2") == nil {
		t.Fatal("fresh Core is missing arcade.game_v2")
	}
	revisions, err := app.FindCollectionByNameOrId("arcade_game_history")
	if err != nil {
		t.Fatalf("find arcade_game_history: %v", err)
	}
	for _, name := range []string{"uncertain", "previous_version", "last_confirmed_at", "last_confirmed_by"} {
		if revisions.Fields.GetByName(name) != nil {
			t.Fatalf("fresh Core unexpectedly contains arcade_game_history.%s", name)
		}
	}

	before := cabinetSchemaFingerprint(t, app)
	if err := applyGameCabinetSchema(app); err != nil {
		t.Fatalf("reapply cabinet schema: %v", err)
	}
	after := cabinetSchemaFingerprint(t, app)
	if before != after {
		t.Fatal("idempotent schema application changed collection metadata")
	}
}

func TestApplyGameCabinetSchemaPreservesExistingGameState(t *testing.T) {
	app := core.NewBaseApp(core.BaseAppConfig{
		DataDir:       t.TempDir(),
		EncryptionEnv: "musecat_core_cabinet_migration_test",
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("bootstrap predecessor app: %v", err)
	}
	t.Cleanup(func() {
		if err := app.ResetBootstrapState(); err != nil {
			t.Errorf("reset predecessor app: %v", err)
		}
	})

	arcades := createCabinetMigrationTestCollection(t, app, "arcade",
		&core.TextField{Name: "name", Required: true},
	)
	users := createCabinetMigrationTestCollection(t, app, "user",
		&core.TextField{Name: "name", Required: true},
	)
	series := createCabinetMigrationTestCollection(t, app, "game_series",
		&core.TextField{Name: "en", Required: true},
	)
	versions := createCabinetMigrationTestCollection(t, app, "game_series_version",
		cabinetMigrationTestRelation("series", series.Id, false, true),
		&core.TextField{Name: "en", Required: true},
	)
	entries := createCabinetMigrationTestCollection(t, app, "arcade_game_id",
		cabinetMigrationTestRelation("arcade", arcades.Id, true, true),
		cabinetMigrationTestRelation("series", series.Id, false, true),
		cabinetMigrationTestRelation("created_by", users.Id, false, false),
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	batches := createCabinetMigrationTestCollection(t, app, "arcade_game_history_batch",
		cabinetMigrationTestRelation("arcade", arcades.Id, true, true),
		cabinetMigrationTestRelation("created_by", users.Id, false, false),
		&core.TextField{Name: "reason", Max: 120},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	one := float64(1)
	revisions := createCabinetMigrationTestCollection(t, app, "arcade_game_history",
		cabinetMigrationTestRelation("batch", batches.Id, true, true),
		cabinetMigrationTestRelation("entry", entries.Id, false, true),
		cabinetMigrationTestRelation("version", versions.Id, false, true),
		&core.TextField{Name: "location", Max: 500},
		&core.NumberField{Name: "quantity", OnlyInt: true, Min: &one},
		&core.JSONField{Name: "price"},
		&core.JSONField{Name: "tag"},
		&core.DateField{Name: "last_modified_at"},
		cabinetMigrationTestRelation("last_modified_by", users.Id, false, false),
		&core.BoolField{Name: "legacy_imported"},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	entries.AddIndex("idx_arcade_game_id_arcade", false, "arcade", "")
	entries.AddIndex("idx_arcade_game_id_series", false, "series", "")
	if err := app.Save(entries); err != nil {
		t.Fatalf("save entry indexes: %v", err)
	}
	batches.AddIndex("idx_arcade_game_history_batch_arcade_created", false, "arcade, created", "")
	if err := app.Save(batches); err != nil {
		t.Fatalf("save batch index: %v", err)
	}
	revisions.AddIndex("idx_arcade_game_history_batch_entry", true, "batch, entry", "")
	revisions.AddIndex("idx_arcade_game_history_entry", false, "entry", "")
	revisions.AddIndex("idx_arcade_game_history_batch_version", true, "batch, version", "")
	if err := app.Save(revisions); err != nil {
		t.Fatalf("save revision indexes: %v", err)
	}

	arcade := saveCabinetMigrationTestRecord(t, app, arcades, map[string]any{"name": "Existing arcade"})
	user := saveCabinetMigrationTestRecord(t, app, users, map[string]any{"name": "Existing user"})
	seriesRecord := saveCabinetMigrationTestRecord(t, app, series, map[string]any{"en": "Existing series"})
	version := saveCabinetMigrationTestRecord(t, app, versions, map[string]any{
		"series": seriesRecord.Id,
		"en":     "Existing version",
	})
	entry := saveCabinetMigrationTestRecord(t, app, entries, map[string]any{
		"arcade":     arcade.Id,
		"series":     seriesRecord.Id,
		"created_by": user.Id,
	})
	batch := saveCabinetMigrationTestRecord(t, app, batches, map[string]any{
		"arcade":     arcade.Id,
		"created_by": user.Id,
		"reason":     "existing reason",
	})
	revision := saveCabinetMigrationTestRecord(t, app, revisions, map[string]any{
		"batch":            batch.Id,
		"entry":            entry.Id,
		"version":          version.Id,
		"location":         "second floor",
		"quantity":         0,
		"price":            map[string]any{"currency": "KRW", "value": 1000},
		"tag":              []map[string]any{{"category": "cabinet", "note": "legacy"}},
		"last_modified_at": "2026-08-01 01:02:03.000Z",
		"last_modified_by": user.Id,
		"legacy_imported":  true,
	})

	entryFields := []string{"arcade", "series", "created_by", "created", "updated"}
	batchFields := []string{"arcade", "created_by", "reason", "created", "updated"}
	revisionFields := []string{
		"batch", "entry", "version", "location", "quantity", "price", "tag",
		"last_modified_at", "last_modified_by", "legacy_imported", "created", "updated",
	}
	beforeEntries := cabinetMigrationRecordFingerprint(t, app, entries.Name, entryFields)
	beforeBatches := cabinetMigrationRecordFingerprint(t, app, batches.Name, batchFields)
	beforeRevisions := cabinetMigrationRecordFingerprint(t, app, revisions.Name, revisionFields)

	if err := applyGameCabinetSchema(app); err != nil {
		t.Fatalf("apply cabinet schema to predecessor: %v", err)
	}
	if err := applyGameCabinetSchema(app); err != nil {
		t.Fatalf("reapply cabinet schema to predecessor: %v", err)
	}

	if got := cabinetMigrationRecordFingerprint(t, app, entries.Name, entryFields); got != beforeEntries {
		t.Fatal("arcade_game_id records changed during schema migration")
	}
	if got := cabinetMigrationRecordFingerprint(t, app, batches.Name, batchFields); got != beforeBatches {
		t.Fatal("arcade_game_revision_batch records changed during schema migration")
	}
	if got := cabinetMigrationRecordFingerprint(t, app, revisions.Name, revisionFields); got != beforeRevisions {
		t.Fatal("arcade_game_revision records changed during schema migration")
	}
	gotRevision, err := app.FindRecordById(revisions.Name, revision.Id)
	if err != nil {
		t.Fatalf("reload existing revision: %v", err)
	}
	if gotRevision.GetString("cabinet") != "" || gotRevision.GetInt("quantity") != 0 {
		t.Fatalf("existing revision cabinet/quantity changed: %#v", gotRevision)
	}
}

func TestApplyGameCabinetSchemaRejectsWrongFieldShape(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("create fresh Core app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	versions, err := app.FindCollectionByNameOrId("game_series_version")
	if err != nil {
		t.Fatal(err)
	}
	versions.Fields.RemoveByName("alias_of")
	versions.Fields.Add(&core.TextField{Name: "alias_of"})
	if err := app.Save(versions); err != nil {
		t.Fatalf("install wrong alias field fixture: %v", err)
	}

	err = applyGameCabinetSchema(app)
	if err == nil || !strings.Contains(err.Error(), "must be a relation field") {
		t.Fatalf("expected wrong relation field guard failure, got %v", err)
	}
}

func TestApplyGameCabinetSchemaRejectsWrongShapeAtomically(t *testing.T) {
	fixture := filepath.Join("..", "testdata", "pb_data")
	app, err := tests.NewTestApp(fixture)
	if err != nil {
		t.Fatalf("create Core fixture app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	revisions, err := app.FindCollectionByNameOrId("arcade_game_history")
	if err != nil {
		t.Fatal(err)
	}
	revisions.AddIndex(revisionKnownCabinetIndex, false, "batch, version, cabinet", "cabinet IS NOT NULL AND cabinet <> ''")
	if err := app.Save(revisions); err != nil {
		t.Fatalf("install wrong index fixture: %v", err)
	}

	err = app.RunInTransaction(func(tx core.App) error {
		return applyGameCabinetSchema(tx)
	})
	if err == nil || !strings.Contains(err.Error(), "incompatible definition") {
		t.Fatalf("expected wrong index guard failure, got %v", err)
	}

}

func cabinetSchemaFingerprint(t *testing.T, app core.App) string {
	t.Helper()
	collections := make([]*core.Collection, 0, 6)
	for _, name := range []string{
		"game_series",
		"game_series_version",
		"game_cabinet",
		"game_series_version_cabinet",
		"arcade_game_history",
	} {
		collection, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			t.Fatalf("find %s: %v", name, err)
		}
		collections = append(collections, collection)
	}
	payload, err := json.Marshal(collections)
	if err != nil {
		t.Fatalf("marshal schema fingerprint: %v", err)
	}
	return string(payload)
}

func createCabinetMigrationTestCollection(t *testing.T, app core.App, name string, fields ...core.Field) *core.Collection {
	t.Helper()
	collection := core.NewBaseCollection(name)
	collection.Fields.Add(fields...)
	if err := app.Save(collection); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return collection
}

func cabinetMigrationTestRelation(name, target string, cascade, required bool) *core.RelationField {
	return &core.RelationField{
		Name:          name,
		CollectionId:  target,
		CascadeDelete: cascade,
		Required:      required,
		MaxSelect:     1,
	}
}

func saveCabinetMigrationTestRecord(t *testing.T, app core.App, collection *core.Collection, values map[string]any) *core.Record {
	t.Helper()
	record := core.NewRecord(collection)
	for name, value := range values {
		record.Set(name, value)
	}
	if err := app.Save(record); err != nil {
		t.Fatalf("create %s record: %v", collection.Name, err)
	}
	return record
}

func cabinetMigrationRecordFingerprint(t *testing.T, app core.App, collection string, fields []string) string {
	t.Helper()
	records, err := app.FindRecordsByFilter(collection, "", "", 0, 0)
	if err != nil {
		t.Fatalf("list %s: %v", collection, err)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Id < records[j].Id })
	rows := make([]map[string]any, 0, len(records))
	for _, record := range records {
		row := map[string]any{"id": record.Id}
		for _, field := range fields {
			row[field] = record.Get(field)
		}
		rows = append(rows, row)
	}
	payload, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal %s fingerprint: %v", collection, err)
	}
	return string(payload)
}
