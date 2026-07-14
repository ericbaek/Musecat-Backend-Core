package arcade_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestListArcadeGames_FiltersByCountrySeriesAndVersion(t *testing.T) {
	headers := map[string]string{}
	scenario := tests.ApiScenario{
		Name:           "GET /arcade/games filters by country, series, and version",
		Method:         http.MethodGet,
		Headers:        headers,
		ExpectedContent: []string{
			"Filter Arcade",
			"Filter Version",
		},
		ExpectedStatus: http.StatusOK,
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	var arcadeID string
	var seriesID string
	var versionID string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUserWithTags(tb, app, []string{"moderator"})
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:      "Filter Arcade",
			Address:   "Filter Street",
			Location:  location{Lat: 37.5665, Lon: 126.9780},
			Country:   "KR",
			Timezone:  "Asia/Seoul",
			Nickname:  []string{"Filter"},
			SubwayLine: []string{"2"},
		})
		makeArcadePublic(tb, app, arcadeID)

		seriesID = seedGameSeries(tb, app, 7, "Filter Series")
		versionID = seedGameSeriesVersionWithSeries(tb, app, seriesID, "2025-06-01", "Filter Version")

		moleculeID := seedArcadeGameMolecule(tb, app, arcadeID)
		seedArcadeGameAtom(tb, app, moleculeID, versionID, "2F")

		otherArcadeID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Other Arcade",
			Address:  "Other Street",
			Location: location{Lat: 35.1796, Lon: 129.0756},
			Country:  "AU",
			Timezone: "Australia/Sydney",
		})
		makeArcadePublic(tb, app, otherArcadeID)
		otherSeriesID := seedGameSeries(tb, app, 9, "Other Series")
		otherVersionID := seedGameSeriesVersionWithSeries(tb, app, otherSeriesID, "2026-01-01", "Other Version")
		otherMoleculeID := seedArcadeGameMolecule(tb, app, otherArcadeID)
		seedArcadeGameAtom(tb, app, otherMoleculeID, otherVersionID, "B1")

		scenario.URL = fmt.Sprintf(
			"/arcade/games?country=KR&game_series=%s&game_series_version=%s",
			seriesID,
			versionID,
		)
	}

	scenario.AfterTestFunc = func(tb testing.TB, _ *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload []map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		if len(payload) != 1 {
			tb.Fatalf("expected one filtered machine, got %d", len(payload))
		}

		item := payload[0]
		if got := item["location"]; got != "2F" {
			tb.Fatalf("expected cabinet location 2F, got %v", got)
		}

		arcade, ok := item["arcade"].(map[string]any)
		if !ok {
			tb.Fatalf("expected arcade object, got %T", item["arcade"])
		}
		if got := arcade["id"]; got != arcadeID {
			tb.Fatalf("expected arcade id %q, got %v", arcadeID, got)
		}
		if got := arcade["country"]; got != "KR" {
			tb.Fatalf("expected country KR, got %v", got)
		}
		if got := arcade["name"]; got != "Filter Arcade" {
			tb.Fatalf("expected arcade name Filter Arcade, got %v", got)
		}

		version, ok := item["version"].(map[string]any)
		if !ok {
			tb.Fatalf("expected version object, got %T", item["version"])
		}
		if got := version["id"]; got != versionID {
			tb.Fatalf("expected version id %q, got %v", versionID, got)
		}

		series, ok := item["series"].(map[string]any)
		if !ok {
			tb.Fatalf("expected series object, got %T", item["series"])
		}
		if got := series["id"]; got != seriesID {
			tb.Fatalf("expected series id %q, got %v", seriesID, got)
		}

		coords, ok := arcade["location"].(map[string]any)
		if !ok {
			tb.Fatalf("expected arcade.location object, got %T", arcade["location"])
		}
		if !floatAlmostEq(coords["lat"].(float64), 37.5665) || !floatAlmostEq(coords["lon"].(float64), 126.9780) {
			tb.Fatalf("unexpected arcade_location: %#v", coords)
		}
	}

	scenario.Test(t)
}

