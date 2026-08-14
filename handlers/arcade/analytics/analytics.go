package analytics

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

const (
	EventPageView       = "page_view"
	EventDirectionClick = "direction_click"
	EventFaultReport    = "fault_report"
)

var analyticsSources = map[string]struct{}{
	"direct": {},
	"search": {},
	"nearby": {},
	"other":  {},
}

type directionClickRequest struct {
	Arcade    string   `json:"arcade"`
	EventType string   `json:"event_type"`
	Source    string   `json:"source"`
	Series    []string `json:"game_series"`
}

type analyticsCount struct {
	Name   string `json:"source,omitempty"`
	Series string `json:"game_series,omitempty"`
	Count  int64  `json:"count"`
}

// GetArcadeAnalytics returns public aggregate metrics. Detailed acquisition,
// direction, and visit metrics are included only for an official arcade owner
// or a developer/moderator.
func GetArcadeAnalytics(re *core.RequestEvent) error {
	arcadeID := strings.TrimSpace(re.Request.URL.Query().Get("arcade"))
	if arcadeID == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "arcade is required"})
	}

	arcade, err := re.App.FindRecordById(arcadeinternal.CollectionArcade, arcadeID)
	if err != nil || !arcade.GetBool("public") || arcade.GetBool("closed") {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "arcade not found"})
	}

	base, err := loadBaseStats(re.App, arcadeID)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load arcade analytics", "details": err.Error()})
	}

	out := map[string]any{
		"arcade":        arcadeID,
		"page_views":    base.PageViews,
		"fault_reports": base.FaultReports,
		"edit_count":    base.EditCount,
	}

	if hasRestrictedAccess(re.Auth, arcadeID) {
		restricted, err := loadRestrictedStats(re.App, arcadeID)
		if err != nil {
			return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load restricted arcade analytics", "details": err.Error()})
		}
		out["page_views_by_source"] = restricted.PageViewsBySource
		out["series_filter_entries"] = restricted.SeriesFilterEntries
		out["direction_clicks"] = restricted.DirectionClicks
		out["visit_verifications"] = restricted.VisitVerifications
		out["distinct_visitors"] = restricted.DistinctVisitors
	}

	return re.JSON(http.StatusOK, out)
}

// RecordDirectionClick handles the anonymous custom event API. It accepts one
// event type for now so callers cannot inject arbitrary analytics categories.
func RecordDirectionClick(re *core.RequestEvent) error {
	var body directionClickRequest
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
	}
	body.Arcade = strings.TrimSpace(body.Arcade)
	if body.Arcade == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "arcade is required"})
	}
	if strings.TrimSpace(body.EventType) != EventDirectionClick {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "event_type must be direction_click"})
	}

	arcade, err := re.App.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
	if err != nil || !arcade.GetBool("public") {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "arcade not found"})
	}

	seriesIDs, err := existingSeriesIDs(re.App, body.Series)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to validate game series", "details": err.Error()})
	}
	if err := recordEventGroup(re.App, body.Arcade, EventDirectionClick, normalizeSource(body.Source), seriesIDs, ""); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to record analytics event", "details": err.Error()})
	}

	return re.JSON(http.StatusOK, map[string]any{"recorded": true})
}

// RecordPageView is intentionally best effort: analytics persistence must not
// turn a successful public arcade detail request into a failed page load.
func RecordPageView(app core.App, arcadeID, source string, rawSeries []string) error {
	seriesIDs, err := existingSeriesIDs(app, rawSeries)
	if err != nil {
		return err
	}
	return recordEventGroup(app, arcadeID, EventPageView, normalizeSource(source), seriesIDs, "")
}

// RecordFaultReportTx records the durable analytics marker inside the caller's
// flag creation transaction. The flag id lets stats retain deleted reports.
func RecordFaultReportTx(app core.App, arcadeID, flagID string) error {
	return recordEventGroupTx(app, arcadeID, EventFaultReport, "other", nil, flagID)
}

func recordEventGroup(app core.App, arcadeID, eventType, source string, seriesIDs []string, subjectID string) error {
	return app.RunInTransaction(func(tx core.App) error {
		return recordEventGroupTx(tx, arcadeID, eventType, source, seriesIDs, subjectID)
	})
}

func recordEventGroupTx(app core.App, arcadeID, eventType, source string, seriesIDs []string, subjectID string) error {
	coll, err := app.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeAnalyticsEvent)
	if err != nil {
		return fmt.Errorf("find analytics event collection: %w", err)
	}

	if len(seriesIDs) == 0 {
		seriesIDs = []string{""}
	}
	groupID := core.GenerateDefaultRandomId()
	for _, seriesID := range seriesIDs {
		record := core.NewRecord(coll)
		record.Set("arcade", arcadeID)
		record.Set("event_group", groupID)
		record.Set("event_type", eventType)
		record.Set("source", source)
		record.Set("subject_id", subjectID)
		if seriesID != "" {
			record.Set("series", seriesID)
		}
		if err := app.Save(record); err != nil {
			return fmt.Errorf("save analytics event: %w", err)
		}
	}
	return nil
}

