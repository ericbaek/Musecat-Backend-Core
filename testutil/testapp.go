package testutil

import (
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/ui"

	_ "github.com/ericbaek/musecat-backend-core/migrations"
)

var (
	goldenMu  sync.Mutex
	goldenDir string
)

func getGoldenDataDir() (string, error) {
	goldenMu.Lock()
	defer goldenMu.Unlock()

	if goldenDir != "" {
		if _, err := os.Stat(goldenDir); err == nil {
			return goldenDir, nil
		}
		goldenDir = ""
	}

	dir, err := os.MkdirTemp("", fmt.Sprintf("musecat_core_golden_%d_*", os.Getpid()))
	if err != nil {
		return "", fmt.Errorf("create golden test dir: %w", err)
	}

	app := core.NewBaseApp(core.BaseAppConfig{
		DataDir:       dir,
		EncryptionEnv: "pb_test_env",
	})

	if err := app.Bootstrap(); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("bootstrap golden app: %w", err)
	}

	if err := app.RunAllMigrations(); err != nil {
		_ = app.ResetBootstrapState()
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("run migrations on golden app: %w", err)
	}

	// Ensure SQLite writes all pages into data.db and auxiliary.db and truncates WAL
	if _, err := app.DB().NewQuery("PRAGMA wal_checkpoint(TRUNCATE)").Execute(); err != nil {
		_ = app.ResetBootstrapState()
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("checkpoint golden data db: %w", err)
	}
	if _, err := app.AuxDB().NewQuery("PRAGMA wal_checkpoint(TRUNCATE)").Execute(); err != nil {
		_ = app.ResetBootstrapState()
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("checkpoint golden aux db: %w", err)
	}

	if err := app.ResetBootstrapState(); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("reset golden app bootstrap state: %w", err)
	}

	goldenDir = dir
	return goldenDir, nil
}

// CleanupGoldenDir removes the process-level golden bootstrap directory if initialized.
// Subsequent calls to NewTestApp will safely re-bootstrap a fresh golden directory.
func CleanupGoldenDir() {
	goldenMu.Lock()
	defer goldenMu.Unlock()

	if goldenDir != "" {
		_ = os.RemoveAll(goldenDir)
		goldenDir = ""
	}
}

// NewTestApp clones a fresh Core bootstrap database without altering its schema.
func NewTestApp(tb testing.TB) *tests.TestApp {
	tb.Helper()

	resolved, err := getGoldenDataDir()
	if err != nil {
		tb.Fatalf("failed to bootstrap golden test data directory: %v", err)
	}
	app, err := tests.NewTestApp(resolved)
	if err != nil {
		tb.Fatalf("failed to initialize test app: %v", err)
	}
	tb.Cleanup(app.Cleanup)

	// PocketBase v0.39.9 registers UI extension routes on every new API router.
	// Core tests create multiple routers but don't exercise the bundled UI.
	ui.DistDirFS = nil
	return app
}
