package flag

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

const flagDeleteWindow = 15 * time.Minute

type DeleteArcadeFlagBody struct {
	Flag string `json:"flag"`
}

func parseDeleteArcadeFlagBody(re *core.RequestEvent) (DeleteArcadeFlagBody, error) {
	var body DeleteArcadeFlagBody
	err := json.NewDecoder(re.Request.Body).Decode(&body)
	return body, err
}

func validateDeleteArcadeFlagBody(body *DeleteArcadeFlagBody) error {
	body.Flag = strings.TrimSpace(body.Flag)
	if body.Flag == "" {
		return fmt.Errorf("flag is required")
	}
	return nil
}

func DeleteArcadeFlag(re *core.RequestEvent) error {
	body, err := parseDeleteArcadeFlagBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}
	if err := validateDeleteArcadeFlagBody(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}

	now := time.Now().UTC()
	var arcadeID string
	var expandedGameValue map[string]any
	var xpFeedback userhandler.ExpFeedback
	if err := re.App.RunInTransaction(func(txApp core.App) error {
		flagRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcadeFlag, body.Flag)
		if err != nil {
			return fmt.Errorf("flag not found: %w", err)
		}
		arcadeID = flagRec.GetString("arcade")
		baseExp, err := userhandler.LoadCurrentExp(txApp, re.Auth.Id)
		if err != nil {
			return fmt.Errorf("failed to load current exp: %w", err)
		}
		currentExp := baseExp

		if flagRec.GetString("createdBy") != re.Auth.Id {
			return fmt.Errorf("only the flag creator can delete this flag")
		}

		createdAt := flagRec.GetDateTime("created").Time().UTC()
		if createdAt.IsZero() {
			createdAt = now
		}
		if now.Sub(createdAt) > flagDeleteWindow {
			return fmt.Errorf("flag can only be deleted within 15 minutes of creation")
		}

		if err := txApp.Delete(flagRec); err != nil {
			return fmt.Errorf("failed to delete flag: %w", err)
		}
		if arcadeRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcade, arcadeID); err == nil && arcadeRec.GetBool("public") {
			positiveKind := userhandler.FlagKind(body.Flag)
			wasAwarded, err := userhandler.HasLevelLogKind(txApp, re.Auth.Id, positiveKind)
			if err != nil {
				return err
			}
			if wasAwarded {
				nextExp, _, err := userhandler.AwardExpTx(txApp, re.Auth.Id, "xp:flag-delete:"+body.Flag, -5, baseExp)
				if err != nil {
					return err
				}
				currentExp = nextExp
			}
		}

		arcadeRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcade, arcadeID)
		if err != nil {
			return fmt.Errorf("arcade not found: %w", err)
		}
		if gameValue, ok := arcadeinternal.BuildExpandedGameValue(txApp, arcadeRec.GetString("game_state")); ok {
			expandedGameValue = gameValue
		} else {
			expandedGameValue = map[string]any{
				"id":    arcadeRec.GetString("game_state"),
				"items": []map[string]any{},
			}
		}

		xpFeedback = userhandler.BuildExpFeedback(baseExp, currentExp)
		return nil
	}); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "flag delete failed",
			"details": err.Error(),
		})
	}

	return re.JSON(http.StatusOK, map[string]any{
		"flag":        body.Flag,
		"deleted":     true,
		"game":        expandedGameValue,
		"xp_feedback": xpFeedback,
	})
}
