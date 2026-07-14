package photo

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

// UpdateArcadePhotoBody represents the request body for updating arcade photos.
type UpdateArcadePhotoBody struct {
	Arcade string   `json:"arcade"`
	Photos []string `json:"photos"`
}

func parseUpdatePhotoBody(re *core.RequestEvent) (UpdateArcadePhotoBody, error) {
	var body UpdateArcadePhotoBody
	err := json.NewDecoder(re.Request.Body).Decode(&body)
	return body, err
}

func normalizeAndValidatePhotoBody(body *UpdateArcadePhotoBody) error {
	if body.Arcade == "" {
		return fmt.Errorf("arcade is required")
	}
	if len(body.Photos) == 0 {
		return fmt.Errorf("photos must have at least one item")
	}

	seen := make(map[string]struct{}, len(body.Photos))
	normalized := make([]string, 0, len(body.Photos))
	for i, id := range body.Photos {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			return fmt.Errorf("photos[%d] is required", i)
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	body.Photos = normalized
	return nil
}

// UpdateArcadePhoto creates a new arcade_photo record with arcade_photo_atoms ids,
// promotes referenced atoms to public=true when needed, and updates arcade.photo.
func UpdateArcadePhoto(re *core.RequestEvent) error {
	body, err := parseUpdatePhotoBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}

	if err := normalizeAndValidatePhotoBody(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}

	var newPhotoID string
	var xpFeedback userhandler.ExpFeedback

	if err := re.App.RunInTransaction(func(txApp core.App) error {
		arcadeRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
		if err != nil {
			return fmt.Errorf("arcade not found: %w", err)
		}
		baseExp, err := userhandler.LoadCurrentExp(txApp, re.Auth.Id)
		if err != nil {
			return fmt.Errorf("failed to load current exp: %w", err)
		}
		currentExp := baseExp

		prevPhotoIDs := []string{}
		oldMoleculeID := strings.TrimSpace(arcadeRec.GetString("photo"))
		if oldMoleculeID != "" {
			prevPhotoRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcadePhoto, oldMoleculeID)
			if err != nil {
				return fmt.Errorf("failed to load previous arcade_photo: %w", err)
			}
			prevPhotoIDs = prevPhotoRec.GetStringSlice("photos")
		}
		prevPhotoSet := map[string]struct{}{}
		for _, photoID := range prevPhotoIDs {
			prevPhotoSet[strings.TrimSpace(photoID)] = struct{}{}
		}

		for i, atomID := range body.Photos {
			atom, err := txApp.FindRecordById(arcadeinternal.CollectionArcadePhotoAtoms, atomID)
			if err != nil {
				return fmt.Errorf("photos[%d] not found: %w", i, err)
			}
			if atom.GetString("arcade") != body.Arcade {
				return fmt.Errorf("photos[%d] does not belong to arcade", i)
			}
			if !atom.GetBool("public") {
				atom.Set("public", true)
				if err := txApp.Save(atom); err != nil {
					return fmt.Errorf("failed to promote photos[%d] to public: %w", i, err)
				}
			}
		}

		photoColl, err := txApp.FindCollectionByNameOrId(arcadeinternal.CollectionArcadePhoto)
		if err != nil {
			return fmt.Errorf("failed to find arcade_photo: %w", err)
		}

		photoRec := core.NewRecord(photoColl)
		photoRec.Set("arcade", body.Arcade)
		photoRec.Set("photos", body.Photos)
		photoRec.Set("createdBy", re.Auth.Id)
		if err := txApp.Save(photoRec); err != nil {
			return fmt.Errorf("failed to create arcade_photo: %w", err)
		}
		newPhotoID = photoRec.Id

		photoLogItems := make([]photoDiffLogItem, 0, len(body.Photos)+len(prevPhotoIDs))
		referencedPrev := map[string]struct{}{}
		for _, atomID := range body.Photos {
			_, existed := prevPhotoSet[atomID]
			prevID := ""
			if existed {
				prevID = atomID
				referencedPrev[atomID] = struct{}{}
			}
			photoLogItems = append(photoLogItems, buildPhotoDiffLogItem(atomID, prevID, existed))
		}
		for _, prevID := range prevPhotoIDs {
			prevID = strings.TrimSpace(prevID)
			if prevID == "" {
				continue
			}
			if _, ok := referencedPrev[prevID]; ok {
				continue
			}
			photoLogItems = append(photoLogItems, buildDeletedPhotoDiffLogItem(prevID))
		}

		if err := arcadeinternal.UpdateArcadeFieldsTxWithLogs(
			txApp,
			body.Arcade,
			map[string]any{"photo": newPhotoID},
			map[string]any{"photo": arcadeinternal.BuildChangelogEnvelope("photo", photoLogItems)},
			re.Auth.Id,
		); err != nil {
			return fmt.Errorf("failed to update arcade.photo: %w", err)
		}
		if arcadeRec.GetBool("public") {
			nextExp, _, err := userhandler.AwardArcadeEditExpTx(txApp, re.Auth.Id, body.Arcade, "photo", 3, baseExp, time.Now().UTC())
			if err != nil {
				return err
			}
			currentExp = nextExp
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
		"photo":       newPhotoID,
		"count":       len(body.Photos),
		"xp_feedback": xpFeedback,
	})
}
