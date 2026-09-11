package query

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

const nearbyPageSize = 15

var regionAliasReplacer = strings.NewReplacer(
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
)

type arcadeDistance struct {
	distance     float64
	sortDistance float64
	order        int
	payload      map[string]any
}

type countryTotal struct {
	Total         int `json:"total"`
	NearestArcade struct {
		ID         string  `json:"id"`
		DistanceKm float64 `json:"distance_km"`
	} `json:"nearest_arcade"`
}

type nearbyGameFilter struct {
	SeriesID  string
	CabinetID string
}

type nearbyGameFilterQuery struct {
	SeriesID  string `json:"series"`
	CabinetID string `json:"cabinet"`
}

// ListArcadesBySeriesAndLocation 는 GET /arcades/nearby?game_filter=...&lat=...&lon=...&address=...&country=...&page=... 요청을 처리한다.
// 여러 game_series 를 모두 포함하는 공개·영업 중 오락실을 거리순으로 최대 15개씩 페이지네이션해 반환한다.
func ListArcadesBySeriesAndLocation(re *core.RequestEvent) error {
	q := re.Request.URL.Query()

	// 1. 쿼리 파라미터에서 게임 시리즈 ID 들을 읽어온다. 쉼표 또는 다중 쿼리 파라미터를 모두 허용한다.
	var gameFilters []nearbyGameFilter
	var err error
	if len(q["game_filter"]) > 0 {
		if len(q["game_series"]) > 0 || len(q["game_cabinet"]) > 0 {
			return re.JSON(http.StatusBadRequest, map[string]any{
				"error": "game_filter cannot be combined with game_series or game_cabinet",
			})
		}
		gameFilters, err = parseGroupedNearbyGameFilters(q["game_filter"])
	} else {
		seriesIDs := parseOrderedIDs(q["game_series"])
		cabinetIDs := parseOrderedIDs(q["game_cabinet"])
		gameFilters, err = buildNearbyGameFilters(seriesIDs, cabinetIDs)
	}
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": err.Error(),
		})
	}
	if hasNearbyCabinetFilter(gameFilters) && re.Auth == nil {
		return re.JSON(http.StatusUnauthorized, map[string]any{
			"error": "authentication required",
		})
	}
	addressFilter := normalizeAddressKeyword(q.Get("address"))
	countryFilter := strings.ToUpper(strings.TrimSpace(q.Get("country")))
	expandGame, err := strconv.ParseBool(strings.TrimSpace(q.Get("expand")))
	if strings.TrimSpace(q.Get("expand")) != "" && err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "expand must be a boolean value",
		})
	}
	distanceLimitKm, hasDistanceLimit, err := parseDistanceLimit(q.Get("distance_limit"))
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid distance_limit value",
			"details": err.Error(),
		})
	}

	// 2. 좌표 파라미터를 검증하고 파싱한다.
	lat, lon, err := parseLatLon(q)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid location",
			"details": err.Error(),
		})
	}

	page := 1
	if pv := strings.TrimSpace(q.Get("page")); pv != "" {
		val, err := strconv.Atoi(pv)
		if err != nil || val < 1 {
			return re.JSON(http.StatusBadRequest, map[string]any{
				"error": "page must be a positive integer",
			})
		}
		page = val
	}

	// 3. 공개 후보 스냅샷을 공유한 뒤, 메모리에서 시리즈/주소/거리 필터를 적용한다.
	arcadeCandidates, err := GetArcadeCandidates(re.App)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to load arcade candidates",
			"details": err.Error(),
		})
	}

	if len(arcadeCandidates) == 0 {
		response := map[string]any{
			"page":           page,
			"per_page":       nearbyPageSize,
			"last_page":      0,
			"total":          0,
			"country_totals": map[string]countryTotal{},
			"items":          []any{},
			"campaigns":      []any{},
		}
		return re.JSON(http.StatusOK, response)
	}

	results := make([]arcadeDistance, 0, len(arcadeCandidates))

	// 5. 각 오락실에 대해 요약 정보와 거리를 계산한다.
	for idx, candidate := range arcadeCandidates {
		if candidate.Closed {
			continue
		}
		if candidate.Name == "" && candidate.Address == "" {
			continue
		}
		item := candidate.Summary(true, true)
		country, _ := item["country"].(string)
		country = strings.TrimSpace(country)
		if countryFilter != "" && !strings.EqualFold(country, countryFilter) {
			continue
		}
		if !matchesAllGameFilters(candidate.GameInstallations, gameFilters) {
			continue
		}
		// 주소 필터: 행정구역 축약/정식 명칭(예: 대구/대구광역시)을 모두 매칭
		if addressFilter != "" && !addressMatchesFilter(candidate.Address, addressFilter) {
			continue
		}

		if candidate.Location == nil {
			continue
		}

		distance := arcadeinternal.DistanceKm(lat, lon, candidate.Location.Lat, candidate.Location.Lon)
		if hasDistanceLimit && distance > distanceLimitKm {
			continue
		}
		item["distance_km"] = distance
		sortDistance := distance
		if expandGame && candidate.GameID != "" {
			if expandedGame, ok := buildExpandedGameValue(re.App, candidate.GameID); ok {
				if len(gameFilters) > 0 {
					expandedGame["items"] = filterExpandedGameItems(expandedGame["items"], gameFilters)
				}
				machineBonus := float64(sumExpandedGameQuantity(expandedGame["items"])) * 3
				sortDistance = distance - machineBonus
				item["game"] = expandedGame
			}
		}

		results = append(results, arcadeDistance{
			distance:     distance,
			sortDistance: sortDistance,
			order:        idx,
			payload:      item,
		})
	}

	if len(results) == 0 {
		response := map[string]any{
			"page":           page,
			"per_page":       nearbyPageSize,
			"last_page":      0,
			"total":          0,
			"country_totals": map[string]countryTotal{},
			"items":          []any{},
			"campaigns":      []any{},
		}
		return re.JSON(http.StatusOK, response)
	}

	// 6. 거리순으로 정렬한다.
	sort.Slice(results, func(i, j int) bool {
		if results[i].sortDistance == results[j].sortDistance {
			if results[i].distance == results[j].distance {
				return results[i].order < results[j].order
			}
			return results[i].distance < results[j].distance
		}
		return results[i].sortDistance < results[j].sortDistance
	})

	// 7. 요청한 페이지에 맞춰 슬라이싱한다.
	total := len(results)
	start := (page - 1) * nearbyPageSize
	if start > total {
		start = total
	}
	end := start + nearbyPageSize
	if end > total {
		end = total
	}

	lastPage := 0
	if total > 0 {
		lastPage = (total + nearbyPageSize - 1) / nearbyPageSize
	}

	items := make([]map[string]any, 0, end-start)
	for _, res := range results[start:end] {
		items = append(items, res.payload)
	}
	campaignFilters := make([]arcadeinternal.NearbyCampaignFilter, 0, len(gameFilters))
	for _, filter := range gameFilters {
		campaignFilters = append(campaignFilters, arcadeinternal.NearbyCampaignFilter{
			SeriesID:  filter.SeriesID,
			CabinetID: filter.CabinetID,
		})
	}
	nearbyCampaigns, err := arcadeinternal.BuildNearbyCampaigns(re.App, campaignFilters)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to load nearby campaign targets",
			"details": err.Error(),
		})
	}
	campaignSummaries, err := decorateNearbyCampaigns(re.App, results[start:end], nearbyCampaigns)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to build nearby campaigns",
			"details": err.Error(),
		})
	}
	countryTotals := summarizeCountryTotals(results)
	response := map[string]any{
		"page":           page,
		"per_page":       nearbyPageSize,
		"last_page":      lastPage,
		"total":          total,
		"country_totals": countryTotals,
		"items":          items,
		"campaigns":      campaignSummaries,
	}

	// 8. 페이지 정보와 함께 응답한다.
	return re.JSON(http.StatusOK, response)
}

