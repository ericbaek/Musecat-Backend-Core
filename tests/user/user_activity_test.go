package user_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/tools/types"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

func TestGetUserActivity_ByID_Default365MergesCounts(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /user/activity by id returns merged heatmap",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"totals":{"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	var userID string
	now := time.Now().UTC()

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		_, userRec := createAuthUser(tb, app, true)
		userID = userRec.Id
		userRec.Set("username", "activity_user_"+userRec.Id)
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to set username: %v", err)
		}

		seedActivityRecord(tb, app, "arcade_changelog", map[string]any{
			"arcade":  "arcade_1",
			"changed": "basic",
			"by":      userID,
			"from":    "",
			"to":      "",
		}, now.Add(-2*time.Hour))
		seedActivityRecord(tb, app, "arcade_flag", map[string]any{
			"arcade":     "arcade_1",
			"createdBy":  userID,
			"disruption": "major",
			"message":    "broken",
			"solved":     false,
		}, now.Add(-90*time.Minute))
		seedActivityRecord(tb, app, "arcade_flag_reaction", map[string]any{
			"flag":      "flag_1",
			"createdBy": userID,
			"reaction":  "fixed",
		}, now.Add(-75*time.Minute))
		seedActivityRecord(tb, app, "z_legacy_tickets", map[string]any{
			"arcade":    "",
			"createdBy": userID,
			"message":   "legacy",
			"status":    "open",
			"type":      "Game",
		}, now.Add(-26*time.Hour))
		seedActivityRecord(tb, app, "z_legacy_tickets", map[string]any{
			"arcade":    "",
			"createdBy": "",
			"message":   "ignored",
			"status":    "open",
			"type":      "Game",
		}, now.Add(-30*time.Minute))

		scenario.URL = "/user/activity?id=" + userID
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		payload := decodeJSON(tb, res)

		if got := payload["user"]; got == nil {
			tb.Fatalf("expected user payload, got nil")
		}
		if got := payload["range"]; got == nil {
			tb.Fatalf("expected range payload, got nil")
		}
		if got := payload["totals"]; got == nil {
			tb.Fatalf("expected totals payload, got nil")
		}

		rangeObj := nestedMap(tb, payload, "range")
		if got := rangeObj["days"]; got != float64(365) {
			tb.Fatalf("expected range.days 365, got %v", got)
		}
		if got := rangeObj["tz"]; got != "UTC" {
			tb.Fatalf("expected default tz UTC, got %v", got)
		}

		days := nestedSlice(tb, payload, "days")
		if len(days) != 365 {
			tb.Fatalf("expected 365 day buckets, got %d", len(days))
		}

		totals := nestedMap(tb, payload, "totals")
		assertJSONNumber(tb, totals["total_count"], 4)
		assertJSONNumber(tb, totals["changelog_count"], 1)
		assertJSONNumber(tb, totals["flag_count"], 2)
		assertJSONNumber(tb, totals["legacy_ticket_count"], 1)
		assertJSONNumber(tb, totals["max_daily_count"], 3)

		todayKey := now.Format("2006-01-02")
		yesterdayKey := now.Add(-24 * time.Hour).Format("2006-01-02")

		dayByDate := mapDaysByDate(tb, days)
		today := dayByDate[todayKey]
		if today == nil {
			tb.Fatalf("expected today bucket %q", todayKey)
		}
		assertJSONNumber(tb, today["changelog_count"], 1)
		assertJSONNumber(tb, today["flag_count"], 2)
		assertJSONNumber(tb, today["legacy_ticket_count"], 0)
		assertJSONNumber(tb, today["total_count"], 3)
		assertJSONNumber(tb, today["level"], 4)

		yesterday := dayByDate[yesterdayKey]
		if yesterday == nil {
			tb.Fatalf("expected yesterday bucket %q", yesterdayKey)
		}
		assertJSONNumber(tb, yesterday["legacy_ticket_count"], 1)
		assertJSONNumber(tb, yesterday["total_count"], 1)
		assertJSONNumber(tb, yesterday["level"], 2)
	}

	scenario.Test(t)
}

