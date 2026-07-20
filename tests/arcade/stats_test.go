package arcade_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/tools/types"
)

func TestGetStats_Default(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /stats returns aggregate counts",
		Method:         http.MethodGet,
		URL:            "/stats",
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade_count":`,
			`"changelog_count":`,
			`"flag_count":`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	var baseArcadeCount int64
	var baseChangelogCount int64
	var baseFlagCount int64

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		baseArcadeCount, baseChangelogCount, baseFlagCount = loadPublicStatsCounts(tb, app)

		_, user := createAuthUser(tb, app)
		arcade1, _ := seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Stats Arcade One",
			Address:  "1 Stats Street",
			Location: location{Lat: 37.5665, Lon: 126.9780},
		})
		arcade2, _ := seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Stats Arcade Two",
			Address:  "2 Stats Street",
			Location: location{Lat: 35.1796, Lon: 129.0756},
		})
		setArcadeVisibility(tb, app, arcade1, true, false)

		now := time.Now().UTC()
		insertStatsRecord(tb, app, "arcade_changelog", map[string]any{
			"arcade":  arcade1,
			"by":      user.Id,
			"changed": "basic",
			"from":    "",
			"to":      "",
		}, now.Add(-3*time.Hour))
		insertStatsRecord(tb, app, "arcade_changelog", map[string]any{
			"arcade":  arcade1,
			"by":      user.Id,
			"changed": "game",
			"from":    "",
			"to":      "",
		}, now.Add(-2*time.Hour))
		insertStatsRecord(tb, app, "arcade_changelog", map[string]any{
			"arcade":  arcade2,
			"by":      user.Id,
			"changed": "photo",
			"from":    "",
			"to":      "",
		}, now.Add(-time.Hour))
		insertStatsRecord(tb, app, "z_legacy_tickets", map[string]any{
			"arcade":    arcade1,
			"createdBy": user.Id,
			"message":   "legacy update",
			"status":    "approved",
			"type":      "legacy",
		}, now.Add(-30*time.Minute))

		flag1 := insertStatsRecord(tb, app, "arcade_flag", map[string]any{
			"arcade":     arcade1,
			"createdBy":  user.Id,
			"disruption": "major",
			"message":    "broken",
			"solved":     false,
		}, now.Add(-90*time.Minute))
		flag2 := insertStatsRecord(tb, app, "arcade_flag", map[string]any{
			"arcade":     arcade2,
			"createdBy":  user.Id,
			"disruption": "minor",
			"message":    "minor issue",
			"solved":     false,
		}, now.Add(-80*time.Minute))

		insertStatsRecord(tb, app, "arcade_flag_reaction", map[string]any{
			"flag":      flag1,
			"createdBy": user.Id,
			"reaction":  "fixed",
		}, now.Add(-70*time.Minute))
		insertStatsRecord(tb, app, "arcade_flag_reaction", map[string]any{
			"flag":      flag1,
			"createdBy": user.Id,
			"reaction":  "wrong",
		}, now.Add(-60*time.Minute))
		insertStatsRecord(tb, app, "arcade_flag_reaction", map[string]any{
			"flag":      flag2,
			"createdBy": user.Id,
			"reaction":  "issue_persist",
		}, now.Add(-50*time.Minute))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		assertInt64(tb, payload["arcade_count"], baseArcadeCount+1)
		assertInt64(tb, payload["changelog_count"], baseChangelogCount+3)
		assertInt64(tb, payload["flag_count"], baseFlagCount+3)
	}

	scenario.Test(t)
}

func loadPublicStatsCounts(tb testing.TB, app *tests.TestApp) (int64, int64, int64) {
	tb.Helper()

	rows, err := app.DB().NewQuery(`
SELECT
	(SELECT COUNT(*) FROM arcade WHERE public = 1) AS arcade_count,
	(
		(
			SELECT COUNT(*)
			FROM arcade_changelog c
			INNER JOIN arcade a ON a.id = c.arcade
			WHERE a.public = 1
		)
		+ (
			SELECT COUNT(*)
			FROM z_legacy_tickets t
			INNER JOIN arcade a ON a.id = t.arcade
			WHERE a.public = 1
		)
	) AS changelog_count,
	(
		(SELECT COUNT(*) FROM arcade_flag f INNER JOIN arcade a ON a.id = f.arcade WHERE a.public = 1)
		+ (
			SELECT COUNT(*)
			FROM arcade_flag_reaction r
			INNER JOIN arcade_flag f ON f.id = r.flag
			INNER JOIN arcade a ON a.id = f.arcade
			WHERE a.public = 1
		)
	) AS flag_count
`).Rows()
	if err != nil {
		tb.Fatalf("failed to load raw stats counts: %v", err)
	}
	defer rows.Close()

	if !rows.Next() {
		tb.Fatalf("failed to load raw stats counts: no rows")
	}

	var arcadeCount int64
	var changelogCount int64
	var flagCount int64
	if err := rows.Scan(&arcadeCount, &changelogCount, &flagCount); err != nil {
		tb.Fatalf("failed to scan raw stats counts: %v", err)
	}

	return arcadeCount, changelogCount, flagCount
}

