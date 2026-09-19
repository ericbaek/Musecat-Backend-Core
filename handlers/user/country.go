package user

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/text/language"
)

const (
	maxProfileCountries        = 3
	multiProfileCountriesLevel = 15
	countryModeAuto            = "auto"
	countryModeManual          = "manual"
	countryModeOff             = "off"
)

// ProfileCountryMode returns a valid persisted country display mode. Existing
// records without the field intentionally resolve to the new auto default.
func ProfileCountryMode(rec *core.Record) string {
	if rec == nil {
		return countryModeAuto
	}
	switch strings.TrimSpace(rec.GetString("country_mode")) {
	case countryModeAuto, countryModeManual, countryModeOff:
		return strings.TrimSpace(rec.GetString("country_mode"))
	default:
		return countryModeAuto
	}
}

func normalizeProfileCountryMode(value string) (string, error) {
	switch strings.TrimSpace(value) {
	case countryModeAuto, countryModeManual, countryModeOff:
		return strings.TrimSpace(value), nil
	default:
		return "", fmt.Errorf("country_mode must be auto, manual, or off")
	}
}

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

// ResolveProfileCountries applies the display mode to stored profile choices.
// Auto ranks verified visits by visit count, distinct arcades, and recency.
func ResolveProfileCountries(countries []string, mode string, stats VisitStats) ([]string, string) {
	switch mode {
	case countryModeOff:
		return []string{}, ""
	case countryModeManual:
		if len(countries) == 0 {
			return []string{}, ""
		}
		return countries, countries[0]
	default:
		country := topVisitedCountry(stats)
		if country == "" {
			return []string{}, ""
		}
		return []string{country}, country
	}
}

// ResolveStoredProfileCountries resolves a display country without reading
// visit history. Auto uses the value maintained when a visit is verified.
func ResolveStoredProfileCountries(countries []string, mode, autoPrimaryCountry string) ([]string, string) {
	switch mode {
	case countryModeOff:
		return []string{}, ""
	case countryModeManual:
		if len(countries) == 0 {
			return []string{}, ""
		}
		return countries, countries[0]
	default:
		country, err := normalizeProfileCountry(autoPrimaryCountry)
		if err != nil {
			return []string{}, ""
		}
		return []string{country}, country
	}
}

// AutoPrimaryProfileCountry returns the persisted Auto-mode country.
func AutoPrimaryProfileCountry(rec *core.Record) string {
	if rec == nil {
		return ""
	}
	country, err := normalizeProfileCountry(rec.GetString("auto_primary_country"))
	if err != nil {
		return ""
	}
	return country
}

// RefreshAutoPrimaryProfileCountry updates the derived Auto-mode country after
// an eligible visit changes. It deliberately aggregates in SQL instead of
// loading the whole passport for a profile read.
func RefreshAutoPrimaryProfileCountry(app core.App, userID string) (string, error) {
	info, err := app.FindRecordById(CollectionUserInfo, userID)
	if err != nil {
		return "", err
	}
	country, err := loadAutoPrimaryProfileCountry(app, userID)
	if err != nil {
		return "", err
	}
	info.Set("auto_primary_country", country)
	if err := app.Save(info); err != nil {
		return "", err
	}
	return country, nil
}

func loadAutoPrimaryProfileCountry(app core.App, userID string) (string, error) {
	rows, err := app.DB().NewQuery(`
SELECT
  UPPER(TRIM(a.country)) AS country,
  COUNT(*) AS visit_count,
  COUNT(DISTINCT v.arcade) AS arcade_count,
  MAX(v.visited_at) AS last_visited_at
FROM arcade_visit v
INNER JOIN arcade a ON a.id = v.arcade
WHERE v.user = {:user} AND a.public = true AND TRIM(COALESCE(a.country, '')) <> ''
GROUP BY UPPER(TRIM(a.country))
`).Bind(dbx.Params{"user": userID}).Rows()
	if err != nil {
		return "", err
	}
	defer rows.Close()

	stats := VisitStats{Countries: []VisitCountry{}, Arcades: []ArcadeVisitCount{}}
	for rows.Next() {
		var country, lastVisitedAt string
		var visitCount, arcadeCount int
		if err := rows.Scan(&country, &visitCount, &arcadeCount, &lastVisitedAt); err != nil {
			return "", err
		}
		stats.Countries = append(stats.Countries, VisitCountry{
			Country: country, VisitCount: visitCount, ArcadeCount: arcadeCount,
		})
		stats.Arcades = append(stats.Arcades, ArcadeVisitCount{
			Country: country, lastVisitedAt: lastVisitedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return topVisitedCountry(stats), nil
}

func topVisitedCountry(stats VisitStats) string {
	type countryRank struct {
		visitCount  int
		arcadeCount int
		lastVisited string
	}
	ranks := map[string]countryRank{}
	countryTotals := map[string]struct{}{}
	for _, item := range stats.Countries {
		country, err := normalizeProfileCountry(item.Country)
		if err != nil {
			continue
		}
		ranks[country] = countryRank{visitCount: item.VisitCount, arcadeCount: item.ArcadeCount}
		countryTotals[country] = struct{}{}
	}
	for _, arcade := range stats.Arcades {
		country, err := normalizeProfileCountry(arcade.Country)
		if err != nil {
			continue
		}
		rank := ranks[country]
		if _, hasTotal := countryTotals[country]; !hasTotal {
			rank.visitCount += arcade.VisitCount
			rank.arcadeCount++
		}
		if arcade.lastVisitedAt > rank.lastVisited {
			rank.lastVisited = arcade.lastVisitedAt
		}
		ranks[country] = rank
	}
	bestCountry := ""
	best := countryRank{}
	for country, rank := range ranks {
		if bestCountry == "" ||
			rank.visitCount > best.visitCount ||
			(rank.visitCount == best.visitCount && rank.arcadeCount > best.arcadeCount) ||
			(rank.visitCount == best.visitCount && rank.arcadeCount == best.arcadeCount && rank.lastVisited > best.lastVisited) ||
			(rank.visitCount == best.visitCount && rank.arcadeCount == best.arcadeCount && rank.lastVisited == best.lastVisited && country < bestCountry) {
			bestCountry, best = country, rank
		}
	}
	return bestCountry
}

// ProfileCountriesFromJSON extracts valid countries from a JSON array stored
// by PocketBase's JSON field.
func ProfileCountriesFromJSON(raw string) []string {
	var countries []string
	if json.Unmarshal([]byte(raw), &countries) != nil {
		return []string{}
	}
	normalized, err := NormalizeProfileCountries(countries)
	if err != nil {
		return []string{}
	}
	return normalized
}

// PrimaryProfileCountryFromJSON extracts the first valid country from a JSON
// array stored by PocketBase's JSON field.
func PrimaryProfileCountryFromJSON(raw string) string {
	countries := ProfileCountriesFromJSON(raw)
	if len(countries) == 0 {
		return ""
	}
	return countries[0]
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
