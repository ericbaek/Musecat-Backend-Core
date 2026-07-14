package query

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// ListArcadeGames handles GET /arcade/games and returns machine rows filtered by country, series, and version.
func ListArcadeGames(re *core.RequestEvent) error {
	q := re.Request.URL.Query()

	countrySet := toUpperSet(parseValues(q["country"]))
	seriesSet := toSet(parseValues(q["game_series"]))
	versionSet := toSet(parseValues(q["game_series_version"]))

	sql, params := buildArcadeGameListQuery(countrySet, seriesSet, versionSet)
	rows, err := re.App.DB().NewQuery(sql).Bind(params).Rows()
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to load arcade games",
			"details": err.Error(),
		})
	}
	defer rows.Close()

	items := make([]map[string]any, 0)

	for rows.Next() {
		raw := dbx.NullStringMap{}
		if err := rows.ScanMap(raw); err != nil {
			return re.JSON(http.StatusBadGateway, map[string]any{
				"error":   "failed to decode arcade games",
				"details": err.Error(),
			})
		}

		item := buildArcadeGameItem(raw)
		if item == nil {
			continue
		}
		items = append(items, item)
	}

	sort.Slice(items, func(i, j int) bool {
		leftCountry := strings.ToUpper(strings.TrimSpace(arcadeCountry(items[i])))
		rightCountry := strings.ToUpper(strings.TrimSpace(arcadeCountry(items[j])))
		if leftCountry != rightCountry {
			return leftCountry < rightCountry
		}

		leftSeriesNumber, leftHasSeriesNumber := gameItemSeriesNumber(items[i])
		rightSeriesNumber, rightHasSeriesNumber := gameItemSeriesNumber(items[j])
		if leftHasSeriesNumber != rightHasSeriesNumber {
			return leftHasSeriesNumber
		}
		if leftHasSeriesNumber && leftSeriesNumber != rightSeriesNumber {
			return leftSeriesNumber < rightSeriesNumber
		}

		leftReleasedOn, leftHasReleasedOn := gameItemReleasedOn(items[i])
		rightReleasedOn, rightHasReleasedOn := gameItemReleasedOn(items[j])
		if leftHasReleasedOn != rightHasReleasedOn {
			return leftHasReleasedOn
		}
		if leftHasReleasedOn && leftReleasedOn != rightReleasedOn {
			return leftReleasedOn > rightReleasedOn
		}

		return strings.TrimSpace(gameItemID(items[i])) < strings.TrimSpace(gameItemID(items[j]))
	})

	return re.JSON(http.StatusOK, items)
}

func buildArcadeGameListQuery(countrySet, seriesSet, versionSet map[string]bool) (string, dbx.Params) {
	params := dbx.Params{}
	clauses := []string{
		"a.public = 1",
		"a.closed = 0",
	}

	if len(countrySet) > 0 {
		keys := sortedMapKeys(countrySet)
		clauses = append(clauses, buildInClause("UPPER(TRIM(a.country))", "country", keys, params))
	}
	if len(seriesSet) > 0 {
		keys := sortedMapKeys(seriesSet)
		clauses = append(clauses, buildInClause("v.series", "series", keys, params))
	}
	if len(versionSet) > 0 {
		keys := sortedMapKeys(versionSet)
		clauses = append(clauses, buildInClause("v.id", "version", keys, params))
	}

	sql := fmt.Sprintf(`
SELECT
	a.id AS arcade_id,
	a.country AS arcade_country,
	a.game AS game_id,
	b.name AS arcade_name,
	b.address AS arcade_address,
	b.location AS arcade_location,
	atom.id AS atom_id,
	atom.location AS machine_location,
	atom.quantity AS quantity,
	atom.price AS price,
	atom.tag AS tag,
	v.id AS version_id,
	v.series AS series_id,
	v.released_on AS released_on,
	v.en AS version_en,
	v.kr AS version_kr,
	v.jp AS version_jp,
	v.price_default AS version_price_default,
	s.id AS series_record_id,
	s.seriesNumber AS series_number,
	s.en AS series_en,
	s.kr AS series_kr,
	s.jp AS series_jp,
	s.en_short AS series_en_short,
	s.kr_short AS series_kr_short,
	s.jp_short AS series_jp_short,
	s.manufacturer AS series_manufacturer
FROM arcade a
INNER JOIN arcade_basic b ON b.id = a.basic
INNER JOIN arcade_game g ON g.id = a.game
INNER JOIN arcade_game_atoms atom ON atom.molecule = g.id
INNER JOIN game_series_version v ON v.id = atom.game
INNER JOIN game_series s ON s.id = v.series
WHERE %s
ORDER BY
	UPPER(TRIM(a.country)) ASC,
	CAST(s.seriesNumber AS INTEGER) ASC,
	CASE WHEN v.released_on IS NULL THEN 1 ELSE 0 END ASC,
	v.released_on DESC,
	atom.id ASC
`, strings.Join(clauses, " AND "))

	return sql, params
}

