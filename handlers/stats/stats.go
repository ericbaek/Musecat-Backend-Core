package stats

import (
	"net/http"

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
	) AS flag_count
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
	var flagCount int64
	if err := rows.Scan(&arcadeCount, &changelogCount, &flagCount); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to load stats",
			"details": err.Error(),
		})
	}

	return re.JSON(http.StatusOK, map[string]any{
		"arcade_count":    arcadeCount,
		"changelog_count": changelogCount,
		"flag_count":      flagCount,
	})
}
