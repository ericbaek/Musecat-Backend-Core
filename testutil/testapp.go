package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/tools/types"
	"github.com/pocketbase/pocketbase/ui"

	_ "github.com/ericbaek/musecat-backend-core/migrations"
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
	// PocketBase v0.39.9 registers UI extension routes on every new API router.
	// Core tests create multiple routers but don't exercise the bundled UI.
	ui.DistDirFS = nil
	ensureVisitSchema(tb, app)
	ensureProfileCountriesSchema(tb, app)
	ensureNoticeAuthorSchema(tb, app)
	ensureFlagResolutionSchema(tb, app)
	ensureGTKTypeCatalog(tb, app)

	return app
}

func ensureProfileCountriesSchema(tb testing.TB, app *tests.TestApp) {
	tb.Helper()
	info, err := app.FindCollectionByNameOrId("user_info")
	if err != nil {
		tb.Fatalf("failed to load user_info: %v", err)
	}
	if info.Fields.GetByName("countries") != nil {
		return
	}
	info.Fields.Add(&core.JSONField{Name: "countries", MaxSize: 128})
	if err := app.Save(info); err != nil {
		tb.Fatalf("failed to add user_info.countries: %v", err)
	}
}

// Production Core's bootstrap schema and Backend Full's forward migration own
// the GTK catalog. The checked-in test fixture predates new GTK values, so
// isolated handler tests add them to their cloned database.
func ensureGTKTypeCatalog(tb testing.TB, app *tests.TestApp) {
	tb.Helper()
	collection, err := app.FindCollectionByNameOrId("arcade_gtk_atoms")
	if err != nil {
		tb.Fatalf("failed to load arcade_gtk_atoms: %v", err)
	}
	field, ok := collection.Fields.GetByName("type").(*core.SelectField)
	if !ok {
		tb.Fatalf("arcade_gtk_atoms.type must be a select field")
	}
	changed := false
	for _, value := range []string{"SellFood", "SeatingArea"} {
		if containsGTKType(field.Values, value) {
			continue
		}
		field.Values = append(field.Values, value)
		changed = true
	}
	if changed {
		if err := app.Save(collection); err != nil {
			tb.Fatalf("failed to update GTK type catalog: %v", err)
		}
	}
}

func containsGTKType(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// Production Core's bootstrap schema and Backend Full's forward migration own
// these fields. Test fixtures are intentionally older than the current schema,
// so keep isolated handler tests compatible without changing fixture databases.
func ensureFlagResolutionSchema(tb testing.TB, app *tests.TestApp) {
	tb.Helper()
	flags, err := app.FindCollectionByNameOrId("arcade_flag")
	if err != nil {
		tb.Fatalf("failed to load arcade_flag: %v", err)
	}
	reactions, err := app.FindCollectionByNameOrId("arcade_flag_reaction")
	if err != nil {
		tb.Fatalf("failed to load arcade_flag_reaction: %v", err)
	}
	flagChanged := false
	if flags.Fields.GetByName("resolution_vote_state") == nil {
		flags.Fields.Add(&core.SelectField{Name: "resolution_vote_state", Values: []string{"idle", "active"}, MaxSelect: 1})
		flagChanged = true
	}
	if flags.Fields.GetByName("resolution_vote_mode") == nil {
		flags.Fields.Add(&core.SelectField{Name: "resolution_vote_mode", Values: []string{"standard", "stale"}, MaxSelect: 1})
		flagChanged = true
	}
	if flags.Fields.GetByName("resolution_vote_round") == nil {
		flags.Fields.Add(&core.TextField{Name: "resolution_vote_round", Max: 64})
		flagChanged = true
	}
	for _, name := range []string{"resolution_vote_started_at", "resolution_vote_resolve_at"} {
		if flags.Fields.GetByName(name) == nil {
			flags.Fields.Add(&core.DateField{Name: name})
			flagChanged = true
		}
	}
	if flagChanged {
		if err := app.Save(flags); err != nil {
			tb.Fatalf("failed to add flag resolution fields: %v", err)
		}
	}
	reactionChanged := false
	if reactions.Fields.GetByName("resolution_context") == nil {
		reactions.Fields.Add(&core.SelectField{Name: "resolution_context", Values: []string{"legacy", "report", "vote"}, MaxSelect: 1})
		reactionChanged = true
	}
	if reactions.Fields.GetByName("vote_round") == nil {
		reactions.Fields.Add(&core.TextField{Name: "vote_round", Max: 64})
		reactionChanged = true
	}
	if reactions.Fields.GetByName("level_snapshot") == nil {
		min := float64(0)
		reactions.Fields.Add(&core.NumberField{Name: "level_snapshot", OnlyInt: true, Min: &min})
		reactionChanged = true
	}
	if reactionChanged {
		if err := app.Save(reactions); err != nil {
			tb.Fatalf("failed to add reaction resolution fields: %v", err)
		}
	}
}

// Production Full owns the forward migration for this field. Core's isolated
// handler tests add the same optional relation to their cloned fixture.
func ensureNoticeAuthorSchema(tb testing.TB, app *tests.TestApp) {
	tb.Helper()
	notices, err := app.FindCollectionByNameOrId("arcade_notice")
	if err != nil {
		tb.Fatalf("failed to load arcade_notice: %v", err)
	}
	if notices.Fields.GetByName("createdBy") != nil {
		return
	}
	users, err := app.FindCollectionByNameOrId("user")
	if err != nil {
		tb.Fatalf("failed to load user: %v", err)
	}
	notices.Fields.Add(&core.RelationField{Name: "createdBy", CollectionId: users.Id, MaxSelect: 1})
	if err := app.Save(notices); err != nil {
		tb.Fatalf("failed to add arcade_notice.createdBy: %v", err)
	}
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
