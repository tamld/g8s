package pathutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsureStateDirStripsWorldBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix perm test")
	}
	dir := filepath.Join(t.TempDir(), "nested")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureStateDir(dir); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("perm %o still has group/other bits", fi.Mode().Perm())
	}
}

func TestPrepareAndHardenSQLite(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	if err := PrepareSQLiteFile(db); err != nil {
		t.Fatal(err)
	}
	if err := HardenSQLiteFiles(db); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(db)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Fatalf("db perm %o", fi.Mode().Perm())
		}
	}
}

func TestPrepareSQLiteSkipsMemory(t *testing.T) {
	if err := PrepareSQLiteFile(":memory:"); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareSQLiteFailsWhenParentMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-parent", "db.sqlite")
	if err := PrepareSQLiteFile(missing); err == nil {
		t.Fatalf("expected error for missing parent dir, got nil")
	}
}

func TestEnsureStateDirSkipsStickySharedTempDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sticky temp dir test")
	}
	if _, err := os.Stat("/tmp"); err != nil {
		t.Skip("/tmp unavailable")
	}
	if err := EnsureStateDir("/tmp"); err != nil {
		t.Fatalf("EnsureStateDir(/tmp) must succeed without failing on sticky dir: %v", err)
	}
}
