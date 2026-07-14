package arcade_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestDeleteArcadeFlag_Success(t *testing.T) {
	headers := map[string]string{}
	var flagID string
	var atomID string

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/flag/delete success within 15m",
		Method:         http.MethodPost,
		URL:            "/arcade/flag/delete",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"deleted":true`,
			`"game":{"id":"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Flag Delete Arcade",
			Address:  "Flag Delete Street",
			Nickname: []string{"FlagDelete"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		atomID = seedGameAtomForFlag(tb, app, arcadeID)
		flagID = createFlagWithReactions(tb, app, arcadeID, user.Id, time.Now().UTC(), nil)

		atomRec, err := app.FindRecordById("arcade_game_atoms", atomID)
		if err != nil {
			tb.Fatalf("failed to load atom: %v", err)
		}
		atomRec.Set("flags", []string{flagID})
		if err := app.Save(atomRec); err != nil {
			tb.Fatalf("failed to set atom flags: %v", err)
		}

		scenario.Body = strings.NewReader(fmt.Sprintf(`{"flag":"%s"}`, flagID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		if got, _ := payload["flag"].(string); got != flagID {
			tb.Fatalf("expected flag %q, got %v", flagID, payload["flag"])
		}
		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object in response, got %T", payload["game"])
		}
		gameID, _ := gameObj["id"].(string)
		if gameID == "" {
			tb.Fatalf("expected game id in response")
		}

		if _, err := app.FindRecordById("arcade_flag", flagID); err == nil {
			tb.Fatalf("expected flag %q to be deleted", flagID)
		}

		atomRec, err := app.FindRecordById("arcade_game_atoms", atomID)
		if err != nil {
			tb.Fatalf("failed to reload atom: %v", err)
		}
		if flags := atomRec.GetStringSlice("flags"); len(flags) != 0 {
			tb.Fatalf("expected atom flags empty after delete, got %#v", flags)
		}
	}

	scenario.Test(t)
}

func TestDeleteArcadeFlag_PermissionAndWindow(t *testing.T) {
	t.Run("non creator blocked", func(t *testing.T) {
		headers := map[string]string{}
		var flagID string

		scenario := tests.ApiScenario{
			Name:           "POST /arcade/flag/delete blocks non-creator",
			Method:         http.MethodPost,
			URL:            "/arcade/flag/delete",
			Headers:        headers,
			ExpectedStatus: http.StatusBadRequest,
			ExpectedContent: []string{
				`"error":"flag delete failed"`,
				`"details":"only the flag creator can delete this flag"`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				return newArcadeTestApp(tb)
			},
		}

		scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
			tb.Helper()
			_, owner := createAuthUser(tb, app)
			token, _ := createAuthUser(tb, app)
			headers["Authorization"] = "Bearer " + token

			arcadeID, _ := seedArcade(tb, app, owner.Id, arcadeSeed{
				Name:     "Permission Arcade",
				Address:  "Permission Street",
				Nickname: []string{"Permission"},
				Location: location{Lat: 37.5665, Lon: 126.978},
			})
			flagID = createFlagWithReactions(tb, app, arcadeID, owner.Id, time.Now().UTC(), nil)
			scenario.Body = strings.NewReader(fmt.Sprintf(`{"flag":"%s"}`, flagID))
		}

		scenario.Test(t)
	})

	t.Run("after 15 minutes blocked", func(t *testing.T) {
		headers := map[string]string{}
		var flagID string

		scenario := tests.ApiScenario{
			Name:           "POST /arcade/flag/delete blocks after 15m",
			Method:         http.MethodPost,
			URL:            "/arcade/flag/delete",
			Headers:        headers,
			ExpectedStatus: http.StatusBadRequest,
			ExpectedContent: []string{
				`"error":"flag delete failed"`,
				`"details":"flag can only be deleted within 15 minutes of creation"`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				return newArcadeTestApp(tb)
			},
		}

		scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
			tb.Helper()
			token, user := createAuthUser(tb, app)
			headers["Authorization"] = "Bearer " + token

			arcadeID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
				Name:     "Delete Expired Arcade",
				Address:  "Delete Expired Street",
				Nickname: []string{"DeleteExpired"},
				Location: location{Lat: 37.5665, Lon: 126.978},
			})
			flagID = createFlagWithReactions(tb, app, arcadeID, user.Id, time.Now().UTC(), nil)
			setRecordTimestamp(tb, app, "arcade_flag", flagID, time.Now().UTC().Add(-16*time.Minute))

			scenario.Body = strings.NewReader(fmt.Sprintf(`{"flag":"%s"}`, flagID))
		}

		scenario.Test(t)
	})
}

func TestDeleteArcadeFlag_BlocksWithdrawnUser(t *testing.T) {
	headers := map[string]string{}

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/flag/delete returns 403 for withdrawn users",
		Method:         http.MethodPost,
		URL:            "/arcade/flag/delete",
		Headers:        headers,
		Body:           strings.NewReader(`{"flag":"x"}`),
		ExpectedStatus: http.StatusForbidden,
		ExpectedContent: []string{
			`"code":"ACCOUNT_WITHDRAWN"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newWithdrawTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)
		_, userRec := createAuthUser(tb, app)
		userRec.Set("withdrawn", true)
		userRec.Set("withdrawnAt", time.Now().UTC())
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to mark user withdrawn: %v", err)
		}

		token, err := userRec.NewAuthToken()
		if err != nil {
			tb.Fatalf("failed to create token: %v", err)
		}
		headers["Authorization"] = "Bearer " + token
	}

	scenario.Test(t)
}