func hasNearbyCabinetFilter(filters []nearbyGameFilter) bool {
	for _, filter := range filters {
		if strings.TrimSpace(filter.CabinetID) != "" {
			return true
		}
	}
	return false
}

func summarizeCountryTotals(results []arcadeDistance) map[string]countryTotal {
	totals := map[string]countryTotal{}
	for _, result := range results {
		item := result.payload
		country, ok := item["country"].(string)
		if !ok {
			continue
		}
		country = strings.TrimSpace(country)
		if country == "" {
			continue
		}
		total, ok := totals[country]
		if !ok || result.distance < total.NearestArcade.DistanceKm {
			total.NearestArcade.ID, _ = item["id"].(string)
			total.NearestArcade.DistanceKm = result.distance
		}
		total.Total++
		totals[country] = total
	}
	return totals
}

func parseOrderedIDs(params []string) []string {
	out := make([]string, 0, len(params))
	seen := map[string]struct{}{}
	for _, p := range params {
		for _, part := range strings.Split(p, ",") {
			if id := strings.TrimSpace(part); id != "" {
				if _, exists := seen[id]; exists {
					continue
				}
				seen[id] = struct{}{}
				out = append(out, id)
			}
		}
	}
	return out
}

func buildNearbyGameFilters(seriesIDs, cabinetIDs []string) ([]nearbyGameFilter, error) {
	if len(cabinetIDs) > 0 && len(seriesIDs) == 0 {
		return nil, errors.New("game_cabinet requires a paired game_series")
	}
	if len(cabinetIDs) > 0 && len(cabinetIDs) != len(seriesIDs) {
		return nil, errors.New("game_series and game_cabinet must have the same number of paired ids")
	}

	filters := make([]nearbyGameFilter, 0, len(seriesIDs))
	for i, seriesID := range seriesIDs {
		filter := nearbyGameFilter{SeriesID: seriesID}
		if len(cabinetIDs) > 0 {
			filter.CabinetID = cabinetIDs[i]
		}
		filters = append(filters, filter)
	}
	return filters, nil
}

