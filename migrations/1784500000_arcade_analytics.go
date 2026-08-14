package migrations

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

const arcadeAnalyticsEventCollection = "arcade_analytics_event"

// Core owns the reusable analytics event schema. It intentionally does not
// backfill or transform any existing Full database rows.
func init() {
	m.Register(func(app core.App) error {
		if err := ensureArcadeAnalyticsEventCollection(app); err != nil {
			return err
		}
		return ensureArcadeAnalyticsSourceIndexes(app)
	}, func(app core.App) error {
		return nil
	})
}

func ensureArcadeAnalyticsEventCollection(app core.App) error {
	collection, err := app.FindCollectionByNameOrId(arcadeAnalyticsEventCollection)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("find %s: %w", arcadeAnalyticsEventCollection, err)
		}

		arcades, err := app.FindCollectionByNameOrId("arcade")
		if err != nil {
			return fmt.Errorf("find arcade: %w", err)
		}
		series, err := app.FindCollectionByNameOrId("game_series")
		if err != nil {
			return fmt.Errorf("find game_series: %w", err)
		}

		collection = core.NewBaseCollection(arcadeAnalyticsEventCollection)
		collection.Fields.Add(
			&core.RelationField{Name: "arcade", CollectionId: arcades.Id, CascadeDelete: true, Required: true, MaxSelect: 1},
			&core.TextField{Name: "event_group", Required: true, Max: 15},
			&core.SelectField{Name: "event_type", Values: []string{"page_view", "direction_click", "fault_report"}, Required: true, MaxSelect: 1},
			&core.RelationField{Name: "series", CollectionId: series.Id, CascadeDelete: false, MaxSelect: 1},
			&core.TextField{Name: "source", Max: 32},
			&core.TextField{Name: "subject_id", Max: 15},
			&core.AutodateField{Name: "created", OnCreate: true},
		)
		collection.AddIndex("idx_arcade_analytics_event_arcade_type_group", false, "arcade, event_type, event_group", "")
		collection.AddIndex("idx_arcade_analytics_event_arcade_type_series", false, "arcade, event_type, series", "")
		collection.AddIndex("idx_arcade_analytics_event_arcade_subject", false, "arcade, subject_id", "")
		if err := app.Save(collection); err != nil {
			return fmt.Errorf("create %s: %w", arcadeAnalyticsEventCollection, err)
		}
		return nil
	}

	if collection.Name != arcadeAnalyticsEventCollection || collection.System || !collection.IsBase() {
		return fmt.Errorf("%s must be a base collection", arcadeAnalyticsEventCollection)
	}
	collection.ListRule = nil
	collection.ViewRule = nil
	collection.CreateRule = nil
	collection.UpdateRule = nil
	collection.DeleteRule = nil
	if err := app.Save(collection); err != nil {
		return fmt.Errorf("lock %s: %w", arcadeAnalyticsEventCollection, err)
	}
	return nil
}

func ensureArcadeAnalyticsSourceIndexes(app core.App) error {
	for _, spec := range []struct {
		collection string
		name       string
		fields     string
	}{
		{collection: "arcade_flag", name: "idx_arcade_flag_arcade", fields: "arcade"},
		{collection: "arcade_changelog", name: "idx_arcade_changelog_arcade", fields: "arcade"},
	} {
		collection, err := app.FindCollectionByNameOrId(spec.collection)
		if err != nil {
			return fmt.Errorf("find %s: %w", spec.collection, err)
		}
		if strings.Contains(collection.GetIndex(spec.name), spec.name) {
			continue
		}
		collection.AddIndex(spec.name, false, spec.fields, "")
		if err := app.Save(collection); err != nil {
			return fmt.Errorf("add %s to %s: %w", spec.name, spec.collection, err)
		}
	}
	return nil
}
