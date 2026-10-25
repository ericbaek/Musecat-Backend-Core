package stats

import (
	"context"
	"fmt"
	"net/http"
	"time"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"

	"github.com/pocketbase/pocketbase/core"
)

// GetStats handles GET /stats.
// It returns aggregate counts using a single SQL row to avoid loading records.
func GetStats(re *core.RequestEvent) error {
	if err := re.Request.Context().Err(); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load stats"})
	}
	value, err := getStatsCache(re.App).load(re.Request.Context(), re.App)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load stats", "details": err.Error()})
	}
	return re.JSON(http.StatusOK, value)
}

func queryStats(ctx context.Context, app core.App) (map[string]any, error) {
	rows, err := app.DB().NewQuery(`
SELECT
	(SELECT COUNT(*) FROM arcade WHERE public = 1) AS arcade_count,
	(
		(
			SELECT COUNT(*)
			FROM arcade_changelog c
			INNER JOIN arcade a ON a.id = c.arcade
			WHERE a.public = 1
		)
		+ (
			SELECT COUNT(*)
			FROM z_legacy_tickets t
			INNER JOIN arcade a ON a.id = t.arcade
			WHERE a.public = 1
		)
	) AS changelog_count,
	(
		(SELECT COUNT(*) FROM arcade_flag f INNER JOIN arcade a ON a.id = f.arcade WHERE a.public = 1)
		+ (
			SELECT COUNT(*)
			FROM arcade_flag_reaction r
			INNER JOIN arcade_flag f ON f.id = r.flag
			INNER JOIN arcade a ON a.id = f.arcade
			WHERE a.public = 1
		)
	) AS flag_count,
 (SELECT COUNT(*) FROM arcade_flag_reaction r JOIN arcade_flag f ON f.id=r.flag JOIN arcade a ON a.id=f.arcade WHERE a.public=1) AS flag_reaction_count,
 (SELECT COUNT(*) FROM arcade_flag f JOIN arcade a ON a.id=f.arcade WHERE a.public=1 AND f.solved=0) AS ongoing_flag_count,
 (SELECT COUNT(*) FROM arcade_flag f JOIN arcade a ON a.id=f.arcade WHERE a.public=1 AND f.solved=1) AS solved_flag_count,
 (SELECT COUNT(*) FROM arcade_visit v JOIN arcade a ON a.id=v.arcade WHERE a.public=1 AND ` + userhandler.VisitAggregateCondition(app) + `) AS visit_count
`).WithContext(ctx).Rows()
	if err != nil {
		return nil, fmt.Errorf("load stats: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, fmt.Errorf("load stats: no aggregate row: %v", rows.Err())
	}

	var arcadeCount int64
	var changelogCount int64
	var flagCount, reactionCount, ongoingCount, solvedCount, visitCount int64
	if err := rows.Scan(&arcadeCount, &changelogCount, &flagCount, &reactionCount, &ongoingCount, &solvedCount, &visitCount); err != nil {
		return nil, fmt.Errorf("decode stats: %w", err)
	}

	rows.Close()
	type viewDay struct {
		Date  string `json:"date"`
		Count int64  `json:"count"`
	}
	var daily []struct {
		Date  string `db:"date"`
		Count int64  `db:"count"`
	}
	// This is a global aggregate: scan events once rather than probing the
	// per-arcade index and fetching event timestamps for every venue.
	if err := app.DB().NewQuery(`
SELECT substr(e.created, 1, 10) AS date, COUNT(DISTINCT e.event_group) AS count
FROM arcade_analytics_event e NOT INDEXED
WHERE e.arcade IN (SELECT id FROM arcade WHERE public=1)
  AND e.event_type='page_view' AND e.created >= '2026-09-30'
GROUP BY substr(e.created, 1, 10) ORDER BY date
`).WithContext(ctx).All(&daily); err != nil {
		return nil, fmt.Errorf("load view stats: %w", err)
	}
	counts := map[string]int64{}
	for _, day := range daily {
		counts[day.Date] = day.Count
	}
	series := []viewDay{}
	var cumulative int64
	start := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	for day := start; !day.After(time.Now().UTC()); day = day.AddDate(0, 0, 1) {
		date := day.Format("2006-01-02")
		cumulative += counts[date]
		series = append(series, viewDay{date, cumulative})
	}
	return map[string]any{
		"ongoing_flag_count":  ongoingCount,
		"solved_flag_count":   solvedCount,
		"visit_count":         visitCount,
		"page_view_count":     cumulative,
		"page_views":          series,
		"arcade_count":        arcadeCount,
		"changelog_count":     changelogCount,
		"flag_count":          flagCount,
		"flag_reaction_count": reactionCount,
	}, nil
}
