package arcade_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	arcadeadmin "github.com/ericbaek/musecat-backend-core/handlers/arcade/admin"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

func TestRequestPublicArcade_Success(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string
	var userID string
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
		userID = user.Id
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
		seedArcadeChangelog(tb, app, arcadeID, "basic", user.Id, time.Now().Add(-3*time.Minute))
		seedArcadeChangelog(tb, app, arcadeID, "game", user.Id, time.Now().Add(-2*time.Minute))
		seedArcadeChangelog(tb, app, arcadeID, "hour", user.Id, time.Now().Add(-time.Minute))

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
		feedback, ok := payload["xp_feedback"].(map[string]any)
		if !ok {
			tb.Fatalf("expected xp_feedback object, got %T", payload["xp_feedback"])
		}
		if got := feedback["diff_exp"]; got != float64(11) {
			tb.Fatalf("expected total public conversion XP diff=11, got %#v", got)
		}

		logs, err := app.FindRecordsByFilter("user_level_log", "user={:user}", "created", 0, 0, map[string]any{"user": userID})
		if err != nil {
			tb.Fatalf("failed to load XP ledger: %v", err)
		}
		expectedLogs := map[string]int{
			userhandler.ArcadePublicKind(arcadeID):                  5,
			userhandler.ArcadePublicBackfillKind(arcadeID, "basic"): 2,
			userhandler.ArcadePublicBackfillKind(arcadeID, "game"):  2,
			userhandler.ArcadePublicBackfillKind(arcadeID, "hour"):  2,
		}
		if len(logs) != len(expectedLogs) {
			tb.Fatalf("expected %d XP ledger rows, got %d", len(expectedLogs), len(logs))
		}
		for _, log := range logs {
			kind := log.GetString("kind")
			want, ok := expectedLogs[kind]
			if !ok {
				tb.Fatalf("unexpected XP ledger kind %q", kind)
			}
			if got := log.GetInt("diff_exp"); got != want {
				tb.Fatalf("XP ledger kind %q diff=%d, want %d", kind, got, want)
			}
			delete(expectedLogs, kind)
		}
		if len(expectedLogs) != 0 {
			tb.Fatalf("missing XP ledger kinds: %#v", expectedLogs)
		}
		level, err := app.FindRecordById("user_level", userID)
		if err != nil {
			tb.Fatalf("failed to load user level: %v", err)
		}
		if got := level.GetInt("exp"); got != 11 {
			tb.Fatalf("expected user level exp=11, got %d", got)
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

func TestRequestPublicArcade_SupporterCannotBypassPublishRequirements(t *testing.T) {
	app := newArcadeTestApp(t)
	token, user := createAuthUserWithTags(t, app, []string{"supporter"})
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Supporter Draft",
		Address:  "Supporter Street",
		Location: location{Lat: 37.5665, Lon: 126.978},
	})

	res := executeJSONRequest(t, app, http.MethodPut, "/arcade/public", fmt.Sprintf(`{"arcade":%q,"bypass_requirements":true}`, arcadeID), map[string]string{
		"Authorization": "Bearer " + token,
	})
	if res.StatusCode != http.StatusBadRequest {
		res.Body.Close()
		t.Fatalf("expected supporter bypass status 400, got %d", res.StatusCode)
	}
	res.Body.Close()

	arcade, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		t.Fatalf("failed to load supporter draft: %v", err)
	}
	if arcade.GetBool("public") {
		t.Fatalf("expected supporter-owned draft to remain private")
	}
}

func TestRequestPublicArcade_IgnoresRemovedBypassFlag(t *testing.T) {
	app := newArcadeTestApp(t)
	token, user := createAuthUser(t, app)
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Member Draft",
		Address:  "Member Street",
		Location: location{Lat: 37.5665, Lon: 126.978},
	})

	res := executeJSONRequest(t, app, http.MethodPut, "/arcade/public", fmt.Sprintf(`{"arcade":%q,"bypass_requirements":true}`, arcadeID), map[string]string{
		"Authorization": "Bearer " + token,
	})
	if res.StatusCode != http.StatusBadRequest {
		res.Body.Close()
		t.Fatalf("expected removed bypass flag to follow normal validation, got %d", res.StatusCode)
	}
	res.Body.Close()

	arcade, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		t.Fatalf("failed to load member draft: %v", err)
	}
	if arcade.GetBool("public") {
		t.Fatalf("expected rejected bypass to leave draft private")
	}
}

