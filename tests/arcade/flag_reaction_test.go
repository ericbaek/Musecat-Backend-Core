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

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

func TestUpdateArcadeFlagReaction_StartsResolutionAndReturnsSummary(t *testing.T) {
	app := newArcadeTestApp(t)
	t.Cleanup(app.Cleanup)
	token, user := createAuthUser(t, app)
	setUserLevel(t, app, user.Id, 5)
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{Name: "Resolution Arcade", Address: "Resolution Street", Nickname: []string{"Resolution"}, Location: location{Lat: 37.5665, Lon: 126.978}})
	flagID := createFlagWithReactions(t, app, arcadeID, user.Id, time.Now().UTC(), nil)

	response := postFlagReaction(t, app, token, flagID, "fixed", "add")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.StatusCode)
	}

	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["solved"] != false {
		t.Fatalf("expected unresolved flag, got %#v", payload["solved"])
	}
	resolution, ok := payload["resolution"].(map[string]any)
	if !ok {
		t.Fatalf("expected resolution summary, got %T", payload["resolution"])
	}
	if resolution["state"] != "active" || resolution["score"] != float64(5) || resolution["fixedLevelTotal"] != float64(5) {
		t.Fatalf("unexpected resolution summary: %#v", resolution)
	}
	if resolution["delaySeconds"] != float64(48*60*60) {
		t.Fatalf("expected 48 hour delay, got %#v", resolution["delaySeconds"])
	}
	if resolution["myVote"] != "fixed" {
		t.Fatalf("expected myVote=fixed, got %#v", resolution["myVote"])
	}
}

func TestUpdateArcadeFlagReaction_NeverPostponesAnEarlierDeadline(t *testing.T) {
	app := newArcadeTestApp(t)
	t.Cleanup(app.Cleanup)
	tokenA, userA := createAuthUser(t, app)
	tokenB, userB := createAuthUser(t, app)
	setUserLevel(t, app, userA.Id, 19)
	setUserLevel(t, app, userB.Id, 2)
	arcadeID, _ := seedArcade(t, app, userA.Id, arcadeSeed{Name: "Deadline Arcade", Address: "Deadline Street", Nickname: []string{"Deadline"}, Location: location{Lat: 37.5665, Lon: 126.978}})
	flagID := createFlagWithReactions(t, app, arcadeID, userA.Id, time.Now().UTC(), nil)

	response := postFlagReaction(t, app, tokenA, flagID, "fixed", "add")
	assertStatus(t, response, http.StatusOK)
	response.Body.Close()

	flag, err := app.FindRecordById("arcade_flag", flagID)
	if err != nil {
		t.Fatalf("load flag: %v", err)
	}
	vote, err := app.FindFirstRecordByFilter("arcade_flag_reaction", "flag = {:flag} && createdBy = {:user}", map[string]any{"flag": flagID, "user": userA.Id})
	if err != nil {
		t.Fatalf("load fixed vote: %v", err)
	}
	vote.Set("level_snapshot", 19)
	if err := app.Save(vote); err != nil {
		t.Fatalf("save fixed vote snapshot: %v", err)
	}
	deadlineBeforeVote := time.Now().UTC().Add(time.Hour)
	flag.Set("resolution_vote_resolve_at", deadlineBeforeVote)
	if err := app.Save(flag); err != nil {
		t.Fatalf("save existing deadline: %v", err)
	}

	voteAt := time.Now().UTC()
	response = postFlagReaction(t, app, tokenB, flagID, "fixed", "add")
	assertStatus(t, response, http.StatusOK)
	response.Body.Close()

	flag, err = app.FindRecordById("arcade_flag", flagID)
	if err != nil {
		t.Fatalf("reload flag: %v", err)
	}
	resolveAt := flag.GetDateTime("resolution_vote_resolve_at").Time().UTC()
	if !resolveAt.After(voteAt.Add(59*time.Minute)) || !resolveAt.Before(voteAt.Add(61*time.Minute)) {
		t.Fatalf("expected earlier one-hour deadline to survive score increase, got %s", resolveAt)
	}
}

