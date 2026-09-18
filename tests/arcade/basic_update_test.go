package arcade_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestUpdateArcadeBasic_EnforcesLocationMovePolicy(t *testing.T) {
	t.Run("level 9 is limited to 1 km", func(t *testing.T) {
		app := newArcadeTestApp(t)
		stubGeoLookup(t)
		token, user := createAuthUser(t, app)
		seedUserLevelExp(t, app, user.Id, userhandler.LevelBaseExp(9))
		arcadeID, basicID := seedArcade(t, app, user.Id, arcadeSeed{
			Name: "Low Level Arcade", Address: "Seoul", Location: location{Lat: 37.5665, Lon: 126.978},
		})

		res := executeJSONRequest(t, app, http.MethodPut, "/arcade/basic", fmt.Sprintf(
			`{"arcade":%q,"location":{"lat":37.58,"lon":126.978}}`, arcadeID,
		), map[string]string{"Authorization": "Bearer " + token})
		defer res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", res.StatusCode)
		}
		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if payload["code"] != "location_move_distance_exceeded" {
			t.Fatalf("expected distance policy code, got %#v", payload)
		}
		arcade, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			t.Fatalf("failed to reload arcade: %v", err)
		}
		if got := arcade.GetString("basic"); got != basicID {
			t.Fatalf("denied movement changed basic pointer to %q; want %q", got, basicID)
		}
	})

	t.Run("level 10 may move beyond 1 km", func(t *testing.T) {
		app := newArcadeTestApp(t)
		stubGeoLookup(t)
		token, user := createAuthUser(t, app)
		seedUserLevelExp(t, app, user.Id, userhandler.LevelBaseExp(10))
		arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{
			Name: "Level 10 Arcade", Address: "Seoul", Location: location{Lat: 37.5665, Lon: 126.978},
		})

		res := executeJSONRequest(t, app, http.MethodPut, "/arcade/basic", fmt.Sprintf(
			`{"arcade":%q,"location":{"lat":37.58,"lon":126.978}}`, arcadeID,
		), map[string]string{"Authorization": "Bearer " + token})
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", res.StatusCode)
		}
	})

	t.Run("level 30 may move far within the same country", func(t *testing.T) {
		app := newArcadeTestApp(t)
		stubGeoLookup(t)
		token, user := createAuthUser(t, app)
		seedUserLevelExp(t, app, user.Id, userhandler.LevelBaseExp(30))
		arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{
			Name: "Level 30 Arcade", Address: "Seoul", Location: location{Lat: 37.5665, Lon: 126.978},
		})

		res := executeJSONRequest(t, app, http.MethodPut, "/arcade/basic", fmt.Sprintf(
			`{"arcade":%q,"location":{"lat":37.75,"lon":126.978}}`, arcadeID,
		), map[string]string{"Authorization": "Bearer " + token})
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", res.StatusCode)
		}
	})

	t.Run("level 30 cannot move to another country", func(t *testing.T) {
		app := newArcadeTestApp(t)
		stubGeoLookupByLocation(t, func(_, _ float64) (string, string) {
			return "JP", "Asia/Tokyo"
		})
		token, user := createAuthUser(t, app)
		seedUserLevelExp(t, app, user.Id, userhandler.LevelBaseExp(30))
		arcadeID, basicID := seedArcade(t, app, user.Id, arcadeSeed{
			Name: "Level 30 Arcade", Address: "Seoul", Location: location{Lat: 37.5665, Lon: 126.978},
		})

		res := executeJSONRequest(t, app, http.MethodPut, "/arcade/basic", fmt.Sprintf(
			`{"arcade":%q,"location":{"lat":37.58,"lon":126.978}}`, arcadeID,
		), map[string]string{"Authorization": "Bearer " + token})
		defer res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", res.StatusCode)
		}
		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if payload["code"] != "location_move_country_changed" {
			t.Fatalf("expected country policy code, got %#v", payload)
		}
		arcade, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			t.Fatalf("failed to reload arcade: %v", err)
		}
		if got := arcade.GetString("basic"); got != basicID {
			t.Fatalf("denied country change changed basic pointer to %q; want %q", got, basicID)
		}
	})
}

