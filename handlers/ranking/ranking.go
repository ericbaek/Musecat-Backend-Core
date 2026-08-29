package ranking

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
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
	metricArcadeVisits metric = "arcade_visits"
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

type rankingStats struct {
	TravelDistanceKm int64 `json:"travel_distance_km"`
}

type arcadeRankingStats struct {
	VisitCount int64 `json:"visit_count"`
}

type arcadeSummary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Country string `json:"country"`
}

type entry struct {
	Rank    int            `json:"rank"`
	Score   int64          `json:"score"`
	Profile *profile       `json:"profile,omitempty"`
	Stats   any            `json:"stats,omitempty"`
	Arcade  *arcadeSummary `json:"arcade,omitempty"`
}

// List handles GET /rankings?metric=<explorer|visits|xp|level|photographer|arcade_visits>&period=<week|month|half_year|year|all>.
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
	entries, viewer, err := load(re.App, re.Request.Context(), m, p, time.Now().UTC(), viewerID)
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
	case metricArcadeVisits:
		return metricArcadeVisits, nil
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

func load(app core.App, ctx context.Context, m metric, p period, now time.Time, viewerID string) ([]entry, *entry, error) {
	if m == metricArcadeVisits {
		return loadArcadeRankings(app, ctx, p, now)
	}

	query, params := metricQuery(app, m, rangeStart(p, now))
	where := "WHERE leaderboard_position <= {:limit}"
	if viewerID != "" {
		params["viewer"] = viewerID
		where += " OR id = {:viewer}"
	}
	rows, err := app.DB().NewQuery(query + `
` + where + `
ORDER BY leaderboard_position ASC
`).Bind(params).WithContext(ctx).Rows()
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	entries := make([]entry, 0)
	var viewer *entry
	var previousScore int64
	var currentRank int
	var hasPreviousScore bool
	for rows.Next() {
		item, leaderboardPosition, rankingScore, err := scanUserEntry(rows, m)
		if err != nil {
			return nil, nil, err
		}
		if !hasPreviousScore || rankingScore != previousScore {
			currentRank = leaderboardPosition
			previousScore = rankingScore
			hasPreviousScore = true
		}
		item.Rank = currentRank
		if leaderboardPosition <= leaderboardLimit {
			entries = append(entries, item)
		}
		if viewerID != "" && item.Profile != nil && item.Profile.ID == viewerID {
			viewerCopy := item
			viewer = &viewerCopy
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if m == metricExplorer {
		if err := attachExplorerDistances(app, ctx, entries, viewer, rangeStart(p, now)); err != nil {
			return nil, nil, err
		}
	}
	return entries, viewer, nil
}

func scanUserEntry(rows interface{ Scan(dest ...any) error }, m metric) (entry, int, int64, error) {
	var item entry
	var rankingScore int64
	var exp int
	var tags string
	var leaderboardPosition int
	item.Profile = &profile{}
	if err := rows.Scan(&rankingScore, &item.Profile.ID, &item.Profile.Nickname, &item.Profile.Username, &item.Profile.Avatar, &exp, &tags, &leaderboardPosition); err != nil {
		return entry{}, 0, 0, err
	}
	item.Score = rankingScore
	item.Profile.Level = userhandler.LevelFromExp(exp)
	item.Profile.Tags = parseTags(tags)
	if m == metricLevel {
		item.Score = int64(item.Profile.Level)
	}
	if m == metricExplorer {
		item.Stats = &rankingStats{}
	}
	return item, leaderboardPosition, rankingScore, nil
}

func loadArcadeRankings(app core.App, ctx context.Context, p period, now time.Time) ([]entry, *entry, error) {
	start := rangeStart(p, now)
	params := dbx.Params{"limit": leaderboardLimit}
	filter := ""
	if start != "" {
		params["start"] = start
		filter = " AND v.visited_at >= {:start}"
	}
	rows, err := app.DB().NewQuery(`
WITH scores AS (
SELECT v.arcade, SUM(COALESCE(v.gained_exp, 0)) AS score, COUNT(*) AS visit_count
FROM arcade_visit v
INNER JOIN arcade a ON a.id = v.arcade
WHERE a.public = true` + filter + `
GROUP BY v.arcade
HAVING SUM(COALESCE(v.gained_exp, 0)) > 0
), ranked AS (
SELECT
  scores.score,
  scores.visit_count,
  a.id,
  COALESCE(NULLIF(ab.name, ''), a.id) AS name,
  COALESCE(a.country, '') AS country,
  RANK() OVER (ORDER BY scores.score DESC) AS rank
FROM scores
INNER JOIN arcade a ON a.id = scores.arcade
LEFT JOIN arcade_basic ab ON ab.id = a.basic
)
SELECT score, visit_count, id, name, country, rank
FROM ranked
ORDER BY score DESC, name COLLATE NOCASE ASC, id ASC
LIMIT {:limit}
`).Bind(params).WithContext(ctx).Rows()
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	entries := make([]entry, 0)
	for rows.Next() {
		var item entry
		item.Arcade = &arcadeSummary{}
		var visitCount int64
		if err := rows.Scan(&item.Score, &visitCount, &item.Arcade.ID, &item.Arcade.Name, &item.Arcade.Country, &item.Rank); err != nil {
			return nil, nil, err
		}
		item.Stats = &arcadeRankingStats{VisitCount: visitCount}
		entries = append(entries, item)
	}
	return entries, nil, rows.Err()
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
		source = `SELECT v.user, COUNT(DISTINCT v.arcade) AS score FROM arcade_visit v INNER JOIN arcade a ON a.id = v.arcade AND a.public = true WHERE 1=1` + filterFor("v.visited_at") + ` GROUP BY v.user`
	case metricVisits:
		source = `SELECT v.user, COUNT(*) AS score FROM arcade_visit v INNER JOIN arcade a ON a.id = v.arcade AND a.public = true WHERE 1=1` + filterFor("v.visited_at") + ` GROUP BY v.user`
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
  ROW_NUMBER() OVER (
    ORDER BY scores.score DESC,
      COALESCE(NULLIF(ui.nickname, ''), u.username) COLLATE NOCASE ASC,
      u.id ASC
  ) AS leaderboard_position
FROM scores
INNER JOIN "user" u ON u.id = scores.user
LEFT JOIN user_info ui ON ui.id = u.id
LEFT JOIN user_level ul ON ul.user = u.id
WHERE COALESCE(u.withdrawn, 0) = 0
  AND scores.score > 0%s
)
SELECT score, id, nickname, username, avatar, exp, tags, leaderboard_position
FROM ranked
`, source, userTags, visitVisibility), params
}

func attachExplorerDistances(app core.App, ctx context.Context, entries []entry, viewer *entry, start string) error {
	ids := make([]string, 0, len(entries)+1)
	seen := make(map[string]struct{}, len(entries)+1)
	add := func(item *entry) {
		if item == nil || item.Profile == nil {
			return
		}
		if _, ok := seen[item.Profile.ID]; ok {
			return
		}
		seen[item.Profile.ID] = struct{}{}
		ids = append(ids, item.Profile.ID)
	}
	for index := range entries {
		add(&entries[index])
	}
	add(viewer)
	if len(ids) == 0 {
		return nil
	}

	params := dbx.Params{}
	placeholders := make([]string, 0, len(ids))
	for index, id := range ids {
		key := fmt.Sprintf("user%d", index)
		params[key] = id
		placeholders = append(placeholders, "{:"+key+"}")
	}
	filter := ""
	if start != "" {
		params["start"] = start
		filter = " AND v.visited_at >= {:start}"
	}
	rows, err := app.DB().NewQuery(`
SELECT v.user, v.visited_at, ab.location
FROM arcade_visit v
INNER JOIN arcade a ON a.id = v.arcade AND a.public = true
LEFT JOIN arcade_basic ab ON ab.id = a.basic
INNER JOIN user_info ui ON ui.id = v.user
WHERE v.user IN (` + strings.Join(placeholders, ", ") + `)
  AND COALESCE(NULLIF(ui.visit_visibility, ''), 'summary') IN ('summary', 'full')` + filter + `
ORDER BY v.user ASC, v.visited_at ASC, v.id ASC
`).Bind(params).WithContext(ctx).Rows()
	if err != nil {
		return err
	}
	defer rows.Close()

	distances := make(map[string]float64, len(ids))
	var currentUser string
	var previousLat, previousLon float64
	var previousValid bool
	for rows.Next() {
		var userID, visitedAt string
		var location sql.NullString
		if err := rows.Scan(&userID, &visitedAt, &location); err != nil {
			return err
		}
		if userID != currentUser {
			currentUser = userID
			previousValid = false
		}
		lat, lon, ok := rankingLocation(location)
		if !ok {
			previousValid = false
			continue
		}
		if previousValid {
			distances[userID] += rankingDistanceKm(previousLat, previousLon, lat, lon) * 1000
		}
		previousLat, previousLon, previousValid = lat, lon, true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for index := range entries {
		if entries[index].Profile != nil {
			if stats, ok := entries[index].Stats.(*rankingStats); ok {
				stats.TravelDistanceKm = int64(math.Round(distances[entries[index].Profile.ID] / 1000))
			}
		}
	}
	if viewer != nil && viewer.Profile != nil {
		if stats, ok := viewer.Stats.(*rankingStats); ok {
			stats.TravelDistanceKm = int64(math.Round(distances[viewer.Profile.ID] / 1000))
		}
	}
	return nil
}

func rankingLocation(raw sql.NullString) (float64, float64, bool) {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return 0, 0, false
	}
	var value any
	if json.Unmarshal([]byte(raw.String), &value) != nil {
		return 0, 0, false
	}
	location, ok := value.(map[string]any)
	if !ok {
		return 0, 0, false
	}
	lat, latOK := rankingFloat(location["lat"])
	lon, lonOK := rankingFloat(location["lon"])
	if !latOK || !lonOK {
		lat, latOK = rankingFloat(location["latitude"])
		lon, lonOK = rankingFloat(location["longitude"])
	}
	return lat, lon, latOK && lonOK
}

func rankingFloat(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case json.Number:
		parsed, err := number.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(number, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func rankingDistanceKm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371.0
	toRadians := func(value float64) float64 { return value * math.Pi / 180 }
	dLat := toRadians(lat2 - lat1)
	dLon := toRadians(lon2 - lon1)
	lat1Radians := toRadians(lat1)
	lat2Radians := toRadians(lat2)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1Radians)*math.Cos(lat2Radians)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return earthRadiusKm * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