func insertStatsRecord(tb testing.TB, app *tests.TestApp, collectionName string, fields map[string]any, createdAt time.Time) string {
	tb.Helper()

	when := createdAt.UTC().Format(types.DefaultDateLayout)
	params := dbx.Params{"created": when, "updated": when}

	var query string
	switch collectionName {
	case "arcade_changelog":
		query = `
INSERT INTO arcade_changelog (arcade, "by", changed, created, "from", "to")
VALUES ({:arcade}, {:by}, {:changed}, {:created}, {:from}, {:to})
`
		params["arcade"] = stringField(fields, "arcade")
		params["by"] = stringField(fields, "by")
		params["changed"] = stringField(fields, "changed")
		params["from"] = stringField(fields, "from")
		params["to"] = stringField(fields, "to")
	case "arcade_flag":
		query = `
INSERT INTO arcade_flag (arcade, created, createdBy, disruption, message, solved, updated)
VALUES ({:arcade}, {:created}, {:createdBy}, {:disruption}, {:message}, {:solved}, {:updated})
`
		params["arcade"] = stringField(fields, "arcade")
		params["createdBy"] = stringField(fields, "createdBy")
		params["disruption"] = stringField(fields, "disruption")
		params["message"] = stringField(fields, "message")
		params["solved"] = boolField(fields, "solved")
	case "arcade_flag_reaction":
		query = `
INSERT INTO arcade_flag_reaction (created, createdBy, flag, reaction, updated)
VALUES ({:created}, {:createdBy}, {:flag}, {:reaction}, {:updated})
`
		params["createdBy"] = stringField(fields, "createdBy")
		params["flag"] = stringField(fields, "flag")
		params["reaction"] = stringField(fields, "reaction")
	case "z_legacy_tickets":
		query = `
INSERT INTO z_legacy_tickets (arcade, created, createdBy, data, message, status, type, updated)
VALUES ({:arcade}, {:created}, {:createdBy}, NULL, {:message}, {:status}, {:type}, {:updated})
`
		params["arcade"] = stringField(fields, "arcade")
		params["createdBy"] = stringField(fields, "createdBy")
		params["message"] = stringField(fields, "message")
		params["status"] = stringField(fields, "status")
		params["type"] = stringField(fields, "type")
	default:
		tb.Fatalf("unsupported collection %q", collectionName)
	}

	if _, err := app.NonconcurrentDB().NewQuery(query).Bind(params).Execute(); err != nil {
		tb.Fatalf("failed to insert %s record: %v", collectionName, err)
	}

	if collectionName == "arcade_flag" {
		// We only need the generated id for subsequent reactions, so return it when available.
		records, err := app.FindRecordsByFilter("arcade_flag", "arcade={:arcade} && createdBy={:createdBy} && disruption={:disruption} && message={:message}", "-created", 1, 0, dbx.Params{
			"arcade":     stringField(fields, "arcade"),
			"createdBy":  stringField(fields, "createdBy"),
			"disruption": stringField(fields, "disruption"),
			"message":    stringField(fields, "message"),
		})
		if err != nil {
			tb.Fatalf("failed to reload inserted arcade_flag: %v", err)
		}
		if len(records) == 0 {
			tb.Fatalf("failed to resolve inserted arcade_flag id")
		}
		return records[0].Id
	}

	return ""
}

func assertInt64(tb testing.TB, raw any, want int64) {
	tb.Helper()

	got, ok := raw.(float64)
	if !ok {
		tb.Fatalf("expected number, got %T", raw)
	}
	if int64(got) != want {
		tb.Fatalf("expected %d, got %v", want, raw)
	}
}

func stringField(fields map[string]any, key string) string {
	value, _ := fields[key].(string)
	return value
}

func boolField(fields map[string]any, key string) bool {
	value, _ := fields[key].(bool)
	return value
}
