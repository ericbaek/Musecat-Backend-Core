package basic

import (
	"math"
	"strconv"
	"testing"
)

func TestLocationMoveDistanceLimits(t *testing.T) {
	tests := []struct {
		level int
		limit float64
	}{
		{level: 0, limit: 1},
		{level: 9, limit: 1},
		{level: 10, limit: 5},
		{level: 14, limit: 5},
		{level: 15, limit: 10},
		{level: 29, limit: 10},
		{level: 30, limit: math.Inf(1)},
	}

	for _, test := range tests {
		t.Run("level "+strconv.Itoa(test.level), func(t *testing.T) {
			if got := locationMoveDistanceLimitKm(test.level); got != test.limit {
				t.Fatalf("locationMoveDistanceLimitKm(%d) = %v, want %v", test.level, got, test.limit)
			}
		})
	}
}

func TestValidateLocationMove(t *testing.T) {
	tests := []struct {
		name          string
		level         int
		distanceKm    float64
		current       string
		next          string
		wantErrorCode string
	}{
		{name: "allows exact level 10 limit", level: 10, distanceKm: 5, current: "KR", next: "KR"},
		{name: "rejects above level 10 limit", level: 10, distanceKm: 5.001, current: "KR", next: "KR", wantErrorCode: locationMoveDistanceExceededCode},
		{name: "level 30 allows unlimited distance in same country", level: 30, distanceKm: 1000, current: "kr", next: " KR "},
		{name: "level 30 cannot change country", level: 30, distanceKm: 1, current: "KR", next: "JP", wantErrorCode: locationMoveCountryChangedCode},
		{name: "level 30 requires a resolved country", level: 30, distanceKm: 1, current: "KR", next: "", wantErrorCode: locationMoveCountryChangedCode},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateLocationMove(test.level, test.distanceKm, test.current, test.next)
			if got := locationMovePolicyCode(err); got != test.wantErrorCode {
				t.Fatalf("locationMovePolicyCode() = %q, want %q (error %v)", got, test.wantErrorCode, err)
			}
		})
	}
}
