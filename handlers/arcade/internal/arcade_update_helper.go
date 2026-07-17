package arcadeinternal

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

var arcadeChangelogTracked = map[string]struct{}{
	"basic": {},
	"hour":  {},
	"sns":   {},
	"gtk":   {},
	"game":  {},
	// game_state is the persistence pointer but game is the user-facing
	// aggregate section and changelog category.
	"game_state": {},
	"photo":      {},
}

// writeArcadeChangelog creates a single changelog row.
func writeArcadeChangelog(app core.App, arcadeID, field string, from, to any, by string, log any) error {
	if by == "" {
		return fmt.Errorf("changelog 'by' is required")
	}
	coll, err := app.FindCollectionByNameOrId(CollectionArcadeChangelog)
	if err != nil {
		return err
	}
	rec := core.NewRecord(coll)
	rec.Set("arcade", arcadeID)
	rec.Set("changed", field)
	rec.Set("from", from)
	rec.Set("to", to)
	rec.Set("by", by)
	if log != nil {
		rec.Set("log", log)
	}
	return app.Save(rec)
}

// WriteArcadeChangelogTx writes one arcade changelog row using the provided app.
func WriteArcadeChangelogTx(app core.App, arcadeID, field string, from, to any, by string, log any) error {
	return writeArcadeChangelog(app, arcadeID, field, from, to, by, log)
}

// UpdateArcadeFieldsTx applies updates to arcade and writes changelog entries using the provided app (which can be a tx-bound app).
func UpdateArcadeFieldsTxWithLogs(app core.App, arcadeID string, updates, logs map[string]any, by string) error {
	arcadeRec, err := app.FindRecordById(CollectionArcade, arcadeID)
	if err != nil {
		return fmt.Errorf("arcade not found: %w", err)
	}
	// Capture old values for tracked fields present in updates
	oldVals := map[string]any{}
	for k := range updates {
		if _, ok := arcadeChangelogTracked[k]; ok {
			oldVals[k] = arcadeRec.Get(k)
		}
	}
	// Apply updates
	for k, v := range updates {
		arcadeRec.Set(k, v)
	}
	if err := app.Save(arcadeRec); err != nil {
		return fmt.Errorf("failed to update arcade: %w", err)
	}
	// Write changelog for changed tracked fields
	for k, oldV := range oldVals {
		newV := arcadeRec.Get(k)
		if fmt.Sprintf("%v", oldV) == fmt.Sprintf("%v", newV) {
			continue
		}
		var log any
		if logs != nil {
			log = logs[k]
			if k == "game_state" && log == nil {
				log = logs["game"]
			}
		}
		changed := k
		if k == "game_state" {
			changed = "game"
		}
		if err := writeArcadeChangelog(app, arcadeID, changed, oldV, newV, by, log); err != nil {
			return err
		}
	}
	return nil
}

// UpdateArcadeFieldsTx applies updates to arcade and writes changelog entries using the provided app (which can be a tx-bound app).
func UpdateArcadeFieldsTx(app core.App, arcadeID string, updates map[string]any, by string) error {
	return UpdateArcadeFieldsTxWithLogs(app, arcadeID, updates, nil, by)
}

// UpdateArcadeFieldsWithLogs wraps UpdateArcadeFieldsTxWithLogs in a transaction
// and derives 'by' from the request auth.
func UpdateArcadeFieldsWithLogs(re *core.RequestEvent, arcadeID string, updates, logs map[string]any) error {
	if re.Auth == nil || re.Auth.Id == "" {
		return fmt.Errorf("auth required: missing 'by'")
	}
	by := re.Auth.Id
	return re.App.RunInTransaction(func(txApp core.App) error {
		return UpdateArcadeFieldsTxWithLogs(txApp, arcadeID, updates, logs, by)
	})
}

// UpdateArcadeFields wraps UpdateArcadeFieldsTx in a transaction and derives 'by' from the request auth.
func UpdateArcadeFields(re *core.RequestEvent, arcadeID string, updates map[string]any) error {
	return UpdateArcadeFieldsWithLogs(re, arcadeID, updates, nil)
}
