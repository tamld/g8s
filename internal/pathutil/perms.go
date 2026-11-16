package pathutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// EnsureStateDir creates dir with mode 0o700 and strips group/other permission bits.
func EnsureStateDir(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" || dir == "." {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	perm := fi.Mode().Perm()
	if perm&0o077 != 0 {
		return os.Chmod(dir, perm&^0o077)
	}
	return nil
}

func cleanSQLitePath(dbPath string) string {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" || dbPath == ":memory:" || strings.Contains(dbPath, "mode=memory") {
		return ""
	}
	clean := strings.TrimPrefix(dbPath, "file:")
	if idx := strings.Index(clean, "?"); idx >= 0 {
		clean = clean[:idx]
	}
	return clean
}

// PrepareSQLiteFile ensures parent dir permissions and creates/tightens the SQLite file.
func PrepareSQLiteFile(dbPath string) error {
	clean := cleanSQLitePath(dbPath)
	if clean == "" {
		return nil
	}
	if err := EnsureStateDir(filepath.Dir(clean)); err != nil {
		return err
	}
	f, err := os.OpenFile(clean, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	fi, err := os.Stat(clean)
	if err != nil {
		return err
	}
	if fi.Mode().Perm()&0o177 != 0 {
		return os.Chmod(clean, 0o600)
	}
	return nil
}

func tightenExisting0600(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.Mode().Perm()&0o177 != 0 {
		return os.Chmod(path, 0o600)
	}
	return nil
}

// HardenSQLiteFiles tightens permissions on the database and WAL/SHM sidecars.
func HardenSQLiteFiles(dbPath string) error {
	clean := cleanSQLitePath(dbPath)
	if clean == "" {
		return nil
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	for _, p := range []string{clean, clean + "-wal", clean + "-shm"} {
		if err := tightenExisting0600(p); err != nil {
			return err
		}
	}
	return nil
}
