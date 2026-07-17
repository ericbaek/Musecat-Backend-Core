package search

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	pbtypes "github.com/pocketbase/pocketbase/tools/types"

	arcadequery "github.com/ericbaek/musecat-backend-core/handlers/arcade/query"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

const maxSearchLimit = 100
const (
	collectionArcade      = "arcade"
	collectionArcadeBasic = "arcade_basic"
)

var regionAliasPairs = []string{
	"서울특별시", "서울",
	"부산광역시", "부산",
	"대구광역시", "대구",
	"인천광역시", "인천",
	"광주광역시", "광주",
	"대전광역시", "대전",
	"울산광역시", "울산",
	"세종특별자치시", "세종",
	"제주특별자치도", "제주",
	"강원특별자치도", "강원",
	"강원도", "강원",
	"경기도", "경기",
	"충청북도", "충북",
	"충청남도", "충남",
	"전북특별자치도", "전북",
	"전라북도", "전북",
	"전라남도", "전남",
	"경상북도", "경북",
	"경상남도", "경남",
}

var regionAliasReplacer = strings.NewReplacer(regionAliasPairs...)

// Search handles GET /search?q=...&limit=...
func Search(re *core.RequestEvent) error {
	startedAt := time.Now()
	q := strings.TrimSpace(re.Request.URL.Query().Get("q"))
	if q == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "missing required query param 'q'",
		})
	}

	limit, err := parseLimit(re.Request.URL.Query().Get("limit"))
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "invalid 'limit' value; expected positive integer",
		})
	}

	lat, lon, hasLocation, err := parseSearchLocation(re.Request.URL.Query())
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid location",
			"details": err.Error(),
		})
	}

	userIDs, err := searchUserIDs(re.App, q, limit)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to search users",
			"details": err.Error(),
		})
	}
	userIDsCompletedAt := time.Now()

	arcadeCandidates, err := arcadequery.GetArcadeCandidates(re.App)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to load arcade candidates",
			"details": err.Error(),
		})
	}
	arcadeCandidatesCompletedAt := time.Now()

	users, err := buildUserResults(re.App, userIDs, limit)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to build user search results",
			"details": err.Error(),
		})
	}
	usersCompletedAt := time.Now()

	arcades, err := buildArcadeResults(arcadeCandidates, q, limit, lat, lon, hasLocation)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to build arcade search results",
			"details": err.Error(),
		})
	}
	arcadesCompletedAt := time.Now()

	re.Response.Header().Set("Server-Timing", fmt.Sprintf(
		"user-ids;dur=%.2f, arcade-candidates;dur=%.2f, user-results;dur=%.2f, arcade-results;dur=%.2f, total;dur=%.2f",
		float64(userIDsCompletedAt.Sub(startedAt).Microseconds())/1000,
		float64(arcadeCandidatesCompletedAt.Sub(userIDsCompletedAt).Microseconds())/1000,
		float64(usersCompletedAt.Sub(arcadeCandidatesCompletedAt).Microseconds())/1000,
		float64(arcadesCompletedAt.Sub(usersCompletedAt).Microseconds())/1000,
		float64(arcadesCompletedAt.Sub(startedAt).Microseconds())/1000,
	))

	return re.JSON(http.StatusOK, map[string]any{
		"users":   users,
		"arcades": arcades,
	})
}

func parseLimit(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return maxSearchLimit, nil
	}

	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	if parsed <= 0 {
		return 0, errors.New("limit must be positive")
	}
	if parsed > maxSearchLimit {
		return maxSearchLimit, nil
	}
	return parsed, nil
}

func searchUserIDs(app core.App, q string, limit int) ([]string, error) {
	params := buildSearchParams(q)
	params["limit"] = limit * 3

	rows, err := app.DB().NewQuery(`
SELECT
	u.id
FROM "user" u
LEFT JOIN user_info ui ON ui.id = u.id
WHERE
	lower(trim(COALESCE(u.username, ''))) LIKE {:contains} ESCAPE '\'
	OR lower(trim(COALESCE(ui.nickname, ''))) LIKE {:contains} ESCAPE '\'
ORDER BY
	CASE
		WHEN lower(trim(COALESCE(u.username, ''))) = {:exact}
			OR lower(trim(COALESCE(ui.nickname, ''))) = {:exact}
		THEN 0
		WHEN lower(trim(COALESCE(u.username, ''))) LIKE {:prefix} ESCAPE '\'
			OR lower(trim(COALESCE(ui.nickname, ''))) LIKE {:prefix} ESCAPE '\'
		THEN 1
		ELSE 2
	END ASC,
	lower(trim(COALESCE(u.username, ''))) ASC,
	u.id ASC
LIMIT {:limit}
`).Bind(params).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanIDs(rows)
}

func buildSearchParams(q string) dbx.Params {
	normalized := normalizeSearchText(q)
	return dbx.Params{
		"exact":    normalized,
		"prefix":   escapeLike(normalized) + "%",
		"contains": "%" + escapeLike(normalized) + "%",
	}
}

