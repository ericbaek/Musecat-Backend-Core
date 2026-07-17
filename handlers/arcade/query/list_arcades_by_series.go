package query

import (
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

// ListArcadesBySeriesAndLocation 는 GET /arcades/nearby?game_series=...&lat=...&lon=...&address=...&country=...&page=... 요청을 처리한다.
// 여러 game_series 를 모두 포함하는 공개·영업 중 오락실을 거리순으로 최대 15개씩 페이지네이션해 반환한다.
func ListArcadesBySeriesAndLocation(re *core.RequestEvent) error {
	q := re.Request.URL.Query()

	// 1. 쿼리 파라미터에서 게임 시리즈 ID 들을 읽어온다. 쉼표 또는 다중 쿼리 파라미터를 모두 허용한다.
	seriesIDs := parseSeriesIDs(q["game_series"])
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
		// 시리즈 필터: 요청된 모든 시리즈를 포함하지 않으면 스킵
		if len(seriesIDs) > 0 && !containsAllSeries(candidate.GameSeries, seriesIDs) {
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
				if len(seriesIDs) > 0 {
					expandedGame["items"] = filterExpandedGameItemsBySeries(expandedGame["items"], seriesIDs)
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
	countryTotals := summarizeCountryTotals(results)
	response := map[string]any{
		"page":           page,
		"per_page":       nearbyPageSize,
		"last_page":      lastPage,
		"total":          total,
		"country_totals": countryTotals,
		"items":          items,
	}

	// 8. 페이지 정보와 함께 응답한다.
	return re.JSON(http.StatusOK, response)
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

func parseSeriesIDs(params []string) []string {
	set := map[string]struct{}{}
	for _, p := range params {
		for _, part := range strings.Split(p, ",") {
			if id := strings.TrimSpace(part); id != "" {
				set[id] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	return out
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

func filterExpandedGameItemsBySeries(items any, seriesIDs []string) []map[string]any {
	raw, ok := items.([]map[string]any)
	if !ok || len(raw) == 0 || len(seriesIDs) == 0 {
		if ok {
			return raw
		}
		return []map[string]any{}
	}

	seriesSet := map[string]struct{}{}
	for _, seriesID := range seriesIDs {
		seriesID = strings.TrimSpace(seriesID)
		if seriesID == "" {
			continue
		}
		seriesSet[seriesID] = struct{}{}
	}
	if len(seriesSet) == 0 {
		return []map[string]any{}
	}

	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		seriesObj, ok := item["series"].(map[string]any)
		if !ok {
			continue
		}
		seriesID, _ := seriesObj["id"].(string)
		if _, exists := seriesSet[strings.TrimSpace(seriesID)]; !exists {
			continue
		}
		out = append(out, item)
	}
	return out
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

// containsAllSeries 는 itemSeries(인터페이스로 전달됨)에 모든 targetSeries 가 포함되어 있는지 확인한다.
func containsAllSeries(itemSeries any, targetSeries []string) bool {
	raw, ok := itemSeries.([]string)
	if !ok || len(raw) == 0 {
		return false
	}
	seriesSet := map[string]struct{}{}
	for _, s := range raw {
		seriesSet[s] = struct{}{}
	}
	for _, target := range targetSeries {
		if _, exists := seriesSet[target]; !exists {
			return false
		}
	}
	return true
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
