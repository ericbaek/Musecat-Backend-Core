package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/tools/types"
)

// NewTestApp clones the configured PocketBase data directory and returns a ready TestApp.
func NewTestApp(tb testing.TB) *tests.TestApp {
	tb.Helper()

	dataDir := os.Getenv("PB_TEST_DATA_DIR")
	resolved, err := resolveDataDir(dataDir)
	if err != nil {
		tb.Fatalf("failed to resolve test data directory: %v", err)
	}

	app, err := tests.NewTestApp(resolved)
	if err != nil {
		tb.Fatalf("failed to initialize test app: %v", err)
	}
	ensureVisitSchema(tb, app)

	return app
}

func ensureVisitSchema(tb testing.TB, app *tests.TestApp) {
	tb.Helper()
	info, err := app.FindCollectionByNameOrId("user_info")
	if err != nil {
		tb.Fatalf("failed to load user_info: %v", err)
	}
	if info.Fields.GetByName("visit_visibility") == nil {
		info.Fields.Add(&core.SelectField{Name: "visit_visibility", Values: []string{"private", "summary", "full"}, MaxSelect: 1})
		if err := app.Save(info); err != nil {
			tb.Fatalf("failed to add visit_visibility: %v", err)
		}
	}
	if visits, err := app.FindCollectionByNameOrId("arcade_visit"); err == nil {
		changed := false
		for _, name := range []string{"distance_meters", "accuracy_meters", "gained_exp"} {
			if field, ok := visits.Fields.GetByName(name).(*core.NumberField); ok && field.Required {
				field.Required = false
				changed = true
			}
		}
		if changed {
			if err := app.Save(visits); err != nil {
				tb.Fatalf("failed to update arcade_visit: %v", err)
			}
		}
		return
	}
	users, err := app.FindCollectionByNameOrId("user")
	if err != nil {
		tb.Fatalf("failed to load user collection: %v", err)
	}
	arcades, err := app.FindCollectionByNameOrId("arcade")
	if err != nil {
		tb.Fatalf("failed to load arcade collection: %v", err)
	}
	visits := core.NewBaseCollection("arcade_visit")
	zero := 0.0
	visits.Fields.Add(&core.RelationField{Name: "user", CollectionId: users.Id, Required: true, MaxSelect: 1}, &core.RelationField{Name: "arcade", CollectionId: arcades.Id, Required: true, MaxSelect: 1}, &core.TextField{Name: "visit_day", Required: true, Max: 10}, &core.DateField{Name: "visited_at", Required: true}, &core.NumberField{Name: "distance_meters", Min: &zero}, &core.NumberField{Name: "accuracy_meters", Min: &zero}, &core.NumberField{Name: "gained_exp", OnlyInt: true})
	visits.Indexes = types.JSONArray[string]{"CREATE UNIQUE INDEX idx_arcade_visit_user_arcade_day ON arcade_visit (user, arcade, visit_day)"}
	if err := app.Save(visits); err != nil {
		tb.Fatalf("failed to create arcade_visit: %v", err)
	}
}

func resolveDataDir(dir string) (string, error) {
	if dir == "" {
		dir = "testdata/pb_data"
	}

	if filepath.IsAbs(dir) {
		if _, err := os.Stat(filepath.Join(dir, "data.db")); err != nil {
			return "", fmt.Errorf("missing PocketBase test data directory %q: %w", dir, err)
		}
		return dir, nil
	}

	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		candidate := filepath.Join(wd, dir)
		if _, err := os.Stat(filepath.Join(candidate, "data.db")); err == nil {
			return candidate, nil
		}

		parent := filepath.Dir(wd)
		if parent == wd {
			break
		}
		wd = parent
	}

	return "", fmt.Errorf("PocketBase test data directory %q not found", dir)
}