func escapeLike(value string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		`%`, `\%`,
		`_`, `\_`,
	)
	return replacer.Replace(value)
}

func buildUserResults(app core.App, userIDs []string, limit int) ([]map[string]any, error) {
	if len(userIDs) == 0 {
		return []map[string]any{}, nil
	}

	params := dbx.Params{}
	placeholders := make([]string, 0, len(userIDs))
	for idx, userID := range userIDs {
		key := fmt.Sprintf("user_id_%d", idx)
		params[key] = userID
		placeholders = append(placeholders, "{:"+key+"}")
	}

	rows, err := app.DB().NewQuery(fmt.Sprintf(`
SELECT
	u.id,
	u.username,
	COALESCE(ui.nickname, '') AS nickname,
	COALESCE(ui.avatar, '') AS avatar,
	COALESCE(ul.exp, 0) AS exp,
	CASE
		WHEN COALESCE(u.withdrawn, 0) THEN '1'
		ELSE '0'
	END AS withdrawn
FROM "user" u
LEFT JOIN user_info ui ON ui.id = u.id
LEFT JOIN user_level ul ON ul.user = u.id
WHERE
	u.id IN (%s)
`, strings.Join(placeholders, ","))).Bind(params).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	profiles := map[string]map[string]any{}
	for rows.Next() {
		raw := dbx.NullStringMap{}
		if err := rows.ScanMap(raw); err != nil {
			return nil, err
		}

		id := strings.TrimSpace(raw["id"].String)
		if id == "" {
			continue
		}
		if raw["withdrawn"].Valid && strings.TrimSpace(raw["withdrawn"].String) == "1" {
			continue
		}

		username := strings.TrimSpace(raw["username"].String)
		nickname := strings.TrimSpace(raw["nickname"].String)
		if nickname == "" {
			nickname = username
		}
		exp, err := strconv.Atoi(strings.TrimSpace(raw["exp"].String))
		if err != nil {
			exp = 0
		}

		profiles[id] = map[string]any{
			"username": username,
			"nickname": nickname,
			"avatar":   strings.TrimSpace(raw["avatar"].String),
			"level":    userhandler.LevelFromExp(exp),
		}
	}

	results := make([]map[string]any, 0, min(limit, len(userIDs)))
	for _, userID := range userIDs {
		profile, ok := profiles[userID]
		if !ok {
			continue
		}
		results = append(results, profile)
		if len(results) == limit {
			break
		}
	}
	return results, nil
}

type arcadeSearchResult struct {
	score   float64
	order   int
	payload map[string]any
}

func buildArcadeResults(arcadeCandidates []arcadequery.ArcadeCandidate, query string, limit int, userLat, userLon float64, hasLocation bool) ([]map[string]any, error) {
	results := make([]arcadeSearchResult, 0, limit)
	for idx, candidate := range arcadeCandidates {
		rank, ok := arcadeTextRank(query, candidate)
		if !ok {
			continue
		}

		score := float64(rank) * 1_000_000_000
		item := map[string]any{
			"id":       candidate.ID,
			"country":  candidate.Country,
			"name":     candidate.Name,
			"address":  candidate.Address,
			"nickname": cloneStringSliceOrEmpty(candidate.Nicknames),
			"closed":   candidate.Closed,
		}

		if hasLocation {
			if candidate.Location == nil {
				continue
			}
			distance := distanceKm(userLat, userLon, candidate.Location.Lat, candidate.Location.Lon)
			item["distance_km"] = distance
			score += distance
		}

		result := arcadeSearchResult{
			score:   score,
			order:   idx,
			payload: item,
		}
		if len(results) == limit && !searchResultBefore(result, results[len(results)-1]) {
			continue
		}
		insertAt := len(results)
		for insertAt > 0 && searchResultBefore(result, results[insertAt-1]) {
			insertAt--
		}
		results = append(results, arcadeSearchResult{})
		copy(results[insertAt+1:], results[insertAt:])
		results[insertAt] = result
		if len(results) > limit {
			results = results[:limit]
		}
	}

	out := make([]map[string]any, 0, len(results))
	for _, result := range results {
		out = append(out, result.payload)
	}
	return out, nil
}

func searchResultBefore(left, right arcadeSearchResult) bool {
	if left.score == right.score {
		return left.order < right.order
	}
	return left.score < right.score
}

func arcadeTextRank(query string, candidate arcadequery.ArcadeCandidate) (int, bool) {
	q := normalizeSearchText(query)
	if q == "" {
		return 2, true
	}

	best := 3
	fields := []string{
		candidate.NameNorm,
		candidate.AddressNorm,
		candidate.AddressAliasNorm,
	}
	fields = append(fields, candidate.NicknameNorms...)

	for _, field := range fields {
		if field == "" {
			continue
		}
		switch {
		case field == q:
			return 0, true
		case strings.HasPrefix(field, q) && best > 1:
			best = 1
		case strings.Contains(field, q) && best > 2:
			best = 2
		}
	}
	if best <= 2 {
		return best, true
	}
	if arcadeTokenMatch(query, fields...) {
		return 2, true
	}
	return 0, false
}

