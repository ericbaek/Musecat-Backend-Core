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

const (
	arcadeGameRollbackAction = "rollback"
	arcadeGameConfirmAction  = "confirm"
)

type arcadeGameUncertainBody struct {
	Arcade      string   `json:"arcade"`
	GameIDs     []string `json:"game_ids"`
	BaseStateID string   `json:"base_state_id"`
}

func applyArcadeGameUncertainAction(re *core.RequestEvent, action string) error {
	var request arcadeGameUncertainBody
	if err := json.NewDecoder(re.Request.Body).Decode(&request); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body", "details": err.Error()})
	}
	request.Arcade, request.BaseStateID = strings.TrimSpace(request.Arcade), strings.TrimSpace(request.BaseStateID)
	if len(request.Arcade) != 15 || len(request.GameIDs) == 0 {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "validation failed", "details": "arcade and game_ids are required"})
	}
	seen := map[string]struct{}{}
	for i := range request.GameIDs {
		request.GameIDs[i] = strings.TrimSpace(request.GameIDs[i])
		if len(request.GameIDs[i]) != 15 {
			return re.JSON(http.StatusBadRequest, map[string]any{"error": "validation failed", "details": fmt.Sprintf("game_ids[%d] is invalid", i)})
		}
		if _, ok := seen[request.GameIDs[i]]; ok {
			return re.JSON(http.StatusBadRequest, map[string]any{"error": "validation failed", "details": "game_ids is duplicated"})
		}
		seen[request.GameIDs[i]] = struct{}{}
	}
	var stateID string
	if err := re.App.RunInTransaction(func(tx core.App) error {
		body, err := arcadegame.BuildUpdateBodyFromCurrentState(tx, request.Arcade)
		if err != nil {
			return err
		}
		if request.BaseStateID != body.BaseStateID {
			return fmt.Errorf("game state conflict")
		}
		selected := map[string]struct{}{}
		for _, id := range request.GameIDs {
			selected[id] = struct{}{}
		}
		for i := range body.Games {
			if _, ok := selected[body.Games[i].ID]; !ok {
				continue
			}
			if !body.Games[i].Uncertain {
				return fmt.Errorf("game_id %s is not uncertain", body.Games[i].ID)
			}
			if action == arcadeGameRollbackAction {
				previous := strings.TrimSpace(body.Games[i].PrevGame)
				if previous == "" {
					return fmt.Errorf("game_id %s has no previous_version", body.Games[i].ID)
				}
				body.Games[i].Game = previous
			}
			body.Games[i].Uncertain = false
			body.Games[i].PrevGame = ""
			delete(selected, body.Games[i].ID)
		}
		if len(selected) > 0 {
			return fmt.Errorf("game_id is not active in the current game state")
		}
		stateID, err = arcadegame.UpdateArcadeGameTxFromExistingAtoms(tx, body, re.Auth.Id, action)
		return err
	}); err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "conflict") {
			status = http.StatusConflict
		}
		return re.JSON(status, map[string]any{"error": "uncertain game update failed", "details": err.Error()})
	}
	gameValue, ok := arcadeinternal.BuildExpandedGameValue(re.App, stateID)
	if !ok {
		gameValue = map[string]any{"id": stateID, "items": []map[string]any{}}
	}
	return re.JSON(http.StatusOK, map[string]any{"action": action, "arcade": request.Arcade, "game": gameValue, "count": len(gameValue["items"].([]map[string]any)), "selected_count": len(request.GameIDs)})
}
func RollbackArcadeGameUncertain(re *core.RequestEvent) error {
	return applyArcadeGameUncertainAction(re, arcadeGameRollbackAction)
}
func ConfirmArcadeGameUncertain(re *core.RequestEvent) error {
	return applyArcadeGameUncertainAction(re, arcadeGameConfirmAction)
}
