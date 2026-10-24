package stats

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/testutil"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

func TestStatsCacheSharesRefreshAndExpires(t *testing.T) {
	t.Cleanup(testutil.CleanupGoldenDir)
	app := testutil.NewTestApp(t)
	var queries atomic.Int64
	app.ConcurrentDB().(*dbx.DB).QueryLogFunc = func(_ context.Context, _ time.Duration, query string, _ *sql.Rows, _ error) {
		if strings.Contains(query, "COUNT(DISTINCT e.event_group)") {
			queries.Add(1)
		}
	}
	var callers sync.WaitGroup
	for range 20 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			if status := statsStatus(app); status != http.StatusOK {
				t.Errorf("stats status %d", status)
			}
		}()
	}
	callers.Wait()
	if got := queries.Load(); got != 1 {
		t.Fatalf("concurrent viewers ran %d aggregates", got)
	}
	cache := getStatsCache(app)
	cache.mu.Lock()
	remaining := time.Until(cache.expires)
	cache.expires = time.Now().Add(-time.Second)
	cache.mu.Unlock()
	if remaining < 29*time.Minute || remaining > statsCacheTTL {
		t.Fatalf("unexpected cache lifetime %s", remaining)
	}
	if status := statsStatus(app); status != http.StatusOK || queries.Load() != 2 {
		t.Fatalf("expired cache was not refreshed: status=%d queries=%d", status, queries.Load())
	}
}

func TestStatsCacheDoesNotCacheErrors(t *testing.T) {
	t.Cleanup(testutil.CleanupGoldenDir)
	app := testutil.NewTestApp(t)
	if _, err := app.NonconcurrentDB().NewQuery("ALTER TABLE arcade_analytics_event RENAME TO unavailable_stats_events").Execute(); err != nil {
		t.Fatal(err)
	}
	if status := statsStatus(app); status != http.StatusBadGateway {
		t.Fatalf("unavailable source returned %d", status)
	}
	if _, err := app.NonconcurrentDB().NewQuery("ALTER TABLE unavailable_stats_events RENAME TO arcade_analytics_event").Execute(); err != nil {
		t.Fatal(err)
	}
	if status := statsStatus(app); status != http.StatusOK {
		t.Fatalf("failed refresh was cached: status=%d", status)
	}
}

func statsStatus(app core.App) int {
	response := httptest.NewRecorder()
	event := &core.RequestEvent{App: app, Event: router.Event{
		Request: httptest.NewRequest(http.MethodGet, "/stats", nil), Response: response,
	}}
	if err := GetStats(event); err != nil {
		return 0
	}
	return response.Code
}
