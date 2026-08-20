package game

import (
	"fmt"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

// BuildUpdateBodyFromCurrentState clones the selected immutable state into an
// update body. Callers mutate only the returned in-memory rows; UpdateArcadeGameTx
// then creates a new batch rather than touching any legacy atom or revision.
func BuildUpdateBodyFromCurrentState(app core.App, arcadeID string) (UpdateArcadeGameBody, error) {
	arcade, err := app.FindRecordById(arcadeinternal.CollectionArcade, arcadeID)
	if err != nil {
		return UpdateArcadeGameBody{}, fmt.Errorf("arcade not found: %w", err)
	}
	stateID := strings.TrimSpace(arcade.GetString("game_v2"))
	if stateID == "" {
		return UpdateArcadeGameBody{}, fmt.Errorf("arcade.game_v2 is empty")
	}
	revisions, err := app.FindRecordsByFilter(arcadeinternal.CollectionArcadeGameRevision, "batch={:batch}", "created", 0, 0, dbx.Params{"batch": stateID})
	if err != nil {
		return UpdateArcadeGameBody{}, err
	}
	body := UpdateArcadeGameBody{Arcade: arcadeID, BaseStateID: stateID, Games: make([]GameAtomInput, 0, len(revisions))}
	for _, revision := range revisions {
		body.Games = append(body.Games, GameAtomInput{ID: revision.GetString("entry"), Game: revision.GetString("version"), Cabinet: revision.GetString("cabinet"), Location: revision.GetString("location"), Quantity: revision.GetInt("quantity"), RawPrice: revision.Get("price"), RawTag: revision.Get("tag")})
	}
	return body, nil
}

// findReusableGameEntryID returns the most recently used inactive installation
// identity for an arcade's series and verified cabinet. The entry collection
// intentionally does not store cabinet, so the historical revision table is
// the source of truth for this match. Unverified cabinets never participate in
// identity reuse because an empty cabinet cannot identify a physical machine.
func findReusableGameEntryID(app core.App, arcadeID, seriesID, cabinetID, currentState string, reserved map[string]struct{}) (string, error) {
	if strings.TrimSpace(cabinetID) == "" {
		return "", nil
	}

	rows, err := app.DB().NewQuery(`
SELECT e.id
FROM arcade_game_id e
INNER JOIN arcade_game_history r ON r.entry = e.id
WHERE e.arcade = {:arcade}
  AND e.series = {:series}
  AND r.cabinet = {:cabinet}
  AND (
    {:current_state} = ''
    OR NOT EXISTS (
      SELECT 1
      FROM arcade_game_history active_r
      WHERE active_r.batch = {:current_state}
        AND active_r.entry = e.id
    )
  )
GROUP BY e.id
ORDER BY MAX(r.created) DESC, MAX(e.created) DESC, e.id DESC
`).Bind(map[string]any{
		"arcade":        strings.TrimSpace(arcadeID),
		"series":        strings.TrimSpace(seriesID),
		"cabinet":       strings.TrimSpace(cabinetID),
		"current_state": strings.TrimSpace(currentState),
	}).Rows()
	if err != nil {
		return "", fmt.Errorf("query reusable game entry failed: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var entryID string
		if err := rows.Scan(&entryID); err != nil {
			return "", fmt.Errorf("scan reusable game entry failed: %w", err)
		}
		if _, alreadyReserved := reserved[entryID]; alreadyReserved {
			continue
		}
		return entryID, nil
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate reusable game entries failed: %w", err)
	}
	return "", nil
}
