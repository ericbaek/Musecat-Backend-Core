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

func TestUpdateArcadeFlagReaction_AddAndSolve(t *testing.T) {
	headers := map[string]string{}
	var flagID string

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/flag/reaction add returns solved state",
		Method:         http.MethodPost,
		URL:            "/arcade/flag/reaction",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"action":"add"`,
			`"reaction":"fixed"`,
			`"solved":true`,
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
			Name:     "Reaction Solve Arcade",
			Address:  "Reaction Solve Street",
			Nickname: []string{"ReactionSolve"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		seedGameAtomForFlag(tb, app, arcadeID)

		flagID = createFlagWithReactions(t, app, arcadeID, user.Id, time.Now().UTC().Add(-40*24*time.Hour), nil)
		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"flag":"%s",
			"reaction":"fixed",
			"action":"add"
		}`, flagID))
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
		if got, _ := payload["solved"].(bool); !got {
			tb.Fatalf("expected solved=true, got %v", payload["solved"])
		}
		if rid, _ := payload["reaction_id"].(string); rid == "" {
			tb.Fatalf("expected reaction_id in response")
		}
		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object in response, got %T", payload["game"])
		}
		gameID, _ := gameObj["id"].(string)
		if gameID == "" {
			tb.Fatalf("expected game id in response")
		}
		items, ok := gameObj["items"].([]any)
		if !ok || len(items) == 0 {
			tb.Fatalf("expected non-empty game items, got %T %#v", gameObj["items"], gameObj["items"])
		}

		flagRec, err := app.FindRecordById("arcade_flag", flagID)
		if err != nil {
			tb.Fatalf("failed to load flag: %v", err)
		}
		if !flagRec.GetBool("solved") {
			tb.Fatalf("expected flag solved=true after add")
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeFlagReaction_PublicArcadeAwardsXP(t *testing.T) {
	headers := map[string]string{}
	var flagID string

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/flag/reaction awards XP for public arcade",
		Method:         http.MethodPost,
		URL:            "/arcade/flag/reaction",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"action":"add"`,
			`"diff_exp":3`,
			`"xp_feedback":{`,
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
			Name:     "Reaction XP Arcade",
			Address:  "Reaction XP Street",
			Nickname: []string{"ReactionXP"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		setArcadeVisibility(tb, app, arcadeID, true, false)
		seedGameAtomForFlag(tb, app, arcadeID)

		flagID = createFlagWithReactions(tb, app, arcadeID, user.Id, time.Now().UTC(), nil)
		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"flag":"%s",
			"reaction":"fixed",
			"action":"add"
		}`, flagID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		feedback, ok := payload["xp_feedback"].(map[string]any)
		if !ok {
			tb.Fatalf("expected xp_feedback object, got %T", payload["xp_feedback"])
		}
		if got := feedback["diff_exp"]; got != float64(3) {
			tb.Fatalf("expected diff_exp=3, got %#v", got)
		}

		reactionID, _ := payload["reaction_id"].(string)
		if reactionID == "" {
			tb.Fatalf("expected reaction_id in response")
		}
		logs, err := app.FindRecordsByFilter("user_level_log", "kind={:kind}", "", 1, 0, map[string]any{
			"kind": "xp:flag-reaction:" + reactionID,
		})
		if err != nil {
			tb.Fatalf("failed to query user_level_log: %v", err)
		}
		if len(logs) != 1 {
			tb.Fatalf("expected one flag reaction XP log, got %d", len(logs))
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeFlagReaction_TaggedUserFixedBypassesRules(t *testing.T) {
	headers := map[string]string{}
	var flagID string

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/flag/reaction add fixed solves immediately for tagged users",
		Method:         http.MethodPost,
		URL:            "/arcade/flag/reaction",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"action":"add"`,
			`"reaction":"fixed"`,
			`"solved":true`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		token, user := createAuthUserWithTags(tb, app, []string{"supporter"})
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Reaction Tagged Bypass Arcade",
			Address:  "Reaction Tagged Bypass Street",
			Nickname: []string{"ReactionTaggedBypass"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		seedGameAtomForFlag(tb, app, arcadeID)

		flagID = createFlagWithReactions(t, app, arcadeID, user.Id, time.Now().UTC(), nil)
		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"flag":"%s",
			"reaction":"fixed",
			"action":"add"
		}`, flagID))
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
		if got, _ := payload["solved"].(bool); !got {
			tb.Fatalf("expected solved=true, got %v", payload["solved"])
		}

		flagRec, err := app.FindRecordById("arcade_flag", flagID)
		if err != nil {
			tb.Fatalf("failed to load flag: %v", err)
		}
		if !flagRec.GetBool("solved") {
			tb.Fatalf("expected flag solved=true after tagged fixed reaction")
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeFlagReaction_TaggedUserWrongBypassesRules(t *testing.T) {
	headers := map[string]string{}
	var flagID string

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/flag/reaction add wrong solves immediately for tagged users",
		Method:         http.MethodPost,
		URL:            "/arcade/flag/reaction",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"action":"add"`,
			`"reaction":"wrong"`,
			`"solved":true`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		token, user := createAuthUserWithTags(tb, app, []string{"supporter"})
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Reaction Tagged Wrong Bypass Arcade",
			Address:  "Reaction Tagged Wrong Bypass Street",
			Nickname: []string{"ReactionTaggedWrongBypass"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		seedGameAtomForFlag(tb, app, arcadeID)

		flagID = createFlagWithReactions(t, app, arcadeID, user.Id, time.Now().UTC(), nil)
		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"flag":"%s",
			"reaction":"wrong",
			"action":"add"
		}`, flagID))
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
		if got, _ := payload["solved"].(bool); !got {
			tb.Fatalf("expected solved=true, got %v", payload["solved"])
		}

		flagRec, err := app.FindRecordById("arcade_flag", flagID)
		if err != nil {
			tb.Fatalf("failed to load flag: %v", err)
		}
		if !flagRec.GetBool("solved") {
			tb.Fatalf("expected flag solved=true after tagged wrong reaction")
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeFlagReaction_AddReturnsGameWhenUnsolved(t *testing.T) {
	headers := map[string]string{}
	var flagID string
	var expectedGameID string

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/flag/reaction add returns game even when unsolved",
		Method:         http.MethodPost,
		URL:            "/arcade/flag/reaction",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"action":"add"`,
			`"reaction":"issue_persist"`,
			`"solved":false`,
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
			Name:     "Reaction Unsolved Arcade",
			Address:  "Reaction Unsolved Street",
			Nickname: []string{"ReactionUnsolved"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		atomID := seedGameAtomForFlag(tb, app, arcadeID)
		atomRec, err := app.FindRecordById("arcade_game_atoms", atomID)
		if err != nil {
			tb.Fatalf("failed to load atom: %v", err)
		}
		expectedGameID = atomRec.GetString("molecule")
		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		arcadeRec.Set("game", "")
		if err := app.Save(arcadeRec); err != nil {
			tb.Fatalf("failed to clear arcade.game: %v", err)
		}

		flagID = createFlagWithReactions(t, app, arcadeID, user.Id, time.Now().UTC(), nil)
		atomRec.Set("flags", []string{flagID})
		if err := app.Save(atomRec); err != nil {
			tb.Fatalf("failed to link flag to atom: %v", err)
		}
		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"flag":"%s",
			"reaction":"issue_persist",
			"action":"add"
		}`, flagID))
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
		if got, _ := payload["solved"].(bool); got {
			tb.Fatalf("expected solved=false, got %v", payload["solved"])
		}
		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected game object in response, got %T", payload["game"])
		}
		actualGameID, _ := gameObj["id"].(string)
		if actualGameID != expectedGameID {
			tb.Fatalf("expected game id %q, got %q", expectedGameID, actualGameID)
		}
		items, ok := gameObj["items"].([]any)
		if !ok || len(items) == 0 {
			tb.Fatalf("expected non-empty game items, got %T %#v", gameObj["items"], gameObj["items"])
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeFlagReaction_DuplicateFixedWrongBlocked(t *testing.T) {
	for _, reaction := range []string{"fixed", "wrong"} {
		reaction := reaction
		t.Run(reaction, func(t *testing.T) {
			headers := map[string]string{}
			var flagID string

			scenario := tests.ApiScenario{
				Name:           "POST /arcade/flag/reaction duplicate " + reaction + " blocked",
				Method:         http.MethodPost,
				URL:            "/arcade/flag/reaction",
				Headers:        headers,
				ExpectedStatus: http.StatusBadRequest,
				ExpectedContent: []string{
					`"error":"reaction update failed"`,
					fmt.Sprintf(`"details":"reaction %s already exists for this user"`, reaction),
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
					Name:     "Reaction Duplicate Arcade",
					Address:  "Reaction Duplicate Street",
					Nickname: []string{"ReactionDup"},
					Location: location{Lat: 37.5665, Lon: 126.978},
				})
				flagID = createFlagWithReactions(tb, app, arcadeID, user.Id, time.Now().UTC(), nil)
				addReaction(tb, app, flagID, user.Id, reaction)

				scenario.Body = strings.NewReader(fmt.Sprintf(`{
					"flag":"%s",
					"reaction":"%s",
					"action":"add"
				}`, flagID, reaction))
			}

			scenario.Test(t)
		})
	}
}

func TestUpdateArcadeFlagReaction_IssuePersistCooldown(t *testing.T) {
	t.Run("blocked within 24h", func(t *testing.T) {
		headers := map[string]string{}
		var flagID string
		var reactionID string

		scenario := tests.ApiScenario{
			Name:           "POST /arcade/flag/reaction blocks issue_persist cooldown",
			Method:         http.MethodPost,
			URL:            "/arcade/flag/reaction",
			Headers:        headers,
			ExpectedStatus: http.StatusBadRequest,
			ExpectedContent: []string{
				`"error":"reaction update failed"`,
				`"details":"issue_persist can be reported again only after 24 hours"`,
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
				Name:     "Issue Persist Cooldown Arcade",
				Address:  "Cooldown Street",
				Nickname: []string{"Cooldown"},
				Location: location{Lat: 37.5665, Lon: 126.978},
			})
			flagID = createFlagWithReactions(tb, app, arcadeID, user.Id, time.Now().UTC(), nil)
			reactionID = addReaction(tb, app, flagID, user.Id, "issue_persist")

			scenario.Body = strings.NewReader(fmt.Sprintf(`{
				"flag":"%s",
				"reaction":"issue_persist",
				"action":"add"
			}`, flagID))
		}

		scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, _ *http.Response) {
			tb.Helper()
			if reactionID == "" {
				tb.Fatalf("expected seeded reaction id")
			}
		}

		scenario.Test(t)
	})

	t.Run("allowed after 24h", func(t *testing.T) {
		headers := map[string]string{}
		var flagID string
		var seededReactionID string

		scenario := tests.ApiScenario{
			Name:           "POST /arcade/flag/reaction allows issue_persist after cooldown",
			Method:         http.MethodPost,
			URL:            "/arcade/flag/reaction",
			Headers:        headers,
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"action":"add"`,
				`"reaction":"issue_persist"`,
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
				Name:     "Issue Persist Cooldown Passed Arcade",
				Address:  "Cooldown Passed Street",
				Nickname: []string{"CooldownPassed"},
				Location: location{Lat: 37.5665, Lon: 126.978},
			})
			seedGameAtomForFlag(tb, app, arcadeID)
			flagID = createFlagWithReactions(tb, app, arcadeID, user.Id, time.Now().UTC(), nil)
			seededReactionID = addReaction(tb, app, flagID, user.Id, "issue_persist")
			setRecordTimestamp(tb, app, "arcade_flag_reaction", seededReactionID, time.Now().UTC().Add(-25*time.Hour))

			scenario.Body = strings.NewReader(fmt.Sprintf(`{
				"flag":"%s",
				"reaction":"issue_persist",
				"action":"add"
			}`, flagID))
		}

		scenario.Test(t)
	})
}

func TestUpdateArcadeFlagReaction_DeleteWindow(t *testing.T) {
	t.Run("delete within 15 minutes succeeds", func(t *testing.T) {
		headers := map[string]string{}
		var flagID string
		var reactionID string

		scenario := tests.ApiScenario{
			Name:           "POST /arcade/flag/reaction delete within 15 minutes",
			Method:         http.MethodPost,
			URL:            "/arcade/flag/reaction",
			Headers:        headers,
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"action":"delete"`,
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
				Name:     "Delete Window Arcade",
				Address:  "Delete Street",
				Nickname: []string{"DeleteWin"},
				Location: location{Lat: 37.5665, Lon: 126.978},
			})
			seedGameAtomForFlag(tb, app, arcadeID)
			flagID = createFlagWithReactions(tb, app, arcadeID, user.Id, time.Now().UTC(), nil)
			reactionID = addReaction(tb, app, flagID, user.Id, "wrong")

			scenario.Body = strings.NewReader(fmt.Sprintf(`{
				"flag":"%s",
				"reaction":"wrong",
				"action":"delete"
			}`, flagID))
		}

		scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, _ *http.Response) {
			tb.Helper()
			if _, err := app.FindRecordById("arcade_flag_reaction", reactionID); err == nil {
				tb.Fatalf("expected reaction %q to be deleted", reactionID)
			}
		}

		scenario.Test(t)
	})

	t.Run("delete after 15 minutes blocked", func(t *testing.T) {
		headers := map[string]string{}
		var flagID string
		var reactionID string

		scenario := tests.ApiScenario{
			Name:           "POST /arcade/flag/reaction delete after 15 minutes blocked",
			Method:         http.MethodPost,
			URL:            "/arcade/flag/reaction",
			Headers:        headers,
			ExpectedStatus: http.StatusBadRequest,
			ExpectedContent: []string{
				`"error":"reaction update failed"`,
				`"details":"reaction can only be deleted within 15 minutes of creation"`,
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
			seedGameAtomForFlag(tb, app, arcadeID)
			flagID = createFlagWithReactions(tb, app, arcadeID, user.Id, time.Now().UTC(), nil)
			reactionID = addReaction(tb, app, flagID, user.Id, "wrong")
			setRecordTimestamp(tb, app, "arcade_flag_reaction", reactionID, time.Now().UTC().Add(-16*time.Minute))

			scenario.Body = strings.NewReader(fmt.Sprintf(`{
				"flag":"%s",
				"reaction":"wrong",
				"action":"delete"
			}`, flagID))
		}

		scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, _ *http.Response) {
			tb.Helper()
			if _, err := app.FindRecordById("arcade_flag_reaction", reactionID); err != nil {
				tb.Fatalf("expected reaction %q to remain after blocked delete: %v", reactionID, err)
			}
		}

		scenario.Test(t)
	})
}
