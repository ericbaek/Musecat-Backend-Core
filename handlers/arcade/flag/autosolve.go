package flag

import (
	"log/slog"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

const (
	AutoSolveCronJobID             = "__arcadeFlagAutoSolve__"
	AutoSolveCronExprUTC           = "10 0 * * *"
	AutoSolveReactionHookHandlerID = "__arcadeFlagAutoSolveOnReactionCreate__"
)

const (
	flagAge7Days  = 7 * 24 * time.Hour
	flagAge30Days = 30 * 24 * time.Hour
	flagAge90Days = 90 * 24 * time.Hour
)

func RegisterAutoSolveCron(app core.App) {
	if err := app.Cron().Add(AutoSolveCronJobID, AutoSolveCronExprUTC, func() {
		total, runErr := RunAutoSolve(app, time.Now().UTC())
		if runErr != nil {
			app.Logger().Error("arcade flag auto-solve cron failed", slog.String("error", runErr.Error()))
			return
		}

		if total > 0 {
			app.Logger().Info("arcade flag auto-solve cron completed", slog.Int("solved", total))
		}
	}); err != nil {
		app.Logger().Error("failed to register arcade flag auto-solve cron", slog.String("error", err.Error()))
	}
}

func RegisterAutoSolveReactionCreateHook(app core.App) {
	app.OnRecordAfterCreateSuccess(arcadeinternal.CollectionArcadeFlagReaction).Bind(&hook.Handler[*core.RecordEvent]{
		Id: AutoSolveReactionHookHandlerID,
		Func: func(e *core.RecordEvent) error {
			if err := e.Next(); err != nil {
				return err
			}

			reactionRec := e.Record
			if reactionRec == nil {
				return nil
			}

			flagID := reactionRec.GetString("flag")
			if flagID == "" {
				return nil
			}

			solved, err := RunAutoSolveForFlag(app, flagID, time.Now().UTC())
			if err != nil {
				app.Logger().Warn("arcade flag auto-solve on reaction create failed",
					slog.String("flagId", flagID),
					slog.String("reactionId", reactionRec.Id),
					slog.String("error", err.Error()),
				)
				return nil
			}

			if solved {
				app.Logger().Info("arcade flag auto-solved on reaction create",
					slog.String("flagId", flagID),
					slog.String("reactionId", reactionRec.Id),
				)
			}

			return nil
		},
	})
}

func RunAutoSolve(app core.App, now time.Time) (int, error) {
	now = normalizeNow(now)

	solved := 0
	err := app.RunInTransaction(func(txApp core.App) error {
		flags, err := txApp.FindRecordsByFilter(
			arcadeinternal.CollectionArcadeFlag,
			"solved=false",
			"",
			0,
			0,
		)
		if err != nil {
			return err
		}

		for _, flagRec := range flags {
			flagSolved, err := solveFlagIfNeeded(txApp, flagRec, now)
			if err != nil {
				return err
			}
			if flagSolved {
				solved++
			}
		}

		return nil
	})

	return solved, err
}

func RunAutoSolveForFlag(app core.App, flagID string, now time.Time) (bool, error) {
	if flagID == "" {
		return false, nil
	}
	now = normalizeNow(now)

	solved := false
	err := app.RunInTransaction(func(txApp core.App) error {
		flagRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcadeFlag, flagID)
		if err != nil {
			return nil
		}

		solved, err = solveFlagIfNeeded(txApp, flagRec, now)
		return err
	})

	return solved, err
}

func solveFlagIfNeeded(app core.App, flagRec *core.Record, now time.Time) (bool, error) {
	if flagRec == nil || flagRec.GetBool("solved") {
		return false, nil
	}

	reactions, err := app.FindRecordsByFilter(
		arcadeinternal.CollectionArcadeFlagReaction,
		"flag={:id}",
		"created",
		0,
		0,
		dbx.Params{"id": flagRec.Id},
	)
	if err != nil {
		return false, err
	}

	if !shouldAutoSolveFlag(flagRec, reactions, now) {
		return false, nil
	}

	flagRec.Set("solved", true)
	if err := app.Save(flagRec); err != nil {
		return false, err
	}

	return true, nil
}

func shouldAutoSolveFlag(flagRec *core.Record, reactions []*core.Record, now time.Time) bool {
	flagCreatedAt := recordTimeOrFallback(flagRec, now)
	lastIssuePersistAt := time.Time{}
	lastReactionAt := time.Time{}
	wrongTotal := 0

	for _, reactionRec := range reactions {
		reaction := reactionRec.GetString("reaction")
		createdAt := recordTimeOrFallback(reactionRec, flagCreatedAt)

		if createdAt.After(lastReactionAt) {
			lastReactionAt = createdAt
		}
		if reaction == "issue_persist" && createdAt.After(lastIssuePersistAt) {
			lastIssuePersistAt = createdAt
		}
		if reaction == "wrong" {
			wrongTotal++
		}
	}

	// 조건 4. 등록 기간과 관계 없이 reaction=wrong 이 2개 이상이면 solved=true
	if wrongTotal >= 2 {
		return true
	}

	// 조건 5. 최근 90일 동안 어떠한 reaction도 없으면 solved=true
	// - reaction 이 아예 없는 경우: flag.created 기준 90일 경과
	// - reaction 이 있는 경우: 마지막 reaction.created 기준 90일 경과
	if len(reactions) == 0 {
		return now.Sub(flagCreatedAt) >= flagAge90Days
	}
	if !lastReactionAt.IsZero() && now.Sub(lastReactionAt) >= flagAge90Days {
		return true
	}

	// 조건 1~3 공통 기준 시점:
	// created 와 마지막 issue_persist.created 중 더 최근 시점을 referenceAt 으로 사용
	referenceAt := flagCreatedAt
	if !lastIssuePersistAt.IsZero() && lastIssuePersistAt.After(referenceAt) {
		referenceAt = lastIssuePersistAt
	}

	fixedSinceReference := 0
	for _, reactionRec := range reactions {
		if reactionRec.GetString("reaction") != "fixed" {
			continue
		}

		createdAt := recordTimeOrFallback(reactionRec, flagCreatedAt)
		if !createdAt.Before(referenceAt) {
			fixedSinceReference++
		}
	}

	age := now.Sub(referenceAt)
	// 조건 1. 기준 시점으로부터 7일 미만 + fixed 누적 3개 이상
	if age < flagAge7Days {
		return fixedSinceReference >= 3
	}
	// 조건 2. 기준 시점으로부터 7일 이상 30일 이하 + fixed 누적 2개 이상
	if age <= flagAge30Days {
		return fixedSinceReference >= 2
	}
	// 조건 3. 기준 시점으로부터 30일 초과 + fixed 누적 1개 이상
	return fixedSinceReference >= 1
}

func recordTimeOrFallback(rec *core.Record, fallback time.Time) time.Time {
	createdAt := rec.GetDateTime("created").Time().UTC()
	if !createdAt.IsZero() {
		return createdAt
	}

	updatedAt := rec.GetDateTime("updated").Time().UTC()
	if !updatedAt.IsZero() {
		return updatedAt
	}

	return fallback
}

func normalizeNow(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now.UTC()
}
