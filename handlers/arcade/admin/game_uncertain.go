package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	arcadegame "github.com/ericbaek/musecat-backend-core/handlers/arcade/game"
	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

const (
	arcadeGameRollbackAction = "rollback"
	arcadeGameConfirmAction  = "confirm"
)

type arcadeGameUncertainBody struct {
	AtomIDs []string `json:"atom_ids"`
}

type arcadeGameUncertainValidationError struct {
	message string
	details map[string]any
}

func (e *arcadeGameUncertainValidationError) Error() string {
	return e.message
}

func newArcadeGameUncertainValidationError(message string, details map[string]any) *arcadeGameUncertainValidationError {
	return &arcadeGameUncertainValidationError{
		message: message,
		details: details,
	}
}

// parseArcadeGameUncertainBody 는 관리자 요청 바디를 읽고 atom ID의 공백을 미리 제거한다.
func parseArcadeGameUncertainBody(re *core.RequestEvent) (arcadeGameUncertainBody, error) {
	var body arcadeGameUncertainBody
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return body, err
	}
	for i := range body.AtomIDs {
		body.AtomIDs[i] = strings.TrimSpace(body.AtomIDs[i])
	}
	return body, nil
}

// validateArcadeGameUncertainBody 는 DB 조회 전에 요청 형식만 먼저 검사한다.
func validateArcadeGameUncertainBody(body arcadeGameUncertainBody) error {
	if len(body.AtomIDs) == 0 {
		return fmt.Errorf("atom_ids is required")
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

// buildArcadeGameUncertainUpdateBody 는 선택한 atom ID들을 전체 업데이트 payload로 변환한다.
// 선택된 atom들이 같은 arcade와 현재 활성 게임에 속하는지도 함께 검증한다.
func buildArcadeGameUncertainUpdateBody(txApp core.App, atomIDs []string, action string) (arcadegame.UpdateArcadeGameBody, int, error) {
	selected := map[string]struct{}{}
	for _, atomID := range atomIDs {
		selected[atomID] = struct{}{}
	}

	var arcadeID string
	var sourceMoleculeID string
	var sourceAtomID string
	for i, atomID := range atomIDs {
		// CollectionArcadeGameAtoms 는 관리자 요청으로 선택된 atom row를 저장한다.
		atomRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcadeGameAtoms, atomID)
		if err != nil {
			return arcadegame.UpdateArcadeGameBody{}, 0, newArcadeGameUncertainValidationError(
				fmt.Sprintf("atom_ids[%d] not found", i),
				map[string]any{"atom_id": atomID},
			)
		}

		moleculeID := strings.TrimSpace(atomRec.GetString("molecule"))
		if moleculeID == "" {
			return arcadegame.UpdateArcadeGameBody{}, 0, newArcadeGameUncertainValidationError(
				fmt.Sprintf("atom_ids[%d] has no molecule", i),
				map[string]any{"atom_id": atomID},
			)
		}
		// CollectionArcadeGame 는 각 atom이 참조하는 molecule/game row를 저장한다.
		moleculeRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcadeGame, moleculeID)
		if err != nil {
			return arcadegame.UpdateArcadeGameBody{}, 0, newArcadeGameUncertainValidationError(
				fmt.Sprintf("atom_ids[%d] molecule not found", i),
				map[string]any{"atom_id": atomID, "molecule_id": moleculeID},
			)
		}

		itemArcadeID := strings.TrimSpace(moleculeRec.GetString("arcade"))
		if itemArcadeID == "" {
			return arcadegame.UpdateArcadeGameBody{}, 0, newArcadeGameUncertainValidationError(
				fmt.Sprintf("atom_ids[%d] molecule has no arcade", i),
				map[string]any{"atom_id": atomID, "molecule_id": moleculeID},
			)
		}
		if arcadeID == "" {
			arcadeID = itemArcadeID
			sourceMoleculeID = moleculeID
			sourceAtomID = atomID
			continue
		}
		if arcadeID != itemArcadeID {
			return arcadegame.UpdateArcadeGameBody{}, 0, newArcadeGameUncertainValidationError(
				fmt.Sprintf("atom_ids[%d] must belong to the same arcade", i),
				map[string]any{
					"atom_id":        atomID,
					"atom_molecule":  moleculeID,
					"current_arcade": arcadeID,
				},
			)
		}
		if sourceMoleculeID != moleculeID {
			return arcadegame.UpdateArcadeGameBody{}, 0, newArcadeGameUncertainValidationError(
				fmt.Sprintf("atom_ids[%d] must belong to the same current game", i),
				map[string]any{
					"atom_id":            atomID,
					"atom_molecule":      moleculeID,
					"source_atom_id":     sourceAtomID,
					"source_molecule_id": sourceMoleculeID,
				},
			)
		}
	}

	// 상위 arcade row를 읽어 선택된 atom들이 현재 활성 게임에 속하는지 확인한다.
	arcadeRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcade, arcadeID)
	if err != nil {
		return arcadegame.UpdateArcadeGameBody{}, 0, newArcadeGameUncertainValidationError(
			"arcade not found",
			map[string]any{"arcade_id": arcadeID},
		)
	}
	if sourceMoleculeID == "" {
		return arcadegame.UpdateArcadeGameBody{}, 0, newArcadeGameUncertainValidationError(
			"source game is empty",
			map[string]any{"arcade_id": arcadeID},
		)
	}
	currentMoleculeID := strings.TrimSpace(arcadeRec.GetString("game"))
	if currentMoleculeID == "" {
		return arcadegame.UpdateArcadeGameBody{}, 0, newArcadeGameUncertainValidationError(
			"arcade.game is empty",
			map[string]any{
				"arcade_id":          arcadeID,
				"source_molecule_id": sourceMoleculeID,
				"source_atom_id":     sourceAtomID,
			},
		)
	}
	if currentMoleculeID != sourceMoleculeID {
		return arcadegame.UpdateArcadeGameBody{}, 0, newArcadeGameUncertainValidationError(
			"atom_ids are not part of the current game",
			map[string]any{
				"arcade_id":          arcadeID,
				"current_game":       currentMoleculeID,
				"source_molecule_id": sourceMoleculeID,
				"source_atom_id":     sourceAtomID,
			},
		)
	}

	// 현재 게임의 모든 atom을 읽어 selected atom만이 아니라 전체 game body를 다시 구성한다.
	currentAtoms, err := txApp.FindRecordsByFilter(
		arcadeinternal.CollectionArcadeGameAtoms,
		"molecule={:id}",
		"+created",
		0,
		0,
		dbx.Params{"id": currentMoleculeID},
	)
	if err != nil {
		return arcadegame.UpdateArcadeGameBody{}, 0, fmt.Errorf("failed to load current game atoms: %w", err)
	}

	body := arcadegame.UpdateArcadeGameBody{
		Arcade: arcadeID,
		Games:  make([]arcadegame.GameAtomInput, 0, len(currentAtoms)),
	}

	// currentAtomIDs 는 현재 게임 포함 여부를 확인하고,
	// currentVersionIDs 는 클라이언트가 atom ID 대신 version ID를 보냈는지 감지한다.
	currentAtomIDs := map[string]struct{}{}
	currentVersionIDs := map[string]struct{}{}

	for i, atom := range currentAtoms {
		input, err := arcadegame.BuildGameAtomInputFromRecord(atom)
		if err != nil {
			return arcadegame.UpdateArcadeGameBody{}, 0, fmt.Errorf("failed to decode atom %s: %w", atom.Id, err)
		}
		input.PrevID = atom.Id
		currentAtomIDs[atom.Id] = struct{}{}
		if versionID := strings.TrimSpace(atom.GetString("game")); versionID != "" {
			currentVersionIDs[versionID] = struct{}{}
		}

		if _, ok := selected[atom.Id]; ok {
			if !atom.GetBool("uncertain") {
				return arcadegame.UpdateArcadeGameBody{}, 0, &arcadeGameUncertainValidationError{message: fmt.Sprintf("atom_ids[%d] must be uncertain", i)}
			}
			if action == arcadeGameRollbackAction {
				// rollback 은 atom의 이전 확정 game version을 복원한다.
				prevGame := strings.TrimSpace(atom.GetString("prev_game"))
				if prevGame == "" {
					return arcadegame.UpdateArcadeGameBody{}, 0, &arcadeGameUncertainValidationError{message: fmt.Sprintf("atom_ids[%d] prev_game is required for rollback", i)}
				}
				if _, err := txApp.FindRecordById(arcadeinternal.CollectionGameSeriesVersion, prevGame); err != nil {
					return arcadegame.UpdateArcadeGameBody{}, 0, &arcadeGameUncertainValidationError{message: fmt.Sprintf("atom_ids[%d] prev_game not found", i)}
				}
				input.Game = prevGame
			}
			input.Uncertain = false
			input.PrevGame = ""
		}

		body.Games = append(body.Games, input)
	}

	for _, atomID := range atomIDs {
		if _, ok := currentAtomIDs[atomID]; !ok {
			if _, looksLikeVersionID := currentVersionIDs[atomID]; looksLikeVersionID {
				return arcadegame.UpdateArcadeGameBody{}, 0, newArcadeGameUncertainValidationError(
					fmt.Sprintf("atom_ids %q looks like a game version id; use game.items[].id", atomID),
					map[string]any{
						"atom_id":   atomID,
						"arcade_id": arcadeID,
					},
				)
			}
			return arcadegame.UpdateArcadeGameBody{}, 0, newArcadeGameUncertainValidationError(
				fmt.Sprintf("atom_ids %q is not part of the current game", atomID),
				map[string]any{
					"atom_id":   atomID,
					"arcade_id": arcadeID,
				},
			)
		}
	}

	return body, len(selected), nil
}

