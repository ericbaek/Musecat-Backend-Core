package analytics

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

const (
	EventPageView       = "page_view"
	EventDirectionClick = "direction_click"
	EventFaultReport    = "fault_report"

	maxAnalyticsEventBodyBytes = 2 * 1024
	maxAnalyticsEventSeries    = 10
	analyticsClientWindow      = 10 * time.Minute
	maxAnalyticsEventsWindow   = 20
	analyticsArcadeCooldown    = 30 * time.Second
	maxAnalyticsClients        = 4096
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

type analyticsClientEvents struct {
	events       []time.Time
	arcadeEvents map[string]time.Time
}

// anonymousAnalyticsEvents deliberately remains process-local. The event
// contract does not retain IP addresses, user agents, or user identity, so a
// persistent limiter key would violate that data-minimization boundary.
var anonymousAnalyticsEvents = struct {
	sync.Mutex
	byClient map[string]analyticsClientEvents
}{byClient: map[string]analyticsClientEvents{}}

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
	if err := decodeDirectionClickRequest(re, &body); err != nil {
		if err == errAnalyticsEventBodyTooLarge {
			return re.JSON(http.StatusRequestEntityTooLarge, map[string]any{"error": "analytics event body is too large"})
		}
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
	if retryAfter, allowed := allowAnonymousAnalyticsEvent(re.App, re.RealIP(), body.Arcade, time.Now()); !allowed {
		re.Response.Header().Set("Retry-After", fmt.Sprintf("%d", int((retryAfter+time.Second-1)/time.Second)))
		return re.JSON(http.StatusTooManyRequests, map[string]any{"error": "analytics event rate limit exceeded"})
	}

	seriesIDs, err := existingSeriesIDs(re.App, body.Series)
	if err != nil {
		if err == errTooManyAnalyticsSeries {
			return re.JSON(http.StatusBadRequest, map[string]any{"error": "game_series must contain at most 10 series"})
		}
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to validate game series", "details": err.Error()})
	}
	if err := recordEventGroup(re.App, body.Arcade, EventDirectionClick, normalizeSource(body.Source), seriesIDs, ""); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to record analytics event", "details": err.Error()})
	}

	return re.JSON(http.StatusOK, map[string]any{"recorded": true})
}

var (
	errAnalyticsEventBodyTooLarge = fmt.Errorf("analytics event body too large")
	errTooManyAnalyticsSeries     = fmt.Errorf("too many analytics game series")
)

func decodeDirectionClickRequest(re *core.RequestEvent, body *directionClickRequest) error {
	raw, err := io.ReadAll(io.LimitReader(re.Request.Body, maxAnalyticsEventBodyBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxAnalyticsEventBodyBytes {
		return errAnalyticsEventBodyTooLarge
	}
	return json.Unmarshal(raw, body)
}

func allowAnonymousAnalyticsEvent(app core.App, clientIP, arcadeID string, now time.Time) (time.Duration, bool) {
	if clientIP == "" {
		clientIP = "unknown"
	}
	clientKey := fmt.Sprintf("%p:%s", app, clientIP)

	anonymousAnalyticsEvents.Lock()
	defer anonymousAnalyticsEvents.Unlock()

	state, exists := anonymousAnalyticsEvents.byClient[clientKey]
	if !exists && len(anonymousAnalyticsEvents.byClient) >= maxAnalyticsClients {
		evictOldestAnonymousAnalyticsClient()
	}
	windowStart := now.Add(-analyticsClientWindow)
	kept := state.events[:0]
	for _, eventAt := range state.events {
		if eventAt.After(windowStart) {
			kept = append(kept, eventAt)
		}
	}
	state.events = kept
	if state.arcadeEvents == nil {
		state.arcadeEvents = map[string]time.Time{}
	}
	cooldownStart := now.Add(-analyticsArcadeCooldown)
	for id, eventAt := range state.arcadeEvents {
		if !eventAt.After(cooldownStart) {
			delete(state.arcadeEvents, id)
		}
	}
	if previous, ok := state.arcadeEvents[arcadeID]; ok {
		if elapsed := now.Sub(previous); elapsed < analyticsArcadeCooldown {
			return analyticsArcadeCooldown - elapsed, false
		}
	}
	if len(state.events) >= maxAnalyticsEventsWindow {
		return state.events[0].Add(analyticsClientWindow).Sub(now), false
	}

	state.events = append(state.events, now)
	state.arcadeEvents[arcadeID] = now
	anonymousAnalyticsEvents.byClient[clientKey] = state
	return 0, true
}

func evictOldestAnonymousAnalyticsClient() {
	var oldestKey string
	var oldestAt time.Time
	for clientKey, state := range anonymousAnalyticsEvents.byClient {
		lastEventAt := time.Time{}
		if len(state.events) > 0 {
			lastEventAt = state.events[len(state.events)-1]
		}
		if oldestKey == "" || lastEventAt.Before(oldestAt) {
			oldestKey = clientKey
			oldestAt = lastEventAt
		}
	}
	if oldestKey != "" {
		delete(anonymousAnalyticsEvents.byClient, oldestKey)
	}
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
	submitted := map[string]struct{}{}
	for _, value := range raw {
		for _, part := range strings.Split(value, ",") {
			id := strings.TrimSpace(part)
			if id == "" {
				continue
			}
			if _, ok := submitted[id]; ok {
				continue
			}
			if len(submitted) >= maxAnalyticsEventSeries {
				return nil, errTooManyAnalyticsSeries
			}
			submitted[id] = struct{}{}
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
