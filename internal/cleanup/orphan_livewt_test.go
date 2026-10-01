package cleanup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tamld/g8s/internal/pathutil"
)

// #477 follow-up: the orphan-dir sweep's registration check can miss on
// Windows (8.3 short names, case variants) — a LIVE worktree registered in
// its own repo then reads as "unregistered" here and was RemoveAll'd. A
// directory containing a `.git` entry is a live worktree of some repo and
// must never be removed by this sweep, whatever the registry comparison
// says (same rule as Pool.Acquire's collision refusal).
func TestSweepOrphanWorktreeDirs_NeverRemovesLiveWorktree(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("G8S_STATE_DIR", stateRoot)

	base := filepath.Join(pathutil.DefaultStateDir(), "worktrees")
	liveWT := filepath.Join(base, "wt-preserved")
	if err := os.MkdirAll(filepath.Join(liveWT, "deep"), 0o755); err != nil {
		t.Fatalf("mkdir live wt: %v", err)
	}
	// The worktree marker: a .git FILE (worktrees carry a pointer file, not
	// a directory).
	if err := os.WriteFile(filepath.Join(liveWT, ".git"), []byte("gitdir: /elsewhere/.git/worktrees/wt-preserved\n"), 0o644); err != nil {
		t.Fatalf("write .git marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(liveWT, "deep", "deliverable.txt"), []byte("uncommitted deliverable"), 0o644); err != nil {
		t.Fatalf("write deliverable: %v", err)
	}
	// Genuine debris next to it must still go.
	debris := filepath.Join(base, "wt-debris")
	if err := os.MkdirAll(debris, 0o755); err != nil {
		t.Fatalf("mkdir debris: %v", err)
	}

	cfg := CleanupConfig{
		RepoDir:   t.TempDir(), // a DIFFERENT repo: the live wt reads as unregistered here
		GitRunner: &MockCleanupGitRunner{PorcelainOutput: ""},
		DryRun:    false,
	}

	items, err := sweepOrphanWorktreeDirs(context.Background(), cfg)
	if err != nil {
		t.Fatalf("sweepOrphanWorktreeDirs: %v", err)
	}

	if _, err := os.Stat(filepath.Join(liveWT, "deep", "deliverable.txt")); err != nil {
		t.Fatalf("live worktree deliverable was destroyed: %v", err)
	}
	if _, err := os.Stat(debris); err == nil {
		t.Errorf("genuine debris must still be removed")
	}
	skipped := false
	for _, it := range items {
		if it.ID == liveWT && it.Action == "skipped" {
			skipped = true
		}
	}
	if !skipped {
		t.Errorf("expected a skipped audit item for the live worktree, got %+v", items)
	}
}
