package query

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

const (
	defaultArcadeListPageSize = 50
	maxArcadeListPageSize     = 100
)

// ListArcades handles GET /arcades. Contract v2 always returns operating,
// public arcades inside a pagination envelope. It intentionally has no draft
// or closed-record escape hatch.
func ListArcades(re *core.RequestEvent) error {
	q := re.Request.URL.Query()
	if raw := strings.TrimSpace(q.Get("public")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil || !value {
			return re.JSON(http.StatusBadRequest, map[string]any{"error": "public=false is not supported by /arcades"})
		}
	}
	if raw := strings.TrimSpace(q.Get("closed")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil || value {
			return re.JSON(http.StatusBadRequest, map[string]any{"error": "closed=true is not supported by /arcades"})
		}
	}

	page, perPage, err := parseArcadeListPagination(q.Get("page"), q.Get("per_page"))
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	country := strings.ToUpper(strings.TrimSpace(q.Get("country")))
	candidates, err := GetArcadeCandidates(re.App)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load arcades", "details": err.Error()})
	}

	items := make([]ArcadeCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Closed {
			continue
		}
		if country != "" && !strings.EqualFold(candidate.Country, country) {
			continue
		}
		items = append(items, candidate)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	total := len(items)
	lastPage := 0
	if total > 0 {
		lastPage = (total + perPage - 1) / perPage
	}
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}
	out := make([]map[string]any, 0, end-start)
	for _, candidate := range items[start:end] {
		out = append(out, candidate.Summary(true, true))
	}
	return re.JSON(http.StatusOK, map[string]any{
		"page":      page,
		"per_page":  perPage,
		"last_page": lastPage,
		"total":     total,
		"items":     out,
	})
}

func parseArcadeListPagination(rawPage, rawPerPage string) (int, int, error) {
	page := 1
	perPage := defaultArcadeListPageSize
	if rawPage = strings.TrimSpace(rawPage); rawPage != "" {
		parsed, err := strconv.Atoi(rawPage)
		if err != nil || parsed < 1 {
			return 0, 0, fmt.Errorf("page must be a positive integer")
		}
		page = parsed
	}
	if rawPerPage = strings.TrimSpace(rawPerPage); rawPerPage != "" {
		parsed, err := strconv.Atoi(rawPerPage)
		if err != nil || parsed < 1 || parsed > maxArcadeListPageSize {
			return 0, 0, fmt.Errorf("per_page must be between 1 and %d", maxArcadeListPageSize)
		}
		perPage = parsed
	}
	return page, perPage, nil
}
