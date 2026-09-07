package arcade_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	arcadeanalytics "github.com/ericbaek/musecat-backend-core/handlers/arcade/analytics"
)

func TestArcadeAnalyticsProtectedFieldsByRole(t *testing.T) {
	app := newArcadeTestApp(t)
	creatorToken, creator := createAuthUser(t, app)
	_ = creatorToken
	arcadeID, _ := seedPublicArcade(t, app, creator.Id, arcadeSeed{
		Name:     "Analytics Arcade",
		Address:  "1 Analytics Street",
		Location: location{Lat: 37.5665, Lon: 126.9780},
	})
	seriesA := seedAnalyticsSeries(t, app, "Series A")
	seriesB := seedAnalyticsSeries(t, app, "Series B")

	if res := executeJSONRequest(t, app, http.MethodGet, "/arcade?id="+arcadeID+"&source=nearby&game_series="+seriesA+","+seriesB, "", nil); res.StatusCode != http.StatusOK {
		t.Fatalf("first detail status=%d", res.StatusCode)
	}
	if res := executeJSONRequest(t, app, http.MethodGet, "/arcade?id="+arcadeID+"&source=search&game_series="+seriesA, "", nil); res.StatusCode != http.StatusOK {
		t.Fatalf("second detail status=%d", res.StatusCode)
	}
	for i := 0; i < 1; i++ {
		body := fmt.Sprintf(`{"arcade":%q,"event_type":"direction_click","source":"nearby"}`, arcadeID)
		res := executeJSONRequest(t, app, http.MethodPost, "/arcade/analytics/event", body, nil)
		if res.StatusCode != http.StatusOK {
			var payload map[string]any
			_ = json.NewDecoder(res.Body).Decode(&payload)
			res.Body.Close()
			t.Fatalf("direction click status=%d payload=%#v", res.StatusCode, payload)
		}
		res.Body.Close()
	}

	seedAnalyticsVisit(t, app, arcadeID, creator.Id, "2026-08-14", "2026-08-14 01:00:00.000Z")
	seedAnalyticsVisit(t, app, arcadeID, creator.Id, "2026-08-15", "2026-08-15 01:00:00.000Z")
	_, secondVisitor := createAuthUser(t, app)
	seedAnalyticsVisit(t, app, arcadeID, secondVisitor.Id, "2026-08-14", "2026-08-14 02:00:00.000Z")

	now := time.Now().UTC()
	insertStatsRecord(t, app, "arcade_changelog", map[string]any{"arcade": arcadeID, "by": creator.Id, "changed": "basic", "from": "", "to": ""}, now)
	insertStatsRecord(t, app, "arcade_changelog", map[string]any{"arcade": arcadeID, "by": creator.Id, "changed": "game", "from": "", "to": ""}, now.Add(time.Second))
	flagID := insertStatsRecord(t, app, "arcade_flag", map[string]any{
		"arcade": arcadeID, "createdBy": creator.Id, "disruption": "major", "message": "broken", "solved": true,
	}, now)
	deletedFlagID := insertStatsRecord(t, app, "arcade_flag", map[string]any{
		"arcade": arcadeID, "createdBy": creator.Id, "disruption": "minor", "message": "deleted", "solved": false,
	}, now.Add(time.Second))
	if err := arcadeanalytics.RecordFaultReportTx(app, arcadeID, deletedFlagID); err != nil {
		t.Fatalf("record deleted flag analytics: %v", err)
	}
	deletedFlag, err := app.FindRecordById("arcade_flag", deletedFlagID)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Delete(deletedFlag); err != nil {
		t.Fatalf("delete flag: %v", err)
	}
	if flagID == "" {
		t.Fatal("expected current flag id")
	}

	assertAnalyticsShape := func(t *testing.T, headers map[string]string, protected bool) {
		t.Helper()
		res := executeJSONRequest(t, app, http.MethodGet, "/arcade/analytics?arcade="+arcadeID, "", headers)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("analytics status=%d", res.StatusCode)
		}
		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			t.Fatalf("decode analytics: %v", err)
		}
		res.Body.Close()
		if payload["page_views"] != float64(2) || payload["fault_reports"] != float64(2) || payload["edit_count"] != float64(2) {
			t.Fatalf("unexpected public analytics: %#v", payload)
		}
		protectedFields := []string{"page_views_by_source", "series_filter_entries", "direction_clicks", "visit_verifications", "distinct_visitors"}
		for _, field := range protectedFields {
			_, exists := payload[field]
			if exists != protected {
				t.Fatalf("field %q presence=%v, protected=%v: %#v", field, exists, protected, payload)
			}
		}
		if !protected {
			return
		}
		if payload["direction_clicks"] != float64(1) || payload["visit_verifications"] != float64(3) || payload["distinct_visitors"] != float64(2) {
			t.Fatalf("unexpected protected analytics: %#v", payload)
		}
		sources := payload["page_views_by_source"].([]any)
		if len(sources) != 2 {
			t.Fatalf("expected two source counts: %#v", sources)
		}
		series := payload["series_filter_entries"].([]any)
		if len(series) != 2 {
			t.Fatalf("expected two series counts: %#v", series)
		}
	}

	assertAnalyticsShape(t, nil, false)

	t.Run("creator alone is not official", func(t *testing.T) {
		assertAnalyticsShape(t, map[string]string{"Authorization": "Bearer " + creatorToken}, false)
	})

	t.Run("owner tag without owns is not official", func(t *testing.T) {
		token, _ := createAuthUserWithTags(t, app, []string{"arcade_owner"})
		assertAnalyticsShape(t, map[string]string{"Authorization": "Bearer " + token}, false)
	})

	t.Run("owns without owner tag is not official", func(t *testing.T) {
		token, user := createAuthUser(t, app)
		user.Set("owns", []string{arcadeID})
		if err := app.Save(user); err != nil {
			t.Fatal(err)
		}
		token, err := user.NewAuthToken()
		if err != nil {
			t.Fatal(err)
		}
		assertAnalyticsShape(t, map[string]string{"Authorization": "Bearer " + token}, false)
	})

	t.Run("official owner receives protected fields", func(t *testing.T) {
		token, user := createAuthUserWithTags(t, app, []string{"arcade_owner"})
		user.Set("owns", []string{arcadeID})
		if err := app.Save(user); err != nil {
			t.Fatal(err)
		}
		token, err := user.NewAuthToken()
		if err != nil {
			t.Fatal(err)
		}
		assertAnalyticsShape(t, map[string]string{"Authorization": "Bearer " + token}, true)
	})

	for _, role := range []string{"developer", "moderator"} {
		t.Run(role+" receives protected fields", func(t *testing.T) {
			token, _ := createAuthUserWithTags(t, app, []string{role})
			assertAnalyticsShape(t, map[string]string{"Authorization": "Bearer " + token}, true)
		})
	}
}

