package arcade_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	arcadeadmin "github.com/ericbaek/musecat-backend-core/handlers/arcade/admin"
)

func TestRequestPublicArcade_Success(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string
	var sentMessage string
	var sentDiscordMessage string

	restore := arcadeadmin.SetTelegramSenderForTest(func(_ context.Context, message string) error {
		sentMessage = message
		return nil
	})
	t.Cleanup(restore)
	restoreDiscord := arcadeadmin.SetDiscordSenderForTest(func(_ context.Context, message string) error {
		sentDiscordMessage = message
		return nil
	})
	t.Cleanup(restoreDiscord)

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/public publishes arcade when requirements are met",
		Method:         http.MethodPut,
		URL:            "/arcade/public",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"public":true`,
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
			Name:     "Private Arcade",
			Address:  "Private Street",
			Nickname: []string{"Private"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		versionID := seedGameSeriesVersion(tb, app)
		gameID := seedArcadeGameMolecule(tb, app, arcadeID)
		seedArcadeGameAtom(tb, app, gameID, versionID, "1F")
		seedHourMolecule(tb, app, arcadeID, user.Id, map[string]any{
			"Monday": map[string]int{"start": 1000, "end": 2200},
		})
		seedPhotoMolecule(tb, app, arcadeID, user.Id, []string{seedExistingPhotoAtomID(tb, app, arcadeID, user.Id)})

		scenario.Body = strings.NewReader(fmt.Sprintf(`{"arcade":"%s"}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		if got := payload["arcade"]; got != arcadeID {
			tb.Fatalf("expected arcade=%q, got %v", arcadeID, got)
		}
		if got := payload["public"]; got != true {
			tb.Fatalf("expected public=true, got %v", got)
		}

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		if !arcadeRec.GetBool("public") {
			tb.Fatalf("expected arcade.public=true")
		}
		if sentMessage == "" {
			tb.Fatalf("expected telegram message to be sent")
		}
		if !strings.Contains(sentMessage, "[arcade_public]") {
			tb.Fatalf("telegram message should include collection tag, got %q", sentMessage)
		}
		if !strings.Contains(sentMessage, arcadeID) {
			tb.Fatalf("telegram message should include arcade id %q, got %q", arcadeID, sentMessage)
		}
		if !strings.Contains(sentMessage, "Private Arcade") {
			tb.Fatalf("telegram message should include arcade name %q, got %q", "Private Arcade", sentMessage)
		}
		if sentDiscordMessage == "" {
			tb.Fatalf("expected discord message to be sent")
		}
		if !strings.Contains(sentDiscordMessage, "[arcade_public]") {
			tb.Fatalf("discord message should include collection tag, got %q", sentDiscordMessage)
		}
		if !strings.Contains(sentDiscordMessage, arcadeID) {
			tb.Fatalf("discord message should include arcade id %q, got %q", arcadeID, sentDiscordMessage)
		}
	}

	scenario.Test(t)
}

func TestRequestPublicArcade_RequiresGame(t *testing.T) {
	testRequestPublicArcadeRequirement(
		t,
		"missing game",
		"at least one game must be registered before making arcade public",
		func(tb testing.TB, app *tests.TestApp, arcadeID, userID string) {
			tb.Helper()

			seedHourMolecule(tb, app, arcadeID, userID, map[string]any{
				"Monday": map[string]int{"start": 1000, "end": 2200},
			})
			seedPhotoMolecule(tb, app, arcadeID, userID, []string{seedExistingPhotoAtomID(tb, app, arcadeID, userID)})
		},
	)
}

func TestRequestPublicArcade_RequiresSNSOrHour(t *testing.T) {
	testRequestPublicArcadeRequirement(
		t,
		"missing sns and hour",
		"either sns or hour must be registered before making arcade public",
		func(tb testing.TB, app *tests.TestApp, arcadeID, userID string) {
			tb.Helper()

			versionID := seedGameSeriesVersion(tb, app)
			gameID := seedArcadeGameMolecule(tb, app, arcadeID)
			seedArcadeGameAtom(tb, app, gameID, versionID, "1F")
			seedPhotoMolecule(tb, app, arcadeID, userID, []string{seedExistingPhotoAtomID(tb, app, arcadeID, userID)})
		},
	)
}

func TestRequestPublicArcade_RequiresPhoto(t *testing.T) {
	testRequestPublicArcadeRequirement(
		t,
		"missing photo",
		"at least one facility photo must be registered before making arcade public",
		func(tb testing.TB, app *tests.TestApp, arcadeID, userID string) {
			tb.Helper()

			versionID := seedGameSeriesVersion(tb, app)
			gameID := seedArcadeGameMolecule(tb, app, arcadeID)
			seedArcadeGameAtom(tb, app, gameID, versionID, "1F")
			seedHourMolecule(tb, app, arcadeID, userID, map[string]any{
				"Monday": map[string]int{"start": 1000, "end": 2200},
			})
		},
	)
}

