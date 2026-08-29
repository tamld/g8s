package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type mockGitRunner struct {
	worktreeListOut string
	worktreeListErr error
	statusOuts      map[string]string
	statusErrs      map[string]error
	removedPaths    []string
	removeErrs      map[string]error
	prunedOut       string
	pruneErr        error
}

func (m *mockGitRunner) WorktreeListPorcelain(ctx context.Context, repoDir string) (string, error) {
	if m.worktreeListErr != nil {
		return "", m.worktreeListErr
	}
	return m.worktreeListOut, nil
}

func (m *mockGitRunner) WorktreeRemove(ctx context.Context, repoDir, wtPath string) error {
	if err, ok := m.removeErrs[wtPath]; ok && err != nil {
		return err
	}
	m.removedPaths = append(m.removedPaths, wtPath)
	return nil
}

func (m *mockGitRunner) WorktreePrune(ctx context.Context, repoDir string) (string, error) {
	if m.pruneErr != nil {
		return "", m.pruneErr
	}
	return m.prunedOut, nil
}

func (m *mockGitRunner) StatusPorcelain(ctx context.Context, wtPath string) (string, error) {
	if err, ok := m.statusErrs[wtPath]; ok && err != nil {
		return "", err
	}
	if out, ok := m.statusOuts[wtPath]; ok {
		return out, nil
	}
	return "", nil
}

func TestCleanupWorktrees_SubagentRemovalAndDryRun(t *testing.T) {
	tempDir := t.TempDir()
	mainRepo := filepath.Join(tempDir, "main-repo")
	staleWT := filepath.Join(tempDir, "wt-stale")
	freshWT := filepath.Join(tempDir, "wt-fresh")
	dirtyWT := filepath.Join(tempDir, "wt-dirty")
	otherWT := filepath.Join(tempDir, "wt-other")

	for _, d := range []string{mainRepo, staleWT, freshWT, dirtyWT, otherWT} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	twoHoursAgo := now.Add(-2 * time.Hour)
	tenMinutesAgo := now.Add(-10 * time.Minute)

	// Set mod times
	if err := os.Chtimes(staleWT, twoHoursAgo, twoHoursAgo); err != nil {
		t.Fatalf("chtimes staleWT: %v", err)
	}
	if err := os.Chtimes(dirtyWT, twoHoursAgo, twoHoursAgo); err != nil {
		t.Fatalf("chtimes dirtyWT: %v", err)
	}
	if err := os.Chtimes(freshWT, tenMinutesAgo, tenMinutesAgo); err != nil {
		t.Fatalf("chtimes freshWT: %v", err)
	}
	if err := os.Chtimes(otherWT, twoHoursAgo, twoHoursAgo); err != nil {
		t.Fatalf("chtimes otherWT: %v", err)
	}

	porcelainOutput := fmt.Sprintf(`worktree %s
HEAD aaaa1111
branch refs/heads/main

worktree %s
HEAD bbbb2222
branch refs/heads/agy/sup-12345-1-sub-1

worktree %s
HEAD cccc3333
branch refs/heads/agy/sup-12345-1-sub-2

worktree %s
HEAD dddd4444
branch refs/heads/agy/sup-12345-1-sub-3

worktree %s
HEAD eeee5555
branch refs/heads/feat/some-feature
`, mainRepo, staleWT, freshWT, dirtyWT, otherWT)

	runner := &mockGitRunner{
		worktreeListOut: porcelainOutput,
		statusOuts: map[string]string{
			staleWT: "",
			freshWT: "",
			dirtyWT: " M modified_file.go\n",
			otherWT: "",
		},
		prunedOut: "pruned 1 dead worktree",
	}

	// 1. Dry run
	var buf bytes.Buffer
	dryReport, err := CleanupWorktrees(context.Background(), CleanupOptions{
		RepoDir:   mainRepo,
		OlderThan: 1 * time.Hour,
		DryRun:    true,
		Clock:     func() time.Time { return now },
		Runner:    runner,
		Writer:    &buf,
	})
	if err != nil {
		t.Fatalf("CleanupWorktrees dry-run failed: %v", err)
	}

	if len(dryReport.Removed) != 1 || dryReport.Removed[0].Path != staleWT {
		t.Errorf("dryRun expected 1 removed candidate (%s), got: %+v", staleWT, dryReport.Removed)
	}
	if len(runner.removedPaths) != 0 {
		t.Errorf("dryRun should not actually remove any paths, removed: %v", runner.removedPaths)
	}

	// 2. Real cleanup
	buf.Reset()
	realReport, err := CleanupWorktrees(context.Background(), CleanupOptions{
		RepoDir:   mainRepo,
		OlderThan: 1 * time.Hour,
		DryRun:    false,
		Clock:     func() time.Time { return now },
		Runner:    runner,
		Writer:    &buf,
	})
	if err != nil {
		t.Fatalf("CleanupWorktrees real run failed: %v", err)
	}

	if len(realReport.Removed) != 1 || realReport.Removed[0].Path != staleWT {
		t.Errorf("real run expected 1 removed worktree (%s), got: %+v", staleWT, realReport.Removed)
	}
	if len(runner.removedPaths) != 1 || runner.removedPaths[0] != staleWT {
		t.Errorf("runner removedPaths = %v, want [%s]", runner.removedPaths, staleWT)
	}
}