func buildArcadeGameItem(raw dbx.NullStringMap) map[string]any {
	arcadeID := nullStringMapValue(raw, "arcade_id")
	atomID := nullStringMapValue(raw, "atom_id")
	versionID := nullStringMapValue(raw, "version_id")
	seriesID := nullStringMapValue(raw, "series_id")
	if arcadeID == "" || atomID == "" || versionID == "" || seriesID == "" {
		return nil
	}

	quantity, _ := strconv.Atoi(nullStringMapValue(raw, "quantity"))
	seriesNumber, _ := strconv.Atoi(nullStringMapValue(raw, "series_number"))

	item := map[string]any{
		"id": atomID,
		"arcade": map[string]any{
			"id":       arcadeID,
			"country":  nullStringMapValue(raw, "arcade_country"),
			"name":     nullStringMapValue(raw, "arcade_name"),
			"address":  nullStringMapValue(raw, "arcade_address"),
			"location": decodeJSONValue(nullStringMapValue(raw, "arcade_location")),
		},
		"game":     nullStringMapValue(raw, "game_id"),
		"location": nullStringMapValue(raw, "machine_location"),
		"quantity": quantity,
		"price":    decodeJSONValue(nullStringMapValue(raw, "price")),
		"tag":      decodeJSONValue(nullStringMapValue(raw, "tag")),
		"version": map[string]any{
			"id":            versionID,
			"series":        seriesID,
			"released_on":   nullStringMapValue(raw, "released_on"),
			"en":            nullStringMapValue(raw, "version_en"),
			"kr":            nullStringMapValue(raw, "version_kr"),
			"jp":            nullStringMapValue(raw, "version_jp"),
			"price_default": decodeJSONValue(nullStringMapValue(raw, "version_price_default")),
		},
		"series": map[string]any{
			"id":           seriesID,
			"seriesNumber": seriesNumber,
			"en":           nullStringMapValue(raw, "series_en"),
			"kr":           nullStringMapValue(raw, "series_kr"),
			"jp":           nullStringMapValue(raw, "series_jp"),
			"en_short":     nullStringMapValue(raw, "series_en_short"),
			"kr_short":     nullStringMapValue(raw, "series_kr_short"),
			"jp_short":     nullStringMapValue(raw, "series_jp_short"),
			"manufacturer": nullStringMapValue(raw, "series_manufacturer"),
		},
	}

	return item
}

func arcadeCountry(item map[string]any) string {
	arcade, ok := item["arcade"].(map[string]any)
	if !ok || arcade == nil {
		return ""
	}
	country, _ := arcade["country"].(string)
	return country
}

func buildInClause(field, prefix string, values []string, params dbx.Params) string {
	parts := make([]string, 0, len(values))
	for idx, value := range values {
		key := fmt.Sprintf("%s_%d", prefix, idx)
		params[key] = value
		parts = append(parts, fmt.Sprintf("%s = {:%s}", field, key))
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

func sortedMapKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func parseValues(values []string) []string {
	set := map[string]struct{}{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if v := strings.TrimSpace(part); v != "" {
				set[v] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	return out
}

func toSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		set[value] = true
	}
	return set
}

func toUpperSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		set[value] = true
	}
	return set
}

func decodeJSONValue(raw any) any {
	switch value := raw.(type) {
	case nil:
		return nil
	case []byte:
		var out any
		if err := json.Unmarshal(value, &out); err == nil {
			return out
		}
		return string(value)
	case json.RawMessage:
		var out any
		if err := json.Unmarshal(value, &out); err == nil {
			return out
		}
		return string(value)
	case string:
		if strings.TrimSpace(value) == "" {
			return nil
		}
		var out any
		if err := json.Unmarshal([]byte(value), &out); err == nil {
			return out
		}
		return value
	default:
		return value
	}
}
