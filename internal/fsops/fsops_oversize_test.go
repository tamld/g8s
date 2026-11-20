package fsops

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// seedOversizeFile writes a file one byte past maxReadFileBytes into dir and returns
// its path plus the seeded size.
func seedOversizeFile(t *testing.T, dir, name string) (string, int) {
	t.Helper()
	path := filepath.Join(dir, name)
	oversize := bytes.Repeat([]byte("a"), int(maxReadFileBytes)+1)
	if err := os.WriteFile(path, oversize, 0o600); err != nil {
		t.Fatalf("seed oversize file: %v", err)
	}
	return path, len(oversize)
}

// TestReadRejectsOversizeFile is the #981 red-test: Read must typed-reject files above
// the 64 MiB hard cap with errors.Is(err, ErrFileTooLarge) instead of buffering the
// whole file for SHA-256/binary-sniff/line-slicing while only bounding returned content.
func TestReadRejectsOversizeFile(t *testing.T) {
	dir := t.TempDir()
	path, size := seedOversizeFile(t, dir, "oversize.txt")

	_, err := Read(context.Background(), ReadOptions{Path: path, ScopeRoot: dir})
	if err == nil {
		t.Fatalf("Read succeeded on %d-byte file, want typed oversize reject (#981)", size)
	}
	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("error = %v, want errors.Is(err, ErrFileTooLarge)", err)
	}
}

// TestEditRejectsOversizeFile pins the same #981 cap on the Edit path (shared guard,
// distinct code path): oversize files must be rejected before their bytes are buffered.
// ReplaceAll is used so the pre-fix red run fails fast on the missing cap instead of
// wandering into the (out-of-scope, pre-existing) quadratic match-line error path.
func TestEditRejectsOversizeFile(t *testing.T) {
	dir := t.TempDir()
	path, size := seedOversizeFile(t, dir, "oversize_edit.txt")

	_, err := Edit(context.Background(), EditOptions{
		Path:       path,
		ScopeRoot:  dir,
		OldString:  "a",
		NewString:  "b",
		ReplaceAll: true,
	})
	if err == nil {
		t.Fatalf("Edit succeeded on %d-byte file, want typed oversize reject (#981)", size)
	}
	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("error = %v, want errors.Is(err, ErrFileTooLarge)", err)
	}
}
