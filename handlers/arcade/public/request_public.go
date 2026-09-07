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

func hasGameRegistration(app core.App, stateID string) (bool, error) {
	stateID = strings.TrimSpace(stateID)
	if stateID == "" {
		return false, nil
	}

	if _, err := app.FindRecordById(arcadeinternal.CollectionArcadeGameRevisionBatch, stateID); err != nil {
		return false, nil
	}

	revisions, err := app.FindRecordsByFilter(
		arcadeinternal.CollectionArcadeGameRevision,
		"batch={:id}",
		"",
		1,
		0,
		dbx.Params{"id": stateID},
	)
	if err != nil {
		return false, err
	}
	return len(revisions) > 0, nil
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

	baseExp, err := userhandler.LoadCurrentExp(re.App, re.Auth.Id)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to load current exp",
			"details": err.Error(),
		})
	}
	levelSnapshot := userhandler.LevelFromExp(baseExp)
	requirements, err := publicConversionRequirements(re.App, arcade, re.Auth.Id, levelSnapshot)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to validate public conversion requirements",
			"details": err.Error(),
		})
	}
	if err := validatePublicConversionRequirements(requirements); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}

	// 5) make arcade public immediately.
	var xpFeedback userhandler.ExpFeedback
	if err := re.App.RunInTransaction(func(txApp core.App) error {
		txArcade, err := txApp.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
		if err != nil {
			return fmt.Errorf("arcade not found: %w", err)
		}
		txBaseExp, err := userhandler.LoadCurrentExp(txApp, re.Auth.Id)
		if err != nil {
			return fmt.Errorf("failed to load current exp: %w", err)
		}
		currentExp := txBaseExp

		basicID := strings.TrimSpace(txArcade.GetString("basic"))
		if basicID == "" {
			return ErrArcadeBasicEmpty
		}

		basic, err := txApp.FindRecordById(arcadeinternal.CollectionArcadeBasic, basicID)
		if err != nil {
			return fmt.Errorf("failed to load arcade basic: %w", err)
		}
		if _, _, ok := arcadeinternal.ReadLocation(basic.Get("location")); !ok {
			return ErrArcadeBasicLocationMissing
		}
		currentRequirements, err := publicConversionRequirements(txApp, txArcade, re.Auth.Id, levelSnapshot)
		if err != nil {
			return err
		}
		if err := validatePublicConversionRequirements(currentRequirements); err != nil {
			return err
		}
		country := strings.ToUpper(strings.TrimSpace(txArcade.GetString("country")))
		timezone := strings.TrimSpace(txArcade.GetString("timezone"))
		if len(country) != 2 || timezone == "" {
			return ErrArcadeGeoUnavailable
		}
		if _, err := time.LoadLocation(timezone); err != nil {
			return ErrArcadeGeoUnavailable
		}

		if err := arcadeinternal.UpdateArcadeFieldsTx(txApp, body.Arcade, map[string]any{
			"public": true,
		}, re.Auth.Id); err != nil {
			return err
		}

		if nextExp, _, err := userhandler.AwardExpTx(txApp, re.Auth.Id, userhandler.ArcadePublicKind(body.Arcade), 5, currentExp); err != nil {
			return err
		} else {
			currentExp = nextExp
		}
		if nextExp, err := userhandler.GrantArcadePublicBackfillTx(txApp, re.Auth.Id, body.Arcade, currentExp); err != nil {
			return err
		} else {
			currentExp = nextExp
		}
		xpFeedback = userhandler.BuildExpFeedback(txBaseExp, currentExp)
		return nil
	}); err != nil {
		if errors.Is(err, arcadeinternal.ErrArcadeCountryConflict) {
			return re.JSON(http.StatusBadRequest, map[string]any{
				"error": err.Error(),
			})
		}
		if isPublicRequirementError(err) {
			return re.JSON(http.StatusBadRequest, map[string]any{
				"error":   "validation failed",
				"details": err.Error(),
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
