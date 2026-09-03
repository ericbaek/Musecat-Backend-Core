package diff

import "github.com/pocketbase/pocketbase/core"

const memoCollection = "arcade_memo"

// Build returns the immutable document snapshots referenced by a memo changelog row.
func Build(app core.App, arcadeID, fromID, toID string) map[string]any {
	read := func(id string) (any, string) {
		if id == "" {
			return nil, "absent"
		}
		record, err := app.FindRecordById(memoCollection, id)
		if err != nil || record.GetString("arcade") != arcadeID {
			return nil, "unavailable"
		}
		return record.Get("document"), "available"
	}
	before, beforeStatus := read(fromID)
	after, afterStatus := read(toID)
	return map[string]any{
		"before": before, "after": after,
		"before_status": beforeStatus, "after_status": afterStatus,
	}
}
