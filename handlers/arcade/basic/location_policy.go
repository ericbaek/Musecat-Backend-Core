package basic

import (
	"errors"
	"math"
	"strings"
)

const (
	locationMoveDistanceExceededCode = "location_move_distance_exceeded"
	locationMoveCountryChangedCode   = "location_move_country_changed"
	locationMoveOriginMissingCode    = "location_move_origin_missing"
)

type locationMovePolicyError struct {
	code string
}

func (e *locationMovePolicyError) Error() string {
	switch e.code {
	case locationMoveDistanceExceededCode:
		return "location move exceeds the distance limit for this level"
	case locationMoveCountryChangedCode:
		return "level 30 or above may move an arcade only within its current country"
	default:
		return "the current arcade location is unavailable for distance validation"
	}
}

func locationMoveDistanceLimitKm(level int) float64 {
	switch {
	case level < 10:
		return 1
	case level < 15:
		return 5
	case level < 30:
		return 10
	default:
		return math.Inf(1)
	}
}

func validateLocationMove(level int, distanceKm float64, currentCountry, nextCountry string) error {
	if level >= 30 {
		current := strings.ToUpper(strings.TrimSpace(currentCountry))
		next := strings.ToUpper(strings.TrimSpace(nextCountry))
		if current == "" || next == "" || current != next {
			return &locationMovePolicyError{code: locationMoveCountryChangedCode}
		}
		return nil
	}

	if math.IsNaN(distanceKm) || math.IsInf(distanceKm, 0) || distanceKm > locationMoveDistanceLimitKm(level) {
		return &locationMovePolicyError{code: locationMoveDistanceExceededCode}
	}
	return nil
}

func locationMovePolicyCode(err error) string {
	var policyErr *locationMovePolicyError
	if errors.As(err, &policyErr) {
		return policyErr.code
	}
	return ""
}
