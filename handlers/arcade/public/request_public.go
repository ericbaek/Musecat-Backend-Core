package public

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

var (
	ErrArcadeBasicEmpty           = errors.New("arcade.basic is empty")
	ErrArcadeBasicLocationMissing = errors.New("missing arcade basic location")
	ErrFacilityPhotoRegistration  = errors.New("at least one facility photo must be registered before making arcade public")
	ErrArcadeGeoUnavailable       = errors.New("arcade country and timezone must be valid before making arcade public")
)

type RequestPublicArcadeBody struct {
	Arcade string `json:"arcade"`
}

func parseRequestPublicArcadeBody(re *core.RequestEvent) (RequestPublicArcadeBody, error) {
	var body RequestPublicArcadeBody
	err := json.NewDecoder(re.Request.Body).Decode(&body)
	return body, err
}

func validateRequestPublicArcadeBody(body RequestPublicArcadeBody) error {
	if body.Arcade == "" {
		return fmt.Errorf("arcade is required")
	}
	return nil
}

func hasGameRegistration(app core.App, moleculeID string) (bool, error) {
	moleculeID = strings.TrimSpace(moleculeID)
	if moleculeID == "" {
		return false, nil
	}

	if _, err := app.FindRecordById(arcadeinternal.CollectionArcadeGame, moleculeID); err != nil {
		return false, nil
	}

	atoms, err := app.FindRecordsByFilter(
		arcadeinternal.CollectionArcadeGameAtoms,
		"molecule={:id}",
		"",
		1,
		0,
		dbx.Params{"id": moleculeID},
	)
	if err != nil {
		return false, err
	}
	return len(atoms) > 0, nil
}

func hasSNSRegistration(app core.App, moleculeID string) (bool, error) {
	moleculeID = strings.TrimSpace(moleculeID)
	if moleculeID == "" {
		return false, nil
	}

	if _, err := app.FindRecordById(arcadeinternal.CollectionArcadeSNS, moleculeID); err != nil {
		return false, nil
	}

	atoms, err := app.FindRecordsByFilter(
		arcadeinternal.CollectionArcadeSNSAtoms,
		"molecule={:id}",
		"",
		1,
		0,
		dbx.Params{"id": moleculeID},
	)
	if err != nil {
		return false, err
	}
	return len(atoms) > 0, nil
}

func hasHourRegistration(app core.App, hourID string) (bool, error) {
	hourID = strings.TrimSpace(hourID)
	if hourID == "" {
		return false, nil
	}

	if _, err := app.FindRecordById(arcadeinternal.CollectionArcadeHour, hourID); err != nil {
		return false, nil
	}
	return true, nil
}

func hasPhotoRegistration(app core.App, moleculeID string) (bool, error) {
	moleculeID = strings.TrimSpace(moleculeID)
	if moleculeID == "" {
		return false, nil
	}

	rec, err := app.FindRecordById(arcadeinternal.CollectionArcadePhoto, moleculeID)
	if err != nil {
		return false, nil
	}
	return len(arcadeinternal.TrimmedStringSlice(rec.GetStringSlice("photos"))) > 0, nil
}

func requiresFacilityPhoto(country string) bool {
	return strings.EqualFold(strings.TrimSpace(country), "KR")
}