func normalizeSearchText(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return ""
	}
	return strings.Join(strings.Fields(normalized), " ")
}

func normalizeAddressQuery(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return ""
	}
	normalized = strings.Join(strings.Fields(normalized), "")
	return regionAliasReplacer.Replace(normalized)
}

func buildNormalizedAddressSQLExpr(baseExpr string) string {
	expr := fmt.Sprintf("replace(%s, ' ', '')", baseExpr)
	for i := 0; i+1 < len(regionAliasPairs); i += 2 {
		expr = fmt.Sprintf(
			"replace(%s, '%s', '%s')",
			expr,
			regionAliasPairs[i],
			regionAliasPairs[i+1],
		)
	}
	return expr
}

func splitSearchTokens(raw string) []string {
	normalized := normalizeSearchText(raw)
	if normalized == "" {
		return nil
	}

	seen := map[string]struct{}{}
	tokens := make([]string, 0)
	for _, token := range strings.Fields(normalized) {
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}
	return tokens
}

func arcadeTokenMatch(query string, fields ...string) bool {
	tokens := splitSearchTokens(query)
	if len(tokens) == 0 {
		return false
	}

	for _, field := range fields {
		if field == "" {
			continue
		}
		matchedAll := true
		for _, token := range tokens {
			if !strings.Contains(field, token) {
				matchedAll = false
				break
			}
		}
		if matchedAll {
			return true
		}
	}
	return false
}

func parseSearchLocation(q url.Values) (float64, float64, bool, error) {
	latStr := strings.TrimSpace(q.Get("lat"))
	lonStr := strings.TrimSpace(q.Get("lon"))
	if latStr == "" && lonStr == "" {
		return 0, 0, false, nil
	}
	if latStr == "" || lonStr == "" {
		return 0, 0, false, errors.New("lat and lon query params must be provided together")
	}

	lat, err := strconv.ParseFloat(latStr, 64)
	if err != nil {
		return 0, 0, false, fmt.Errorf("invalid lat: %w", err)
	}
	lon, err := strconv.ParseFloat(lonStr, 64)
	if err != nil {
		return 0, 0, false, fmt.Errorf("invalid lon: %w", err)
	}
	if err := validateLocationCoords(lat, lon); err != nil {
		return 0, 0, false, err
	}
	return lat, lon, true, nil
}

func validateLocationCoords(lat, lon float64) error {
	if math.IsNaN(lat) || lat < -90 || lat > 90 || lat == 0 {
		return fmt.Errorf("location.lat out of range %f", lat)
	}
	if math.IsNaN(lon) || lon < -180 || lon > 180 || lon == 0 {
		return fmt.Errorf("location.lon out of rang %f", lon)
	}
	return nil
}

func readLocation(v any) (float64, float64, bool) {
	switch t := v.(type) {
	case pbtypes.GeoPoint:
		return t.Lat, t.Lon, true
	case *pbtypes.GeoPoint:
		if t == nil {
			return 0, 0, false
		}
		return t.Lat, t.Lon, true
	case map[string]any:
		if _, ok := t["latitude"]; ok {
			return toFloat(t["latitude"]), toFloat(t["longitude"]), true
		}
		if _, ok := t["lng"]; ok {
			return toFloat(t["lat"]), toFloat(t["lng"]), true
		}
		if _, ok := t["lon"]; ok {
			return toFloat(t["lat"]), toFloat(t["lon"]), true
		}
		return toFloat(t["lat"]), toFloat(t["lon"]), true
	}

	return 0, 0, false
}

func toFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case int32:
		return float64(t)
	case uint:
		return float64(t)
	case uint64:
		return float64(t)
	case uint32:
		return float64(t)
	case json.Number:
		f, _ := t.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	default:
		return 0
	}
}

func distanceKm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371.0
	toRad := func(deg float64) float64 { return deg * math.Pi / 180 }

	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)

	lat1Rad := toRad(lat1)
	lat2Rad := toRad(lat2)

	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1Rad)*math.Cos(lat2Rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return earthRadiusKm * c
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func cloneStringSliceOrEmpty(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	return append([]string(nil), in...)
}

func scanIDs(rows *dbx.Rows) ([]string, error) {
	ids := make([]string, 0)
	for rows.Next() {
		raw := dbx.NullStringMap{}
		if err := rows.ScanMap(raw); err != nil {
			return nil, err
		}
		id, ok := raw["id"]
		if !ok || !id.Valid || strings.TrimSpace(id.String) == "" {
			continue
		}
		ids = append(ids, strings.TrimSpace(id.String))
	}
	return ids, nil
}