func TestPreviewPublicArcadeXP(t *testing.T) {
	app := newArcadeTestApp(t)
	token, user := createAuthUser(t, app)
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Preview Arcade",
		Address:  "Preview Street",
		Location: location{Lat: 37.5665, Lon: 126.978},
	})
	seedArcadeChangelog(t, app, arcadeID, "basic", user.Id, time.Now().Add(-3*time.Minute))
	seedArcadeChangelog(t, app, arcadeID, "game", user.Id, time.Now().Add(-2*time.Minute))
	seedArcadeChangelog(t, app, arcadeID, "hour", user.Id, time.Now().Add(-time.Minute))

	res := executeJSONRequest(t, app, http.MethodGet, "/arcade/public?arcade="+arcadeID, "", map[string]string{
		"Authorization": "Bearer " + token,
	})
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		t.Fatalf("expected preview status 200, got %d", res.StatusCode)
	}
	defer res.Body.Close()

	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode preview response: %v", err)
	}
	preview, ok := payload["xp_preview"].(map[string]any)
	if !ok {
		t.Fatalf("expected xp_preview object, got %T", payload["xp_preview"])
	}
	for key, want := range map[string]float64{
		"current_exp":    0,
		"public_exp":     5,
		"backfill_exp":   6,
		"estimated_exp":  11,
		"estimated_gain": 11,
	} {
		if got := preview[key]; got != want {
			t.Fatalf("preview %s=%v, want %v", key, got, want)
		}
	}

	arcade, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		t.Fatalf("failed to load arcade: %v", err)
	}
	if arcade.GetBool("public") {
		t.Fatalf("XP preview must not publish the arcade")
	}
	logs, err := app.FindRecordsByFilter("user_level_log", "user={:user}", "", 0, 0, map[string]any{"user": user.Id})
	if err != nil {
		t.Fatalf("failed to query XP ledger after preview: %v", err)
	}
	if len(logs) != 0 {
		t.Fatalf("XP preview must not write ledger rows, got %d", len(logs))
	}
}

