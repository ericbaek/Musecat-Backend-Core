package arcade_test

import (
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/dbutils"
)

func TestGameCabinetSchema(t *testing.T) {
	app := newArcadeTestApp(t)

	versions := requireSchemaCollection(t, app, "game_series_version")
	cabinets := requireSchemaCollection(t, app, "game_cabinet")
	versionCabinets := requireSchemaCollection(t, app, "game_series_version_cabinet")
	revisions := requireSchemaCollection(t, app, "arcade_game_history")

	assertSchemaRelation(t, versions, "alias_of", versions.Id, false, false)
	if versions.Fields.GetByName("cabinets") != nil || revisions.Fields.GetByName("cabinets") != nil {
		t.Fatal("plural cabinet draft fields must not exist")
	}

	assertLockedRules(t, cabinets)
	for _, name := range []string{"en", "kr", "jp"} {
		field, ok := cabinets.Fields.GetByName(name).(*core.TextField)
		if !ok || !field.Required || field.Min != 0 || field.Max != 0 || field.Pattern != "" || field.AutogeneratePattern != "" || field.PrimaryKey {
			t.Fatalf("unexpected game_cabinet.%s field: %#v", name, field)
		}
	}
	assertAutodate(t, cabinets, "created", true, false)
	assertAutodate(t, cabinets, "updated", true, true)

	assertLockedRules(t, versionCabinets)
	assertSchemaRelation(t, versionCabinets, "version", versions.Id, true, true)
	assertSchemaRelation(t, versionCabinets, "cabinet", cabinets.Id, true, true)
	priceDefault, ok := versionCabinets.Fields.GetByName("price_default").(*core.JSONField)
	if !ok || priceDefault.Required || priceDefault.MaxSize != 0 {
		t.Fatalf("unexpected game_series_version_cabinet.price_default: %#v", priceDefault)
	}
	assertAutodate(t, versionCabinets, "created", true, false)
	assertAutodate(t, versionCabinets, "updated", true, true)
	assertSchemaIndex(t, versionCabinets, "idx_game_series_version_cabinet_version_cabinet", true, []string{"version", "cabinet"}, "")
	assertSchemaIndex(t, versionCabinets, "idx_game_series_version_cabinet_cabinet_version", false, []string{"cabinet", "version"}, "")

	assertSchemaRelation(t, revisions, "cabinet", cabinets.Id, false, false)
	quantity, ok := revisions.Fields.GetByName("quantity").(*core.NumberField)
	if !ok || !quantity.OnlyInt || quantity.Required || quantity.Min == nil || *quantity.Min != 0 {
		t.Fatalf("unexpected arcade_game_revision.quantity: %#v", quantity)
	}
	assertSchemaIndex(t, revisions, "idx_arcade_game_history_batch_entry", true, []string{"batch", "entry"}, "")
	assertSchemaIndex(t, revisions, "idx_arcade_game_history_batch_version_cabinet", true, []string{"batch", "version", "cabinet"}, "cabinet IS NOT NULL AND cabinet <> ''")
	assertSchemaIndex(t, revisions, "idx_arcade_game_history_batch_version_unknown", true, []string{"batch", "version"}, "legacy_imported = 0 AND cabinet IS NULL OR legacy_imported = 0 AND cabinet = ''")
	if revisions.GetIndex("idx_arcade_game_history_batch_version") != "" {
		t.Fatal("legacy unique (batch, version) index must be removed")
	}

	for _, collectionName := range []string{"game_cabinet", "game_series_version_cabinet"} {
		records, err := app.FindRecordsByFilter(collectionName, "", "", 0, 0)
		if err != nil {
			t.Fatalf("list %s: %v", collectionName, err)
		}
		if len(records) != 0 {
			t.Fatalf("schema-only migration populated %s", collectionName)
		}
	}
	for _, revision := range mustFindRecords(t, app, "arcade_game_history") {
		if revision.GetString("cabinet") != "" {
			t.Fatalf("schema-only migration backfilled revision %s cabinet", revision.Id)
		}
	}
}

