package reach

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func tempOwnedPaths(t *testing.T) *OwnedPaths {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "reach_owned_paths.json")
	t.Setenv("G8S_REACH_OWNED_PATHS_PATH", path)
	return NewOwnedPaths()
}

func TestOwnedPathsRegisterDeleteSuccess(t *testing.T) {
	op := tempOwnedPaths(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "artifact.txt")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := op.RegisterOwnedPath(target); err != nil {
		t.Fatalf("RegisterOwnedPath: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("Stat before remove: %v", err)
	}
	if err := op.RemoveRegistered(target); err != nil {
		t.Fatalf("RemoveRegistered: %v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat after remove = %v", err)
	}
	if err := op.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	abs, _ := filepath.Abs(filepath.Clean(target))
	op.mu.Lock()
	_, ok := op.paths[abs]
	op.mu.Unlock()
	if ok {
		t.Fatal("path still registered after delete")
	}
}

func TestOwnedPathsDeleteUnregisteredFails(t *testing.T) {
	op := tempOwnedPaths(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "nope.txt")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	err := op.RemoveRegistered(target)
	if !errors.Is(err, ErrPathNotRegistered) {
		t.Fatalf("err = %v, want ErrPathNotRegistered", err)
	}
}

func TestOwnedPathsRejectSymlink(t *testing.T) {
	op := tempOwnedPaths(t)
	dir := t.TempDir()
	real := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(real, []byte("data"), 0o600); err != nil {
		t.Fatalf("WriteFile real: %v", err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks not supported")
	}
	if err := op.RegisterOwnedPath(link); err != nil {
		t.Fatalf("RegisterOwnedPath: %v", err)
	}
	err := op.RemoveRegistered(link)
	if !errors.Is(err, ErrSymlinkRefused) {
		t.Fatalf("err = %v, want ErrSymlinkRefused", err)
	}
}

func TestOwnedPathsLoadMissingFileEmpty(t *testing.T) {
	op := tempOwnedPaths(t)
	if err := op.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(op.paths) != 0 {
		t.Fatalf("paths = %v", op.paths)
	}
}
