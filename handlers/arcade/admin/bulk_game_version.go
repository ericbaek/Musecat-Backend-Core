package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

const bulkGameVersionChanged = "bulk_game_version"

type BulkUpdateArcadeGameVersionBody struct {
	AtomIDs                  []string `json:"atom_ids"`
	CurrentGameVersionSeries string   `json:"current_game_version_series"`
	NewGameVersionSeries     string   `json:"new_game_version_series"`
}

type bulkGameVersionValidationError struct {
	message string
}

func (e *bulkGameVersionValidationError) Error() string {
	return e.message
}

type bulkGameVersionLogItem struct {
	AtomID     string `json:"atom_id"`
	ArcadeID   string `json:"arcade_id"`
	ArcadeName string `json:"arcade_name"`
}

type bulkGameVersionLog struct {
	Type       string                   `json:"type"`
	Version    int                      `json:"version"`
	BeforeGame string                   `json:"before_game"`
	AfterGame  string                   `json:"after_game"`
	Items      []bulkGameVersionLogItem `json:"items"`
}

func parseBulkUpdateArcadeGameVersionBody(re *core.RequestEvent) (BulkUpdateArcadeGameVersionBody, error) {
	var body BulkUpdateArcadeGameVersionBody
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return body, err
	}
	body.CurrentGameVersionSeries = strings.TrimSpace(body.CurrentGameVersionSeries)
	body.NewGameVersionSeries = strings.TrimSpace(body.NewGameVersionSeries)
	for i := range body.AtomIDs {
		body.AtomIDs[i] = strings.TrimSpace(body.AtomIDs[i])
	}
	return body, nil
}

func validateBulkUpdateArcadeGameVersionBody(body BulkUpdateArcadeGameVersionBody) error {
	if len(body.AtomIDs) == 0 {
		return fmt.Errorf("atom_ids is required")
	}
	if body.CurrentGameVersionSeries == "" {
		return fmt.Errorf("current_game_version_series is required")
	}
	if len(body.CurrentGameVersionSeries) != 15 {
		return fmt.Errorf("current_game_version_series must be a valid record id")
	}
	if body.NewGameVersionSeries == "" {
		return fmt.Errorf("new_game_version_series is required")
	}
	if len(body.NewGameVersionSeries) != 15 {
		return fmt.Errorf("new_game_version_series must be a valid record id")
	}
	if body.CurrentGameVersionSeries == body.NewGameVersionSeries {
		return fmt.Errorf("new_game_version_series must be different from current_game_version_series")
	}

	seen := map[string]struct{}{}
	for i, atomID := range body.AtomIDs {
		if atomID == "" {
			return fmt.Errorf("atom_ids[%d] is required", i)
		}
		if len(atomID) != 15 {
			return fmt.Errorf("atom_ids[%d] must be a valid record id", i)
		}
		if _, ok := seen[atomID]; ok {
			return fmt.Errorf("atom_ids[%d] is duplicated", i)
		}
		seen[atomID] = struct{}{}
	}

	return nil
}