func TestGameCabinetSchemaRevisionUniqueness(t *testing.T) {
	app := newArcadeTestApp(t)
	arcadeID, _ := seedArcade(t, app, "", arcadeSeed{Name: "Cabinet schema", Address: "Schema street", Location: location{Lat: 37.5, Lon: 127.0}})

	seriesCollection := requireSchemaCollection(t, app, "game_series")
	series := core.NewRecord(seriesCollection)
	series.Set("en", "Cabinet test series")
	if err := app.Save(series); err != nil {
		t.Fatal(err)
	}

	versionCollection := requireSchemaCollection(t, app, "game_series_version")
	version := core.NewRecord(versionCollection)
	version.Set("series", series.Id)
	version.Set("en", "Cabinet test version")
	if err := app.Save(version); err != nil {
		t.Fatal(err)
	}
	otherVersion := core.NewRecord(versionCollection)
	otherVersion.Set("series", series.Id)
	otherVersion.Set("en", "Other cabinet test version")
	if err := app.Save(otherVersion); err != nil {
		t.Fatal(err)
	}

	cabinetCollection := requireSchemaCollection(t, app, "game_cabinet")
	cabinetA := newCabinetRecord(cabinetCollection, "Cabinet A")
	if err := app.Save(cabinetA); err != nil {
		t.Fatal(err)
	}
	cabinetB := newCabinetRecord(cabinetCollection, "Cabinet B")
	if err := app.Save(cabinetB); err != nil {
		t.Fatal(err)
	}

	unknownBatch := newGameSchemaBatch(t, app, arcadeID)
	unknownEntry := newGameSchemaEntry(t, app, arcadeID, series.Id)
	unknown := newGameSchemaRevision(app, unknownBatch, unknownEntry, version.Id, "", 0)
	if err := app.Save(unknown); err != nil {
		t.Fatalf("save first unknown revision: %v", err)
	}
	if got := unknown.GetInt("quantity"); got != 0 {
		t.Fatalf("quantity 0 changed to %d", got)
	}
	duplicateUnknown := newGameSchemaRevision(app, unknownBatch, newGameSchemaEntry(t, app, arcadeID, series.Id), version.Id, "", 1)
	if err := app.Save(duplicateUnknown); err == nil {
		t.Fatal("expected duplicate unknown cabinet to fail")
	}

	historicalBatch := newGameSchemaBatch(t, app, arcadeID)
	historicalOne := newGameSchemaRevision(app, historicalBatch, newGameSchemaEntry(t, app, arcadeID, series.Id), version.Id, "", 1)
	historicalOne.Set("legacy_imported", true)
	if err := app.Save(historicalOne); err != nil {
		t.Fatalf("save imported unknown revision: %v", err)
	}
	historicalTwo := newGameSchemaRevision(app, historicalBatch, newGameSchemaEntry(t, app, arcadeID, series.Id), version.Id, "", 2)
	historicalTwo.Set("legacy_imported", true)
	if err := app.Save(historicalTwo); err != nil {
		t.Fatalf("imported unknown revisions must coexist for rollback: %v", err)
	}

	knownBatch := newGameSchemaBatch(t, app, arcadeID)
	knownA := newGameSchemaRevision(app, knownBatch, newGameSchemaEntry(t, app, arcadeID, series.Id), version.Id, cabinetA.Id, 1)
	if err := app.Save(knownA); err != nil {
		t.Fatalf("save first known cabinet: %v", err)
	}
	knownB := newGameSchemaRevision(app, knownBatch, newGameSchemaEntry(t, app, arcadeID, series.Id), version.Id, cabinetB.Id, 1)
	if err := app.Save(knownB); err != nil {
		t.Fatalf("same version with different cabinet must be allowed: %v", err)
	}
	duplicateKnown := newGameSchemaRevision(app, knownBatch, newGameSchemaEntry(t, app, arcadeID, series.Id), version.Id, cabinetA.Id, 1)
	if err := app.Save(duplicateKnown); err == nil {
		t.Fatal("expected duplicate known cabinet to fail")
	}

	mixedBatch := newGameSchemaBatch(t, app, arcadeID)
	if err := app.Save(newGameSchemaRevision(app, mixedBatch, newGameSchemaEntry(t, app, arcadeID, series.Id), version.Id, "", 1)); err != nil {
		t.Fatalf("save mixed unknown revision: %v", err)
	}
	if err := app.Save(newGameSchemaRevision(app, mixedBatch, newGameSchemaEntry(t, app, arcadeID, series.Id), version.Id, cabinetA.Id, 1)); err != nil {
		t.Fatalf("known and unknown cabinet rows should coexist during transition: %v", err)
	}

	entryBatch := newGameSchemaBatch(t, app, arcadeID)
	reusedEntry := newGameSchemaEntry(t, app, arcadeID, series.Id)
	if err := app.Save(newGameSchemaRevision(app, entryBatch, reusedEntry, version.Id, cabinetA.Id, 1)); err != nil {
		t.Fatalf("save first entry revision: %v", err)
	}
	if err := app.Save(newGameSchemaRevision(app, entryBatch, reusedEntry, otherVersion.Id, cabinetB.Id, 1)); err == nil {
		t.Fatal("expected duplicate (batch, entry) to fail")
	}
}

