package migrations

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(applyGameCatalogManagement, func(app core.App) error { return nil })
}

func applyGameCatalogManagement(app core.App) error {
	users, err := app.FindCollectionByNameOrId("user")
	if err != nil {
		return fmt.Errorf("find user collection: %w", err)
	}
	series, err := findRequiredBaseCollection(app, "game_series")
	if err != nil {
		return err
	}
	if series.Fields.GetByName("alias_of") == nil {
		series.Fields.Add(&core.RelationField{Name: "alias_of", CollectionId: series.Id, MaxSelect: 1})
	}
	cabinets, err := findRequiredBaseCollection(app, "game_cabinet")
	if err != nil {
		return err
	}
	if cabinets.Fields.GetByName("series") == nil {
		cabinets.Fields.Add(&core.RelationField{Name: "series", CollectionId: series.Id, Required: true, MaxSelect: 1})
		if err := app.Save(cabinets); err != nil {
			return fmt.Errorf("add game_cabinet.series: %w", err)
		}
	}

	for _, name := range []string{"game_manufacturer", "game_series", "game_series_version", "game_cabinet", "game_series_version_cabinet"} {
		collection, err := findRequiredBaseCollection(app, name)
		if err != nil {
			return err
		}
		changed := false
		if collection.Fields.GetByName("archived") == nil {
			collection.Fields.Add(&core.BoolField{Name: "archived"})
			changed = true
		}
		if collection.Fields.GetByName("archived_at") == nil {
			collection.Fields.Add(&core.DateField{Name: "archived_at"})
			changed = true
		}
		if collection.Fields.GetByName("archived_by") == nil {
			collection.Fields.Add(&core.RelationField{Name: "archived_by", CollectionId: users.Id, MaxSelect: 1})
			changed = true
		}
		if collection.Fields.GetByName("revision") == nil {
			one := float64(1)
			collection.Fields.Add(&core.NumberField{Name: "revision", OnlyInt: true, Min: &one})
			changed = true
		}
		if changed || name == "game_series" {
			if err := app.Save(collection); err != nil {
				return fmt.Errorf("save %s catalog management fields: %w", name, err)
			}
		}
	}

	if _, err := app.FindCollectionByNameOrId("game_catalog_changelog"); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("find game_catalog_changelog: %w", err)
	}

	changes := core.NewBaseCollection("game_catalog_changelog")
	changes.Fields.Add(
		&core.TextField{Name: "operation_id", Required: true, Max: 64},
		&core.TextField{Name: "payload_hash", Required: true, Max: 64},
		&core.RelationField{Name: "actor", CollectionId: users.Id, Required: true, MaxSelect: 1},
		&core.JSONField{Name: "actor_tags", Required: true, MaxSize: 4096},
		&core.SelectField{Name: "action", Values: []string{"create", "update", "archive", "restore", "revert"}, Required: true, MaxSelect: 1},
		&core.SelectField{Name: "entity_type", Values: []string{"series", "version", "cabinet", "compatibility", "manufacturer"}, Required: true, MaxSelect: 1},
		&core.TextField{Name: "entity_id", Required: true, Max: 64},
		&core.JSONField{Name: "before", MaxSize: 131072},
		&core.JSONField{Name: "after", MaxSize: 131072},
		&core.TextField{Name: "reason", Required: true, Max: 500},
		&core.NumberField{Name: "revision_before", OnlyInt: true},
		&core.NumberField{Name: "revision_after", OnlyInt: true},
		&core.TextField{Name: "reverts_change", Max: 64},
		&core.AutodateField{Name: "created", OnCreate: true},
	)
	changes.AddIndex("idx_game_catalog_changelog_operation", true, "operation_id", "")
	changes.AddIndex("idx_game_catalog_changelog_entity_created", false, "entity_type, entity_id, created", "")
	changes.AddIndex("idx_game_catalog_changelog_actor_created", false, "actor, created", "")
	if err := app.Save(changes); err != nil {
		return fmt.Errorf("create game_catalog_changelog: %w", err)
	}
	return nil
}
