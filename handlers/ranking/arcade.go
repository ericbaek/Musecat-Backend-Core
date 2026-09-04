package ranking

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

const arcadeContributionRankingLimit = 5

var arcadeEditRankingParts = []string{
	"basic",
	"game",
	"hour",
	"sns",
	"gtk",
	"photo",
	"memo",
}

// ArcadeVisitRanking handles GET /arcade/ranking?arcade=<id>.
// It ranks contributors by XP earned from this arcade's visit verifications
// and arcade changelog edit grants.
func ArcadeVisitRanking(re *core.RequestEvent) error {
	arcadeID := strings.TrimSpace(re.Request.URL.Query().Get("arcade"))
	if arcadeID == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "arcade is required"})
	}

	arcade, err := re.App.FindRecordById("arcade", arcadeID)
	if err != nil || !arcade.GetBool("public") {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "arcade not found"})
	}

	entries, err := loadArcadeContributionRankings(re.App, re.Request.Context(), arcadeID)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load arcade ranking", "details": err.Error()})
	}

	return re.JSON(http.StatusOK, map[string]any{
		"arcade":  arcadeID,
		"entries": entries,
	})
}

func loadArcadeContributionRankings(app core.App, ctx context.Context, arcadeID string) ([]entry, error) {
	params := dbx.Params{
		"arcade": arcadeID,
		"limit":  arcadeContributionRankingLimit,
	}
	editPredicates := make([]string, 0, len(arcadeEditRankingParts))
	for index, part := range arcadeEditRankingParts {
		key := fmt.Sprintf("edit%d", index)
		params[key] = "xp:arcade-edit:" + part + ":" + arcadeID + "%"
		editPredicates = append(editPredicates, "l.kind LIKE {:"+key+"}")
	}

	userTags := "'[]'"
	if collection, err := app.FindCollectionByNameOrId("user"); err == nil && collection.Fields.GetByName("tags") != nil {
		userTags = "COALESCE(u.tags, '[]')"
	}

	rows, err := app.DB().NewQuery(fmt.Sprintf(`
WITH contributions AS (
SELECT v.user AS user_id, SUM(COALESCE(v.gained_exp, 0)) AS score
FROM arcade_visit v
LEFT JOIN user_info ui ON ui.id = v.user
WHERE v.arcade = {:arcade}
  AND COALESCE(v.gained_exp, 0) > 0
  AND COALESCE(NULLIF(ui.visit_visibility, ''), 'summary') IN ('summary', 'full')
GROUP BY v.user
UNION ALL
SELECT l.user AS user_id, SUM(COALESCE(l.diff_exp, 0)) AS score
FROM user_level_log l
WHERE %s
GROUP BY l.user
), scores AS (
SELECT user_id, SUM(score) AS score
FROM contributions
GROUP BY user_id
HAVING SUM(score) > 0
), ranked AS (
SELECT
  scores.score,
  u.id,
  COALESCE(NULLIF(ui.nickname, ''), u.username) AS nickname,
  u.username,
  COALESCE(ui.avatar, '') AS avatar,
	  COALESCE(ui.countries, '[]') AS countries,
  COALESCE(ul.exp, 0) AS exp,
  %s AS tags,
  RANK() OVER (ORDER BY scores.score DESC) AS rank,
  ROW_NUMBER() OVER (
    ORDER BY scores.score DESC,
      COALESCE(NULLIF(ui.nickname, ''), u.username) COLLATE NOCASE ASC,
      u.id ASC
  ) AS leaderboard_position
FROM scores
INNER JOIN "user" u ON u.id = scores.user_id
LEFT JOIN user_info ui ON ui.id = u.id
LEFT JOIN user_level ul ON ul.user = u.id
WHERE COALESCE(u.withdrawn, 0) = 0
)
SELECT score, id, nickname, username, avatar, countries, exp, tags, rank
FROM ranked
WHERE leaderboard_position <= {:limit}
ORDER BY leaderboard_position ASC
`, strings.Join(editPredicates, " OR "), userTags)).Bind(params).WithContext(ctx).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]entry, 0, arcadeContributionRankingLimit)
	for rows.Next() {
		var item entry
		var exp int
		var countries string
		var tags string
		item.Profile = &profile{}
		if err := rows.Scan(
			&item.Score,
			&item.Profile.ID,
			&item.Profile.Nickname,
			&item.Profile.Username,
			&item.Profile.Avatar,
			&countries,
			&exp,
			&tags,
			&item.Rank,
		); err != nil {
			return nil, err
		}
		item.Profile.Level = userhandler.LevelFromExp(exp)
		item.Profile.PrimaryCountry = userhandler.PrimaryProfileCountryFromJSON(countries)
		item.Profile.Tags = parseTags(tags)
		entries = append(entries, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}