func BulkUpdateArcadeGameVersion(re *core.RequestEvent) error {
	body, err := parseBulkUpdateArcadeGameVersionBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}
	if err := validateBulkUpdateArcadeGameVersionBody(body); err != nil {
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

	type targetAtom struct {
		record *core.Record
		item   bulkGameVersionLogItem
	}

	seenArcades := map[string]string{}
	targets := make([]targetAtom, 0, len(body.AtomIDs))

	if err := re.App.RunInTransaction(func(txApp core.App) error {
		currentVersionRec, err := txApp.FindRecordById(arcadeinternal.CollectionGameSeriesVersion, body.CurrentGameVersionSeries)
		if err != nil {
			return &bulkGameVersionValidationError{message: "current_game_version_series not found"}
		}
		_ = currentVersionRec

		if _, err := txApp.FindRecordById(arcadeinternal.CollectionGameSeriesVersion, body.NewGameVersionSeries); err != nil {
			return &bulkGameVersionValidationError{message: "new_game_version_series not found"}
		}

		for i, atomID := range body.AtomIDs {
			atomRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcadeGameAtoms, atomID)
			if err != nil {
				return &bulkGameVersionValidationError{message: fmt.Sprintf("atom_ids[%d] not found", i)}
			}

			currentGame := strings.TrimSpace(atomRec.GetString("game"))
			if currentGame != body.CurrentGameVersionSeries {
				return &bulkGameVersionValidationError{message: fmt.Sprintf("atom_ids[%d] game must match current_game_version_series", i)}
			}

			moleculeID := strings.TrimSpace(atomRec.GetString("molecule"))
			if moleculeID == "" {
				return &bulkGameVersionValidationError{message: fmt.Sprintf("atom_ids[%d] has no molecule", i)}
			}
			moleculeRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcadeGame, moleculeID)
			if err != nil {
				return &bulkGameVersionValidationError{message: fmt.Sprintf("atom_ids[%d] molecule not found", i)}
			}

			itemArcadeID := strings.TrimSpace(moleculeRec.GetString("arcade"))
			if itemArcadeID == "" {
				return &bulkGameVersionValidationError{message: fmt.Sprintf("atom_ids[%d] molecule has no arcade", i)}
			}

			arcadeName, err := loadArcadeName(txApp, itemArcadeID)
			if err != nil {
				return &bulkGameVersionValidationError{message: fmt.Sprintf("atom_ids[%d] %s", i, err.Error())}
			}
			seenArcades[itemArcadeID] = arcadeName

			targets = append(targets, targetAtom{
				record: atomRec,
				item: bulkGameVersionLogItem{
					AtomID:     atomRec.Id,
					ArcadeID:   itemArcadeID,
					ArcadeName: arcadeName,
				},
			})
		}

		for _, target := range targets {
			target.record.Set("prev_game", body.CurrentGameVersionSeries)
			target.record.Set("uncertain", true)
			target.record.Set("game", body.NewGameVersionSeries)
			if err := txApp.Save(target.record); err != nil {
				return fmt.Errorf("failed to update atom %s: %w", target.record.Id, err)
			}
		}

		coll, err := txApp.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeChangelog)
		if err != nil {
			return fmt.Errorf("failed to find arcade_changelog collection: %w", err)
		}

		log := bulkGameVersionLog{
			Type:       bulkGameVersionChanged + "_diff",
			Version:    1,
			BeforeGame: body.CurrentGameVersionSeries,
			AfterGame:  body.NewGameVersionSeries,
			Items:      make([]bulkGameVersionLogItem, 0, len(targets)),
		}
		for _, target := range targets {
			log.Items = append(log.Items, target.item)
		}

		rec := core.NewRecord(coll)
		rec.Set("changed", bulkGameVersionChanged)
		rec.Set("from", body.CurrentGameVersionSeries)
		rec.Set("to", body.NewGameVersionSeries)
		rec.Set("by", re.Auth.Id)
		rec.Set("log", log)

		if err := txApp.Save(rec); err != nil {
			return fmt.Errorf("failed to create arcade_changelog: %w", err)
		}

		return nil
	}); err != nil {
		var validationErr *bulkGameVersionValidationError
		if errors.As(err, &validationErr) {
			return re.JSON(http.StatusBadRequest, map[string]any{
				"error":   "validation failed",
				"details": validationErr.Error(),
			})
		}
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "bulk update failed",
			"details": err.Error(),
		})
	}

	return re.JSON(http.StatusOK, map[string]any{
		"from":      body.CurrentGameVersionSeries,
		"to":        body.NewGameVersionSeries,
		"count":     len(targets),
		"changelog": bulkGameVersionChanged,
	})
}

func loadArcadeName(app core.App, arcadeID string) (string, error) {
	arcadeRec, err := app.FindRecordById(arcadeinternal.CollectionArcade, arcadeID)
	if err != nil {
		return "", fmt.Errorf("arcade not found")
	}
	basicID := strings.TrimSpace(arcadeRec.GetString("basic"))
	if basicID == "" {
		return "", fmt.Errorf("arcade basic is required")
	}
	basicRec, err := app.FindRecordById(arcadeinternal.CollectionArcadeBasic, basicID)
	if err != nil {
		return "", fmt.Errorf("arcade basic not found")
	}
	arcadeName := strings.TrimSpace(basicRec.GetString("name"))
	if arcadeName == "" {
		return "", fmt.Errorf("arcade name is required")
	}
	return arcadeName, nil
}