func existingSeriesIDs(app core.App, raw []string) ([]string, error) {
	ids := make([]string, 0, len(raw))
	seen := map[string]struct{}{}
	for _, value := range raw {
		for _, part := range strings.Split(value, ",") {
			id := strings.TrimSpace(part)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			if _, err := app.FindRecordById(arcadeinternal.CollectionGameSeries, id); err != nil {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func normalizeSource(source string) string {
	source = strings.ToLower(strings.TrimSpace(source))
	if _, ok := analyticsSources[source]; ok {
		return source
	}
	return "direct"
}

func hasRestrictedAccess(auth *core.Record, arcadeID string) bool {
	if auth == nil {
		return false
	}
	if hasTag(auth, "developer") || hasTag(auth, "moderator") {
		return true
	}
	return hasTag(auth, "arcade_owner") && ownsArcade(auth, arcadeID)
}

func hasTag(auth *core.Record, target string) bool {
	for _, values := range [][]string{auth.GetStringSlice("tag"), auth.GetStringSlice("tags")} {
		for _, value := range values {
			if strings.EqualFold(strings.TrimSpace(value), target) {
				return true
			}
		}
	}
	return false
}

func ownsArcade(auth *core.Record, arcadeID string) bool {
	for _, owned := range auth.GetStringSlice("owns") {
		if strings.TrimSpace(owned) == arcadeID {
			return true
		}
	}
	return false
}

type baseStats struct {
	PageViews    int64
	FaultReports int64
	EditCount    int64
}

func loadBaseStats(app core.App, arcadeID string) (baseStats, error) {
	var stats baseStats
	rows, err := app.DB().NewQuery(`
SELECT
	(SELECT COUNT(DISTINCT event_group) FROM arcade_analytics_event WHERE arcade = {:arcade} AND event_type = 'page_view'),
	(SELECT COUNT(DISTINCT subject_id) FROM (
		SELECT id AS subject_id FROM arcade_flag WHERE arcade = {:arcade}
		UNION ALL
		SELECT subject_id FROM arcade_analytics_event
		WHERE arcade = {:arcade} AND event_type = 'fault_report' AND subject_id != ''
	)),
	(SELECT COUNT(*) FROM arcade_changelog WHERE arcade = {:arcade})
`).Bind(dbx.Params{"arcade": arcadeID}).Rows()
	if err != nil {
		return stats, err
	}
	defer rows.Close()
	if !rows.Next() {
		return stats, rows.Err()
	}
	if err := rows.Scan(&stats.PageViews, &stats.FaultReports, &stats.EditCount); err != nil {
		return stats, err
	}
	return stats, rows.Err()
}

type restrictedStats struct {
	PageViewsBySource   []analyticsCount
	SeriesFilterEntries []analyticsCount
	DirectionClicks     int64
	VisitVerifications  int64
	DistinctVisitors    int64
}

func loadRestrictedStats(app core.App, arcadeID string) (restrictedStats, error) {
	stats := restrictedStats{
		PageViewsBySource:   []analyticsCount{},
		SeriesFilterEntries: []analyticsCount{},
	}
	if err := app.DB().NewQuery(`
SELECT COUNT(DISTINCT event_group)
FROM arcade_analytics_event
WHERE arcade = {:arcade} AND event_type = 'direction_click'
`).Bind(dbx.Params{"arcade": arcadeID}).Row(&stats.DirectionClicks); err != nil {
		return stats, err
	}
	if err := app.DB().NewQuery(`
SELECT COUNT(*), COUNT(DISTINCT user)
FROM arcade_visit
WHERE arcade = {:arcade}
`).Bind(dbx.Params{"arcade": arcadeID}).Row(&stats.VisitVerifications, &stats.DistinctVisitors); err != nil {
		return stats, err
	}

	sourceRows, err := app.DB().NewQuery(`
SELECT COALESCE(NULLIF(source, ''), 'direct'), COUNT(DISTINCT event_group)
FROM arcade_analytics_event
WHERE arcade = {:arcade} AND event_type = 'page_view'
GROUP BY COALESCE(NULLIF(source, ''), 'direct')
`).Bind(dbx.Params{"arcade": arcadeID}).Rows()
	if err != nil {
		return stats, err
	}
	for sourceRows.Next() {
		var item analyticsCount
		if err := sourceRows.Scan(&item.Name, &item.Count); err != nil {
			sourceRows.Close()
			return stats, err
		}
		stats.PageViewsBySource = append(stats.PageViewsBySource, item)
	}
	if err := sourceRows.Err(); err != nil {
		sourceRows.Close()
		return stats, err
	}
	sourceRows.Close()

	seriesRows, err := app.DB().NewQuery(`
SELECT series, COUNT(DISTINCT event_group)
FROM arcade_analytics_event
WHERE arcade = {:arcade} AND event_type = 'page_view' AND series != ''
GROUP BY series
`).Bind(dbx.Params{"arcade": arcadeID}).Rows()
	if err != nil {
		return stats, err
	}
	for seriesRows.Next() {
		var item analyticsCount
		if err := seriesRows.Scan(&item.Series, &item.Count); err != nil {
			seriesRows.Close()
			return stats, err
		}
		stats.SeriesFilterEntries = append(stats.SeriesFilterEntries, item)
	}
	if err := seriesRows.Err(); err != nil {
		seriesRows.Close()
		return stats, err
	}
	seriesRows.Close()

	sort.Slice(stats.PageViewsBySource, func(i, j int) bool {
		if stats.PageViewsBySource[i].Count == stats.PageViewsBySource[j].Count {
			return stats.PageViewsBySource[i].Name < stats.PageViewsBySource[j].Name
		}
		return stats.PageViewsBySource[i].Count > stats.PageViewsBySource[j].Count
	})
	sort.Slice(stats.SeriesFilterEntries, func(i, j int) bool {
		if stats.SeriesFilterEntries[i].Count == stats.SeriesFilterEntries[j].Count {
			return stats.SeriesFilterEntries[i].Series < stats.SeriesFilterEntries[j].Series
		}
		return stats.SeriesFilterEntries[i].Count > stats.SeriesFilterEntries[j].Count
	})
	return stats, nil
}
