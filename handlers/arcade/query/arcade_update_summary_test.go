package query

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/tests"
)

const summaryFixtureSQL = `SELECT 'arcade' AS arcade, 'basic' AS changed, 'author' AS changed_by,
 '2030-01-01' AS created, '' AS bulk_id, '2030-01-01' AS block_started_at,
 '2030-01-01' AS block_ended_at, 1 AS block_id, 0 AS total_blocks`

func TestPublicUpdateSummaryCoalescesConcurrentLoadsWithoutCaching(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	app.ConcurrentDB().(*dbx.DB).QueryLogFunc = func(_ context.Context, _ time.Duration, query string, _ *sql.Rows, _ error) {
		if query != summaryFixtureSQL {
			return
		}
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := loadSharedArcadeUpdateSummary(context.Background(), app, summaryFixtureSQL)
			if err == nil && len(rows) != 1 {
				err = errors.New("missing summary")
			}
			results <- err
		}()
	}
	<-started
	// Keep the query in flight while the other request goroutines join it.
	time.AfterFunc(100*time.Millisecond, func() { close(release) })
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent SQL loads: %d", calls.Load())
	}
	if _, err := loadSharedArcadeUpdateSummary(context.Background(), app, summaryFixtureSQL); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("public summary was cached across loads")
	}
}

func TestCanceledSummaryCallerDoesNotCancelSharedQuery(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()
	started, release, finished := make(chan context.Context, 1), make(chan struct{}), make(chan struct{})
	app.ConcurrentDB().(*dbx.DB).QueryLogFunc = func(ctx context.Context, _ time.Duration, query string, _ *sql.Rows, _ error) {
		if query == summaryFixtureSQL {
			started <- ctx
			<-release
			close(finished)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := loadSharedArcadeUpdateSummary(ctx, app, summaryFixtureSQL); result <- err }()
	queryCtx := <-started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller error: %v", err)
	}
	if queryCtx.Err() != nil {
		t.Fatal("caller canceled the shared query")
	}
	deadline, ok := queryCtx.Deadline()
	if !ok || time.Until(deadline) > 12*time.Second {
		t.Fatal("shared query is not bounded")
	}
	close(release)
	<-finished
	// Wait for the worker to release its rows before closing the test database.
	arcadeUpdateSummaryLoads.Do(appCacheKey(app), func() (any, error) { return nil, nil })
}
