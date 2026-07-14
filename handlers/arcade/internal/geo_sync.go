package arcadeinternal

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ericbaek/musecat-backend-core/geo"

	"github.com/pocketbase/pocketbase/core"
)

var ErrArcadeCountryConflict = errors.New("country changed for public arcade")

// SyncArcadeCountryByLocation ensures the arcade country matches the country
// resolved from the provided location.
//
// If the country changes:
// - public arcades are rejected
// - private arcades are updated to the resolved country and timezone
func SyncArcadeCountryByLocation(ctx context.Context, app core.App, arcadeID string, lat, lon float64) (bool, error) {
	res, err := geo.LookupCountryAndTimezone(ctx, lat, lon)
	if err != nil {
		return false, err
	}

	return SyncArcadeCountryByGeoResult(app, arcadeID, res)
}

// SyncArcadeCountryByGeoResult applies a geo lookup result to an arcade.
func SyncArcadeCountryByGeoResult(app core.App, arcadeID string, res geo.Result) (bool, error) {
	arcadeRec, err := app.FindRecordById(CollectionArcade, arcadeID)
	if err != nil {
		return false, fmt.Errorf("arcade not found: %w", err)
	}

	currentCountry := strings.ToUpper(strings.TrimSpace(arcadeRec.GetString("country")))
	nextCountry := strings.ToUpper(strings.TrimSpace(res.Country))
	currentTimezone := strings.TrimSpace(arcadeRec.GetString("timezone"))
	nextTimezone := strings.TrimSpace(res.Timezone)

	countryChanged := currentCountry != nextCountry
	timezoneChanged := currentTimezone != nextTimezone
	if !countryChanged && !timezoneChanged {
		return false, nil
	}

	if countryChanged && arcadeRec.GetBool("public") {
		return false, ErrArcadeCountryConflict
	}

	if countryChanged {
		arcadeRec.Set("country", nextCountry)
	}
	if timezoneChanged {
		arcadeRec.Set("timezone", nextTimezone)
	}
	if err := app.Save(arcadeRec); err != nil {
		return false, fmt.Errorf("failed to update arcade country: %w", err)
	}

	return true, nil
}