func TestUpdateArcadeFlagReaction_StillChangesContextAndSwitchesVote(t *testing.T) {
	app := newArcadeTestApp(t)
	t.Cleanup(app.Cleanup)
	tokenA, userA := createAuthUser(t, app)
	tokenB, userB := createAuthUser(t, app)
	setUserLevel(t, app, userA.Id, 5)
	setUserLevel(t, app, userB.Id, 5)
	arcadeID, _ := seedArcade(t, app, userA.Id, arcadeSeed{Name: "Context Arcade", Address: "Context Street", Nickname: []string{"Context"}, Location: location{Lat: 37.5665, Lon: 126.978}})
	flagID := createFlagWithReactions(t, app, arcadeID, userA.Id, time.Now().UTC(), nil)
	flagBefore, err := app.FindRecordById("arcade_flag", flagID)
	if err != nil {
		t.Fatalf("load flag before persistence report: %v", err)
	}
	updatedBefore := flagBefore.GetDateTime("updated").Time().UTC()

	response := postFlagReaction(t, app, tokenA, flagID, "issue_persist", "add")
	assertStatus(t, response, http.StatusOK)
	var reportPayload map[string]any
	decodeResponse(t, response, &reportPayload)
	reports, ok := reportPayload["reportHistory"].([]any)
	if !ok || len(reports) != 2 {
		t.Fatalf("expected initial report and one persistence report, got %#v", reportPayload["reportHistory"])
	}
	if reports[0].(map[string]any)["kind"] != "issue_persist" || reports[1].(map[string]any)["kind"] != "initial" {
		t.Fatalf("expected newest-first report history, got %#v", reports)
	}
	flagAfter, err := app.FindRecordById("arcade_flag", flagID)
	if err != nil {
		t.Fatalf("load flag after persistence report: %v", err)
	}
	if !flagAfter.GetDateTime("updated").Time().UTC().After(updatedBefore) {
		t.Fatalf("expected issue_persist to refresh flag.updated: before=%s after=%s", updatedBefore, flagAfter.GetDateTime("updated").Time().UTC())
	}

	response = postFlagReaction(t, app, tokenA, flagID, "fixed", "add")
	assertStatus(t, response, http.StatusOK)
	decodeResponse(t, response, &reportPayload)
	setUserLevel(t, app, userA.Id, 30)

	response = postFlagReaction(t, app, tokenB, flagID, "wrong", "add")
	assertStatus(t, response, http.StatusOK)
	var votePayload map[string]any
	decodeResponse(t, response, &votePayload)
	resolution := votePayload["resolution"].(map[string]any)
	if resolution["wrongVoterCount"] != float64(1) || resolution["score"] != float64(0) || resolution["state"] != "active" {
		t.Fatalf("expected active zero-score vote, got %#v", resolution)
	}

	response = postFlagReaction(t, app, tokenB, flagID, "fixed", "add")
	assertStatus(t, response, http.StatusOK)
	decodeResponse(t, response, &votePayload)
	resolution = votePayload["resolution"].(map[string]any)
	if resolution["fixedVoterCount"] != float64(2) || resolution["wrongVoterCount"] != float64(0) || resolution["score"] != float64(10) {
		t.Fatalf("expected switched fixed vote, got %#v", resolution)
	}

	response = postFlagReaction(t, app, tokenA, flagID, "issue_persist", "add")
	assertStatus(t, response, http.StatusBadRequest)
	response.Body.Close()
}

func TestUpdateArcadeFlagReaction_DeleteRecalculatesAndAwardsXP(t *testing.T) {
	app := newArcadeTestApp(t)
	t.Cleanup(app.Cleanup)
	token, user := createAuthUser(t, app)
	setUserLevel(t, app, user.Id, 5)
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{Name: "Delete Resolution Arcade", Address: "Delete Resolution Street", Nickname: []string{"DeleteResolution"}, Location: location{Lat: 37.5665, Lon: 126.978}})
	setArcadeVisibility(t, app, arcadeID, true, false)
	flagID := createFlagWithReactions(t, app, arcadeID, user.Id, time.Now().UTC(), nil)

	response := postFlagReaction(t, app, token, flagID, "fixed", "add")
	assertStatus(t, response, http.StatusOK)
	var payload map[string]any
	decodeResponse(t, response, &payload)
	if payload["xp_feedback"].(map[string]any)["diff_exp"] != float64(3) {
		t.Fatalf("expected +3 XP, got %#v", payload["xp_feedback"])
	}

	response = postFlagReaction(t, app, token, flagID, "fixed", "delete")
	assertStatus(t, response, http.StatusOK)
	decodeResponse(t, response, &payload)
	resolution := payload["resolution"].(map[string]any)
	if resolution["state"] != "idle" || resolution["fixedVoterCount"] != float64(0) {
		t.Fatalf("expected idle resolution after delete, got %#v", resolution)
	}
	exp, err := userhandler.LoadCurrentExp(app, user.Id)
	if err != nil {
		t.Fatalf("load user exp after delete: %v", err)
	}
	if exp != userhandler.LevelBaseExp(5) {
		t.Fatalf("expected reaction XP rollback to level-5 base, got %d", exp)
	}
}

