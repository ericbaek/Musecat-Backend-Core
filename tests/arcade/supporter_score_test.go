package arcade_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/tools/types"

	arcadeadmin "github.com/ericbaek/musecat-backend-core/handlers/arcade/admin"
)

func TestGetSupporterScore_BreakdownAndExclusion(t *testing.T) {
	headers := map[string]string{}
	var publicArcadeID string

	scenario := tests.ApiScenario{
		Name:           "GET /supporter/score returns ledger timeline from user_level_log",
		Method:         http.MethodGet,
		URL:            "/supporter/score",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"total_exp":41`,
			`"qualified":false`,
			`"can_request":false`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		publicArcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Supporter Public Arcade",
			Address:  "Supporter Street",
			Nickname: []string{"Supporter"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		setArcadeVisibilityAndUpdated(tb, app, publicArcadeID, true, false, time.Now().UTC())

		ts := time.Now().Add(-time.Hour)
		seedUserLevelExp(tb, app, user.Id, 41)
		seedSupporterLedgerEntry(tb, app, user.Id, `xp:arcade-public:`+publicArcadeID, 0, 10, ts)
		seedSupporterLedgerEntry(tb, app, user.Id, `xp:arcade-edit:basic:`+publicArcadeID+`:1`, 10, 13, ts.Add(time.Minute))
		seedSupporterLedgerEntry(tb, app, user.Id, `xp:arcade-edit:game:`+publicArcadeID+`:2`, 13, 16, ts.Add(2*time.Minute))
		seedSupporterLedgerEntry(tb, app, user.Id, `xp:arcade-edit:hour:`+publicArcadeID+`:3`, 16, 19, ts.Add(3*time.Minute))
		seedSupporterLedgerEntry(tb, app, user.Id, `xp:arcade-edit:sns:`+publicArcadeID+`:4`, 19, 22, ts.Add(4*time.Minute))
		seedSupporterLedgerEntry(tb, app, user.Id, `xp:arcade-edit:gtk:`+publicArcadeID+`:5`, 22, 25, ts.Add(5*time.Minute))
		seedSupporterLedgerEntry(tb, app, user.Id, `xp:arcade-edit:photo:`+publicArcadeID+`:6`, 25, 28, ts.Add(6*time.Minute))
		seedSupporterLedgerEntry(tb, app, user.Id, `xp:arcade-photo-submission:`+publicArcadeID, 28, 33, ts.Add(7*time.Minute))
		seedSupporterLedgerEntry(tb, app, user.Id, "xp:flag:flag_seed_1", 33, 38, ts.Add(8*time.Minute))
		seedSupporterLedgerEntry(tb, app, user.Id, "xp:flag-reaction:reaction_seed_1", 38, 41, ts.Add(9*time.Minute))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload struct {
			TotalExp   int  `json:"total_exp"`
			Qualified  bool `json:"qualified"`
			CanRequest bool `json:"can_request"`
			Entries    []struct {
				Kind        string         `json:"kind"`
				Source      string         `json:"source"`
				Action      string         `json:"action"`
				Exp         int            `json:"exp"`
				PreviousExp int            `json:"previous_exp"`
				NewExp      int            `json:"new_exp"`
				ArcadeID    string         `json:"arcade_id"`
				ArcadeName  string         `json:"arcade_name"`
				TargetID    string         `json:"target_id"`
				Detail      map[string]any `json:"detail"`
			} `json:"entries"`
		}
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		if payload.TotalExp != 41 {
			tb.Fatalf("expected total_exp=41, got %d", payload.TotalExp)
		}
		if payload.Qualified {
			tb.Fatalf("expected qualified=false")
		}
		if payload.CanRequest {
			tb.Fatalf("expected can_request=false")
		}
		if len(payload.Entries) != 10 {
			tb.Fatalf("expected 10 ledger entries, got %d", len(payload.Entries))
		}
		if got := payload.Entries[0].Kind; got != "xp:flag-reaction:reaction_seed_1" {
			tb.Fatalf("expected latest entry first, got %q", got)
		}
		if got := payload.Entries[len(payload.Entries)-1].Kind; got != `xp:arcade-public:`+publicArcadeID {
			tb.Fatalf("expected oldest entry last, got %q", got)
		}

		typeCounts := map[string]int{}
		for _, item := range payload.Entries {
			typeCounts[item.Kind]++
		}
		expectedKinds := []string{
			`xp:arcade-public:` + publicArcadeID,
			`xp:arcade-edit:basic:` + publicArcadeID + `:1`,
			`xp:arcade-edit:game:` + publicArcadeID + `:2`,
			`xp:arcade-edit:hour:` + publicArcadeID + `:3`,
			`xp:arcade-edit:sns:` + publicArcadeID + `:4`,
			`xp:arcade-edit:gtk:` + publicArcadeID + `:5`,
			`xp:arcade-edit:photo:` + publicArcadeID + `:6`,
			`xp:arcade-photo-submission:` + publicArcadeID,
			"xp:flag:flag_seed_1",
			"xp:flag-reaction:reaction_seed_1",
		}
		for _, kind := range expectedKinds {
			if typeCounts[kind] != 1 {
				tb.Fatalf("expected kind %q once in ledger, got %#v", kind, typeCounts)
			}
		}
		for _, item := range payload.Entries {
			if item.Kind == `xp:arcade-public:`+publicArcadeID && item.ArcadeID != publicArcadeID {
				tb.Fatalf("expected public entry to include arcade id %q, got %q", publicArcadeID, item.ArcadeID)
			}
		}
	}

	scenario.Test(t)
}

func TestCreateSupporterRequest_RejectedBelowThreshold(t *testing.T) {
	headers := map[string]string{}

	scenario := tests.ApiScenario{
		Name:           "POST /supporter/request rejects when score is below threshold",
		Method:         http.MethodPost,
		URL:            "/supporter/request",
		Headers:        headers,
		ExpectedStatus: http.StatusBadRequest,
		ExpectedContent: []string{
			`"error":"exp threshold not met"`,
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

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		if got := fmt.Sprintf("%v", payload["details"]); !strings.Contains(got, "need at least 300 exp") {
			tb.Fatalf("expected threshold error details, got %v", payload["details"])
		}
	}

	scenario.Test(t)
}

func TestCreateSupporterRequest_SendsNotifications(t *testing.T) {
	headers := map[string]string{}
	var sentTelegramMessage string
	var sentDiscordMessage string

	restoreTelegram := arcadeadmin.SetTelegramSenderForTest(func(_ context.Context, message string) error {
		sentTelegramMessage = message
		return nil
	})
	t.Cleanup(restoreTelegram)

	restoreDiscord := arcadeadmin.SetDiscordSenderForTest(func(_ context.Context, message string) error {
		sentDiscordMessage = message
		return nil
	})
	t.Cleanup(restoreDiscord)

	scenario := tests.ApiScenario{
		Name:           "POST /supporter/request creates pending request and sends notifications",
		Method:         http.MethodPost,
		URL:            "/supporter/request",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"status":"pending"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		base := time.Now().Add(-2 * time.Hour)
		totalExp := 0
		for i := 0; i < 30; i++ {
			arcadeID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
				Name:     fmt.Sprintf("Supporter High Score Arcade %d", i+1),
				Address:  fmt.Sprintf("High Score Street %d", i+1),
				Nickname: []string{fmt.Sprintf("High%d", i+1)},
				Location: location{Lat: 37.5665 + float64(i)/1000, Lon: 126.978 + float64(i)/1000},
			})
			setArcadeVisibilityAndUpdated(tb, app, arcadeID, true, false, time.Now().UTC())
			totalExp += 10
			seedSupporterLedgerEntry(tb, app, user.Id, `xp:arcade-public:`+arcadeID, totalExp-10, totalExp, base.Add(time.Duration(i)*time.Minute))
		}
		seedUserLevelExp(tb, app, user.Id, totalExp)

		scenario.Body = strings.NewReader(`{}`)
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		if got := fmt.Sprintf("%v", payload["status"]); got != "pending" {
			tb.Fatalf("expected pending status, got %v", payload["status"])
		}
		scoreObj, ok := payload["exp"].(map[string]any)
		if !ok {
			tb.Fatalf("expected exp payload object, got %T", payload["exp"])
		}
		scoreTotal := fmt.Sprintf("%v", scoreObj["total_exp"])
		if scoreTotal == "" || scoreTotal == "<nil>" {
			tb.Fatalf("expected exp payload total_exp, got %v", scoreObj["total_exp"])
		}
		if got := fmt.Sprintf("%v", scoreObj["qualified"]); got != "true" {
			tb.Fatalf("expected exp payload qualified true, got %v", scoreObj["qualified"])
		}
		if total, ok := scoreObj["total_exp"].(float64); !ok || total < 300 {
			tb.Fatalf("expected exp payload total_exp >= 300, got %v", scoreObj["total_exp"])
		}

		reqID := fmt.Sprintf("%v", payload["id"])
		if reqID == "" || reqID == "<nil>" {
			tb.Fatalf("expected request id in response")
		}

		rec, err := app.FindRecordById("supporter_request", reqID)
		if err != nil {
			tb.Fatalf("failed to load supporter_request record: %v", err)
		}
		if rec.GetString("status") != "pending" {
			tb.Fatalf("expected saved status pending, got %q", rec.GetString("status"))
		}
		if rec.GetInt("score_total") < 300 {
			tb.Fatalf("expected saved score_total >= 300, got %d", rec.GetInt("score_total"))
		}

		if sentTelegramMessage == "" {
			tb.Fatalf("expected telegram notification to be sent")
		}
		if !strings.Contains(sentTelegramMessage, reqID) {
			tb.Fatalf("telegram message should include request id %q, got %q", reqID, sentTelegramMessage)
		}
		if !strings.Contains(sentTelegramMessage, "[supporter_request]") {
			tb.Fatalf("telegram message should include collection tag, got %q", sentTelegramMessage)
		}
		if sentDiscordMessage == "" {
			tb.Fatalf("expected discord notification to be sent")
		}
		if !strings.Contains(sentDiscordMessage, "[supporter_request]") {
			tb.Fatalf("discord message should include collection tag, got %q", sentDiscordMessage)
		}
	}

	scenario.Test(t)
}

func seedUserLevelExp(tb testing.TB, app *tests.TestApp, userID string, exp int) {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("user_level")
	if err != nil {
		tb.Fatalf("failed to load user_level collection: %v", err)
	}

	rec, err := app.FindRecordById("user_level", userID)
	if err != nil {
		rec = core.NewRecord(coll)
		rec.Set("id", userID)
		rec.Set("user", userID)
	}
	rec.Set("exp", exp)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save user_level: %v", err)
	}
}

func seedSupporterLedgerEntry(tb testing.TB, app *tests.TestApp, userID, kind string, previousExp, newExp int, ts time.Time) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("user_level_log")
	if err != nil {
		tb.Fatalf("failed to load user_level_log collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("user", userID)
	rec.Set("kind", kind)
	rec.Set("previous_exp", previousExp)
	rec.Set("new_exp", newExp)
	rec.Set("diff_exp", newExp-previousExp)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save user_level_log: %v", err)
	}

	when := ts.UTC().Format(types.DefaultDateLayout)
	if _, err := app.NonconcurrentDB().
		NewQuery("UPDATE user_level_log SET created={:created} WHERE id={:id}").
		Bind(dbx.Params{"created": when, "id": rec.Id}).
		Execute(); err != nil {
		tb.Fatalf("failed to update user_level_log.created for %s: %v", rec.Id, err)
	}

	return rec.Id
}

func createFlagViaAPI(tb testing.TB, app *tests.TestApp, authHeader, arcadeID, atomID, disruption, message string) string {
	tb.Helper()

	headers := map[string]string{"Authorization": authHeader}
	body := fmt.Sprintf(`{"arcade":"%s","game_atom_id":"%s","disruption":"%s","message":"%s"}`, arcadeID, atomID, disruption, message)
	res := executeJSONRequest(tb, app, http.MethodPost, "/arcade/flag", body, headers)
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		var payload map[string]any
		_ = json.NewDecoder(res.Body).Decode(&payload)
		tb.Fatalf("flag creation failed: status=%d payload=%v", res.StatusCode, payload)
	}

	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		tb.Fatalf("failed to decode flag response: %v", err)
	}
	flagID := fmt.Sprintf("%v", payload["flag"])
	if flagID == "" || flagID == "<nil>" {
		tb.Fatalf("expected flag id in response")
	}
	return flagID
}

func createFlagReactionViaAPI(tb testing.TB, app *tests.TestApp, authHeader, flagID, reaction string) string {
	tb.Helper()

	headers := map[string]string{"Authorization": authHeader}
	body := fmt.Sprintf(`{"flag":"%s","reaction":"%s","action":"add"}`, flagID, reaction)
	res := executeJSONRequest(tb, app, http.MethodPost, "/arcade/flag/reaction", body, headers)
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		var payload map[string]any
		_ = json.NewDecoder(res.Body).Decode(&payload)
		tb.Fatalf("flag reaction failed: status=%d payload=%v", res.StatusCode, payload)
	}

	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		tb.Fatalf("failed to decode reaction response: %v", err)
	}
	reactionID := fmt.Sprintf("%v", payload["reaction_id"])
	if reactionID == "" || reactionID == "<nil>" {
		tb.Fatalf("expected reaction id in response")
	}
	return reactionID
}

func seedSupporterPhotoSubmission(tb testing.TB, app *tests.TestApp, arcadeID, createdBy string) string {
	tb.Helper()

	atomID := seedPhotoAtom(tb, app, arcadeID, createdBy, true)
	return atomID
}
