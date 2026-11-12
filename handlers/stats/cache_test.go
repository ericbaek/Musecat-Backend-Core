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
	if status := statsStatus(app); status != http.StatusOK {
		t.Fatalf("stale cache unavailable: status=%d", status)
	}
	deadline := time.Now().Add(3 * time.Second)
	for queries.Load() != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	// Join the background refresh before the app is cleaned up.
	if _, err, _ := cache.build.Do("stats", func() (any, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if queries.Load() != 2 {
		t.Fatalf("stale refresh queries=%d", queries.Load())
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

func TestStatsStaleRefreshFailureBackoffAndMaximumAge(t *testing.T) {
	t.Cleanup(testutil.CleanupGoldenDir)
	app := testutil.NewTestApp(t)
	cache := getStatsCache(app)
	if _, err := cache.load(context.Background(), app); err != nil {
		t.Fatal(err)
	}
	if _, err := app.NonconcurrentDB().NewQuery("ALTER TABLE arcade_analytics_event RENAME TO unavailable_stats_events").Execute(); err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	cache.expires = time.Now().Add(-time.Second)
	cache.mu.Unlock()
	value, err := cache.load(context.Background(), app)
	if err != nil || value == nil {
		t.Fatalf("last good snapshot unavailable: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		cache.mu.RLock()
		retry := cache.retryAfter
		cache.mu.RUnlock()
		if !retry.IsZero() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("refresh did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	cache.mu.RLock()
	retry := cache.retryAfter
	cache.mu.RUnlock()
	if _, err := cache.load(context.Background(), app); err != nil {
		t.Fatal(err)
	}
	cache.mu.RLock()
	unchanged := cache.retryAfter.Equal(retry)
	cache.mu.RUnlock()
	if !unchanged {
		t.Fatal("stale failure was retried before cooldown")
	}
	cache.mu.Lock()
	cache.expires = time.Now().Add(-statsStaleTTL - time.Second)
	cache.mu.Unlock()
	if _, err := cache.load(context.Background(), app); err == nil {
		t.Fatal("returned a snapshot older than maximum stale age")
	}
}

func TestStatsCanceledViewerDoesNotStartRefresh(t *testing.T) {
	t.Cleanup(testutil.CleanupGoldenDir)
	app := testutil.NewTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := getStatsCache(app).load(ctx, app); err != context.Canceled {
		t.Fatalf("got %v", err)
	}
}
