package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Install the immutable game-state collections for a fresh Core database.
func init() {
	m.Register(func(app core.App) error {
		arcade, err := app.FindCollectionByNameOrId("arcade")
		if err != nil {
			return fmt.Errorf("find arcade: %w", err)
		}
		users, err := app.FindCollectionByNameOrId("user")
		if err != nil {
			return fmt.Errorf("find user: %w", err)
		}
		series, err := app.FindCollectionByNameOrId("game_series")
		if err != nil {
			return fmt.Errorf("find game_series: %w", err)
		}
		versions, err := app.FindCollectionByNameOrId("game_series_version")
		if err != nil {
			return fmt.Errorf("find game_series_version: %w", err)
		}
		entries, err := ensureGameEntry(app, arcade, series, users)
		if err != nil {
			return err
		}
		batches, err := ensureGameBatch(app, arcade, users)
		if err != nil {
			return err
		}
		if _, err := ensureGameRevision(app, batches, entries, versions, users); err != nil {
			return err
		}
		if err := ensureArcadeGameStateField(app, arcade, batches); err != nil {
			return err
		}
		if err := ensureFlagGameEntryField(app, entries); err != nil {
			return err
		}
		for _, name := range []string{
			"arcade_game_id", "arcade_game_history_batch", "arcade_game_history",
		} {
			if collection, findErr := app.FindCollectionByNameOrId(name); findErr == nil {
				collection.ListRule, collection.ViewRule, collection.CreateRule, collection.UpdateRule, collection.DeleteRule = nil, nil, nil, nil, nil
				if saveErr := app.Save(collection); saveErr != nil {
					return fmt.Errorf("lock %s: %w", name, saveErr)
				}
			}
		}
		return nil
	}, func(app core.App) error { return nil })
}

func ensureCollection(app core.App, name string, fields ...core.Field) (*core.Collection, error) {
	if existing, err := app.FindCollectionByNameOrId(name); err == nil {
		return existing, nil
	}
	c := core.NewBaseCollection(name)
	for _, field := range fields {
		c.Fields.Add(field)
	}
	// Base collections do not automatically receive timestamp fields in the
	// programmatic constructor. Every immutable revision collection needs them
	// both for audit and for its ordered indexes.
	c.Fields.Add(&core.AutodateField{Name: "created", OnCreate: true})
	c.Fields.Add(&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true})
	if err := app.Save(c); err != nil {
		return nil, fmt.Errorf("create %s: %w", name, err)
	}
	return c, nil
}

func relation(name, collectionID string, cascade bool, required bool) *core.RelationField {
	return &core.RelationField{Name: name, CollectionId: collectionID, CascadeDelete: cascade, Required: required, MaxSelect: 1}
}

func ensureGameEntry(app core.App, arcade, series, users *core.Collection) (*core.Collection, error) {
	c, err := ensureCollection(app, "arcade_game_id",
		relation("arcade", arcade.Id, true, true), relation("series", series.Id, false, true),
		relation("created_by", users.Id, false, false),
	)
	if err != nil {
		return nil, err
	}
	c.AddIndex("idx_arcade_game_id_arcade", false, "arcade", "")
	c.AddIndex("idx_arcade_game_id_series", false, "series", "")
	if err := app.Save(c); err != nil {
		return nil, err
	}
	return c, nil
}

func ensureGameBatch(app core.App, arcade, users *core.Collection) (*core.Collection, error) {
	c, err := ensureCollection(app, "arcade_game_history_batch",
		relation("arcade", arcade.Id, true, true), relation("created_by", users.Id, false, false),
		&core.TextField{Name: "reason", Max: 120},
	)
	if err != nil {
		return nil, err
	}
	c.AddIndex("idx_arcade_game_history_batch_arcade_created", false, "arcade, created", "")
	if err := app.Save(c); err != nil {
		return nil, err
	}
	return c, nil
}

func ensureGameRevision(app core.App, batch, entry, version, users *core.Collection) (*core.Collection, error) {
	c, err := ensureCollection(app, "arcade_game_history",
		relation("batch", batch.Id, true, true), relation("entry", entry.Id, false, true), relation("version", version.Id, false, true),
		&core.TextField{Name: "location", Max: 500}, &core.NumberField{Name: "quantity", OnlyInt: true, Min: func() *float64 { v := float64(1); return &v }()},
		&core.JSONField{Name: "price"}, &core.JSONField{Name: "tag"},
		&core.DateField{Name: "last_modified_at"}, relation("last_modified_by", users.Id, false, false),
		&core.BoolField{Name: "legacy_imported"},
	)
	if err != nil {
		return nil, err
	}
	c.AddIndex("idx_arcade_game_history_batch_entry", true, "batch, entry", "")
	c.AddIndex("idx_arcade_game_history_entry", false, "entry", "")
	c.AddIndex("idx_arcade_game_history_batch_version", true, "batch, version", "")
	if err := app.Save(c); err != nil {
		return nil, err
	}
	return c, nil
}

func ensureArcadeGameStateField(app core.App, arcade, batch *core.Collection) error {
	if arcade.Fields.GetByName("game_state") == nil {
		arcade.Fields.Add(relation("game_state", batch.Id, false, false))
	}
	return app.Save(arcade)
}

func ensureFlagGameEntryField(app core.App, entry *core.Collection) error {
	flag, err := app.FindCollectionByNameOrId("arcade_flag")
	if err != nil {
		return err
	}
	if flag.Fields.GetByName("game_entry") == nil {
		flag.Fields.Add(relation("game_entry", entry.Id, false, false))
	}
	flag.AddIndex("idx_arcade_flag_game_entry_open", false, "game_entry, solved, created", "")
	return app.Save(flag)
}