func TestGetUserActivity_ByUsername_WithShortRange(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /user/activity by username respects shorter range",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"totals":{"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	now := time.Now().UTC()
	var username string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		_, userRec := createAuthUser(tb, app, true)
		username = "activity_short_" + userRec.Id
		userRec.Set("username", username)
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to set username: %v", err)
		}

		seedActivityRecord(tb, app, "arcade_changelog", map[string]any{
			"arcade":  "arcade_2",
			"changed": "photo",
			"by":      userRec.Id,
			"from":    "",
			"to":      "",
		}, now.Add(-5*24*time.Hour))
		seedActivityRecord(tb, app, "arcade_changelog", map[string]any{
			"arcade":  "arcade_2",
			"changed": "photo",
			"by":      userRec.Id,
			"from":    "",
			"to":      "",
		}, now.Add(-40*24*time.Hour))

		scenario.URL = "/user/activity?username=" + username + "&days=30"
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		payload := decodeJSON(tb, res)

		rangeObj := nestedMap(tb, payload, "range")
		assertJSONNumber(tb, rangeObj["days"], 30)

		days := nestedSlice(tb, payload, "days")
		if len(days) != 30 {
			tb.Fatalf("expected 30 day buckets, got %d", len(days))
		}

		totals := nestedMap(tb, payload, "totals")
		assertJSONNumber(tb, totals["total_count"], 1)
		assertJSONNumber(tb, totals["changelog_count"], 1)
		assertJSONNumber(tb, totals["flag_count"], 0)
		assertJSONNumber(tb, totals["legacy_ticket_count"], 0)
	}

	scenario.Test(t)
}

func TestGetUserActivity_InvalidDays(t *testing.T) {
	cases := []string{"0", "-1", "366", "abc"}
	for _, rawDays := range cases {
		t.Run(rawDays, func(t *testing.T) {
			scenario := tests.ApiScenario{
				Name:           "GET /user/activity rejects invalid days " + rawDays,
				Method:         http.MethodGet,
				URL:            "/user/activity?id=test&days=" + rawDays,
				ExpectedStatus: http.StatusBadRequest,
				ExpectedContent: []string{
					`"error":"invalid 'days' value; expected integer between 1 and 365"`,
				},
				TestAppFactory: func(tb testing.TB) *tests.TestApp {
					return newUserFetchTestApp(tb)
				},
			}

			scenario.Test(t)
		})
	}
}

func TestGetUserActivity_InvalidTimezone(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /user/activity rejects invalid timezone",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusBadRequest,
		ExpectedContent: []string{
			`"error":"invalid 'tz' value; expected IANA timezone"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		_, userRec := createAuthUser(tb, app, true)
		scenario.URL = "/user/activity?id=" + userRec.Id + "&tz=Not/A_Real_Zone"
	}

	scenario.Test(t)
}

func TestGetUserActivity_NotFound(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /user/activity not found",
		Method:         http.MethodGet,
		URL:            "/user/activity?id=not_exist_user",
		ExpectedStatus: http.StatusNotFound,
		ExpectedContent: []string{
			`"error":"user not found"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	scenario.Test(t)
}

func TestGetUserActivity_TimezoneBucketing(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /user/activity buckets by requested timezone",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"days":[{`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	loc, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Fatalf("failed to load timezone: %v", err)
	}
	localNow := time.Now().In(loc)
	boundary := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc).Add(-24 * time.Hour)
	beforeBoundary := boundary.Add(-30 * time.Minute).UTC()
	afterBoundary := boundary.Add(30 * time.Minute).UTC()
	var userID string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		_, userRec := createAuthUser(tb, app, true)
		userID = userRec.Id
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to save user: %v", err)
		}

		seedActivityRecord(tb, app, "arcade_changelog", map[string]any{
			"arcade":  "arcade_3",
			"changed": "basic",
			"by":      userID,
			"from":    "",
			"to":      "",
		}, beforeBoundary)
		seedActivityRecord(tb, app, "arcade_changelog", map[string]any{
			"arcade":  "arcade_3",
			"changed": "sns",
			"by":      userID,
			"from":    "",
			"to":      "",
		}, afterBoundary)

		scenario.URL = "/user/activity?id=" + userID + "&tz=Asia/Seoul&days=3"
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		payload := decodeJSON(tb, res)
		days := mapDaysByDate(tb, nestedSlice(tb, payload, "days"))

		beforeKey := beforeBoundary.In(loc).Format("2006-01-02")
		afterKey := afterBoundary.In(loc).Format("2006-01-02")
		if beforeKey == afterKey {
			tb.Fatalf("expected different local day buckets, got %q", beforeKey)
		}

		assertJSONNumber(tb, days[beforeKey]["changelog_count"], 1)
		assertJSONNumber(tb, days[afterKey]["changelog_count"], 1)
	}

	scenario.Test(t)
}