func requireSchemaCollection(t *testing.T, app core.App, name string) *core.Collection {
	t.Helper()
	collection, err := app.FindCollectionByNameOrId(name)
	if err != nil {
		t.Fatalf("find %s: %v", name, err)
	}
	return collection
}

func assertLockedRules(t *testing.T, collection *core.Collection) {
	t.Helper()
	if collection.ListRule != nil || collection.ViewRule != nil || collection.CreateRule != nil || collection.UpdateRule != nil || collection.DeleteRule != nil {
		t.Fatalf("%s raw REST rules must all be nil", collection.Name)
	}
}

func assertSchemaRelation(t *testing.T, collection *core.Collection, name, target string, required, cascade bool) {
	t.Helper()
	field, ok := collection.Fields.GetByName(name).(*core.RelationField)
	if !ok || field.CollectionId != target || field.Required != required || field.CascadeDelete != cascade || field.MinSelect != 0 || field.MaxSelect != 1 {
		t.Fatalf("unexpected %s.%s relation: %#v", collection.Name, name, field)
	}
}

func assertAutodate(t *testing.T, collection *core.Collection, name string, onCreate, onUpdate bool) {
	t.Helper()
	field, ok := collection.Fields.GetByName(name).(*core.AutodateField)
	if !ok || field.OnCreate != onCreate || field.OnUpdate != onUpdate {
		t.Fatalf("unexpected %s.%s autodate: %#v", collection.Name, name, field)
	}
}

func assertSchemaIndex(t *testing.T, collection *core.Collection, name string, unique bool, columns []string, where string) {
	t.Helper()
	index := dbutils.ParseIndex(collection.GetIndex(name))
	if !index.IsValid() || index.IndexName != name || index.TableName != collection.Name || index.Unique != unique || len(index.Columns) != len(columns) || normalizeTestSQL(index.Where) != normalizeTestSQL(where) {
		t.Fatalf("unexpected %s index %s: %#v", collection.Name, name, index)
	}
	for i, column := range columns {
		if index.Columns[i].Name != column {
			t.Fatalf("unexpected %s index %s column %d: %q", collection.Name, name, i, index.Columns[i].Name)
		}
	}
}

func normalizeTestSQL(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func mustFindRecords(t *testing.T, app core.App, collection string) []*core.Record {
	t.Helper()
	records, err := app.FindRecordsByFilter(collection, "", "", 0, 0)
	if err != nil {
		t.Fatalf("list %s: %v", collection, err)
	}
	return records
}

func newCabinetRecord(collection *core.Collection, name string) *core.Record {
	record := core.NewRecord(collection)
	record.Set("en", name)
	record.Set("kr", name)
	record.Set("jp", name)
	return record
}

func newGameSchemaEntry(t *testing.T, app core.App, arcadeID, seriesID string) string {
	t.Helper()
	record := core.NewRecord(requireSchemaCollection(t, app, "arcade_game_id"))
	record.Set("arcade", arcadeID)
	record.Set("series", seriesID)
	if err := app.Save(record); err != nil {
		t.Fatalf("save game entry: %v", err)
	}
	return record.Id
}

func newGameSchemaBatch(t *testing.T, app core.App, arcadeID string) string {
	t.Helper()
	record := core.NewRecord(requireSchemaCollection(t, app, "arcade_game_history_batch"))
	record.Set("arcade", arcadeID)
	if err := app.Save(record); err != nil {
		t.Fatalf("save game batch: %v", err)
	}
	return record.Id
}

func newGameSchemaRevision(app core.App, batchID, entryID, versionID, cabinetID string, quantity int) *core.Record {
	collection, _ := app.FindCollectionByNameOrId("arcade_game_history")
	record := core.NewRecord(collection)
	record.Set("batch", batchID)
	record.Set("entry", entryID)
	record.Set("version", versionID)
	record.Set("cabinet", cabinetID)
	record.Set("quantity", quantity)
	return record
}
