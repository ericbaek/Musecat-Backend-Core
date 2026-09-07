package public

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

type verifyArcadeLocationBody struct {
	Arcade   string  `json:"arcade"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	Accuracy float64 `json:"accuracy"`
}

var errOutsidePublicLocationRadius = errors.New("outside arcade location verification radius")

// VerifyArcadeLocation stores a one-time publication proof for the arcade's
// current basic-information revision. It does not create a Passport visit or
// award XP; Passport remains the post-publication visit system.
func VerifyArcadeLocation(re *core.RequestEvent) error {
	if re.Auth == nil {
		return re.JSON(http.StatusUnauthorized, map[string]any{"error": "authentication required"})
	}

	var body verifyArcadeLocationBody
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
	}
	body.Arcade = strings.TrimSpace(body.Arcade)
	if body.Arcade == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "arcade is required"})
	}
	if err := arcadeinternal.ValidateLocationCoords(body.Lat, body.Lon); err != nil ||
		math.IsNaN(body.Accuracy) || body.Accuracy < 0 || body.Accuracy > publicLocationMaxAccuracyMeters {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid verification location or accuracy"})
	}

	arcade, err := re.App.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
	if err != nil {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "arcade not found"})
	}
	if arcade.GetString("createdBy") != re.Auth.Id {
		return re.JSON(http.StatusForbidden, map[string]any{"error": "only the creator can verify arcade location"})
	}
	if arcade.GetBool("public") || arcade.GetBool("closed") {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "location verification is only available for an open private arcade"})
	}

	var distance float64
	var basicID string
	err = re.App.RunInTransaction(func(txApp core.App) error {
		txArcade, err := txApp.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
		if err != nil {
			return fmt.Errorf("arcade not found: %w", err)
		}
		if txArcade.GetString("createdBy") != re.Auth.Id {
			return fmt.Errorf("only the creator can verify arcade location")
		}
		if txArcade.GetBool("public") || txArcade.GetBool("closed") {
			return fmt.Errorf("location verification is only available for an open private arcade")
		}

		basicID = strings.TrimSpace(txArcade.GetString("basic"))
		if basicID == "" {
			return ErrArcadeBasicEmpty
		}
		basic, err := txApp.FindRecordById(arcadeinternal.CollectionArcadeBasic, basicID)
		if err != nil {
			return ErrArcadeBasicLocationMissing
		}
		lat, lon, ok := arcadeinternal.ReadLocation(basic.Get("location"))
		if !ok {
			return ErrArcadeBasicLocationMissing
		}
		distance = arcadeinternal.DistanceKm(body.Lat, body.Lon, lat, lon) * 1000
		if distance > publicLocationRadiusMeters {
			return errOutsidePublicLocationRadius
		}

		txArcade.Set("location_verification_basic", basicID)
		txArcade.Set("location_verification_by", re.Auth.Id)
		txArcade.Set("location_verification_at", time.Now().UTC().Format(time.RFC3339Nano))
		txArcade.Set("location_verification_distance_meters", distance)
		txArcade.Set("location_verification_accuracy_meters", body.Accuracy)
		return txApp.Save(txArcade)
	})
	if err != nil {
		switch {
		case errors.Is(err, errOutsidePublicLocationRadius):
			return re.JSON(http.StatusForbidden, map[string]any{
				"error":           err.Error(),
				"distance_meters": distance,
			})
		case errors.Is(err, ErrArcadeBasicEmpty), errors.Is(err, ErrArcadeBasicLocationMissing):
			return re.JSON(http.StatusConflict, map[string]any{"error": err.Error()})
		default:
			return re.JSON(http.StatusBadGateway, map[string]any{
				"error":   "location verification failed",
				"details": err.Error(),
			})
		}
	}

	return re.JSON(http.StatusOK, map[string]any{
		"arcade":          body.Arcade,
		"verified":        true,
		"basic":           basicID,
		"distance_meters": distance,
		"accuracy_meters": body.Accuracy,
	})
}
