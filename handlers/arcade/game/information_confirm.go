package game

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// ConfirmArcadeGameInformation confirms one stable arcade_game_entry in the
// current state. It creates a replacement batch; legacy atoms stay immutable.
func ConfirmArcadeGameInformation(re *core.RequestEvent) error {
	gameID := strings.TrimSpace(re.Request.URL.Query().Get("game_id"))
	if gameID == "" {
		gameID = strings.TrimSpace(re.Request.URL.Query().Get("id"))
	}
	if len(gameID) != 15 {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "validation failed", "details": "game_id is required and must be a valid record id"})
	}
	arcadeID := strings.TrimSpace(re.Request.URL.Query().Get("arcade"))
	if len(arcadeID) != 15 {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "validation failed", "details": "arcade is required and must be a valid record id"})
	}
	var stateID string
	if err := re.App.RunInTransaction(func(tx core.App) error {
		body, err := BuildUpdateBodyFromCurrentState(tx, arcadeID)
		if err != nil {
			return err
		}
		found := false
		for i := range body.Games {
			if body.Games[i].ID == gameID {
				body.Games[i].Confirm = true
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("game_id is not active in the current game state")
		}
		stateID, err = UpdateArcadeGameTxFromExistingAtoms(tx, body, re.Auth.Id, "confirm_information")
		return err
	}); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "information confirmation failed", "details": err.Error()})
	}
	return re.JSON(http.StatusOK, map[string]any{"arcade": arcadeID, "game_id": gameID, "state_id": stateID})
}
