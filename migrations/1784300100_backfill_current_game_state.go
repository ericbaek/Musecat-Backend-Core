package migrations

import (
	"fmt"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Backfill only the selected legacy molecule for each arcade. It never guesses
// relationships across historical molecules: those need Phase-A staff mapping.
// This makes the current UI state safe to edit while preserving all old rows as
// archive evidence for the later full-history conversion.
func init() {
	m.Register(func(app core.App) error {
		return app.RunInTransaction(func(txApp core.App) error {
			arcades, err := txApp.FindRecordsByFilter("arcade", "game_state='' && game!=''", "", 0, 0)
			if err != nil {
				return fmt.Errorf("load legacy arcades: %w", err)
			}
			for _, arcade := range arcades {
				if err := backfillCurrentGameState(txApp, arcade); err != nil {
					return err
				}
			}
			return nil
		})
	}, func(app core.App) error { return nil })
}

func backfillCurrentGameState(app core.App, arcade *core.Record) error {
	molecule, err := app.FindRecordById("arcade_game", arcade.GetString("game"))
	if err != nil || molecule.GetString("arcade") != arcade.Id {
		return nil
	}
	author := strings.TrimSpace(molecule.GetString("createdBy"))
	if author == "" {
		author = strings.TrimSpace(arcade.GetString("createdBy"))
	}
	if author == "" {
		return createCurrentGameBackfillIssue(app, arcade.Id, "current game molecule has no creator attribution")
	}
	atoms, err := app.FindRecordsByFilter("arcade_game_atoms", "molecule={:molecule}", "created", 0, 0, dbx.Params{"molecule": molecule.Id})
	if err != nil {
		return err
	}
	if len(atoms) == 0 {
		return nil
	}
	seenVersions := map[string]struct{}{}
	for _, atom := range atoms {
		versionID := strings.TrimSpace(atom.GetString("game"))
		version, findErr := app.FindRecordById("game_series_version", versionID)
		if findErr != nil || strings.TrimSpace(version.GetString("series")) == "" {
			return createCurrentGameBackfillIssue(app, arcade.Id, "current game atom has no valid version series")
		}
		if _, exists := seenVersions[versionID]; exists {
			return createCurrentGameBackfillIssue(app, arcade.Id, "current game state has duplicate version rows")
		}
		seenVersions[versionID] = struct{}{}
	}
	batchColl, err := app.FindCollectionByNameOrId("arcade_game_revision_batch")
	if err != nil {
		return err
	}
	entryColl, err := app.FindCollectionByNameOrId("arcade_game_entry")
	if err != nil {
		return err
	}
	revisionColl, err := app.FindCollectionByNameOrId("arcade_game_revision")
	if err != nil {
		return err
	}
	mapColl, err := app.FindCollectionByNameOrId("arcade_game_legacy_map")
	if err != nil {
		return err
	}
	batch := core.NewRecord(batchColl)
	batch.Set("arcade", arcade.Id)
	batch.Set("created_by", author)
	batch.Set("reason", "legacy_current_backfill")
	if err := app.Save(batch); err != nil {
		return err
	}
	for _, atom := range atoms {
		versionID := strings.TrimSpace(atom.GetString("game"))
		version, _ := app.FindRecordById("game_series_version", versionID)
		entry := core.NewRecord(entryColl)
		entry.Set("arcade", arcade.Id)
		entry.Set("series", version.GetString("series"))
		entry.Set("created_by", author)
		if err := app.Save(entry); err != nil {
			return err
		}
		revision := core.NewRecord(revisionColl)
		revision.Set("batch", batch.Id)
		revision.Set("entry", entry.Id)
		revision.Set("version", versionID)
		revision.Set("location", atom.GetString("location"))
		revision.Set("quantity", atom.GetInt("quantity"))
		revision.Set("price", atom.Get("price"))
		revision.Set("tag", atom.Get("tag"))
		revision.Set("uncertain", atom.GetBool("uncertain"))
		revision.Set("previous_version", atom.GetString("prev_game"))
		revision.Set("last_modified_at", atom.Get("updated"))
		revision.Set("last_modified_by", author)
		revision.Set("legacy_imported", true)
		if err := app.Save(revision); err != nil {
			return err
		}
		legacyMap := core.NewRecord(mapColl)
		legacyMap.Set("arcade", arcade.Id)
		legacyMap.Set("legacy_atom", atom.Id)
		legacyMap.Set("entry", entry.Id)
		legacyMap.Set("status", "mapped")
		if err := app.Save(legacyMap); err != nil {
			return err
		}
		for _, flagID := range atom.GetStringSlice("flags") {
			if flag, flagErr := app.FindRecordById("arcade_flag", flagID); flagErr == nil && flag.GetString("game_entry") == "" {
				flag.Set("game_entry", entry.Id)
				if err := app.Save(flag); err != nil {
					return err
				}
			}
		}
	}
	arcade.Set("game_state", batch.Id)
	return app.Save(arcade)
}

func createCurrentGameBackfillIssue(app core.App, arcadeID, reason string) error {
	coll, err := app.FindCollectionByNameOrId("arcade_game_migration_issue")
	if err != nil {
		return err
	}
	issue := core.NewRecord(coll)
	issue.Set("arcade", arcadeID)
	issue.Set("reason", reason)
	issue.Set("resolved", false)
	return app.Save(issue)
}
