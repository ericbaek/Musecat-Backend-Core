package flag

import (
	"log/slog"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

const (
	AutoSolveCronJobID             = "__arcadeFlagAutoSolve__"
	AutoSolveCronExprUTC           = "* * * * *"
	StaleResolutionCronJobID       = "__arcadeFlagStaleResolution__"
	StaleResolutionCronExprUTC     = "0 0 * * *"
	StaleResolutionAge             = 120 * 24 * time.Hour
	AutoSolveReactionHookHandlerID = "__arcadeFlagAutoSolveOnReactionCreate__"
)

func ResolutionDelay(score int) time.Duration {
	return arcadeinternal.FlagResolutionDelay(score)
}

func RegisterAutoSolveCron(app core.App) {
	if err := app.Cron().Add(AutoSolveCronJobID, AutoSolveCronExprUTC, func() {
		total, runErr := RunAutoSolve(app, time.Now().UTC())
		if runErr != nil {
			app.Logger().Error("arcade flag resolution cron failed", slog.String("error", runErr.Error()))
			return
		}
		if total > 0 {
			app.Logger().Info("arcade flag resolution cron completed", slog.Int("solved", total))
		}
	}); err != nil {
		app.Logger().Error("failed to register arcade flag resolution cron", slog.String("error", err.Error()))
	}
	if err := app.Cron().Add(StaleResolutionCronJobID, StaleResolutionCronExprUTC, func() {
		opened, runErr := RunStaleResolutionSweep(app, time.Now().UTC())
		if runErr != nil {
			app.Logger().Error("arcade flag stale-resolution cron failed", slog.String("error", runErr.Error()))
			return
		}
		if opened > 0 {
			app.Logger().Info("arcade flag stale-resolution cron completed", slog.Int("opened", opened))
		}
	}); err != nil {
		app.Logger().Error("failed to register arcade flag stale-resolution cron", slog.String("error", err.Error()))
	}
}

func RegisterAutoSolveReactionCreateHook(app core.App) {
	app.OnRecordAfterCreateSuccess(arcadeinternal.CollectionArcadeFlagReaction).Bind(&hook.Handler[*core.RecordEvent]{
		Id: AutoSolveReactionHookHandlerID,
		Func: func(e *core.RecordEvent) error {
			if err := e.Next(); err != nil {
				return err
			}
			if e.Record == nil || e.Record.GetString("flag") == "" {
				return nil
			}
			if _, err := RunAutoSolveForFlag(app, e.Record.GetString("flag"), time.Now().UTC()); err != nil {
				app.Logger().Warn("failed to reconcile arcade flag after reaction create",
					slog.String("flagId", e.Record.GetString("flag")), slog.String("error", err.Error()))
			}
			return nil
		},
	})
}

func RunAutoSolve(app core.App, now time.Time) (int, error) {
	now = normalizeNow(now)
	solved := 0
	err := app.RunInTransaction(func(txApp core.App) error {
		flags, err := txApp.FindRecordsByFilter(arcadeinternal.CollectionArcadeFlag, "solved=false && resolution_vote_state='active'", "", 0, 0)
		if err != nil {
			return err
		}
		for _, flagRec := range flags {
			flagSolved, err := reconcileFlag(txApp, flagRec, now)
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
		solved, err = reconcileFlag(txApp, flagRec, now)
		return err
	})
	return solved, err
}

func RunStaleResolutionSweep(app core.App, now time.Time) (int, error) {
	now = normalizeNow(now)
	cutoff := now.Add(-StaleResolutionAge)
	opened := 0
	err := app.RunInTransaction(func(txApp core.App) error {
		flags, err := txApp.FindRecordsByFilter(arcadeinternal.CollectionArcadeFlag, "solved=false && resolution_vote_state='idle'", "-updated", 0, 0)
		if err != nil {
			return err
		}
		for _, flagRec := range flags {
			activityAt := flagRec.GetDateTime("updated").Time().UTC()
			if activityAt.IsZero() {
				activityAt = flagRec.GetDateTime("created").Time().UTC()
			}
			if activityAt.IsZero() || activityAt.After(cutoff) {
				continue
			}
			if err := arcadeinternal.StartStaleFlagResolutionTx(txApp, flagRec, now); err != nil {
				return err
			}
			opened++
		}
		return nil
	})
	return opened, err
}

func reconcileFlag(app core.App, flagRec *core.Record, now time.Time) (bool, error) {
	return arcadeinternal.ReconcileFlagResolutionTx(app, flagRec, now, false)
}

func normalizeNow(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now.UTC()
}
