package user

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/text/language"
)

const (
	maxProfileCountries        = 3
	multiProfileCountriesLevel = 15
)

// NormalizeProfileCountries validates a user-supplied, ordered ISO 3166-1
// alpha-2 country list. The first country is the profile's primary country.
func NormalizeProfileCountries(values []string) ([]string, error) {
	if len(values) > maxProfileCountries {
		return nil, fmt.Errorf("countries may contain at most %d items", maxProfileCountries)
	}

	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		country, err := normalizeProfileCountry(value)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[country]; ok {
			return nil, fmt.Errorf("countries must not contain duplicates")
		}
		seen[country] = struct{}{}
		out = append(out, country)
	}

	return out, nil
}

func normalizeProfileCountry(value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) != 2 {
		return "", fmt.Errorf("countries must contain ISO 3166-1 alpha-2 codes")
	}
	region, err := language.ParseRegion(value)
	if err != nil || !region.IsCountry() || region.String() != value {
		return "", fmt.Errorf("countries must contain ISO 3166-1 alpha-2 codes")
	}
	return value, nil
}

func profileCountriesFromRecord(rec *core.Record) []string {
	if rec == nil {
		return []string{}
	}

	values := rec.GetStringSlice("countries")
	if len(values) == 0 {
		var parsed []string
		if raw := strings.TrimSpace(rec.GetString("countries")); raw != "" && json.Unmarshal([]byte(raw), &parsed) == nil {
			values = parsed
		}
	}

	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		country, err := normalizeProfileCountry(value)
		if err != nil {
			continue
		}
		if _, ok := seen[country]; ok {
			continue
		}
		seen[country] = struct{}{}
		out = append(out, country)
	}
	return out
}

func primaryProfileCountry(rec *core.Record) string {
	countries := profileCountriesFromRecord(rec)
	if len(countries) == 0 {
		return ""
	}
	return countries[0]
}

// PrimaryProfileCountry returns the first valid stored country code, if any.
func PrimaryProfileCountry(rec *core.Record) string {
	return primaryProfileCountry(rec)
}

// PrimaryProfileCountryFromJSON extracts the first valid country from a JSON
// array stored by PocketBase's JSON field.
func PrimaryProfileCountryFromJSON(raw string) string {
	var countries []string
	if json.Unmarshal([]byte(raw), &countries) != nil {
		return ""
	}
	normalized, err := NormalizeProfileCountries(countries)
	if err != nil || len(normalized) == 0 {
		return ""
	}
	return normalized[0]
}

func hasCountrySelectionAccess(app core.App, userRec *core.Record) bool {
	if userRec == nil {
		return false
	}
	exp, err := LoadCurrentExp(app, userRec.Id)
	return err == nil && LevelFromExp(exp) >= multiProfileCountriesLevel
}

func publicProfileCountries(app core.App, userRec, userInfoRec *core.Record, includePrivate bool) []string {
	countries := profileCountriesFromRecord(userInfoRec)
	if !includePrivate && !hasCountrySelectionAccess(app, userRec) && len(countries) > 1 {
		return append([]string(nil), countries[:1]...)
	}
	return countries
}
