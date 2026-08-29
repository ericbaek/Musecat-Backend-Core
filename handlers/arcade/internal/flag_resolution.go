package arcadeinternal

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

const (
	FlagResolutionStateIdle          = "idle"
	FlagResolutionStateActive        = "active"
	FlagResolutionModeStandard       = "standard"
	FlagResolutionModeStale          = "stale"
	FlagResolutionContextLegacy      = "legacy"
	FlagResolutionContextReport      = "report"
	FlagResolutionContextVote        = "vote"
	flagResolutionReportDeleteWindow = 15 * time.Minute
	flagResolutionReportCooldown     = 24 * time.Hour
)

type FlagResolutionSummary struct {
	State           string
	Round           string
	StartedAt       string
	ResolveAt       string
	DelaySeconds    int64
	FixedLevelTotal int
	WrongLevelTotal int
	Score           int
	FixedVoterCount int
	WrongVoterCount int
	MyVote          *string
	MyReport        *FlagResolutionReport
}

type FlagResolutionReport struct {
	Action string  `json:"action"`
	NextAt *string `json:"nextAt"`
}

func CanWithdrawFlag(flagRec *core.Record, userID string, now time.Time) bool {
	if flagRec == nil || strings.TrimSpace(userID) == "" || flagRec.GetBool("solved") {
		return false
	}
	if flagRec.GetString("createdBy") != userID {
		return false
	}
	createdAt := flagRec.GetDateTime("created").Time().UTC()
	if createdAt.IsZero() {
		return false
	}
	return now.UTC().Sub(createdAt) >= 0 && now.UTC().Sub(createdAt) <= 15*time.Minute
}

type FlagResolutionVote struct {
	Record   *core.Record
	UserID   string
	Reaction string
	Level    int
}

func FlagResolutionDelay(score int) time.Duration {
	switch {
	case score >= 30:
		return 15 * time.Minute
	case score >= 20:
		return 3 * time.Hour
	case score >= 10:
		return 24 * time.Hour
	case score >= 5:
		return 48 * time.Hour
	default:
		return 72 * time.Hour
	}
}

func NewFlagResolutionRound(flagID string, now time.Time) string {
	return fmt.Sprintf("%s:%d", flagID, now.UTC().UnixNano())
}

func FindFlagReactions(app core.App, flagID string) ([]*core.Record, error) {
	return app.FindRecordsByFilter(
		CollectionArcadeFlagReaction,
		"flag={:flag}",
		"created",
		0,
		0,
		dbx.Params{"flag": flagID},
	)
}

func FindCurrentFlagVotes(app core.App, flagID, round string) ([]FlagResolutionVote, error) {
	reactions, err := FindFlagReactions(app, flagID)
	if err != nil {
		return nil, err
	}

	byUser := map[string]FlagResolutionVote{}
	for _, rec := range reactions {
		if rec.GetString("resolution_context") != FlagResolutionContextVote ||
			rec.GetString("vote_round") != round {
			continue
		}
		reaction := rec.GetString("reaction")
		if reaction != "fixed" && reaction != "wrong" {
			continue
		}
		userID := rec.GetString("createdBy")
		if userID == "" {
			continue
		}
		byUser[userID] = FlagResolutionVote{
			Record:   rec,
			UserID:   userID,
			Reaction: reaction,
			Level:    maxInt(0, rec.GetInt("level_snapshot")),
		}
	}

	votes := make([]FlagResolutionVote, 0, len(byUser))
	for _, vote := range byUser {
		votes = append(votes, vote)
	}
	sort.SliceStable(votes, func(i, j int) bool {
		return votes[i].Record.Id < votes[j].Record.Id
	})
	return votes, nil
}

func BuildFlagResolutionValue(app core.App, flagRec *core.Record) (map[string]any, error) {
	return BuildFlagResolutionValueForUser(app, flagRec, "")
}

