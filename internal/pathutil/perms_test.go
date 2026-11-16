package pathutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestEnsureStateDirNoopPaths(t *testing.T) {
	if err := EnsureStateDir(""); err != nil {
		t.Fatalf("EnsureStateDir(\"\"): %v", err)
	}
	if err := EnsureStateDir(" . "); err != nil {
		t.Fatalf("EnsureStateDir(\" . \"): %v", err)
	}
}

func TestPrepareSQLiteFileURIAndErrors(t *testing.T) {
	dir := t.TempDir()
	uri := "file:" + filepath.Join(dir, "uri.db") + "?_pragma=busy_timeout(5000)"
	if err := PrepareSQLiteFile(uri); err != nil {
		t.Fatalf("PrepareSQLiteFile URI: %v", err)
	}
	db := filepath.Join(dir, "uri.db")
	if _, err := os.Stat(db); err != nil {
		t.Fatalf("expected uri.db to exist: %v", err)
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(db, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := PrepareSQLiteFile(db); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(db)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o177 != 0 {
			t.Fatalf("db still permissive after PrepareSQLiteFile: %o", fi.Mode().Perm())
		}
	}

	reg := filepath.Join(t.TempDir(), "reg")
	if err := os.WriteFile(reg, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := PrepareSQLiteFile(filepath.Join(reg, "db.sqlite"))
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("expected not a directory error, got %v", err)
	}
}

func TestHardenSQLiteFilesMemoryAndSidecars(t *testing.T) {
	if err := HardenSQLiteFiles(":memory:"); err != nil {
		t.Fatalf("HardenSQLiteFiles(:memory:): %v", err)
	}
	if err := HardenSQLiteFiles("file:mem?mode=memory"); err != nil {
		t.Fatalf("HardenSQLiteFiles(mode=memory): %v", err)
	}

	dir := t.TempDir()
	db := filepath.Join(dir, "sidecars.db")
	targets := []string{db, db + "-wal", db + "-shm"}
	for _, p := range targets {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" {
			if err := os.Chmod(p, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := HardenSQLiteFiles("file:" + db + "?mode=rwc"); err != nil {
		t.Fatalf("HardenSQLiteFiles URI: %v", err)
	}
	if runtime.GOOS != "windows" {
		for _, p := range targets {
			fi, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode().Perm()&0o177 != 0 {
				t.Fatalf("%s still permissive: %o", p, fi.Mode().Perm())
			}
		}
	}
}