func TestArcadePublicBackfillIgnoresEditCooldownAndDeduplicatesArea(t *testing.T) {
	app := newArcadeTestApp(t)
	token, user := createAuthUser(t, app)
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Backfill Arcade",
		Address:  "Backfill Street",
		Location: location{Lat: 37.5665, Lon: 126.978},
	})
	now := time.Now().UTC()

	// A recent normal game edit grant must not suppress the draft game's
	// one-time public-conversion backfill.
	if err := app.RunInTransaction(func(txApp core.App) error {
		_, granted, err := userhandler.AwardArcadeEditExpTx(txApp, user.Id, arcadeID, "game", 2, 0, now)
		if err != nil {
			return err
		}
		if !granted {
			return fmt.Errorf("expected the normal game edit grant to be awarded")
		}
		return nil
	}); err != nil {
		t.Fatalf("failed to seed recent normal edit XP: %v", err)
	}

	// Two changelog rows in the same area still produce one backfill grant.
	seedArcadeChangelog(t, app, arcadeID, "basic", user.Id, now.Add(-2*time.Minute))
	seedArcadeChangelog(t, app, arcadeID, "basic", user.Id, now.Add(-time.Minute))
	seedArcadeChangelog(t, app, arcadeID, "game", user.Id, now.Add(-30*time.Second))

	res := executeJSONRequest(t, app, http.MethodGet, "/arcade/public?arcade="+arcadeID, "", map[string]string{
		"Authorization": "Bearer " + token,
	})
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		t.Fatalf("expected preview status 200, got %d", res.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		res.Body.Close()
		t.Fatalf("failed to decode preview response: %v", err)
	}
	res.Body.Close()
	preview, ok := payload["xp_preview"].(map[string]any)
	if !ok {
		t.Fatalf("expected xp_preview object, got %T", payload["xp_preview"])
	}
	for key, want := range map[string]float64{
		"current_exp":    2,
		"public_exp":     5,
		"backfill_exp":   4,
		"estimated_exp":  11,
		"estimated_gain": 9,
	} {
		if got := preview[key]; got != want {
			t.Fatalf("preview %s=%v, want %v", key, got, want)
		}
	}

	arcade, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		t.Fatalf("failed to load arcade: %v", err)
	}
	arcade.Set("public", true)
	if err := app.Save(arcade); err != nil {
		t.Fatalf("failed to make arcade public for backfill test: %v", err)
	}

	if err := app.RunInTransaction(func(txApp core.App) error {
		current, err := userhandler.LoadCurrentExp(txApp, user.Id)
		if err != nil {
			return err
		}
		next, err := userhandler.GrantArcadePublicBackfillTx(txApp, user.Id, arcadeID, current)
		if err != nil {
			return err
		}
		if next != 6 {
			return fmt.Errorf("expected current exp 6 after backfill, got %d", next)
		}

		// The backfill must not consume the normal edit cooldown for another area.
		next, granted, err := userhandler.AwardArcadeEditExpTx(txApp, user.Id, arcadeID, "basic", 2, next, now)
		if err != nil {
			return err
		}
		if !granted || next != 8 {
			return fmt.Errorf("expected immediate normal basic edit grant after backfill, granted=%t exp=%d", granted, next)
		}

		// Re-running the backfill is idempotent for both areas.
		next, err = userhandler.GrantArcadePublicBackfillTx(txApp, user.Id, arcadeID, next)
		if err != nil {
			return err
		}
		if next != 8 {
			return fmt.Errorf("expected repeated backfill to leave exp at 8, got %d", next)
		}
		return nil
	}); err != nil {
		t.Fatalf("failed to apply public backfill: %v", err)
	}

	logs, err := app.FindRecordsByFilter("user_level_log", "user={:user}", "created", 0, 0, map[string]any{"user": user.Id})
	if err != nil {
		t.Fatalf("failed to load XP ledger: %v", err)
	}
	if len(logs) != 4 {
		t.Fatalf("expected one normal game, two backfill, and one normal basic log, got %d", len(logs))
	}
	for _, part := range []string{"basic", "game"} {
		count := 0
		for _, log := range logs {
			if log.GetString("kind") == userhandler.ArcadePublicBackfillKind(arcadeID, part) {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("expected exactly one %s backfill log, got %d", part, count)
		}
	}
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
		"at least one facility photo or a location verification must be completed before making arcade public",
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

func TestRequestPublicArcade_LevelTenRequiresOnlyGame(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/public level 10 requires only game",
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
		setUserLevelExp(tb, app, user.Id, userhandler.LevelBaseExp(10))

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
		scenario.Body = strings.NewReader(fmt.Sprintf(`{"arcade":"%s"}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, _ *http.Response) {
		tb.Helper()

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		if !arcadeRec.GetBool("public") {
			tb.Fatalf("expected level-10 arcade.public=true without photo or hours")
		}
	}

	scenario.Test(t)
}

func TestRequestPublicArcade_LevelFiveDoesNotRequireSNSOrHour(t *testing.T) {
	app := newArcadeTestApp(t)
	token, user := createAuthUser(t, app)
	setUserLevelExp(t, app, user.Id, userhandler.LevelBaseExp(5))
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Level Five Arcade",
		Address:  "Level Five Street",
		Location: location{Lat: 37.5665, Lon: 126.978},
	})
	versionID := seedGameSeriesVersion(t, app)
	gameID := seedArcadeGameMolecule(t, app, arcadeID)
	seedArcadeGameAtom(t, app, gameID, versionID, "1F")
	seedPhotoMolecule(t, app, arcadeID, user.Id, []string{seedExistingPhotoAtomID(t, app, arcadeID, user.Id)})

	res := executeJSONRequest(t, app, http.MethodPut, "/arcade/public", fmt.Sprintf(`{"arcade":%q}`, arcadeID), map[string]string{
		"Authorization": "Bearer " + token,
	})
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		t.Fatalf("expected level-5 public conversion status 200, got %d", res.StatusCode)
	}
	res.Body.Close()
}

func TestRequestPublicArcade_LocationVerificationInvalidatedByBasicEdit(t *testing.T) {
	app := newArcadeTestApp(t)
	token, user := createAuthUser(t, app)
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Location Verified Arcade",
		Address:  "Verification Street",
		Location: location{Lat: 37.5665, Lon: 126.978},
	})
	versionID := seedGameSeriesVersion(t, app)
	gameID := seedArcadeGameMolecule(t, app, arcadeID)
	seedArcadeGameAtom(t, app, gameID, versionID, "1F")
	seedHourMolecule(t, app, arcadeID, user.Id, map[string]any{
		"Monday": map[string]int{"start": 1000, "end": 2200},
	})

	res := executeJSONRequest(t, app, http.MethodPost, "/arcade/location-verification", fmt.Sprintf(`{"arcade":%q,"lat":37.5665,"lon":126.978,"accuracy":20}`, arcadeID), map[string]string{
		"Authorization": "Bearer " + token,
	})
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		t.Fatalf("expected location verification status 200, got %d", res.StatusCode)
	}
	res.Body.Close()

	res = executeJSONRequest(t, app, http.MethodPut, "/arcade/basic", fmt.Sprintf(`{"arcade":%q,"name":"Updated Arcade"}`, arcadeID), map[string]string{
		"Authorization": "Bearer " + token,
	})
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		t.Fatalf("expected basic update status 200, got %d", res.StatusCode)
	}
	res.Body.Close()

	res = executeJSONRequest(t, app, http.MethodPut, "/arcade/public", fmt.Sprintf(`{"arcade":%q}`, arcadeID), map[string]string{
		"Authorization": "Bearer " + token,
	})
	if res.StatusCode != http.StatusBadRequest {
		res.Body.Close()
		t.Fatalf("expected invalidated location verification status 400, got %d", res.StatusCode)
	}
	res.Body.Close()
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

func setUserLevelExp(tb testing.TB, app *tests.TestApp, userID string, exp int) {
	tb.Helper()
	coll, err := app.FindCollectionByNameOrId(userhandler.CollectionUserLevel)
	if err != nil {
		tb.Fatalf("failed to load user level collection: %v", err)
	}
	record, err := app.FindRecordById(userhandler.CollectionUserLevel, userID)
	if err != nil {
		record = core.NewRecord(coll)
		record.Set("id", userID)
		record.Set("user", userID)
	}
	record.Set("exp", exp)
	if err := app.Save(record); err != nil {
		tb.Fatalf("failed to save user level: %v", err)
	}
}
