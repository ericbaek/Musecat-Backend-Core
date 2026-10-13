package stats

import (
	"net/http"
	"time"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"

	"github.com/pocketbase/pocketbase/core"
)

// GetStats handles GET /stats.
// It returns aggregate counts using a single SQL row to avoid loading records.
func GetStats(re *core.RequestEvent) error {
	rows, err := re.App.DB().NewQuery(`
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
 (SELECT COUNT(*) FROM arcade_visit v JOIN arcade a ON a.id=v.arcade WHERE a.public=1 AND ` + userhandler.VisitAggregateCondition(re.App) + `) AS visit_count
`).Rows()
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to load stats",
			"details": err.Error(),
		})
	}
	defer rows.Close()

	if !rows.Next() {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error": "failed to load stats",
		})
	}

	var arcadeCount int64
	var changelogCount int64
	var flagCount, reactionCount, ongoingCount, solvedCount, visitCount int64
	if err := rows.Scan(&arcadeCount, &changelogCount, &flagCount, &reactionCount, &ongoingCount, &solvedCount, &visitCount); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to load stats",
			"details": err.Error(),
		})
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
	if err := re.App.DB().NewQuery(`
SELECT substr(e.created, 1, 10) AS date, COUNT(DISTINCT e.event_group) AS count
FROM arcade_analytics_event e JOIN arcade a ON a.id=e.arcade
WHERE a.public=1 AND e.event_type='page_view' AND e.created >= '2026-09-30'
GROUP BY substr(e.created, 1, 10) ORDER BY date
`).All(&daily); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load view stats"})
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
	return re.JSON(http.StatusOK, map[string]any{
		"ongoing_flag_count":  ongoingCount,
		"solved_flag_count":   solvedCount,
		"visit_count":         visitCount,
		"page_view_count":     cumulative,
		"page_views":          series,
		"arcade_count":        arcadeCount,
		"changelog_count":     changelogCount,
		"flag_count":          flagCount,
		"flag_reaction_count": reactionCount,
	})
}