func BuildFlagResolutionValueForUser(app core.App, flagRec *core.Record, userID string) (map[string]any, error) {
	if flagRec == nil {
		return nil, fmt.Errorf("flag is required")
	}
	state := flagRec.GetString("resolution_vote_state")
	if state == "" {
		state = FlagResolutionStateIdle
	}
	round := flagRec.GetString("resolution_vote_round")
	resolution := FlagResolutionSummary{
		State:     state,
		Round:     round,
		StartedAt: flagRec.GetString("resolution_vote_started_at"),
		ResolveAt: flagRec.GetString("resolution_vote_resolve_at"),
	}
	if state == FlagResolutionStateActive && round != "" {
		votes, err := FindCurrentFlagVotes(app, flagRec.Id, round)
		if err != nil {
			return nil, err
		}
		for _, vote := range votes {
			if vote.Reaction == "fixed" {
				resolution.FixedLevelTotal += vote.Level
				resolution.FixedVoterCount++
			} else {
				resolution.WrongLevelTotal += vote.Level
				resolution.WrongVoterCount++
			}
			if vote.UserID == userID {
				voteReaction := vote.Reaction
				resolution.MyVote = &voteReaction
			}
		}
		resolution.Score = resolution.FixedLevelTotal - resolution.WrongLevelTotal
		resolution.DelaySeconds = int64(FlagResolutionDelay(resolution.Score).Seconds())
	}
	if userID != "" {
		report, err := findUserReport(app, flagRec.Id, userID)
		if err != nil {
			return nil, err
		}
		if report != nil {
			resolution.MyReport = buildReportState(report)
		}
	}

	return map[string]any{
		"state":           resolution.State,
		"round":           resolution.Round,
		"startedAt":       nullableString(resolution.StartedAt),
		"resolveAt":       nullableString(resolution.ResolveAt),
		"delaySeconds":    resolution.DelaySeconds,
		"fixedLevelTotal": resolution.FixedLevelTotal,
		"wrongLevelTotal": resolution.WrongLevelTotal,
		"score":           resolution.Score,
		"fixedVoterCount": resolution.FixedVoterCount,
		"wrongVoterCount": resolution.WrongVoterCount,
		"myVote":          resolution.MyVote,
		"myReport":        resolution.MyReport,
	}, nil
}

func findUserReport(app core.App, flagID, userID string) (*core.Record, error) {
	records, err := app.FindRecordsByFilter(
		CollectionArcadeFlagReaction,
		"flag={:flag} && reaction='issue_persist' && createdBy={:user}",
		"-created",
		1,
		0,
		dbx.Params{"flag": flagID, "user": userID},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query user's issue reports: %w", err)
	}
	if len(records) == 0 {
		return nil, nil
	}
	return records[0], nil
}

func buildReportState(report *core.Record) *FlagResolutionReport {
	createdAt := report.GetDateTime("created").Time().UTC()
	if createdAt.IsZero() {
		return &FlagResolutionReport{Action: "cooldown"}
	}
	age := time.Now().UTC().Sub(createdAt)
	if age <= flagResolutionReportDeleteWindow {
		return &FlagResolutionReport{Action: "delete"}
	}
	nextAt := createdAt.Add(flagResolutionReportCooldown)
	if age < flagResolutionReportCooldown {
		nextAtValue := nextAt.Format(types.DefaultDateLayout)
		return &FlagResolutionReport{Action: "cooldown", NextAt: &nextAtValue}
	}
	return &FlagResolutionReport{Action: "add"}
}

func BuildFlagReportHistoryValue(app core.App, flagRec *core.Record) ([]map[string]any, error) {
	if flagRec == nil {
		return nil, fmt.Errorf("flag is required")
	}
	reactions, err := FindFlagReactions(app, flagRec.Id)
	if err != nil {
		return nil, err
	}
	history := []map[string]any{{
		"id":        "initial:" + flagRec.Id,
		"kind":      "initial",
		"createdBy": flagRec.GetString("createdBy"),
		"created":   flagRec.Get("created"),
	}}
	for _, rec := range reactions {
		if rec.GetString("reaction") != "issue_persist" {
			continue
		}
		history = append(history, map[string]any{
			"id":        rec.Id,
			"kind":      "issue_persist",
			"createdBy": rec.GetString("createdBy"),
			"created":   rec.Get("created"),
		})
	}
	sort.SliceStable(history, func(i, j int) bool {
		return recordValueTime(history[i]["created"]).After(recordValueTime(history[j]["created"]))
	})
	return history, nil
}

