package analytics

import (
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/pocketbase/pocketbase/core"
	_ "github.com/pocketbase/pocketbase/migrations"
	"testing"
)

func TestVisitAnalyticsUsesDeploymentAggregatePolicy(t *testing.T) {
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir()})
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	defer app.ResetBootstrapState()
	events := core.NewBaseCollection("arcade_analytics_event")
	for _, name := range []string{"arcade", "event_type", "event_group", "source", "series"} {
		events.Fields.Add(&core.TextField{Name: name})
	}
	if err := app.Save(events); err != nil {
		t.Fatal(err)
	}
	visits := core.NewBaseCollection("arcade_visit")
	visits.Fields.Add(&core.TextField{Name: "user"}, &core.TextField{Name: "arcade"})
	if err := app.Save(visits); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"excluded", "excluded", "included"} {
		r := core.NewRecord(visits)
		r.Set("user", id)
		r.Set("arcade", "target")
		if err := app.Save(r); err != nil {
			t.Fatal(err)
		}
	}
	before, err := loadRestrictedStats(app, "target")
	if err != nil || before.VisitVerifications != 3 || before.DistinctVisitors != 2 {
		t.Fatalf("before=%+v err=%v", before, err)
	}
	userhandler.RegisterVisitPolicy(app, userhandler.VisitPolicy{AggregateCondition: "v.user != 'excluded'"})
	filtered, err := loadRestrictedStats(app, "target")
	if err != nil || filtered.VisitVerifications != 1 || filtered.DistinctVisitors != 1 {
		t.Fatalf("filtered=%+v err=%v", filtered, err)
	}
	userhandler.RegisterVisitPolicy(app, userhandler.VisitPolicy{})
	restored, err := loadRestrictedStats(app, "target")
	if err != nil || restored.VisitVerifications != 3 {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
}