func TestRequestPublicArcade_DoesNotRequirePhotoOutsideKR(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/public allows missing photo outside KR",
		Method:         http.MethodPut,
		URL:            "/arcade/public",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"public":true`,
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
			Name:     "Global Arcade",
			Address:  "Global Street",
			Nickname: []string{"Global"},
			Location: location{Lat: 35.6895, Lon: 139.6917},
			Country:  "JP",
			Timezone: "Asia/Tokyo",
		})

		versionID := seedGameSeriesVersion(tb, app)
		gameID := seedArcadeGameMolecule(tb, app, arcadeID)
		seedArcadeGameAtom(tb, app, gameID, versionID, "1F")
		seedHourMolecule(tb, app, arcadeID, user.Id, map[string]any{
			"Monday": map[string]int{"start": 1000, "end": 2200},
		})

		scenario.Body = strings.NewReader(fmt.Sprintf(`{"arcade":"%s"}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, _ *http.Response) {
		tb.Helper()

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		if !arcadeRec.GetBool("public") {
			tb.Fatalf("expected arcade.public=true for non-KR without photo")
		}
	}

	scenario.Test(t)
}

func TestRequestPublicArcade_UsesStoredGeoWithoutLookup(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string
	geoCalls := 0

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/public uses stored geo without lookup",
		Method:         http.MethodPut,
		URL:            "/arcade/public",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"public":true`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		stubGeoLookupWithResolver(tb, func(_ *http.Request) (string, string, error) {
			geoCalls++
			return "", "", fmt.Errorf("publication must not call geo lookup")
		})

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Private Geo Arcade",
			Address:  "Geo Street",
			Nickname: []string{"Geo"},
			Location: location{Lat: 35.6895, Lon: 139.6917},
		})

		versionID := seedGameSeriesVersion(tb, app)
		gameID := seedArcadeGameMolecule(tb, app, arcadeID)
		seedArcadeGameAtom(tb, app, gameID, versionID, "1F")
		seedHourMolecule(tb, app, arcadeID, user.Id, map[string]any{
			"Monday": map[string]int{"start": 1000, "end": 2200},
		})
		seedPhotoMolecule(tb, app, arcadeID, user.Id, []string{seedExistingPhotoAtomID(tb, app, arcadeID, user.Id)})

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		arcadeRec.Set("country", "KR")
		arcadeRec.Set("timezone", "Asia/Seoul")
		if err := app.Save(arcadeRec); err != nil {
			tb.Fatalf("failed to force mismatched country: %v", err)
		}

		scenario.Body = strings.NewReader(fmt.Sprintf(`{"arcade":"%s"}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		if geoCalls != 0 {
			tb.Fatalf("expected no geo lookup during publication, got %d calls", geoCalls)
		}
		if got := arcadeRec.GetString("country"); got != "KR" {
			tb.Fatalf("expected stored country KR after public request, got %q", got)
		}
		if got := arcadeRec.GetString("timezone"); got != "Asia/Seoul" {
			tb.Fatalf("expected stored timezone Asia/Seoul after public request, got %q", got)
		}
		if !arcadeRec.GetBool("public") {
			tb.Fatalf("expected arcade.public=true")
		}
	}

	scenario.Test(t)
}

func testRequestPublicArcadeRequirement(
	t *testing.T,
	name string,
	expectedDetail string,
	setup func(tb testing.TB, app *tests.TestApp, arcadeID, userID string),
) {
	t.Helper()

	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/public " + name,
		Method:         http.MethodPut,
		URL:            "/arcade/public",
		Headers:        headers,
		ExpectedStatus: http.StatusBadRequest,
		ExpectedContent: []string{
			`"error":"validation failed"`,
			fmt.Sprintf(`"details":"%s"`, expectedDetail),
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
			Name:     "Requirement Arcade",
			Address:  "Requirement Street",
			Nickname: []string{"Requirement"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		setup(tb, app, arcadeID, user.Id)

		scenario.Body = strings.NewReader(fmt.Sprintf(`{"arcade":"%s"}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, _ *http.Response) {
		tb.Helper()

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		if arcadeRec.GetBool("public") {
			tb.Fatalf("expected arcade.public=false when validation fails")
		}
	}

	scenario.Test(t)
}

func seedExistingPhotoAtomID(tb testing.TB, app *tests.TestApp, arcadeID, createdBy string) string {
	tb.Helper()
	return seedPhotoAtom(tb, app, arcadeID, createdBy, true)
}