func TestArcadeAnalyticsEventRejectsAnonymousAbuse(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUser(t, app)
	arcadeID, _ := seedPublicArcade(t, app, user.Id, arcadeSeed{
		Name:     "Rate Limit Arcade",
		Address:  "1 Rate Limit Street",
		Location: location{Lat: 37.5665, Lon: 126.9780},
	})

	t.Run("limits request body before decoding", func(t *testing.T) {
		res := executeJSONRequest(t, app, http.MethodPost, "/arcade/analytics/event", strings.Repeat("x", 2049), nil)
		defer res.Body.Close()
		if res.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("status=%d, want %d", res.StatusCode, http.StatusRequestEntityTooLarge)
		}
	})

	t.Run("caps submitted game series", func(t *testing.T) {
		capArcadeID, _ := seedPublicArcade(t, app, user.Id, arcadeSeed{
			Name:     "Series Cap Arcade",
			Address:  "2 Rate Limit Street",
			Location: location{Lat: 37.5666, Lon: 126.9781},
		})
		series := make([]string, 0, 11)
		for i := 0; i < 11; i++ {
			series = append(series, seedAnalyticsSeries(t, app, fmt.Sprintf("Series %d", i)))
		}
		body, err := json.Marshal(map[string]any{"arcade": capArcadeID, "event_type": "direction_click", "game_series": series})
		if err != nil {
			t.Fatal(err)
		}
		res := executeJSONRequest(t, app, http.MethodPost, "/arcade/analytics/event", string(body), nil)
		defer res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d, want %d", res.StatusCode, http.StatusBadRequest)
		}
	})

	t.Run("deduplicates rapid events from the same anonymous client", func(t *testing.T) {
		body := fmt.Sprintf(`{"arcade":%q,"event_type":"direction_click"}`, arcadeID)
		first := executeJSONRequest(t, app, http.MethodPost, "/arcade/analytics/event", body, nil)
		if first.StatusCode != http.StatusOK {
			first.Body.Close()
			t.Fatalf("first status=%d, want %d", first.StatusCode, http.StatusOK)
		}
		first.Body.Close()

		second := executeJSONRequest(t, app, http.MethodPost, "/arcade/analytics/event", body, nil)
		defer second.Body.Close()
		if second.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("second status=%d, want %d", second.StatusCode, http.StatusTooManyRequests)
		}
		if second.Header.Get("Retry-After") == "" {
			t.Fatal("rate limited response must provide Retry-After")
		}
	})
}

func TestArcadeAnalyticsRejectsPrivateAndClosedArcades(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUser(t, app)
	closedID, _ := seedPublicArcade(t, app, user.Id, arcadeSeed{Name: "Closed", Address: "Closed", Location: location{Lat: 1, Lon: 1}})
	setArcadeVisibility(t, app, closedID, true, true)
	privateID, _ := seedArcade(t, app, user.Id, arcadeSeed{Name: "Private", Address: "Private", Location: location{Lat: 1, Lon: 1}})

	for _, id := range []string{closedID, privateID} {
		res := executeJSONRequest(t, app, http.MethodGet, "/arcade/analytics?arcade="+id, "", nil)
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("arcade %q status=%d", id, res.StatusCode)
		}
		res.Body.Close()
	}
}

func seedAnalyticsSeries(tb testing.TB, app *tests.TestApp, name string) string {
	tb.Helper()
	coll, err := app.FindCollectionByNameOrId("game_series")
	if err != nil {
		tb.Fatalf("find game_series: %v", err)
	}
	rec := core.NewRecord(coll)
	rec.Set("seriesNumber", time.Now().UnixNano())
	rec.Set("en", name)
	rec.Set("kr", name)
	rec.Set("jp", name)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("save game_series: %v", err)
	}
	return rec.Id
}

func seedAnalyticsVisit(tb testing.TB, app *tests.TestApp, arcadeID, userID, day, visitedAt string) {
	tb.Helper()
	coll, err := app.FindCollectionByNameOrId("arcade_visit")
	if err != nil {
		tb.Fatalf("find arcade_visit: %v", err)
	}
	rec := core.NewRecord(coll)
	rec.Set("arcade", arcadeID)
	rec.Set("user", userID)
	rec.Set("visit_day", day)
	rec.Set("visited_at", visitedAt)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("save arcade_visit: %v", err)
	}
}
