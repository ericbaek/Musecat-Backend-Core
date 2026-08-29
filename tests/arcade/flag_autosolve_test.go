package arcade_test

import (
	"net/http"
	"testing"
	"time"

	arcadeflag "github.com/ericbaek/musecat-backend-core/handlers/arcade/flag"
)

func TestFlagResolutionDelayBoundaries(t *testing.T) {
	tests := []struct {
		score int
		want  time.Duration
	}{
		{score: 0, want: 72 * time.Hour},
		{score: 1, want: 72 * time.Hour},
		{score: 4, want: 72 * time.Hour},
		{score: 5, want: 48 * time.Hour},
		{score: 9, want: 48 * time.Hour},
		{score: 10, want: 24 * time.Hour},
		{score: 19, want: 24 * time.Hour},
		{score: 20, want: 3 * time.Hour},
		{score: 29, want: 3 * time.Hour},
		{score: 30, want: 15 * time.Minute},
		{score: 100, want: 15 * time.Minute},
	}
	for _, test := range tests {
		if got := arcadeflag.ResolutionDelay(test.score); got != test.want {
			t.Errorf("score %d: delay %s, want %s", test.score, got, test.want)
		}
	}
}

func TestArcadeFlagAutoSolve_ResolvesOnlyDueActiveVotes(t *testing.T) {
	app := newArcadeTestApp(t)
	t.Cleanup(app.Cleanup)
	token, user := createAuthUser(t, app)
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{Name: "Resolution Cron Arcade", Address: "Resolution Cron Street", Nickname: []string{"ResolutionCron"}, Location: location{Lat: 37.5665, Lon: 126.978}})
	now := time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)
	flagID := createFlagWithReactions(t, app, arcadeID, user.Id, now.Add(-time.Hour), nil)
	flag, err := app.FindRecordById("arcade_flag", flagID)
	if err != nil {
		t.Fatalf("load flag: %v", err)
	}
	flag.Set("resolution_vote_state", "active")
	flag.Set("resolution_vote_round", "round-1")
	flag.Set("resolution_vote_resolve_at", now.Add(-time.Minute))
	if err := app.Save(flag); err != nil {
		t.Fatalf("save active flag: %v", err)
	}
	reactionID := addReaction(t, app, flagID, user.Id, "fixed")
	reaction, err := app.FindRecordById("arcade_flag_reaction", reactionID)
	if err != nil {
		t.Fatalf("load reaction: %v", err)
	}
	reaction.Set("resolution_context", "vote")
	reaction.Set("vote_round", "round-1")
	reaction.Set("level_snapshot", 5)
	if err := app.Save(reaction); err != nil {
		t.Fatalf("save vote reaction: %v", err)
	}

	solved, err := arcadeflag.RunAutoSolve(app, now)
	if err != nil {
		t.Fatalf("run resolution cron: %v", err)
	}
	if solved != 1 {
		t.Fatalf("expected one solved flag, got %d", solved)
	}
	flag, err = app.FindRecordById("arcade_flag", flagID)
	if err != nil || !flag.GetBool("solved") {
		t.Fatalf("expected due active flag to be solved: err=%v solved=%v", err, flag.GetBool("solved"))
	}
	response := postFlagReaction(t, app, token, flagID, "fixed", "add")
	assertStatus(t, response, http.StatusBadRequest)
	response.Body.Close()
}

func TestArcadeFlagStaleResolutionSweep_OpensZeroScoreWindow(t *testing.T) {
	app := newArcadeTestApp(t)
	t.Cleanup(app.Cleanup)
	_, user := createAuthUser(t, app)
	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{Name: "Stale Resolution Arcade", Address: "Stale Resolution Street", Nickname: []string{"StaleResolution"}, Location: location{Lat: 37.5665, Lon: 126.978}})
	now := time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)
	flagID := createFlagWithReactions(t, app, arcadeID, user.Id, now.Add(-arcadeflag.StaleResolutionAge), nil)
	flag, err := app.FindRecordById("arcade_flag", flagID)
	if err != nil {
		t.Fatalf("load stale flag: %v", err)
	}
	flag.Set("resolution_vote_state", "idle")
	flag.Set("resolution_vote_mode", "standard")
	if err := app.Save(flag); err != nil {
		t.Fatalf("initialize stale flag state: %v", err)
	}
	setRecordTimestamp(t, app, "arcade_flag", flagID, now.Add(-arcadeflag.StaleResolutionAge))

	opened, err := arcadeflag.RunStaleResolutionSweep(app, now)
	if err != nil {
		t.Fatalf("run stale resolution sweep: %v", err)
	}
	if opened != 1 {
		t.Fatalf("expected one stale resolution window, got %d", opened)
	}

	flag, err = app.FindRecordById("arcade_flag", flagID)
	if err != nil {
		t.Fatalf("reload stale flag: %v", err)
	}
	if flag.GetString("resolution_vote_state") != "active" || flag.GetString("resolution_vote_mode") != "stale" {
		t.Fatalf("expected stale active resolution, got state=%q mode=%q", flag.GetString("resolution_vote_state"), flag.GetString("resolution_vote_mode"))
	}
	if got := flag.GetDateTime("resolution_vote_resolve_at").Time().UTC(); !got.Equal(now.Add(72 * time.Hour)) {
		t.Fatalf("expected stale resolution deadline at %s, got %s", now.Add(72*time.Hour), got)
	}

	solved, err := arcadeflag.RunAutoSolve(app, now.Add(72*time.Hour))
	if err != nil {
		t.Fatalf("resolve stale window: %v", err)
	}
	if solved != 1 {
		t.Fatalf("expected stale window to solve after 72 hours, got %d", solved)
	}
}

func TestArcadeFlagAutoSolve_CronRunsEveryMinute(t *testing.T) {
	app := newArcadeTestApp(t)
	t.Cleanup(app.Cleanup)
	arcadeflag.RegisterAutoSolveCron(app)
	foundResolution := false
	foundStale := false
	for _, job := range app.Cron().Jobs() {
		if job.Id() == arcadeflag.AutoSolveCronJobID {
			foundResolution = true
			if job.Expression() != arcadeflag.AutoSolveCronExprUTC {
				t.Fatalf("cron expression=%q, want %q", job.Expression(), arcadeflag.AutoSolveCronExprUTC)
			}
		}
		if job.Id() == arcadeflag.StaleResolutionCronJobID {
			foundStale = true
			if job.Expression() != arcadeflag.StaleResolutionCronExprUTC {
				t.Fatalf("stale cron expression=%q, want %q", job.Expression(), arcadeflag.StaleResolutionCronExprUTC)
			}
		}
	}
	if !foundResolution {
		t.Fatalf("expected cron job %q", arcadeflag.AutoSolveCronJobID)
	}
	if !foundStale {
		t.Fatalf("expected cron job %q", arcadeflag.StaleResolutionCronJobID)
	}
}
