package arcade_test

import (
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/tools/types"

	arcadeflag "github.com/ericbaek/musecat-backend-core/handlers/arcade/flag"
)

type reactionSeed struct {
	reaction  string
	createdAt time.Time
}

func TestArcadeFlagAutoSolve_RunAutoSolve(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUser(t, app)
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Auto Solve Arcade",
		Address:  "Auto Solve Street",
		Nickname: []string{"AutoSolve"},
		Location: location{Lat: 37.5665, Lon: 126.978},
	})

	now := time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)
	daysAgo := func(days int) time.Time {
		return now.Add(-time.Duration(days) * 24 * time.Hour)
	}

	ids := map[string]string{
		"lt7_fixed3": createFlagWithReactions(t, app, arcadeID, user.Id, daysAgo(2), []reactionSeed{
			{reaction: "fixed", createdAt: now.Add(-36 * time.Hour)},
			{reaction: "fixed", createdAt: now.Add(-24 * time.Hour)},
			{reaction: "fixed", createdAt: now.Add(-12 * time.Hour)},
		}),
		"between7and30_fixed2_after_issue_persist": createFlagWithReactions(t, app, arcadeID, user.Id, daysAgo(20), []reactionSeed{
			{reaction: "fixed", createdAt: daysAgo(19)},
			{reaction: "issue_persist", createdAt: daysAgo(10)},
			{reaction: "fixed", createdAt: daysAgo(9)},
			{reaction: "fixed", createdAt: daysAgo(8)},
		}),
		"gt30_fixed1": createFlagWithReactions(t, app, arcadeID, user.Id, daysAgo(40), []reactionSeed{
			{reaction: "fixed", createdAt: daysAgo(35)},
		}),
		"wrong2_anytime": createFlagWithReactions(t, app, arcadeID, user.Id, daysAgo(1), []reactionSeed{
			{reaction: "wrong", createdAt: now.Add(-20 * time.Hour)},
			{reaction: "wrong", createdAt: now.Add(-10 * time.Hour)},
		}),
		"stale_no_reaction_90d": createFlagWithReactions(t, app, arcadeID, user.Id, daysAgo(91), nil),
		"stale_last_reaction_90d": createFlagWithReactions(t, app, arcadeID, user.Id, daysAgo(200), []reactionSeed{
			{reaction: "issue_persist", createdAt: daysAgo(150)},
		}),
		"lt7_fixed2_only": createFlagWithReactions(t, app, arcadeID, user.Id, daysAgo(3), []reactionSeed{
			{reaction: "fixed", createdAt: daysAgo(2)},
			{reaction: "fixed", createdAt: daysAgo(1)},
		}),
		"between7and30_fixed1_after_issue_persist": createFlagWithReactions(t, app, arcadeID, user.Id, daysAgo(20), []reactionSeed{
			{reaction: "issue_persist", createdAt: daysAgo(9)},
			{reaction: "fixed", createdAt: daysAgo(8)},
		}),
		"gt30_no_fixed": createFlagWithReactions(t, app, arcadeID, user.Id, daysAgo(40), []reactionSeed{
			{reaction: "wrong", createdAt: daysAgo(2)},
		}),
		"recent_no_reaction": createFlagWithReactions(t, app, arcadeID, user.Id, daysAgo(10), nil),
	}

	solved, err := arcadeflag.RunAutoSolve(app, now)
	if err != nil {
		t.Fatalf("RunAutoSolve failed: %v", err)
	}
	if solved < 6 {
		t.Fatalf("expected solved count >= 6, got %d", solved)
	}

	expectedSolved := map[string]bool{
		"lt7_fixed3": true,
		"between7and30_fixed2_after_issue_persist": true,
		"gt30_fixed1":                              true,
		"wrong2_anytime":                           true,
		"stale_no_reaction_90d":                    true,
		"stale_last_reaction_90d":                  true,
		"lt7_fixed2_only":                          false,
		"between7and30_fixed1_after_issue_persist": false,
		"gt30_no_fixed":                            false,
		"recent_no_reaction":                       false,
	}

	for name, id := range ids {
		flagRec, err := app.FindRecordById("arcade_flag", id)
		if err != nil {
			t.Fatalf("failed to load %s flag: %v", name, err)
		}

		if got := flagRec.GetBool("solved"); got != expectedSolved[name] {
			t.Fatalf("%s solved mismatch: expected %v, got %v", name, expectedSolved[name], got)
		}
	}
}

func TestArcadeFlagAutoSolve_RegisterAutoSolveCron(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUser(t, app)
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Auto Solve Cron Arcade",
		Address:  "Cron Street",
		Nickname: []string{"AutoSolveCron"},
		Location: location{Lat: 37.5665, Lon: 126.978},
	})

	flagID := createFlagWithReactions(t, app, arcadeID, user.Id, time.Now().UTC().Add(-120*24*time.Hour), nil)
	arcadeflag.RegisterAutoSolveCron(app)

	var matched bool
	for _, job := range app.Cron().Jobs() {
		if job.Id() != arcadeflag.AutoSolveCronJobID {
			continue
		}

		matched = true
		if job.Expression() != arcadeflag.AutoSolveCronExprUTC {
			t.Fatalf("expected cron expr %q, got %q", arcadeflag.AutoSolveCronExprUTC, job.Expression())
		}

		job.Run()
		break
	}

	if !matched {
		t.Fatalf("expected cron job %q to be registered", arcadeflag.AutoSolveCronJobID)
	}

	flagRec, err := app.FindRecordById("arcade_flag", flagID)
	if err != nil {
		t.Fatalf("failed to load flag after cron run: %v", err)
	}
	if !flagRec.GetBool("solved") {
		t.Fatalf("expected flag to be solved by cron run")
	}
}

