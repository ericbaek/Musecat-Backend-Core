package community

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

const (
	TranslationCronJobID   = "__communityPostTranslation__"
	TranslationCronExprUTC = "* * * * *"
	translationBatchSize   = 25
	maxTranslationAttempts = 5
	translationLease       = 10 * time.Minute
)

func RegisterTranslationCron(app core.App) {
	if err := app.Cron().Add(TranslationCronJobID, TranslationCronExprUTC, func() {
		config := TranslationConfigFromEnv()
		if config.APIKey == "" {
			return
		}
		translator, err := NewConfiguredTranslator(config, &http.Client{Timeout: 30 * time.Second})
		if err != nil {
			app.Logger().Error("community translation configuration failed", slog.String("error", err.Error()))
			return
		}
		processed, runErr := RunDueTranslations(context.Background(), app, translator, time.Now().UTC())
		if runErr != nil {
			app.Logger().Error("community translation cron failed", slog.String("error", runErr.Error()))
			return
		}
		if processed > 0 {
			app.Logger().Info("community translation cron completed", slog.Int("processed", processed))
		}
	}); err != nil {
		app.Logger().Error("failed to register community translation cron", slog.String("error", err.Error()))
	}
}

// RunDueTranslations claims due posts in a short transaction, performs the
// external translation call outside the transaction, and then atomically saves
// the result. It is exported so tests and operators can run the same worker.
func RunDueTranslations(ctx context.Context, app core.App, translator Translator, now time.Time) (int, error) {
	if translator == nil {
		return 0, fmt.Errorf("translator is required")
	}
	now = now.UTC()
	staleBefore := now.Add(-translationLease)
	records, err := app.FindRecordsByFilter(
		CollectionPost,
		"status = 'active' && translation_attempts < {:max_attempts} && translate_after <= {:now} && (translation_status = 'pending' || translation_status = 'failed' || (translation_status = 'processing' && translation_started_at <= {:stale_before}))",
		"translate_after",
		translationBatchSize,
		0,
		dbx.Params{"max_attempts": maxTranslationAttempts, "now": now, "stale_before": staleBefore},
	)
	if err != nil {
		return 0, fmt.Errorf("list due community translations: %w", err)
	}

	processed := 0
	var runErrors []error
	for _, candidate := range records {
		claimed, source, sourceHash, attempt, err := claimTranslation(app, candidate.Id, now)
		if err != nil {
			runErrors = append(runErrors, err)
			continue
		}
		if !claimed {
			continue
		}

		callCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
		result, callErr := translator.Translate(callCtx, source)
		cancel()
		if callErr != nil {
			if err := failTranslation(app, candidate.Id, sourceHash, attempt, now, callErr); err != nil {
				runErrors = append(runErrors, err)
			} else {
				processed++
			}
			continue
		}
		if err := completeTranslation(app, candidate.Id, sourceHash, attempt, result, now); err != nil {
			runErrors = append(runErrors, err)
			continue
		}
		processed++
	}
	return processed, errors.Join(runErrors...)
}

func claimTranslation(app core.App, id string, now time.Time) (bool, SourceContent, string, int, error) {
	var source SourceContent
	var sourceHash string
	attempt := 0
	claimed := false
	err := app.RunInTransaction(func(tx core.App) error {
		record, err := tx.FindRecordById(CollectionPost, id)
		if err != nil {
			return nil
		}
		if !translationClaimable(record, now) {
			return nil
		}
		source = SourceContent{Title: record.GetString("title"), Body: record.GetString("body")}
		sourceHash = contentHash(source.Title, source.Body)
		attempt = record.GetInt("translation_attempts") + 1
		record.Set("translation_status", "processing")
		record.Set("translation_attempts", attempt)
		record.Set("translation_started_at", now)
		record.Set("translation_error", "")
		record.Set("original_hash", sourceHash)
		if err := tx.Save(record); err != nil {
			return fmt.Errorf("claim translation %s: %w", id, err)
		}
		claimed = true
		return nil
	})
	return claimed, source, sourceHash, attempt, err
}

func translationClaimable(record *core.Record, now time.Time) bool {
	if record == nil || record.GetString("status") != "active" || record.GetInt("translation_attempts") >= maxTranslationAttempts {
		return false
	}
	if record.GetDateTime("translate_after").Time().UTC().After(now) {
		return false
	}
	switch record.GetString("translation_status") {
	case "pending", "failed":
		return true
	case "processing":
		started := record.GetDateTime("translation_started_at").Time().UTC()
		return !started.IsZero() && !started.After(now.Add(-translationLease))
	default:
		return false
	}
}

func completeTranslation(app core.App, id, sourceHash string, attempt int, result TranslationResult, now time.Time) error {
	return app.RunInTransaction(func(tx core.App) error {
		record, err := tx.FindRecordById(CollectionPost, id)
		if err != nil {
			return fmt.Errorf("reload translated post %s: %w", id, err)
		}
		if record.GetString("status") != "active" || record.GetString("translation_status") != "processing" || record.GetInt("translation_attempts") != attempt {
			return nil
		}
		currentHash := contentHash(record.GetString("title"), record.GetString("body"))
		if currentHash != sourceHash || record.GetString("original_hash") != sourceHash {
			record.Set("translation_status", "pending")
			record.Set("translation_attempts", 0)
			record.Set("translation_started_at", "")
			record.Set("translation_error", "source changed while translating")
			return tx.Save(record)
		}
		record.Set("translations", result)
		record.Set("translation_status", "ready")
		record.Set("translation_started_at", "")
		record.Set("translated_at", now)
		record.Set("translation_error", "")
		if err := tx.Save(record); err != nil {
			return fmt.Errorf("save translation %s: %w", id, err)
		}
		return nil
	})
}

func failTranslation(app core.App, id, sourceHash string, attempt int, now time.Time, cause error) error {
	return app.RunInTransaction(func(tx core.App) error {
		record, err := tx.FindRecordById(CollectionPost, id)
		if err != nil {
			return fmt.Errorf("reload failed translation %s: %w", id, err)
		}
		if record.GetString("status") != "active" || record.GetString("translation_status") != "processing" || record.GetInt("translation_attempts") != attempt || record.GetString("original_hash") != sourceHash {
			return nil
		}
		record.Set("translation_status", "failed")
		record.Set("translation_started_at", "")
		record.Set("translation_error", truncate(strings.TrimSpace(cause.Error()), 2000))
		if attempt < maxTranslationAttempts {
			record.Set("translate_after", now.Add(retryDelay(attempt)))
		}
		if err := tx.Save(record); err != nil {
			return fmt.Errorf("record translation failure %s: %w", id, err)
		}
		return nil
	})
}

func retryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return time.Minute
	case 2:
		return 5 * time.Minute
	case 3:
		return 15 * time.Minute
	default:
		return time.Hour
	}
}
