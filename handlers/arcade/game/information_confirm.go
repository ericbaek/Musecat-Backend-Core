package game

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

type gameInformationConfirmLogItem struct {
	AtomID      string `json:"atom_id"`
	GameID      string `json:"game_id"`
	UpdatedFrom string `json:"updated_from"`
	UpdatedTo   string `json:"updated_to"`
}

type gameInformationConfirmLog struct {
	Type    string                          `json:"type"`
	Version int                             `json:"version"`
	Items   []gameInformationConfirmLogItem `json:"items"`
}

func parseGameInformationConfirmID(re *core.RequestEvent) string {
	return strings.TrimSpace(re.Request.URL.Query().Get("id"))
}

func validateGameInformationConfirmID(atomID string) error {
	if atomID == "" {
		return fmt.Errorf("id is required")
	}
	if len(atomID) != 15 {
		return fmt.Errorf("id must be a valid record id")
	}
	return nil
}

// ConfirmArcadeGameInformation marks a single game atom as freshly confirmed.
func ConfirmArcadeGameInformation(re *core.RequestEvent) error {
	atomID := parseGameInformationConfirmID(re)
	if err := validateGameInformationConfirmID(atomID); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}
	if re.Auth == nil || strings.TrimSpace(re.Auth.Id) == "" {
		return re.JSON(http.StatusUnauthorized, map[string]any{
			"error": "authentication required",
		})
	}

	var arcadeID string
	var versionID string
	var moleculeID string
	var oldUpdated string
	var newUpdated string

	if err := re.App.RunInTransaction(func(txApp core.App) error {
		atomRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcadeGameAtoms, atomID)
		if err != nil {
			return fmt.Errorf("game atom not found: %w", err)
		}

		versionID = strings.TrimSpace(atomRec.GetString("game"))
		if versionID == "" {
			return fmt.Errorf("game atom has no game")
		}

		moleculeID = strings.TrimSpace(atomRec.GetString("molecule"))
		if moleculeID == "" {
			return fmt.Errorf("game atom has no molecule")
		}

		gameRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcadeGame, moleculeID)
		if err != nil {
			return fmt.Errorf("game not found: %w", err)
		}
		arcadeID = strings.TrimSpace(gameRec.GetString("arcade"))
		if arcadeID == "" {
			return fmt.Errorf("game has no arcade")
		}

		oldUpdated = arcadeinternal.GameAtomUpdatedValue(atomRec)
		confirmAt := time.Now().UTC()
		atomRec.Set("updated", confirmAt)
		if err := txApp.Save(atomRec); err != nil {
			return fmt.Errorf("failed to confirm game atom: %w", err)
		}
		newUpdated = arcadeinternal.GameAtomUpdatedValue(atomRec)
		if newUpdated == "" {
			newUpdated = confirmAt.Format(time.RFC3339Nano)
		}

		log := gameInformationConfirmLog{
			Type:    "game_information_confirm_diff",
			Version: 1,
			Items: []gameInformationConfirmLogItem{
				{
					AtomID:      atomID,
					GameID:      versionID,
					UpdatedFrom: oldUpdated,
					UpdatedTo:   newUpdated,
				},
			},
		}

		if err := arcadeinternal.WriteArcadeChangelogTx(
			txApp,
			arcadeID,
			"game",
			oldUpdated,
			newUpdated,
			re.Auth.Id,
			log,
		); err != nil {
			return fmt.Errorf("failed to write arcade changelog: %w", err)
		}

		return nil
	}); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "transaction failed",
			"details": err.Error(),
		})
	}

	return re.JSON(http.StatusOK, map[string]any{
		"arcade":  arcadeID,
		"game":    moleculeID,
		"atom":    atomID,
		"updated": newUpdated,
	})
}