func TestArcadeFlagAutoSolve_ReactionCreateHook(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUser(t, app)
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Reaction Hook Arcade",
		Address:  "Reaction Hook Street",
		Nickname: []string{"ReactionHook"},
		Location: location{Lat: 37.5665, Lon: 126.978},
	})

	targetFlagID := createFlagWithReactions(t, app, arcadeID, user.Id, time.Now().UTC().Add(-24*time.Hour), nil)
	otherFlagID := createFlagWithReactions(t, app, arcadeID, user.Id, time.Now().UTC().Add(-24*time.Hour), nil)

	arcadeflag.RegisterAutoSolveReactionCreateHook(app)

	addReaction(t, app, targetFlagID, user.Id, "fixed")
	addReaction(t, app, targetFlagID, user.Id, "fixed")

	targetFlag, err := app.FindRecordById("arcade_flag", targetFlagID)
	if err != nil {
		t.Fatalf("failed to load target flag after 2 reactions: %v", err)
	}
	if targetFlag.GetBool("solved") {
		t.Fatalf("expected target flag to remain unsolved with 2 fixed reactions (<7d)")
	}

	addReaction(t, app, targetFlagID, user.Id, "fixed")

	targetFlag, err = app.FindRecordById("arcade_flag", targetFlagID)
	if err != nil {
		t.Fatalf("failed to load target flag after 3 reactions: %v", err)
	}
	if !targetFlag.GetBool("solved") {
		t.Fatalf("expected target flag to be auto-solved on reaction create")
	}

	otherFlag, err := app.FindRecordById("arcade_flag", otherFlagID)
	if err != nil {
		t.Fatalf("failed to load other flag: %v", err)
	}
	if otherFlag.GetBool("solved") {
		t.Fatalf("expected other flag to remain unsolved (targeted flag only)")
	}
}

func createFlagWithReactions(tb testing.TB, app *tests.TestApp, arcadeID, createdBy string, createdAt time.Time, reactions []reactionSeed) string {
	tb.Helper()

	flagColl, err := app.FindCollectionByNameOrId("arcade_flag")
	if err != nil {
		tb.Fatalf("failed to load arcade_flag collection: %v", err)
	}

	flagRec := core.NewRecord(flagColl)
	flagRec.Set("arcade", arcadeID)
	flagRec.Set("disruption", "minor")
	flagRec.Set("solved", false)
	flagRec.Set("message", "auto-solve target")
	flagRec.Set("createdBy", createdBy)
	if err := app.Save(flagRec); err != nil {
		tb.Fatalf("failed to save arcade_flag: %v", err)
	}

	setRecordTimestamp(tb, app, "arcade_flag", flagRec.Id, createdAt)

	if len(reactions) == 0 {
		return flagRec.Id
	}

	reactionColl, err := app.FindCollectionByNameOrId("arcade_flag_reaction")
	if err != nil {
		tb.Fatalf("failed to load arcade_flag_reaction collection: %v", err)
	}

	for _, seed := range reactions {
		reactionRec := core.NewRecord(reactionColl)
		reactionRec.Set("flag", flagRec.Id)
		reactionRec.Set("reaction", seed.reaction)
		reactionRec.Set("createdBy", createdBy)
		if err := app.Save(reactionRec); err != nil {
			tb.Fatalf("failed to save arcade_flag_reaction: %v", err)
		}

		setRecordTimestamp(tb, app, "arcade_flag_reaction", reactionRec.Id, seed.createdAt)
	}

	return flagRec.Id
}

func addReaction(tb testing.TB, app *tests.TestApp, flagID, createdBy, reaction string) string {
	tb.Helper()

	reactionColl, err := app.FindCollectionByNameOrId("arcade_flag_reaction")
	if err != nil {
		tb.Fatalf("failed to load arcade_flag_reaction collection: %v", err)
	}

	reactionRec := core.NewRecord(reactionColl)
	reactionRec.Set("flag", flagID)
	reactionRec.Set("reaction", reaction)
	reactionRec.Set("createdBy", createdBy)
	if err := app.Save(reactionRec); err != nil {
		tb.Fatalf("failed to save reaction(%s): %v", reaction, err)
	}

	return reactionRec.Id
}

func setRecordTimestamp(tb testing.TB, app *tests.TestApp, table, id string, ts time.Time) {
	tb.Helper()

	when := ts.UTC().Format(types.DefaultDateLayout)
	if _, err := app.NonconcurrentDB().
		NewQuery("UPDATE " + table + " SET created={:created}, updated={:updated} WHERE id={:id}").
		Bind(dbx.Params{"created": when, "updated": when, "id": id}).
		Execute(); err != nil {
		tb.Fatalf("failed to update %s timestamps for %s: %v", table, id, err)
	}
}
