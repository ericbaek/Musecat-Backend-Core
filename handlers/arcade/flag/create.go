package flag

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

var validDisruptions = map[string]struct{}{
	"unplayable": {},
	"major":      {},
	"bearable":   {},
	"minor":      {},
}

const maxFlagPhotosPerRequest = 3

type CreateArcadeFlagBody struct {
	Arcade     string `json:"arcade"`
	GameAtomID string `json:"game_atom_id"`
	Disruption string `json:"disruption"`
	Message    string `json:"message"`
	Photos     []*filesystem.File
}

func parseCreateArcadeFlagBody(re *core.RequestEvent) (CreateArcadeFlagBody, error) {
	contentType := strings.ToLower(strings.TrimSpace(re.Request.Header.Get("Content-Type")))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := re.Request.ParseMultipartForm(32 << 20); err != nil {
			return CreateArcadeFlagBody{}, err
		}

		body := CreateArcadeFlagBody{
			Arcade:     re.Request.FormValue("arcade"),
			GameAtomID: re.Request.FormValue("game_atom_id"),
			Disruption: re.Request.FormValue("disruption"),
			Message:    re.Request.FormValue("message"),
		}

		files, err := re.FindUploadedFiles("photos")
		if err != nil {
			if errors.Is(err, http.ErrMissingFile) {
				return body, nil
			}
			return CreateArcadeFlagBody{}, err
		}

		body.Photos = files
		return body, nil
	}

	var body CreateArcadeFlagBody
	err := json.NewDecoder(re.Request.Body).Decode(&body)
	return body, err
}

func validateCreateArcadeFlagBody(body *CreateArcadeFlagBody) error {
	body.Arcade = strings.TrimSpace(body.Arcade)
	body.GameAtomID = strings.TrimSpace(body.GameAtomID)
	body.Disruption = strings.TrimSpace(body.Disruption)
	body.Message = strings.TrimSpace(body.Message)

	if body.Arcade == "" {
		return fmt.Errorf("arcade is required")
	}
	if body.GameAtomID == "" {
		return fmt.Errorf("game_atom_id is required")
	}
	if body.Disruption == "" {
		return fmt.Errorf("disruption is required")
	}
	if _, ok := validDisruptions[body.Disruption]; !ok {
		return fmt.Errorf("disruption must be one of unplayable, major, bearable, minor")
	}
	if body.Message == "" {
		return fmt.Errorf("message is required")
	}
	if len(body.Photos) > maxFlagPhotosPerRequest {
		return fmt.Errorf("photos must have at most %d items", maxFlagPhotosPerRequest)
	}

	return nil
}

func CreateArcadeFlag(re *core.RequestEvent) error {
	body, err := parseCreateArcadeFlagBody(re)
	if err != nil {
		errorMessage := "invalid JSON body"
		contentType := strings.ToLower(strings.TrimSpace(re.Request.Header.Get("Content-Type")))
		if strings.HasPrefix(contentType, "multipart/form-data") {
			errorMessage = "invalid multipart body"
		}
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   errorMessage,
			"details": err.Error(),
		})
	}
	if err := validateCreateArcadeFlagBody(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}

	var newFlagID string
	var expandedGameValue map[string]any
	var xpFeedback userhandler.ExpFeedback

	if err := re.App.RunInTransaction(func(txApp core.App) error {
		arcadeRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
		if err != nil {
			return fmt.Errorf("arcade not found: %w", err)
		}

		atomRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcadeGameAtoms, body.GameAtomID)
		if err != nil {
			return fmt.Errorf("game atom not found: %w", err)
		}

		moleculeID := strings.TrimSpace(atomRec.GetString("molecule"))
		if moleculeID == "" {
			return fmt.Errorf("game atom has no molecule")
		}

		moleculeRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcadeGame, moleculeID)
		if err != nil {
			return fmt.Errorf("game molecule not found: %w", err)
		}
		if moleculeRec.GetString("arcade") != body.Arcade {
			return fmt.Errorf("game_atom_id does not belong to arcade")
		}
		baseExp, err := userhandler.LoadCurrentExp(txApp, re.Auth.Id)
		if err != nil {
			return fmt.Errorf("failed to load current exp: %w", err)
		}
		currentExp := baseExp

		flagColl, err := txApp.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeFlag)
		if err != nil {
			return fmt.Errorf("failed to find arcade_flag: %w", err)
		}

		flagRec := core.NewRecord(flagColl)
		flagRec.Set("arcade", body.Arcade)
		flagRec.Set("disruption", body.Disruption)
		flagRec.Set("message", body.Message)
		flagRec.Set("solved", false)
		flagRec.Set("createdBy", re.Auth.Id)
		if len(body.Photos) > 0 {
			flagRec.Set("photos", body.Photos)
		}
		if err := txApp.Save(flagRec); err != nil {
			return fmt.Errorf("failed to create arcade_flag: %w", err)
		}
		newFlagID = flagRec.Id

		flags := atomRec.GetStringSlice("flags")
		flags = append(flags, newFlagID)
		atomRec.Set("flags", flags)
		if err := txApp.Save(atomRec); err != nil {
			return fmt.Errorf("failed to update game atom flags: %w", err)
		}
		if arcadeRec.GetBool("public") {
			nextExp, _, err := userhandler.AwardExpTx(txApp, re.Auth.Id, userhandler.FlagKind(newFlagID), 5, baseExp)
			if err != nil {
				return err
			}
			currentExp = nextExp
		}

		if gameValue, ok := arcadeinternal.BuildExpandedGameValue(txApp, arcadeRec.GetString("game")); ok {
			expandedGameValue = gameValue
		} else {
			expandedGameValue = map[string]any{
				"id":    arcadeRec.GetString("game"),
				"items": []map[string]any{},
			}
		}

		xpFeedback = userhandler.BuildExpFeedback(baseExp, currentExp)
		return nil
	}); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "transaction failed",
			"details": err.Error(),
		})
	}

	return re.JSON(http.StatusOK, map[string]any{
		"arcade":      body.Arcade,
		"atom":        body.GameAtomID,
		"flag":        newFlagID,
		"game":        expandedGameValue,
		"xp_feedback": xpFeedback,
	})
}
