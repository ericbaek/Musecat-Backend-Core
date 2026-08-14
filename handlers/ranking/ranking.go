package ranking

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

const leaderboardLimit = 100

type metric string

const (
	metricExplorer     metric = "explorer"
	metricVisits       metric = "visits"
	metricXP           metric = "xp"
	metricLevel        metric = "level"
	metricPhotographer metric = "photographer"
)

type period string

const (
	periodWeek     period = "week"
	periodMonth    period = "month"
	periodHalfYear period = "half_year"
	periodYear     period = "year"
	periodAll      period = "all"
)

type profile struct {
	ID       string   `json:"id"`
	Nickname string   `json:"nickname"`
	Username string   `json:"username"`
	Avatar   string   `json:"avatar"`
	Level    int      `json:"level"`
	Tags     []string `json:"tags"`
}

type entry struct {
	Rank    int     `json:"rank"`
	Score   int64   `json:"score"`
	Profile profile `json:"profile"`
}

// List handles GET /rankings?metric=<explorer|visits|xp|level|photographer>&period=<week|month|half_year|year|all>.
func List(re *core.RequestEvent) error {
	m, err := parseMetric(re.Request.URL.Query().Get("metric"))
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	p, err := parsePeriod(re.Request.URL.Query().Get("period"))
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	if m == metricLevel && p != periodAll {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "period must be all for level rankings"})
	}

	viewerID := ""
	if re.Auth != nil && re.Auth.Collection().Name == "user" {
		viewerID = re.Auth.Id
	}
	entries, viewer, err := load(re.App, m, p, time.Now().UTC(), viewerID)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load rankings", "details": err.Error()})
	}
	return re.JSON(http.StatusOK, map[string]any{
		"metric":  m,
		"period":  p,
		"entries": entries,
		"viewer":  viewer,
	})
}

func parseMetric(raw string) (metric, error) {
	switch metric(strings.TrimSpace(raw)) {
	case metricExplorer, metricVisits, metricXP, metricLevel, metricPhotographer:
		return metric(strings.TrimSpace(raw)), nil
	default:
		return "", fmt.Errorf("invalid ranking metric")
	}
}

func parsePeriod(raw string) (period, error) {
	switch period(strings.TrimSpace(raw)) {
	case periodWeek, periodMonth, periodHalfYear, periodYear, periodAll:
		return period(strings.TrimSpace(raw)), nil
	default:
		return "", fmt.Errorf("invalid ranking period")
	}
}

func rangeStart(p period, now time.Time) string {
	var duration time.Duration
	switch p {
	case periodWeek:
		duration = 7 * 24 * time.Hour
	case periodMonth:
		duration = 30 * 24 * time.Hour
	case periodHalfYear:
		duration = 183 * 24 * time.Hour
	case periodYear:
		duration = 365 * 24 * time.Hour
	default:
		return ""
	}
	return now.Add(-duration).Format("2006-01-02 15:04:05.000Z")
}

func load(app core.App, m metric, p period, now time.Time, viewerID string) ([]entry, *entry, error) {
	query, params := metricQuery(app, m, rangeStart(p, now))
	rows, err := app.DB().NewQuery(query + `
ORDER BY score DESC, nickname COLLATE NOCASE ASC, id ASC
LIMIT {:limit}
`).Bind(params).Rows()
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	entries := make([]entry, 0)
	for rows.Next() {
		item, err := scanEntry(rows, m)
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if viewerID == "" {
		return entries, nil, nil
	}

	params["viewer"] = viewerID
	viewerRows, err := app.DB().NewQuery(query + `
WHERE id = {:viewer}
LIMIT 1
`).Bind(params).Rows()
	if err != nil {
		return nil, nil, err
	}
	defer viewerRows.Close()
	if !viewerRows.Next() {
		return entries, nil, viewerRows.Err()
	}
	viewer, err := scanEntry(viewerRows, m)
	if err != nil {
		return nil, nil, err
	}
	return entries, &viewer, viewerRows.Err()
}

func scanEntry(rows interface{ Scan(dest ...any) error }, m metric) (entry, error) {
	var item entry
	var exp int
	var tags string
	if err := rows.Scan(&item.Score, &item.Profile.ID, &item.Profile.Nickname, &item.Profile.Username, &item.Profile.Avatar, &exp, &tags, &item.Rank); err != nil {
		return entry{}, err
	}
	item.Profile.Level = userhandler.LevelFromExp(exp)
	item.Profile.Tags = parseTags(tags)
	if m == metricLevel {
		item.Score = int64(item.Profile.Level)
	}
	return item, nil
}

func parseTags(raw string) []string {
	var tags []string
	if json.Unmarshal([]byte(raw), &tags) != nil {
		return []string{}
	}
	return tags
}

func metricQuery(app core.App, m metric, start string) (string, dbx.Params) {
	params := dbx.Params{"limit": leaderboardLimit}
	filterFor := func(column string) string {
		if start == "" {
			return ""
		}
		params["start"] = start
		return " AND " + column + " >= {:start}"
	}

	var source string
	switch m {
	case metricExplorer:
		source = `SELECT user, COUNT(DISTINCT arcade) AS score FROM arcade_visit WHERE 1=1` + filterFor("visited_at") + ` GROUP BY user`
	case metricVisits:
		source = `SELECT user, COUNT(*) AS score FROM arcade_visit WHERE 1=1` + filterFor("visited_at") + ` GROUP BY user`
	case metricXP:
		source = `SELECT user, SUM(diff_exp) AS score FROM user_level_log WHERE 1=1` + filterFor("created") + ` GROUP BY user HAVING SUM(diff_exp) > 0`
	case metricLevel:
		source = `SELECT user, exp AS score FROM user_level WHERE exp > 0`
	case metricPhotographer:
		source = `SELECT createdBy AS user, COUNT(*) AS score FROM arcade_photo_atoms WHERE public = 1` + filterFor("created") + ` GROUP BY createdBy`
	}

	visitVisibility := ""
	if m == metricExplorer || m == metricVisits {
		visitVisibility = " AND COALESCE(NULLIF(ui.visit_visibility, ''), 'summary') IN ('summary', 'full')"
	}
	userTags := "'[]'"
	if collection, err := app.FindCollectionByNameOrId("user"); err == nil && collection.Fields.GetByName("tags") != nil {
		userTags = "COALESCE(u.tags, '[]')"
	}

	return fmt.Sprintf(`
WITH scores AS (%s),
ranked AS (
SELECT
  scores.score,
  u.id,
  COALESCE(NULLIF(ui.nickname, ''), u.username) AS nickname,
  u.username,
  COALESCE(ui.avatar, '') AS avatar,
  COALESCE(ul.exp, 0) AS exp,
  %s AS tags,
  RANK() OVER (ORDER BY scores.score DESC) AS rank
FROM scores
INNER JOIN "user" u ON u.id = scores.user
LEFT JOIN user_info ui ON ui.id = u.id
LEFT JOIN user_level ul ON ul.user = u.id
WHERE COALESCE(u.withdrawn, 0) = 0
  AND scores.score > 0%s
)
SELECT score, id, nickname, username, avatar, exp, tags, rank
FROM ranked
`, source, userTags, visitVisibility), params
}
