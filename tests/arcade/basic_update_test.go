package arcade_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

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
			"location":{"lat":37.57,"lon":126.99}
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

func TestUpdateArcadeBasic_PrivateLocationChangeUpdatesCountry(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/basic private location change updates country",
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
		if got := arcadeRec.GetString("country"); got != "JP" {
			tb.Fatalf("expected country JP, got %q", got)
		}
		if got := arcadeRec.GetString("timezone"); got != "Asia/Tokyo" {
			tb.Fatalf("expected timezone Asia/Tokyo, got %q", got)
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
		if got := arcadeRec.GetString("timezone"); got != "Asia/Tokyo" {
			tb.Fatalf("expected timezone Asia/Tokyo, got %q", got)
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