func TestGetUserActivity_WithdrawnMaskedUser(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /user/activity masks withdrawn profile",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"withdrawn":true`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newUserFetchTestApp(tb)
		},
	}

	now := time.Now().UTC()
	var userID string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)

		_, userRec := createAuthUser(tb, app, true)
		userID = userRec.Id
		userRec.Set("username", "withdrawn_activity_user")
		userRec.Set("withdrawn", true)
		userRec.Set("withdrawnAt", time.Now().UTC())
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to mark withdrawn: %v", err)
		}

		userInfo := ensureUserInfo(tb, app, userRec.Id)
		userInfo.Set("nickname", "hidden_nick")
		if err := app.Save(userInfo); err != nil {
			tb.Fatalf("failed to save user_info: %v", err)
		}

		seedActivityRecord(tb, app, "arcade_flag", map[string]any{
			"arcade":     "arcade_4",
			"createdBy":  userID,
			"disruption": "minor",
			"message":    "issue",
			"solved":     false,
		}, now.Add(-time.Hour))

		scenario.URL = "/user/activity?id=" + userID
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		payload := decodeJSON(tb, res)

		userObj := nestedMap(tb, payload, "user")
		if got := userObj["username"]; got != userhandler.WithdrawnDisplayName() {
			tb.Fatalf("expected masked username %q, got %v", userhandler.WithdrawnDisplayName(), got)
		}
		if got := userObj["nickname"]; got != userhandler.WithdrawnDisplayName() {
			tb.Fatalf("expected masked nickname %q, got %v", userhandler.WithdrawnDisplayName(), got)
		}

		totals := nestedMap(tb, payload, "totals")
		assertJSONNumber(tb, totals["flag_count"], 1)
		assertJSONNumber(tb, totals["total_count"], 1)
	}

	scenario.Test(t)
}

func seedActivityRecord(tb testing.TB, app *tests.TestApp, collectionName string, fields map[string]any, createdAt time.Time) {
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
}

func stringField(fields map[string]any, key string) string {
	value, _ := fields[key].(string)
	return value
}

func boolField(fields map[string]any, key string) bool {
	value, _ := fields[key].(bool)
	return value
}

func nestedMap(tb testing.TB, payload map[string]any, key string) map[string]any {
	tb.Helper()
	out, ok := payload[key].(map[string]any)
	if !ok {
		tb.Fatalf("expected %s object, got %T", key, payload[key])
	}
	return out
}

func nestedSlice(tb testing.TB, payload map[string]any, key string) []any {
	tb.Helper()
	out, ok := payload[key].([]any)
	if !ok {
		tb.Fatalf("expected %s array, got %T", key, payload[key])
	}
	return out
}

func mapDaysByDate(tb testing.TB, raw []any) map[string]map[string]any {
	tb.Helper()
	out := make(map[string]map[string]any, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			tb.Fatalf("expected day object, got %T", item)
		}
		date, ok := entry["date"].(string)
		if !ok || date == "" {
			tb.Fatalf("expected day.date string, got %v", entry["date"])
		}
		out[date] = entry
	}
	return out
}

func assertJSONNumber(tb testing.TB, raw any, want int) {
	tb.Helper()
	got, ok := raw.(float64)
	if !ok {
		tb.Fatalf("expected float64 JSON number, got %T", raw)
	}
	if int(got) != want {
		tb.Fatalf("expected %d, got %v", want, raw)
	}
}
