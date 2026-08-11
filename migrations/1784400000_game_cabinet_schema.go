package migrations

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/dbutils"
)

const (
	revisionKnownCabinetIndex   = "idx_arcade_game_history_batch_version_cabinet"
	revisionUnknownCabinetIndex = "idx_arcade_game_history_batch_version_unknown"
	revisionUnknownCabinetWhere = "legacy_imported = 0 AND cabinet IS NULL OR legacy_imported = 0 AND cabinet = ''"
)

// This migration intentionally changes schema only. Cabinet catalog rows,
// series/version aliases, and revision cabinet values are populated by a
// separately reviewed data migration.
func init() {
	m.Register(applyGameCabinetSchema, func(app core.App) error { return nil })
}

func applyGameCabinetSchema(app core.App) error {
	versions, err := findRequiredBaseCollection(app, "game_series_version")
	if err != nil {
		return err
	}
	revisions, err := findRequiredBaseCollection(app, "arcade_game_history")
	if err != nil {
		return err
	}

	if versions.Fields.GetByName("cabinets") != nil {
		return fmt.Errorf("game_series_version.cabinets is an unsupported legacy draft field")
	}
	if revisions.Fields.GetByName("cabinets") != nil {
		return fmt.Errorf("arcade_game_history.cabinets is an unsupported legacy draft field")
	}

	versionsChanged, err := ensureExactRelationField(versions, "alias_of", versions.Id, false, false)
	if err != nil {
		return err
	}
	if versionsChanged {
		if err := app.Save(versions); err != nil {
			return fmt.Errorf("save game_series_version.alias_of: %w", err)
		}
	}

	cabinets, err := ensureGameCabinetCollection(app)
	if err != nil {
		return err
	}
	if _, err := ensureGameSeriesVersionCabinetCollection(app, versions, cabinets); err != nil {
		return err
	}
	if err := updateGameRevisionCabinetSchema(app, revisions, cabinets); err != nil {
		return err
	}

	return nil
}

func findRequiredBaseCollection(app core.App, name string) (*core.Collection, error) {
	collection, err := app.FindCollectionByNameOrId(name)
	if err != nil {
		return nil, fmt.Errorf("find %s: %w", name, err)
	}
	if collection.Name != name || collection.System || !collection.IsBase() {
		return nil, fmt.Errorf("%s must be a base collection", name)
	}
	return collection, nil
}

func ensureGameCabinetCollection(app core.App) (*core.Collection, error) {
	collection, err := app.FindCollectionByNameOrId("game_cabinet")
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("find game_cabinet: %w", err)
		}

		collection = core.NewBaseCollection("game_cabinet")
		collection.Fields.Add(
			&core.TextField{Name: "en", Required: true},
			&core.TextField{Name: "kr", Required: true},
			&core.TextField{Name: "jp", Required: true},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		if err := app.Save(collection); err != nil {
			return nil, fmt.Errorf("create game_cabinet: %w", err)
		}
		return collection, nil
	}

	if collection.Name != "game_cabinet" || collection.System || !collection.IsBase() {
		return nil, fmt.Errorf("game_cabinet must be a base collection")
	}
	if err := requireLockedCollectionRules(collection); err != nil {
		return nil, err
	}
	for _, name := range []string{"en", "kr", "jp"} {
		if err := requireExactTextField(collection, name, true); err != nil {
			return nil, err
		}
	}
	if err := requireExactAutodateField(collection, "created", true, false); err != nil {
		return nil, err
	}
	if err := requireExactAutodateField(collection, "updated", true, true); err != nil {
		return nil, err
	}
	return collection, nil
}

func ensureGameSeriesVersionCabinetCollection(app core.App, versions, cabinets *core.Collection) (*core.Collection, error) {
	collection, err := app.FindCollectionByNameOrId("game_series_version_cabinet")
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("find game_series_version_cabinet: %w", err)
		}

		collection = core.NewBaseCollection("game_series_version_cabinet")
		collection.Fields.Add(
			&core.RelationField{Name: "version", CollectionId: versions.Id, CascadeDelete: true, Required: true, MaxSelect: 1},
			&core.RelationField{Name: "cabinet", CollectionId: cabinets.Id, CascadeDelete: true, Required: true, MaxSelect: 1},
			&core.JSONField{Name: "price_default"},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		collection.AddIndex("idx_game_series_version_cabinet_version_cabinet", true, "version, cabinet", "")
		collection.AddIndex("idx_game_series_version_cabinet_cabinet_version", false, "cabinet, version", "")
		if err := app.Save(collection); err != nil {
			return nil, fmt.Errorf("create game_series_version_cabinet: %w", err)
		}
		return collection, nil
	}

	if collection.Name != "game_series_version_cabinet" || collection.System || !collection.IsBase() {
		return nil, fmt.Errorf("game_series_version_cabinet must be a base collection")
	}
	if err := requireLockedCollectionRules(collection); err != nil {
		return nil, err
	}
	if err := requireExactRelationField(collection, "version", versions.Id, true, true); err != nil {
		return nil, err
	}
	if err := requireExactRelationField(collection, "cabinet", cabinets.Id, true, true); err != nil {
		return nil, err
	}
	if err := requireExactJSONField(collection, "price_default", false); err != nil {
		return nil, err
	}
	if err := requireExactAutodateField(collection, "created", true, false); err != nil {
		return nil, err
	}
	if err := requireExactAutodateField(collection, "updated", true, true); err != nil {
		return nil, err
	}
	if err := requireExactIndex(collection, "idx_game_series_version_cabinet_version_cabinet", true, []string{"version", "cabinet"}, ""); err != nil {
		return nil, err
	}
	if err := requireExactIndex(collection, "idx_game_series_version_cabinet_cabinet_version", false, []string{"cabinet", "version"}, ""); err != nil {
		return nil, err
	}
	return collection, nil
}

