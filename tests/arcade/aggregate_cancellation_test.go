package arcade_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	arcadequery "github.com/ericbaek/musecat-backend-core/handlers/arcade/query"
	"github.com/ericbaek/musecat-backend-core/handlers/stats"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

func TestAggregateQueriesRespectRequestCancellation(t *testing.T) {
	for _, tc := range []struct {
		path    string
		handler func(*core.RequestEvent) error
	}{
		{"/stats", stats.GetStats},
		{"/arcades/updates", arcadequery.ListArcadeUpdates},
		{"/arcades/updates/history", arcadequery.ListArcadeUpdateHistory},
	} {
		t.Run(tc.path, func(t *testing.T) {
			app := newArcadeTestApp(t)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			response := httptest.NewRecorder()
			event := &core.RequestEvent{
				App: app,
				Event: router.Event{
					Request:  httptest.NewRequest(http.MethodGet, tc.path, nil).WithContext(ctx),
					Response: response,
				},
			}
			if err := tc.handler(event); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusBadGateway {
				t.Fatalf("cancelled aggregate returned %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestArcadeUpdatesReleaseConnectionBeforeEnrichment(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUser(t, app)
	id, _ := seedArcade(t, app, user.Id, arcadeSeed{Name: "Single connection", Address: "A", Location: location{Lat: 37, Lon: 127}, Country: "KR"})
	setArcadeVisibilityAndUpdated(t, app, id, true, false, time.Now())
	seedArcadeChangelog(t, app, id, "basic", user.Id, time.Now())
	pool := app.ConcurrentDB().(*dbx.DB).DB()
	pool.SetMaxOpenConns(1)
	response := httptest.NewRecorder()
	event := &core.RequestEvent{App: app, Event: router.Event{
		Request: httptest.NewRequest(http.MethodGet, "/arcades/updates", nil), Response: response,
	}}
	done := make(chan error, 1)
	go func() { done <- arcadequery.ListArcadeUpdates(event) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		// Unblock a regressed implementation before test-app cleanup.
		pool.SetMaxOpenConns(2)
		<-done
		t.Fatal("update enrichment waited on its own aggregate connection")
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), id) {
		t.Fatalf("missing update with a single DB connection: %d %s", response.Code, response.Body.String())
	}
}