// applyArcadeGameUncertainAction 는 confirm과 rollback이 공유하는 HTTP 핸들러다.
func applyArcadeGameUncertainAction(re *core.RequestEvent, action string) error {
	body, err := parseArcadeGameUncertainBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}
	if err := validateArcadeGameUncertainBody(body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}

	var newMoleculeID string
	arcadeID := ""
	var totalCount int
	if err := re.App.RunInTransaction(func(txApp core.App) error {
		updateBody, _, err := buildArcadeGameUncertainUpdateBody(txApp, body.AtomIDs, action)
		if err != nil {
			return err
		}
		arcadeID = updateBody.Arcade
		totalCount = len(updateBody.Games)
		createdBy := ""
		if re.Auth != nil {
			createdBy = re.Auth.Id
		}
		newMoleculeID, err = arcadegame.UpdateArcadeGameTxFromExistingAtoms(txApp, updateBody, createdBy, action)
		return err
	}); err != nil {
		var validationErr *arcadeGameUncertainValidationError
		if errors.As(err, &validationErr) {
			payload := map[string]any{
				"error":   "validation failed",
				"details": validationErr.Error(),
			}
			if len(validationErr.details) > 0 {
				payload["context"] = validationErr.details
			}
			return re.JSON(http.StatusBadRequest, payload)
		}
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "transaction failed",
			"details": err.Error(),
		})
	}

	gameValue, ok := arcadeinternal.BuildExpandedGameValue(re.App, newMoleculeID)
	if !ok {
		gameValue = map[string]any{
			"id":    newMoleculeID,
			"items": []map[string]any{},
		}
	}

	return re.JSON(http.StatusOK, map[string]any{
		"action":         action,
		"arcade":         arcadeID,
		"game":           gameValue,
		"count":          totalCount,
		"selected_count": len(body.AtomIDs),
	})
}

func RollbackArcadeGameUncertain(re *core.RequestEvent) error {
	return applyArcadeGameUncertainAction(re, arcadeGameRollbackAction)
}

// ConfirmArcadeGameUncertain 은 prev_game을 복원하지 않고 uncertain 상태만 해제한다.
func ConfirmArcadeGameUncertain(re *core.RequestEvent) error {
	return applyArcadeGameUncertainAction(re, arcadeGameConfirmAction)
}