func ensureGameCatalogMigrationOriginCollection(app core.App) (*core.Collection, error) {
	collection, err := app.FindCollectionByNameOrId("game_catalog_migration_origin")
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("find game_catalog_migration_origin: %w", err)
		}

		collection = core.NewBaseCollection("game_catalog_migration_origin")
		collection.Fields.Add(
			&core.TextField{Name: "migration_key", Required: true},
			&core.TextField{Name: "manifest_hash", Required: true},
			&core.TextField{Name: "collection", Required: true},
			&core.TextField{Name: "record_id", Required: true},
			&core.JSONField{Name: "before", Required: true},
			&core.TextField{Name: "before_hash", Required: true},
			&core.AutodateField{Name: "created", OnCreate: true},
		)
		collection.AddIndex("idx_game_catalog_migration_origin_record", true, "migration_key, collection, record_id", "")
		if err := app.Save(collection); err != nil {
			return nil, fmt.Errorf("create game_catalog_migration_origin: %w", err)
		}
		return collection, nil
	}

	if collection.Name != "game_catalog_migration_origin" || collection.System || !collection.IsBase() {
		return nil, fmt.Errorf("game_catalog_migration_origin must be a base collection")
	}
	if err := requireLockedCollectionRules(collection); err != nil {
		return nil, err
	}
	for _, name := range []string{"migration_key", "manifest_hash", "collection", "record_id", "before_hash"} {
		if err := requireExactTextField(collection, name, true); err != nil {
			return nil, err
		}
	}
	if err := requireExactJSONField(collection, "before", true); err != nil {
		return nil, err
	}
	if err := requireExactAutodateField(collection, "created", true, false); err != nil {
		return nil, err
	}
	if err := requireExactIndex(collection, "idx_game_catalog_migration_origin_record", true, []string{"migration_key", "collection", "record_id"}, ""); err != nil {
		return nil, err
	}
	return collection, nil
}

func updateGameRevisionCabinetSchema(app core.App, revisions, cabinets *core.Collection) error {
	changed, err := ensureExactRelationField(revisions, "cabinet", cabinets.Id, false, false)
	if err != nil {
		return err
	}

	quantity, ok := revisions.Fields.GetByName("quantity").(*core.NumberField)
	if !ok {
		return fmt.Errorf("arcade_game_history.quantity must be a number field")
	}
	if !quantity.OnlyInt || quantity.Required || quantity.Max != nil || quantity.System || quantity.Hidden || quantity.Presentable {
		return fmt.Errorf("arcade_game_history.quantity has incompatible settings")
	}
	if quantity.Min == nil || (*quantity.Min != 0 && *quantity.Min != 1) {
		return fmt.Errorf("arcade_game_history.quantity min must be the deployed value 1 or target value 0")
	}
	if *quantity.Min != 0 {
		zero := float64(0)
		quantity.Min = &zero
		changed = true
	}

	if err := requireExactIndex(revisions, "idx_arcade_game_history_batch_entry", true, []string{"batch", "entry"}, ""); err != nil {
		return err
	}

	knownWhere := "cabinet IS NOT NULL AND cabinet <> ''"
	if revisions.GetIndex(revisionKnownCabinetIndex) == "" {
		revisions.AddIndex(revisionKnownCabinetIndex, true, "batch, version, cabinet", knownWhere)
		changed = true
	} else if err := requireExactIndex(revisions, revisionKnownCabinetIndex, true, []string{"batch", "version", "cabinet"}, knownWhere); err != nil {
		return err
	}

	// Historical imports can legitimately contain two entries with the same
	// version and no cabinet (the legacy schema had no cabinet identity). Keep
	// those rows for rollback, while retaining the one-unknown-version rule for
	// revisions created by the current API.
	unknownWhere := revisionUnknownCabinetWhere
	if revisions.GetIndex(revisionUnknownCabinetIndex) == "" {
		revisions.AddIndex(revisionUnknownCabinetIndex, true, "batch, version", unknownWhere)
		changed = true
	} else if err := requireExactIndex(revisions, revisionUnknownCabinetIndex, true, []string{"batch", "version"}, unknownWhere); err != nil {
		return err
	}

	if raw := revisions.GetIndex("idx_arcade_game_history_batch_version"); raw != "" {
		if err := requireExactIndex(revisions, "idx_arcade_game_history_batch_version", true, []string{"batch", "version"}, ""); err != nil {
			return err
		}
		revisions.RemoveIndex("idx_arcade_game_history_batch_version")
		changed = true
	}
	for _, raw := range revisions.Indexes {
		parsed := dbutils.ParseIndex(raw)
		if parsed.IndexName == revisionUnknownCabinetIndex || !parsed.Unique {
			continue
		}
		if equalIndexColumns(parsed, []string{"batch", "version"}) {
			return fmt.Errorf("arcade_game_history has unexpected legacy unique index %s", parsed.IndexName)
		}
	}

	if changed {
		if err := app.Save(revisions); err != nil {
			return fmt.Errorf("save arcade_game_history cabinet schema: %w", err)
		}
	}
	return nil
}

