package public

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

const (
	publicLocationRadiusMeters      = 100.0
	publicLocationMaxAccuracyMeters = 100.0
)

var (
	ErrPublicGameRequired          = errors.New("at least one game must be registered before making arcade public")
	ErrPublicChannelOrHourRequired = errors.New("either sns or hour must be registered before making arcade public")
	ErrPublicPhotoOrLocation       = errors.New("at least one facility photo or a location verification must be completed before making arcade public")
)

type PublicRequirement struct {
	Kind      string `json:"kind"`
	Satisfied bool   `json:"satisfied"`
}

type PublicConversionState struct {
	Level            int                 `json:"level"`
	Requirements     []PublicRequirement `json:"requirements"`
	LocationVerified bool                `json:"location_verified"`
}

func loadPublicConversionState(app core.App, arcade *core.Record, userID string) (PublicConversionState, error) {
	exp, err := userhandler.LoadCurrentExp(app, userID)
	if err != nil {
		return PublicConversionState{}, fmt.Errorf("failed to load current exp: %w", err)
	}
	level := userhandler.LevelFromExp(exp)
	requirements, err := publicConversionRequirements(app, arcade, userID, level)
	if err != nil {
		return PublicConversionState{}, err
	}
	return PublicConversionState{
		Level:            level,
		Requirements:     requirements,
		LocationVerified: publicLocationVerified(app, arcade, userID),
	}, nil
}

func publicConversionRequirements(app core.App, arcade *core.Record, userID string, level int) ([]PublicRequirement, error) {
	hasGame, err := hasGameRegistration(app, arcade.GetString("game_v2"))
	if err != nil {
		return nil, fmt.Errorf("failed to validate game registration: %w", err)
	}
	requirements := []PublicRequirement{{Kind: "game", Satisfied: hasGame}}

	if level < 10 {
		if level < 5 {
			hasSNS, err := hasSNSRegistration(app, arcade.GetString("sns"))
			if err != nil {
				return nil, fmt.Errorf("failed to validate sns registration: %w", err)
			}
			hasHour, err := hasHourRegistration(app, arcade.GetString("hour"))
			if err != nil {
				return nil, fmt.Errorf("failed to validate hour registration: %w", err)
			}
			requirements = append(requirements, PublicRequirement{Kind: "channelOrHour", Satisfied: hasSNS || hasHour})
		}

		hasPhoto, err := hasPhotoRegistration(app, arcade.GetString("photo"))
		if err != nil {
			return nil, fmt.Errorf("failed to validate photo registration: %w", err)
		}
		requirements = append(requirements, PublicRequirement{
			Kind:      "photoOrLocation",
			Satisfied: hasPhoto || publicLocationVerified(app, arcade, userID),
		})
	}

	return requirements, nil
}

func validatePublicConversionRequirements(requirements []PublicRequirement) error {
	for _, requirement := range requirements {
		if requirement.Satisfied {
			continue
		}
		switch requirement.Kind {
		case "game":
			return ErrPublicGameRequired
		case "channelOrHour":
			return ErrPublicChannelOrHourRequired
		case "photoOrLocation":
			return ErrPublicPhotoOrLocation
		}
	}
	return nil
}

func isPublicRequirementError(err error) bool {
	return errors.Is(err, ErrPublicGameRequired) ||
		errors.Is(err, ErrPublicChannelOrHourRequired) ||
		errors.Is(err, ErrPublicPhotoOrLocation)
}

func publicLocationVerified(app core.App, arcade *core.Record, userID string) bool {
	basicID := strings.TrimSpace(arcade.GetString("basic"))
	verifiedBasicID := strings.TrimSpace(arcade.GetString("location_verification_basic"))
	verifiedBy := strings.TrimSpace(arcade.GetString("location_verification_by"))
	if basicID == "" || verifiedBasicID == "" || verifiedBy == "" || verifiedBy != userID || verifiedBasicID != basicID {
		return false
	}
	if _, err := app.FindRecordById(arcadeinternal.CollectionArcadeBasic, basicID); err != nil {
		return false
	}
	return true
}
