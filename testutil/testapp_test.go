package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	CleanupGoldenDir()
	os.Exit(code)
}

func TestGoldenDataDirBootstrap(t *testing.T) {
	dir, err := getGoldenDataDir()
	if err != nil {
		t.Fatalf("getGoldenDataDir failed: %v", err)
	}

	if dir == "" {
		t.Fatal("expected non-empty goldenDir")
	}

	// Verify data.db and auxiliary.db exist in the golden dir
	dataDB := filepath.Join(dir, "data.db")
	if info, err := os.Stat(dataDB); err != nil || info.Size() == 0 {
		t.Fatalf("expected non-empty data.db in golden dir, got stat err: %v", err)
	}
	auxDB := filepath.Join(dir, "auxiliary.db")
	if info, err := os.Stat(auxDB); err != nil || info.Size() == 0 {
		t.Fatalf("expected non-empty auxiliary.db in golden dir, got stat err: %v", err)
	}

	// Calling getGoldenDataDir again returns the exact same cached directory
	dir2, err := getGoldenDataDir()
	if err != nil {
		t.Fatalf("second getGoldenDataDir call failed: %v", err)
	}
	if dir != dir2 {
		t.Fatalf("expected same cached goldenDir %q, got %q", dir, dir2)
	}
}

func TestNewTestAppGoldenReusabilityAndIsolation(t *testing.T) {
	// First app instance
	app1 := NewTestApp(t)
	if app1 == nil {
		t.Fatal("expected non-nil app1")
	}
	dir1 := app1.DataDir()
	if _, err := os.Stat(filepath.Join(dir1, "data.db")); err != nil {
		t.Fatalf("expected data.db in app1: %v", err)
	}

	// Verify critical collections exist in app1
	for _, collName := range []string{"user", "arcade", "arcade_basic", "arcade_flag", "arcade_flag_reaction", "arcade_visit", "game_catalog_changelog"} {
		if _, err := app1.FindCollectionByNameOrId(collName); err != nil {
			t.Errorf("expected collection %s in app1: %v", collName, err)
		}
	}

	// Second app instance in subtest to verify isolation
	t.Run("isolated subtest app", func(subT *testing.T) {
		app2 := NewTestApp(subT)
		if app2 == nil {
			subT.Fatal("expected non-nil app2")
		}
		dir2 := app2.DataDir()
		if dir1 == dir2 {
			subT.Fatalf("expected distinct temporary clone dirs, got same %q", dir1)
		}
	})
}

func TestGoldenDirRebootstrapAfterCleanup(t *testing.T) {
	dir1, err := getGoldenDataDir()
	if err != nil {
		t.Fatalf("first getGoldenDataDir failed: %v", err)
	}
	if dir1 == "" {
		t.Fatal("expected non-empty dir1")
	}

	// Verify dir1 exists on disk
	if _, err := os.Stat(filepath.Join(dir1, "data.db")); err != nil {
		t.Fatalf("expected data.db in dir1: %v", err)
	}

	// Clean up golden directory
	CleanupGoldenDir()

	// Verify dir1 was removed from disk
	if _, err := os.Stat(dir1); !os.IsNotExist(err) {
		t.Fatalf("expected dir1 %q to be deleted after CleanupGoldenDir, got err: %v", dir1, err)
	}

	// Calling getGoldenDataDir again must re-bootstrap a fresh golden directory
	dir2, err := getGoldenDataDir()
	if err != nil {
		t.Fatalf("re-bootstrap getGoldenDataDir failed: %v", err)
	}
	if dir2 == "" {
		t.Fatal("expected non-empty dir2")
	}
	if dir1 == dir2 {
		t.Fatalf("expected different directory after cleanup and re-bootstrap, got same %q", dir1)
	}
	if _, err := os.Stat(filepath.Join(dir2, "data.db")); err != nil {
		t.Fatalf("expected data.db in re-bootstrapped dir2: %v", err)
	}

	// Verify NewTestApp works with re-bootstrapped directory
	app := NewTestApp(t)
	if app == nil {
		t.Fatal("expected non-nil app after re-bootstrap")
	}
	if _, err := app.FindCollectionByNameOrId("user"); err != nil {
		t.Fatalf("expected user collection in app after re-bootstrap: %v", err)
	}
}