func parseGroupedNearbyGameFilters(params []string) ([]nearbyGameFilter, error) {
	if len(params) == 0 {
		return nil, errors.New("game_filter requires at least one filter")
	}

	filters := make([]nearbyGameFilter, 0, len(params))
	seenSeries := make(map[string]struct{}, len(params))
	for _, raw := range params {
		var query nearbyGameFilterQuery
		if err := json.Unmarshal([]byte(raw), &query); err != nil {
			return nil, errors.New("game_filter must be valid JSON")
		}

		seriesID := strings.TrimSpace(query.SeriesID)
		if seriesID == "" {
			return nil, errors.New("game_filter series is required")
		}
		if _, exists := seenSeries[seriesID]; exists {
			return nil, errors.New("game_filter cannot contain duplicate series")
		}
		seenSeries[seriesID] = struct{}{}

		filters = append(filters, nearbyGameFilter{
			SeriesID:  seriesID,
			CabinetID: strings.TrimSpace(query.CabinetID),
		})
	}
	return filters, nil
}

func parseDistanceLimit(raw string) (float64, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false, nil
	}
	val, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false, err
	}
	if val < 0 {
		return 0, false, errors.New("distance_limit must be non-negative")
	}
	return val, true, nil
}

func buildExpandedGameValue(app core.App, moleculeID string) (map[string]any, bool) {
	return arcadeinternal.BuildExpandedGameValue(app, moleculeID)
}

func filterExpandedGameItems(items any, filters []nearbyGameFilter) []map[string]any {
	raw, ok := items.([]map[string]any)
	if !ok || len(raw) == 0 || len(filters) == 0 {
		if ok {
			return raw
		}
		return []map[string]any{}
	}

	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		seriesID := expandedGameItemSeriesID(item)
		cabinetID := expandedGameItemCabinetID(item)
		for _, filter := range filters {
			if seriesID == filter.SeriesID && nearbyCabinetMatches(filter, cabinetID) {
				out = append(out, item)
				break
			}
		}
	}
	return out
}

func expandedGameItemCabinetID(item map[string]any) string {
	if item == nil {
		return ""
	}
	switch cabinet := item["cabinet"].(type) {
	case string:
		return strings.TrimSpace(cabinet)
	case map[string]any:
		id, _ := cabinet["id"].(string)
		return strings.TrimSpace(id)
	default:
		return ""
	}
}

func expandedGameItemSeriesID(item map[string]any) string {
	seriesObj, ok := item["series"].(map[string]any)
	if !ok {
		return ""
	}
	seriesID, _ := seriesObj["id"].(string)
	return strings.TrimSpace(seriesID)
}

func sumExpandedGameQuantity(items any) int {
	raw, ok := items.([]map[string]any)
	if !ok {
		return 0
	}
	total := 0
	for _, item := range raw {
		total += expandedGameItemQuantity(item)
	}
	return total
}

func expandedGameItemQuantity(item map[string]any) int {
	if item == nil {
		return 0
	}
	raw, ok := item["quantity"]
	if !ok {
		return 0
	}
	switch v := raw.(type) {
	case int:
		return v
	case int8:
		return int(v)
	case int16:
		return int(v)
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float32:
		return int(v)
	case float64:
		return int(v)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return n
		}
	}
	return 0
}

func matchesAllGameFilters(installations []ArcadeGameInstallation, filters []nearbyGameFilter) bool {
	for _, filter := range filters {
		matched := false
		for _, installation := range installations {
			if installation.SeriesID != filter.SeriesID {
				continue
			}
			if !nearbyCabinetMatches(filter, installation.CabinetID) {
				continue
			}
			matched = true
			break
		}
		if !matched {
			return false
		}
	}
	return true
}

func nearbyCabinetMatches(filter nearbyGameFilter, cabinetID string) bool {
	return filter.CabinetID == "" || strings.TrimSpace(cabinetID) == filter.CabinetID
}

func addressMatchesFilter(rawAddress any, normalizedFilter string) bool {
	address, ok := rawAddress.(string)
	if !ok {
		return false
	}
	normalizedAddress := normalizeAddressKeyword(address)
	if normalizedAddress == "" || normalizedFilter == "" {
		return false
	}

	addressTokens := strings.Fields(normalizedAddress)
	filterTokens := strings.Fields(normalizedFilter)
	if len(addressTokens) == 0 || len(filterTokens) == 0 {
		return false
	}

	// 단일 토큰 검색은 각 주소 토큰의 prefix 로 매칭해 오탐(예: 해운대구 vs 대구)을 줄인다.
	if len(filterTokens) == 1 {
		filterToken := filterTokens[0]
		for _, token := range addressTokens {
			if strings.HasPrefix(token, filterToken) {
				return true
			}
		}
		return false
	}

	// 다중 토큰 검색은 연속된 주소 토큰 구간에서 각 토큰 prefix 일치를 확인한다.
	window := len(filterTokens)
	for i := 0; i+window <= len(addressTokens); i++ {
		matched := true
		for j := 0; j < window; j++ {
			if !strings.HasPrefix(addressTokens[i+j], filterTokens[j]) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func normalizeAddressKeyword(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return ""
	}
	normalized = strings.Join(strings.Fields(normalized), " ")
	return regionAliasReplacer.Replace(normalized)
}
