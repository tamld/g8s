package fsops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadDeniedPathFragments proves the worker-facing fsops Read path
// enforces the same DeniedPathFragments deny-list as the harness
// add-dirs / supervisor prompt gates (#986 B-8). Reads of paths containing
// (or resolving through symlinks to) a denied sensitive fragment must fail
// with the typed ErrDeniedPathFragment error; ordinary in-scope reads,
// including non-denied dotfiles, must still succeed.
func TestReadDeniedPathFragments(t *testing.T) {
	ctx := context.Background()
	scope := t.TempDir()

	secretEnv := filepath.Join(scope, ".env")
	if err := os.WriteFile(secretEnv, []byte("API_TOKEN=supersecret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(scope, "notes.txt")
	if err := os.WriteFile(notes, []byte("public team notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitignore := filepath.Join(scope, ".gitignore")
	if err := os.WriteFile(gitignore, []byte("*.tmp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bareKey := filepath.Join(scope, "master.key")
	if err := os.WriteFile(bareKey, []byte("k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	upperEnv := filepath.Join(scope, ".ENV")
	if err := os.WriteFile(upperEnv, []byte("X=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	innocentLink := filepath.Join(scope, "innocent-link")
	if err := os.Symlink(secretEnv, innocentLink); err != nil {
		t.Fatal(err)
	}

	read := func(t *testing.T, path string) error {
		t.Helper()
		_, err := Read(ctx, ReadOptions{Path: path, ScopeRoot: scope})
		return err
	}

	t.Run("denies .env inside scope with typed error", func(t *testing.T) {
		err := read(t, secretEnv)
		if err == nil {
			t.Fatal("expected Read of scope .env to be denied, got nil error")
		}
		if !errors.Is(err, ErrDeniedPathFragment) {
			t.Fatalf("expected ErrDeniedPathFragment, got %v", err)
		}
	})

	t.Run("denies bare fragment (master.key)", func(t *testing.T) {
		err := read(t, bareKey)
		if !errors.Is(err, ErrDeniedPathFragment) {
			t.Fatalf("expected ErrDeniedPathFragment for master.key, got %v", err)
		}
	})

	t.Run("denies case-variant fragment (.ENV)", func(t *testing.T) {
		err := read(t, upperEnv)
		if !errors.Is(err, ErrDeniedPathFragment) {
			t.Fatalf("expected ErrDeniedPathFragment for .ENV, got %v", err)
		}
	})

	t.Run("denies symlink resolving to denied path", func(t *testing.T) {
		err := read(t, innocentLink)
		if !errors.Is(err, ErrDeniedPathFragment) {
			t.Fatalf("expected ErrDeniedPathFragment via symlink, got %v", err)
		}
	})

	t.Run("denies cwd-relative path via canonical resolution", func(t *testing.T) {
		// A worker reading ".env" from its repo-root cwd supplies a path with
		// no leading slash, so the supplied-path check alone cannot see the
		// "/.env" fragment; the symlink-resolved absolute canonical target
		// must still be denied (#986 B-8).
		t.Chdir(scope)
		_, err := Read(ctx, ReadOptions{Path: ".env", ScopeRoot: "."})
		if !errors.Is(err, ErrDeniedPathFragment) {
			t.Fatalf("expected ErrDeniedPathFragment for relative .env, got %v", err)
		}
	})

	t.Run("allowed reads still pass", func(t *testing.T) {
		res, err := Read(ctx, ReadOptions{Path: notes, ScopeRoot: scope})
		if err != nil {
			t.Fatalf("expected plain in-scope read to succeed, got %v", err)
		}
		if !strings.Contains(res.Content, "public team notes") {
			t.Fatalf("unexpected content: %q", res.Content)
		}
	})

	t.Run("non-denied dotfiles still readable", func(t *testing.T) {
		if _, err := Read(ctx, ReadOptions{Path: gitignore, ScopeRoot: scope}); err != nil {
			t.Fatalf("expected .gitignore read to succeed, got %v", err)
		}
	})
}