func TestUpdateArcadeFlagReaction_NegativeVoteClosesAndRefreshesActivity(t *testing.T) {
	app := newArcadeTestApp(t)
	t.Cleanup(app.Cleanup)
	tokenA, userA := createAuthUser(t, app)
	tokenB, userB := createAuthUser(t, app)
	setUserLevel(t, app, userA.Id, 5)
	setUserLevel(t, app, userB.Id, 30)
	arcadeID, _ := seedArcade(t, app, userA.Id, arcadeSeed{Name: "Negative Resolution Arcade", Address: "Negative Resolution Street", Nickname: []string{"NegativeResolution"}, Location: location{Lat: 37.5665, Lon: 126.978}})
	flagID := createFlagWithReactions(t, app, arcadeID, userA.Id, time.Now().UTC(), nil)

	response := postFlagReaction(t, app, tokenA, flagID, "fixed", "add")
	assertStatus(t, response, http.StatusOK)
	response.Body.Close()
	flag, err := app.FindRecordById("arcade_flag", flagID)
	if err != nil {
		t.Fatalf("load active flag: %v", err)
	}
	updatedBeforeClose := flag.GetDateTime("updated").Time().UTC()

	response = postFlagReaction(t, app, tokenB, flagID, "wrong", "add")
	assertStatus(t, response, http.StatusOK)
	response.Body.Close()

	flag, err = app.FindRecordById("arcade_flag", flagID)
	if err != nil {
		t.Fatalf("reload closed flag: %v", err)
	}
	if flag.GetString("resolution_vote_state") != "idle" || flag.GetBool("solved") {
		t.Fatalf("expected unresolved round to close without solving, got state=%q solved=%v", flag.GetString("resolution_vote_state"), flag.GetBool("solved"))
	}
	if !flag.GetDateTime("updated").Time().UTC().After(updatedBeforeClose) {
		t.Fatalf("expected round closure to refresh flag.updated: before=%s after=%s", updatedBeforeClose, flag.GetDateTime("updated").Time().UTC())
	}
	if !flag.GetDateTime("resolution_vote_resolve_at").Time().UTC().IsZero() {
		t.Fatalf("expected closed round deadline to be cleared, got %s", flag.GetDateTime("resolution_vote_resolve_at").Time().UTC())
	}
}

func TestUpdateArcadeFlagReaction_SameUserCannotVoteWrongBeforeResolution(t *testing.T) {
	app := newArcadeTestApp(t)
	t.Cleanup(app.Cleanup)
	token, user := createAuthUser(t, app)
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{Name: "Wrong Guard Arcade", Address: "Wrong Guard Street", Nickname: []string{"WrongGuard"}, Location: location{Lat: 37.5665, Lon: 126.978}})
	flagID := createFlagWithReactions(t, app, arcadeID, user.Id, time.Now().UTC(), nil)

	response := postFlagReaction(t, app, token, flagID, "wrong", "add")
	assertStatus(t, response, http.StatusBadRequest)
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.Contains(fmt.Sprint(payload["details"]), "only available after") {
		t.Fatalf("unexpected error: %#v", payload)
	}
}

func postFlagReaction(tb testing.TB, app *tests.TestApp, token, flagID, reaction, action string) *http.Response {
	tb.Helper()
	body := fmt.Sprintf(`{"flag":%q,"reaction":%q,"action":%q}`, flagID, reaction, action)
	return executeJSONRequest(tb, app, http.MethodPost, "/arcade/flag/reaction", body, map[string]string{
		"Authorization": "Bearer " + token,
	})
}

func assertStatus(tb testing.TB, response *http.Response, expected int) {
	tb.Helper()
	if response.StatusCode != expected {
		body := make([]byte, 4096)
		_, _ = response.Body.Read(body)
		tb.Fatalf("expected status %d, got %d: %s", expected, response.StatusCode, strings.TrimSpace(string(body)))
	}
}

func decodeResponse(tb testing.TB, response *http.Response, target *map[string]any) {
	tb.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		tb.Fatalf("decode response: %v", err)
	}
}

func setUserLevel(tb testing.TB, app *tests.TestApp, userID string, level int) {
	tb.Helper()
	collection, err := app.FindCollectionByNameOrId(userhandler.CollectionUserLevel)
	if err != nil {
		tb.Fatalf("load user level collection: %v", err)
	}
	record, err := app.FindRecordById(userhandler.CollectionUserLevel, userID)
	if err != nil {
		record = core.NewRecord(collection)
		record.Set("id", userID)
		record.Set("user", userID)
	}
	record.Set("exp", userhandler.LevelBaseExp(level))
	if err := app.Save(record); err != nil {
		tb.Fatalf("save user level: %v", err)
	}
}
