package diff

import "github.com/pocketbase/pocketbase/core"

const memoCollection = "arcade_memo"

// Build returns the immutable document snapshots referenced by a memo changelog row.
func Build(app core.App, fromID, toID string) map[string]any {
	read := func(id string) any {
		if id == "" {
			return nil
		}
		record, err := app.FindRecordById(memoCollection, id)
		if err != nil {
			return nil
		}
		return record.Get("document")
	}
	return map[string]any{"before": read(fromID), "after": read(toID)}
}
