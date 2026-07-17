package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	arcadegame "github.com/ericbaek/musecat-backend-core/handlers/arcade/game"
	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

// Bulk version updates use stable entry ids and emit one normal game timeline
// event per arcade. They never modify legacy atoms or historical revisions.
type BulkUpdateArcadeGameVersionBody struct {
	GameIDs                  []string `json:"game_ids"`
	CurrentGameVersionSeries string   `json:"current_game_version_series"`
	NewGameVersionSeries     string   `json:"new_game_version_series"`
}

func BulkUpdateArcadeGameVersion(re *core.RequestEvent) error {
	var body BulkUpdateArcadeGameVersionBody
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body", "details": err.Error()})
	}
	body.CurrentGameVersionSeries = strings.TrimSpace(body.CurrentGameVersionSeries)
	body.NewGameVersionSeries = strings.TrimSpace(body.NewGameVersionSeries)
	if len(body.GameIDs) == 0 || len(body.CurrentGameVersionSeries) != 15 || len(body.NewGameVersionSeries) != 15 || body.CurrentGameVersionSeries == body.NewGameVersionSeries {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "validation failed", "details": "game_ids, current_game_version_series and new_game_version_series are required"})
	}
	for i := range body.GameIDs {
		body.GameIDs[i] = strings.TrimSpace(body.GameIDs[i])
		if len(body.GameIDs[i]) != 15 {
			return re.JSON(http.StatusBadRequest, map[string]any{"error": "validation failed", "details": fmt.Sprintf("game_ids[%d] is invalid", i)})
		}
	}
	updated := 0
	if err := re.App.RunInTransaction(func(tx core.App) error {
		newVersion, err := tx.FindRecordById(arcadeinternal.CollectionGameSeriesVersion, body.NewGameVersionSeries)
		if err != nil {
			return fmt.Errorf("new_game_version_series not found")
		}
		byArcade := map[string]map[string]struct{}{}
		for _, id := range body.GameIDs {
			entry, findErr := tx.FindRecordById(arcadeinternal.CollectionArcadeGameEntry, id)
			if findErr != nil {
				return fmt.Errorf("game_id %s not found", id)
			}
			if entry.GetString("series") != newVersion.GetString("series") {
				return fmt.Errorf("game_id %s cannot change series", id)
			}
			set := byArcade[entry.GetString("arcade")]
			if set == nil {
				set = map[string]struct{}{}
				byArcade[entry.GetString("arcade")] = set
			}
			if _, exists := set[id]; exists {
				return fmt.Errorf("game_ids is duplicated")
			}
			set[id] = struct{}{}
		}
		for arcadeID, selected := range byArcade {
			update, buildErr := arcadegame.BuildUpdateBodyFromCurrentState(tx, arcadeID)
			if buildErr != nil {
				return buildErr
			}
			for i := range update.Games {
				if _, ok := selected[update.Games[i].ID]; !ok {
					continue
				}
				if update.Games[i].Game != body.CurrentGameVersionSeries {
					return fmt.Errorf("game_id %s does not match current_game_version_series", update.Games[i].ID)
				}
				update.Games[i].PrevGame = update.Games[i].Game
				update.Games[i].Game = body.NewGameVersionSeries
				update.Games[i].Uncertain = true
				updated++
			}
			if _, err := arcadegame.UpdateArcadeGameTxFromExistingAtoms(tx, update, re.Auth.Id, "bulk_version"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "bulk update failed", "details": err.Error()})
	}
	return re.JSON(http.StatusOK, map[string]any{"from": body.CurrentGameVersionSeries, "to": body.NewGameVersionSeries, "count": updated, "changed": "game"})
}