func ensureExactRelationField(collection *core.Collection, name, target string, required, cascade bool) (bool, error) {
	field := collection.Fields.GetByName(name)
	if field == nil {
		collection.Fields.Add(&core.RelationField{
			Name:          name,
			CollectionId:  target,
			CascadeDelete: cascade,
			Required:      required,
			MaxSelect:     1,
		})
		return true, nil
	}
	return false, requireExactRelationField(collection, name, target, required, cascade)
}

func requireExactRelationField(collection *core.Collection, name, target string, required, cascade bool) error {
	field, ok := collection.Fields.GetByName(name).(*core.RelationField)
	if !ok {
		return fmt.Errorf("%s.%s must be a relation field", collection.Name, name)
	}
	if field.CollectionId != target || field.Required != required || field.CascadeDelete != cascade || field.MinSelect != 0 || field.MaxSelect != 1 || field.System || field.Hidden || field.Presentable {
		return fmt.Errorf("%s.%s has incompatible relation settings", collection.Name, name)
	}
	return nil
}

func requireExactTextField(collection *core.Collection, name string, required bool) error {
	field, ok := collection.Fields.GetByName(name).(*core.TextField)
	if !ok {
		return fmt.Errorf("%s.%s must be a text field", collection.Name, name)
	}
	if field.Required != required || field.Min != 0 || field.Max != 0 || field.Pattern != "" || field.AutogeneratePattern != "" || field.PrimaryKey || field.System || field.Hidden || field.Presentable {
		return fmt.Errorf("%s.%s has incompatible text settings", collection.Name, name)
	}
	return nil
}

func requireExactJSONField(collection *core.Collection, name string, required bool) error {
	field, ok := collection.Fields.GetByName(name).(*core.JSONField)
	if !ok {
		return fmt.Errorf("%s.%s must be a JSON field", collection.Name, name)
	}
	if field.Required != required || field.MaxSize != 0 || field.System || field.Hidden || field.Presentable {
		return fmt.Errorf("%s.%s has incompatible JSON settings", collection.Name, name)
	}
	return nil
}

func requireExactAutodateField(collection *core.Collection, name string, onCreate, onUpdate bool) error {
	field, ok := collection.Fields.GetByName(name).(*core.AutodateField)
	if !ok {
		return fmt.Errorf("%s.%s must be an autodate field", collection.Name, name)
	}
	if field.OnCreate != onCreate || field.OnUpdate != onUpdate || field.System || field.Hidden || field.Presentable {
		return fmt.Errorf("%s.%s has incompatible autodate settings", collection.Name, name)
	}
	return nil
}

func requireLockedCollectionRules(collection *core.Collection) error {
	if collection.ListRule != nil || collection.ViewRule != nil || collection.CreateRule != nil || collection.UpdateRule != nil || collection.DeleteRule != nil {
		return fmt.Errorf("%s raw REST rules must all be locked", collection.Name)
	}
	return nil
}

func requireExactIndex(collection *core.Collection, name string, unique bool, columns []string, where string) error {
	raw := collection.GetIndex(name)
	if raw == "" {
		return fmt.Errorf("%s is missing index %s", collection.Name, name)
	}
	parsed := dbutils.ParseIndex(raw)
	if !parsed.IsValid() || parsed.IndexName != name || parsed.SchemaName != "" || parsed.Optional || parsed.TableName != collection.Name || parsed.Unique != unique || !equalIndexColumns(parsed, columns) || normalizeSQL(parsed.Where) != normalizeSQL(where) {
		return fmt.Errorf("%s index %s has incompatible definition", collection.Name, name)
	}
	return nil
}

func equalIndexColumns(index dbutils.Index, expected []string) bool {
	if len(index.Columns) != len(expected) {
		return false
	}
	for i, column := range index.Columns {
		if !strings.EqualFold(strings.TrimSpace(column.Name), expected[i]) || column.Collate != "" || column.Sort != "" {
			return false
		}
	}
	return true
}

func normalizeSQL(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}