func ReconcileFlagResolutionTx(app core.App, flagRec *core.Record, now time.Time, resetDeadline bool) (bool, error) {
	if flagRec == nil || flagRec.GetBool("solved") {
		return false, nil
	}
	now = normalizeResolutionNow(now)
	state := flagRec.GetString("resolution_vote_state")
	round := flagRec.GetString("resolution_vote_round")
	if state != FlagResolutionStateActive || round == "" {
		return false, nil
	}

	resolveAt := flagRec.GetDateTime("resolution_vote_resolve_at").Time().UTC()
	if !resolveAt.IsZero() && !now.Before(resolveAt) {
		flagRec.Set("solved", true)
		flagRec.Set("resolution_vote_state", FlagResolutionStateIdle)
		flagRec.Set("resolution_vote_resolve_at", "")
		if err := app.Save(flagRec); err != nil {
			return false, fmt.Errorf("failed to solve resolved flag: %w", err)
		}
		return true, nil
	}

	votes, err := FindCurrentFlagVotes(app, flagRec.Id, round)
	if err != nil {
		return false, err
	}
	fixedTotal, wrongTotal, fixedCount := 0, 0, 0
	for _, vote := range votes {
		if vote.Reaction == "fixed" {
			fixedTotal += vote.Level
			fixedCount++
		} else {
			wrongTotal += vote.Level
		}
	}
	score := fixedTotal - wrongTotal
	if fixedCount == 0 || score < 0 {
		if flagRec.GetString("resolution_vote_mode") == FlagResolutionModeStale && len(votes) == 0 {
			return false, nil
		}
		return closeFlagResolutionTx(app, flagRec)
	}

	candidateResolveAt := now.Add(FlagResolutionDelay(score))
	if resolveAt.IsZero() || (resetDeadline && candidateResolveAt.Before(resolveAt)) {
		flagRec.Set("resolution_vote_resolve_at", candidateResolveAt)
		if err := app.Save(flagRec); err != nil {
			return false, fmt.Errorf("failed to save flag resolution deadline: %w", err)
		}
	}
	return false, nil
}

func StartFlagResolutionTx(app core.App, flagRec *core.Record, now time.Time) error {
	now = normalizeResolutionNow(now)
	flagRec.Set("resolution_vote_state", FlagResolutionStateActive)
	flagRec.Set("resolution_vote_mode", FlagResolutionModeStandard)
	flagRec.Set("resolution_vote_round", NewFlagResolutionRound(flagRec.Id, now))
	flagRec.Set("resolution_vote_started_at", now)
	flagRec.Set("resolution_vote_resolve_at", "")
	if err := app.Save(flagRec); err != nil {
		return fmt.Errorf("failed to start flag resolution: %w", err)
	}
	return nil
}

func StartStaleFlagResolutionTx(app core.App, flagRec *core.Record, now time.Time) error {
	now = normalizeResolutionNow(now)
	flagRec.Set("resolution_vote_state", FlagResolutionStateActive)
	flagRec.Set("resolution_vote_mode", FlagResolutionModeStale)
	flagRec.Set("resolution_vote_round", NewFlagResolutionRound(flagRec.Id, now))
	flagRec.Set("resolution_vote_started_at", now)
	flagRec.Set("resolution_vote_resolve_at", now.Add(72*time.Hour))
	if err := app.Save(flagRec); err != nil {
		return fmt.Errorf("failed to start stale flag resolution: %w", err)
	}
	return nil
}

func CloseFlagResolutionTx(app core.App, flagRec *core.Record) (bool, error) {
	return closeFlagResolutionTx(app, flagRec)
}

func closeFlagResolutionTx(app core.App, flagRec *core.Record) (bool, error) {
	changed := flagRec.GetString("resolution_vote_state") == FlagResolutionStateActive ||
		flagRec.GetString("resolution_vote_resolve_at") != ""
	if !changed {
		return false, nil
	}
	flagRec.Set("resolution_vote_state", FlagResolutionStateIdle)
	flagRec.Set("resolution_vote_mode", FlagResolutionModeStandard)
	flagRec.Set("resolution_vote_resolve_at", "")
	// Closing an unresolved round is activity for the stale-resolution clock.
	// Saving the flag refreshes PocketBase's system updated timestamp.
	if err := app.Save(flagRec); err != nil {
		return false, fmt.Errorf("failed to close flag resolution: %w", err)
	}
	return false, nil
}

func LevelSnapshot(app core.App, userID string) (int, error) {
	exp, err := userhandler.LoadCurrentExp(app, userID)
	if err != nil {
		return 0, err
	}
	return userhandler.LevelFromExp(exp), nil
}

func normalizeResolutionNow(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now.UTC()
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func recordValueTime(value any) time.Time {
	if dateTime, ok := value.(types.DateTime); ok {
		return dateTime.Time().UTC()
	}
	if parsed, ok := value.(time.Time); ok {
		return parsed.UTC()
	}
	if raw, ok := value.(string); ok {
		for _, layout := range []string{time.RFC3339Nano, types.DefaultDateLayout} {
			if parsed, err := time.Parse(layout, raw); err == nil {
				return parsed.UTC()
			}
		}
	}
	return time.Time{}
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