func TestListArcadeGames_SortsBySeriesAndRelease(t *testing.T) {
	headers := map[string]string{}
	scenario := tests.ApiScenario{
		Name:           "GET /arcade/games sorts by series and release",
		Method:         http.MethodGet,
		URL:            "/arcade/games?country=KR",
		Headers:        headers,
		ExpectedContent: []string{
			"Arcade A",
			"Arcade B",
		},
		ExpectedStatus: http.StatusOK,
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	var firstVersionID string
	var secondVersionID string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUserWithTags(tb, app, []string{"developer"})
		headers["Authorization"] = "Bearer " + token

		arcadeAID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Arcade A",
			Address:  "A Street",
			Location: location{Lat: 37.0, Lon: 127.0},
			Country:  "KR",
		})
		makeArcadePublic(tb, app, arcadeAID)
		arcadeBID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Arcade B",
			Address:  "B Street",
			Location: location{Lat: 37.1, Lon: 127.1},
			Country:  "KR",
		})
		makeArcadePublic(tb, app, arcadeBID)

		series1ID := seedGameSeries(tb, app, 1, "Series One")
		series2ID := seedGameSeries(tb, app, 2, "Series Two")

		firstVersionID = seedGameSeriesVersionWithSeries(tb, app, series2ID, "2026-01-01", "Series Two Latest")
		secondVersionID = seedGameSeriesVersionWithSeries(tb, app, series1ID, "2024-01-01", "Series One Old")

		moleculeA := seedArcadeGameMolecule(tb, app, arcadeAID)
		seedArcadeGameAtom(tb, app, moleculeA, firstVersionID, "B1")

		moleculeB := seedArcadeGameMolecule(tb, app, arcadeBID)
		seedArcadeGameAtom(tb, app, moleculeB, secondVersionID, "1F")
	}

	scenario.AfterTestFunc = func(tb testing.TB, _ *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload []map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		var arcadeAIndex = -1
		var arcadeBIndex = -1
		var arcadeAItem, arcadeBItem map[string]any
		for idx, raw := range payload {
			arcade, ok := raw["arcade"].(map[string]any)
			if !ok {
				continue
			}
			switch arcade["name"] {
			case "Arcade A":
				arcadeAIndex = idx
				arcadeAItem = raw
			case "Arcade B":
				arcadeBIndex = idx
				arcadeBItem = raw
			}
		}

		if arcadeAIndex < 0 || arcadeBIndex < 0 {
			tb.Fatalf("expected both custom arcades to be present, got A=%d B=%d", arcadeAIndex, arcadeBIndex)
		}
		if arcadeBIndex > arcadeAIndex {
			tb.Fatalf("expected lower series number first, got Arcade A at %d and Arcade B at %d", arcadeAIndex, arcadeBIndex)
		}

		versionA := arcadeAItem["version"].(map[string]any)["id"]
		versionB := arcadeBItem["version"].(map[string]any)["id"]
		if versionA != firstVersionID {
			tb.Fatalf("expected Arcade A to use series2 version, got %v", versionA)
		}
		if versionB != secondVersionID {
			tb.Fatalf("expected Arcade B to use series1 version, got %v", versionB)
		}
	}

	scenario.Test(t)
}

func TestListArcadeGames_RejectsNonModerator(t *testing.T) {
	headers := map[string]string{}
	scenario := tests.ApiScenario{
		Name:           "GET /arcade/games rejects users without moderator tags",
		Method:         http.MethodGet,
		URL:            "/arcade/games",
		Headers:        headers,
		ExpectedStatus: http.StatusForbidden,
		ExpectedContent: []string{
			`"error":"moderator access required"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, _ := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token
	}

	scenario.Test(t)
}

func makeArcadePublic(tb testing.TB, app *tests.TestApp, arcadeID string) {
	tb.Helper()

	rec, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		tb.Fatalf("failed to load arcade %s: %v", arcadeID, err)
	}
	rec.Set("public", true)
	rec.Set("closed", false)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to make arcade public: %v", err)
	}
}