func TestUpdateArcadeBasic_PrivateWritesStructuredChangelog(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/basic private writes structured changelog",
		Method:         http.MethodPut,
		URL:            "/arcade/basic",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"basic":"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		stubGeoLookup(tb)

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:       "Old Basic Arcade",
			Address:    "Old Street",
			Direction:  "B1",
			Nickname:   []string{"Old"},
			SubwayLine: []string{"2"},
			Location:   location{Lat: 37.5665, Lon: 126.978},
		})

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"name":"New Basic Arcade",
			"location":{"lat":37.57,"lon":126.98}
		}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		basicID, _ := payload["basic"].(string)
		if basicID == "" {
			arcadeRec, err := app.FindRecordById("arcade", arcadeID)
			if err != nil {
				tb.Fatalf("failed to load arcade: %v", err)
			}
			basicID = arcadeRec.GetString("basic")
		}
		if basicID == "" {
			tb.Fatalf("expected basic id in response")
		}

		basicRec, err := app.FindRecordById("arcade_basic", basicID)
		if err != nil {
			tb.Fatalf("failed to load arcade_basic: %v", err)
		}
		if got := basicRec.GetStringSlice("subway_line"); len(got) != 1 || got[0] != "2" {
			tb.Fatalf("expected subway_line to be preserved, got %#v", got)
		}

		changes := loadChangelogRecords(tb, app, arcadeID, "basic")
		if len(changes) != 1 {
			tb.Fatalf("expected 1 basic changelog row, got %d", len(changes))
		}
		logObj := decodeLogObject(tb, changes[0].Get("log"))
		if got, _ := logObj["type"].(string); got != "basic_diff" {
			tb.Fatalf("expected changelog.log.type=basic_diff, got %v", logObj["type"])
		}
		items, ok := logObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected 1 basic log item, got %T %#v", logObj["items"], logObj["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected basic log item object, got %T", items[0])
		}
		if got, _ := item["change_type"].(string); got != "updated" {
			tb.Fatalf("expected basic change_type=updated, got %v", item["change_type"])
		}
		bullets, ok := item["bullets"].([]any)
		if !ok || len(bullets) == 0 {
			tb.Fatalf("expected basic bullets, got %T %#v", item["bullets"], item["bullets"])
		}
		keys := i18nBulletKeySet(bullets)
		if !keys["arcade.changelog.basic.name.changed"] || !keys["arcade.changelog.basic.location.changed"] {
			tb.Fatalf("expected basic name+location changed bullets, got %#v", keys)
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeBasic_SubwayLineChangeIsApplied(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/basic subway_line change is applied",
		Method:         http.MethodPut,
		URL:            "/arcade/basic",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"basic":"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:       "Subway Arcade",
			Address:    "Line Street",
			Nickname:   []string{"Line"},
			SubwayLine: []string{"2"},
			Location:   location{Lat: 37.5665, Lon: 126.978},
		})

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"subway_line":["3","4"]
		}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		changed, ok := payload["changed"].([]any)
		if !ok {
			tb.Fatalf("expected changed array in response, got %T", payload["changed"])
		}
		if len(changed) != 1 || changed[0] != "subway_line" {
			tb.Fatalf("expected only subway_line changed, got %#v", changed)
		}

		basicID, _ := payload["basic"].(string)
		if basicID == "" {
			arcadeRec, err := app.FindRecordById("arcade", arcadeID)
			if err != nil {
				tb.Fatalf("failed to load arcade: %v", err)
			}
			basicID = arcadeRec.GetString("basic")
		}
		if basicID == "" {
			tb.Fatalf("expected basic id in response")
		}
		basicRec, err := app.FindRecordById("arcade_basic", basicID)
		if err != nil {
			tb.Fatalf("failed to load arcade_basic: %v", err)
		}
		if got := basicRec.GetStringSlice("subway_line"); len(got) != 2 || got[0] != "3" || got[1] != "4" {
			tb.Fatalf("expected subway_line to update, got %#v", got)
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeBasic_PublicWritesStructuredChangelog(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/basic public writes structured changelog",
		Method:         http.MethodPut,
		URL:            "/arcade/basic",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"basic":"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Public Basic Arcade",
			Address:  "Public Street",
			Nickname: []string{"Public"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		arcadeRec.Set("public", true)
		if err := app.Save(arcadeRec); err != nil {
			tb.Fatalf("failed to mark arcade public: %v", err)
		}

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"name":"Public Basic Arcade Updated"
		}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		changes := loadChangelogRecords(tb, app, arcadeID, "basic")
		if len(changes) != 1 {
			tb.Fatalf("expected 1 basic changelog row for public update, got %d", len(changes))
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeBasic_PrivateLocationChangeRejectsCountryChange(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/basic private location change rejects country change",
		Method:         http.MethodPut,
		URL:            "/arcade/basic",
		Headers:        headers,
		ExpectedStatus: http.StatusForbidden,
		ExpectedContent: []string{
			`"code":"location_move_distance_exceeded"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		stubGeoLookupByLocation(tb, func(lat, lon float64) (string, string) {
			if floatAlmostEq(lat, 35.6895) && floatAlmostEq(lon, 139.6917) {
				return "JP", "Asia/Tokyo"
			}
			return "KR", "Asia/Seoul"
		})

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Geo Arcade",
			Address:  "Seoul Street",
			Nickname: []string{"Geo"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"location":{"lat":35.6895,"lon":139.6917}
		}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		if got := arcadeRec.GetString("country"); got != "KR" {
			tb.Fatalf("expected country to remain KR, got %q", got)
		}
		if got := arcadeRec.GetString("timezone"); got != "Asia/Seoul" {
			tb.Fatalf("expected timezone to remain Asia/Seoul, got %q", got)
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeBasic_PrivateLocationChangeUpdatesTimezoneWithinSameCountry(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/basic private location change updates timezone within same country",
		Method:         http.MethodPut,
		URL:            "/arcade/basic",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"basic":"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		stubGeoLookupByLocation(tb, func(lat, lon float64) (string, string) {
			if floatAlmostEq(lat, 35.1796) && floatAlmostEq(lon, 129.0756) {
				return "KR", "Asia/Tokyo"
			}
			return "KR", "Asia/Seoul"
		})

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token
		seedUserLevelExp(tb, app, user.Id, userhandler.LevelBaseExp(30))

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Timezone Arcade",
			Address:  "Seoul Street",
			Nickname: []string{"Timezone"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"location":{"lat":35.1796,"lon":129.0756}
		}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		if got := arcadeRec.GetString("country"); got != "KR" {
			tb.Fatalf("expected country KR, got %q", got)
		}
		if got := arcadeRec.GetString("timezone"); got != "Asia/Seoul" {
			tb.Fatalf("expected timezone Asia/Seoul, got %q", got)
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeBasic_PublicLocationChangeRejectsCountryChange(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/basic public location change rejects country change",
		Method:         http.MethodPut,
		URL:            "/arcade/basic",
		Headers:        headers,
		ExpectedStatus: http.StatusBadRequest,
		ExpectedContent: []string{
			`"country changed for public arcade"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		stubGeoLookupByLocation(tb, func(lat, lon float64) (string, string) {
			if floatAlmostEq(lat, 35.6895) && floatAlmostEq(lon, 139.6917) {
				return "JP", "Asia/Tokyo"
			}
			return "KR", "Asia/Seoul"
		})

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Public Geo Arcade",
			Address:  "Seoul Street",
			Nickname: []string{"PublicGeo"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		arcadeRec.Set("public", true)
		if err := app.Save(arcadeRec); err != nil {
			tb.Fatalf("failed to mark arcade public: %v", err)
		}

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"location":{"lat":35.6895,"lon":139.6917}
		}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		if got := arcadeRec.GetString("country"); got != "KR" {
			tb.Fatalf("expected country to remain KR, got %q", got)
		}
	}

	scenario.Test(t)
}
