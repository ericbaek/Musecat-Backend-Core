package arcadeinternal

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ericbaek/musecat-backend-core/geo"
	"github.com/ericbaek/musecat-backend-core/passportcities"

	"github.com/pocketbase/pocketbase/core"
)

// ResolveCityID turns an offline GeoNames source id or the nearest imported
// city in the same country into the local Passport City relation id. The
// nearest fallback is deliberate: Passport coverage prefers a deterministic
// city label over leaving a venue unclassified. Address text is ignored at
// request time; the Full backfill still has the richer address candidate tool
// for diagnostics.
func ResolveCityID(app core.App, result geo.Result, _ string, lat, lon float64) (string, string, error) {
	if result.CitySourceID != "" {
		record, err := app.FindRecordsByFilter("passport_city", "source_id={:source}", "", 1, 0, map[string]any{"source": result.CitySourceID})
		if err != nil {
			return "", "unmapped", err
		}
		if len(record) == 1 && strings.EqualFold(record[0].GetString("country"), result.Country) {
			return record[0].Id, "resolved", nil
		}
		// A deployment may provide a city boundary before that GeoNames row is
		// present in the imported catalog. Fall through to the deterministic
		// country-scoped nearest-city policy instead of dropping the assignment.
	}
	records, err := app.FindRecordsByFilter("passport_city", "country={:country}", "", 0, 0, map[string]any{"country": result.Country})
	if err != nil {
		return "", "unmapped", err
	}
	cities := make([]passportcities.City, 0, len(records))
	cityIDs := make(map[string]string, len(records))
	for _, record := range records {
		point := record.GetGeoPoint("location")
		sourceID := record.GetString("source_id")
		cities = append(cities, passportcities.City{SourceID: sourceID, Name: record.GetString("name"), Country: record.GetString("country"), Admin1: record.GetString("admin1"), Aliases: record.GetStringSlice("aliases"), Lat: point.Lat, Lon: point.Lon})
		cityIDs[sourceID] = record.Id
	}
	city, ok := passportcities.Nearest(cities, result.Country, lat, lon)
	if !ok {
		return "", "unmapped", nil
	}
	cityID := strings.TrimSpace(cityIDs[city.SourceID])
	if cityID == "" {
		return "", "unmapped", nil
	}
	return cityID, "resolved", nil
}

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