func RequestPublicArcade(re *core.RequestEvent) error {
	// 1) parse
	body, err := parseRequestPublicArcadeBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}

	// 2) validate basic constraints
	if err := validateRequestPublicArcadeBody(body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}

	// 3) load arcade
	arcade, err := re.App.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "arcade not found",
			"details": err.Error(),
		})
	}

	// 4) check conditions
	if arcade.GetString("createdBy") != re.Auth.Id {
		return re.JSON(http.StatusForbidden, map[string]any{
			"error": "only the creator can request public conversion",
		})
	}
	if arcade.GetBool("closed") {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "cannot request public conversion for closed arcade",
		})
	}
	if arcade.GetBool("public") {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "arcade is already public",
		})
	}

	hasGame, err := hasGameRegistration(re.App, arcade.GetString("game"))
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to validate game registration",
			"details": err.Error(),
		})
	}
	if !hasGame {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": "at least one game must be registered before making arcade public",
		})
	}

	hasSNS, err := hasSNSRegistration(re.App, arcade.GetString("sns"))
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to validate sns registration",
			"details": err.Error(),
		})
	}
	hasHour, err := hasHourRegistration(re.App, arcade.GetString("hour"))
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to validate hour registration",
			"details": err.Error(),
		})
	}
	if !hasSNS && !hasHour {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": "either sns or hour must be registered before making arcade public",
		})
	}

	// 5) make arcade public immediately.
	var xpFeedback userhandler.ExpFeedback
	if err := re.App.RunInTransaction(func(txApp core.App) error {
		txArcade, err := txApp.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
		if err != nil {
			return fmt.Errorf("arcade not found: %w", err)
		}
		baseExp, err := userhandler.LoadCurrentExp(txApp, re.Auth.Id)
		if err != nil {
			return fmt.Errorf("failed to load current exp: %w", err)
		}
		currentExp := baseExp

		basicID := strings.TrimSpace(txArcade.GetString("basic"))
		if basicID == "" {
			return ErrArcadeBasicEmpty
		}

		if _, err := txApp.FindRecordById(arcadeinternal.CollectionArcadeBasic, basicID); err != nil {
			return fmt.Errorf("failed to load arcade basic: %w", err)
		}
		country := strings.ToUpper(strings.TrimSpace(txArcade.GetString("country")))
		timezone := strings.TrimSpace(txArcade.GetString("timezone"))
		if len(country) != 2 || timezone == "" {
			return ErrArcadeGeoUnavailable
		}
		if _, err := time.LoadLocation(timezone); err != nil {
			return ErrArcadeGeoUnavailable
		}

		if requiresFacilityPhoto(country) {
			hasPhoto, err := hasPhotoRegistration(txApp, txArcade.GetString("photo"))
			if err != nil {
				return fmt.Errorf("failed to validate photo registration: %w", err)
			}
			if !hasPhoto {
				return ErrFacilityPhotoRegistration
			}
		}

		if err := arcadeinternal.UpdateArcadeFieldsTx(txApp, body.Arcade, map[string]any{
			"public": true,
		}, re.Auth.Id); err != nil {
			return err
		}

		if nextExp, _, err := userhandler.AwardExpTx(txApp, re.Auth.Id, userhandler.ArcadePublicKind(body.Arcade), 10, currentExp); err != nil {
			return err
		} else {
			currentExp = nextExp
		}
		if nextExp, err := userhandler.GrantArcadePublicBackfillTx(txApp, re.Auth.Id, body.Arcade, currentExp); err != nil {
			return err
		} else {
			currentExp = nextExp
		}
		xpFeedback = userhandler.BuildExpFeedback(baseExp, currentExp)
		return nil
	}); err != nil {
		if errors.Is(err, arcadeinternal.ErrArcadeCountryConflict) {
			return re.JSON(http.StatusBadRequest, map[string]any{
				"error": err.Error(),
			})
		}
		if errors.Is(err, ErrFacilityPhotoRegistration) {
			return re.JSON(http.StatusBadRequest, map[string]any{
				"error":   "validation failed",
				"details": ErrFacilityPhotoRegistration.Error(),
			})
		}
		if errors.Is(err, ErrArcadeGeoUnavailable) {
			return re.JSON(http.StatusBadRequest, map[string]any{
				"error":   "validation failed",
				"details": ErrArcadeGeoUnavailable.Error(),
			})
		}
		if errors.Is(err, ErrArcadeBasicEmpty) || errors.Is(err, ErrArcadeBasicLocationMissing) {
			return re.JSON(http.StatusBadGateway, map[string]any{
				"error": err.Error(),
			})
		}
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to update arcade visibility",
			"details": err.Error(),
		})
	}

	// 7) success
	return re.JSON(http.StatusOK, map[string]any{
		"arcade":      body.Arcade,
		"public":      true,
		"xp_feedback": xpFeedback,
	})
}
