package cleanup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tamld/g8s/internal/pathutil"
)

// #465: the orphan-dir sweep must default to THIS instance's state-dir
// worktree root, not the host-global TMPDIR — a foreign session's preserved
// worktrees under TMPDIR were being RemoveAll'd as "unregistered".
func TestSweepOrphanWorktreeDirs_DefaultsToStateDirRoot(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("G8S_STATE_DIR", stateRoot)

	ownUnregistered := filepath.Join(pathutil.DefaultStateDir(), "worktrees", "wt-orphan")
	if err := os.MkdirAll(ownUnregistered, 0o755); err != nil {
		t.Fatalf("mkdir own orphan: %v", err)
	}
	legacyTMP := filepath.Join(os.TempDir(), "g8s-worktrees", "wt-foreign-session")
	if err := os.MkdirAll(legacyTMP, 0o755); err != nil {
		t.Fatalf("mkdir foreign tmp tree: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(os.TempDir(), "g8s-worktrees", "wt-foreign-session")) })

	cfg := CleanupConfig{
		RepoDir:   t.TempDir(),
		GitRunner: &MockCleanupGitRunner{PorcelainOutput: ""},
		DryRun:    false,
	}

	items, err := sweepOrphanWorktreeDirs(context.Background(), cfg)
	if err != nil {
		t.Fatalf("sweepOrphanWorktreeDirs: %v", err)
	}

	if _, err := os.Stat(ownUnregistered); err == nil {
		t.Errorf("own unregistered dir under state root must be removed")
	}
	if _, err := os.Stat(legacyTMP); err != nil {
		t.Errorf("foreign tree under host TMPDIR must be untouched by the default sweep")
	}

	removed := false
	for _, it := range items {
		if it.Target == TargetOrphanDir && it.ID == ownUnregistered && it.Action == "removed" {
			removed = true
		}
	}
	if !removed {
		t.Errorf("expected a removed item for %s, got %+v", ownUnregistered, items)
	}
}

// The explicit --worktree-base-dir override keeps working for one-time
// legacy TMPDIR migration.
func TestSweepOrphanWorktreeDirs_ExplicitBaseDirOverride(t *testing.T) {
	legacyBase := t.TempDir()
	legacyOrphan := filepath.Join(legacyBase, "wt-legacy")
	if err := os.MkdirAll(legacyOrphan, 0o755); err != nil {
		t.Fatalf("mkdir legacy orphan: %v", err)
	}

	cfg := CleanupConfig{
		RepoDir:         t.TempDir(),
		GitRunner:       &MockCleanupGitRunner{PorcelainOutput: ""},
		DryRun:          false,
		WorktreeBaseDir: legacyBase,
	}

	if _, err := sweepOrphanWorktreeDirs(context.Background(), cfg); err != nil {
		t.Fatalf("sweepOrphanWorktreeDirs: %v", err)
	}
	if _, err := os.Stat(legacyOrphan); err == nil {
		t.Errorf("orphan under explicit --worktree-base-dir must be removed")
	}
}
