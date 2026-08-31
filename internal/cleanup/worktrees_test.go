package cleanup

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func samePath(p1, p2 string) bool {
	c1 := filepath.Clean(p1)
	c2 := filepath.Clean(p2)
	if strings.EqualFold(c1, c2) {
		return true
	}
	if strings.EqualFold(strings.TrimPrefix(c1, "/private"), strings.TrimPrefix(c2, "/private")) {
		return true
	}
	r1, err1 := filepath.EvalSymlinks(c1)
	if err1 != nil {
		parent1, errP1 := filepath.EvalSymlinks(filepath.Dir(c1))
		if errP1 == nil {
			r1 = filepath.Join(parent1, filepath.Base(c1))
			err1 = nil
		}
	}
	r2, err2 := filepath.EvalSymlinks(c2)
	if err2 != nil {
		parent2, errP2 := filepath.EvalSymlinks(filepath.Dir(c2))
		if errP2 == nil {
			r2 = filepath.Join(parent2, filepath.Base(c2))
			err2 = nil
		}
	}
	if err1 == nil && err2 == nil && strings.EqualFold(filepath.Clean(r1), filepath.Clean(r2)) {
		return true
	}
	return false
}

func TestParseWorktreeListPorcelain(t *testing.T) {
	raw := `worktree /path/to/main
HEAD a35d205182fac282310d5add78ff0cc27e2fc7eb
branch refs/heads/main

worktree /path/to/wt-1
HEAD bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
branch refs/heads/agy/sup-1788003093729656000-5-sub-2

worktree /path/to/wt-detached
HEAD cccccccccccccccccccccccccccccccccccccccc
detached

worktree /path/to/wt-bare
bare

worktree /path/to/wt-locked
HEAD dddddddddddddddddddddddddddddddddddddddd
branch refs/heads/agy/sup-123-sub-1
locked reason for lock
prunable gitdir file points to non-existent location
`

	entries := ParseWorktreeListPorcelain(raw)
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}

	// 1st entry: main
	if !entries[0].IsMain || entries[0].Branch != "main" || !samePath(entries[0].Path, "/path/to/main") {
		t.Errorf("entry 0 mismatch: %+v", entries[0])
	}

	// 2nd entry: agy subagent
	if entries[1].IsMain || entries[1].Branch != "agy/sup-1788003093729656000-5-sub-2" || !samePath(entries[1].Path, "/path/to/wt-1") {
		t.Errorf("entry 1 mismatch: %+v", entries[1])
	}

	// 3rd entry: detached
	if !entries[2].Detached || entries[2].Branch != "" {
		t.Errorf("entry 2 mismatch: %+v", entries[2])
	}

	// 4th entry: bare
	if !entries[3].IsBare {
		t.Errorf("entry 3 mismatch: %+v", entries[3])
	}

	// 5th entry: locked and prunable
	if !entries[4].Locked || !entries[4].Prunable || entries[4].Branch != "agy/sup-123-sub-1" {
		t.Errorf("entry 4 mismatch: %+v", entries[4])
	}
}

func TestSubagentBranchPattern(t *testing.T) {
	tests := []struct {
		branch string
		match  bool
	}{
		{"agy/sup-1788003093729656000-5-sub-2", true},
		{"agy/sup-1-sub-49", true},
		{"agy/sup-abc-sub-0", true},
		{"agy/sup-test-branch-sub-123", true},
		{"main", false},
		{"master", false},
		{"feat/debt26-cleanup-worktrees", false},
		{"agy/other-branch", false},
		{"agy/sup-1-sub-", false},
		{"agy/sup-sub", false},
		{"sup-1-sub-1", false},
		{"refs/heads/agy/sup-1-sub-1", false},
	}

	for _, tt := range tests {
		got := SubagentBranchPattern.MatchString(tt.branch)
		if got != tt.match {
			t.Errorf("SubagentBranchPattern.MatchString(%q) = %v, want %v", tt.branch, got, tt.match)
		}
	}
}

type mockGitRunner struct {
	porcelainOutput string
	listErr         error
	statusOutputs   map[string]string // wtPath -> output
	statusErr       map[string]error
	removedPaths    []string
	removeErr       error
	pruneCalled     bool
	pruneErr        error
}

func (m *mockGitRunner) WorktreeListPorcelain(_ context.Context, _ string) (string, error) {
	if m.listErr != nil {
		return "", m.listErr
	}
	return m.porcelainOutput, nil
}

func (m *mockGitRunner) WorktreeRemove(_ context.Context, _, wtPath string, force bool) error {
	if m.removeErr != nil {
		return m.removeErr
	}
	m.removedPaths = append(m.removedPaths, wtPath)
	return nil
}

func (m *mockGitRunner) WorktreePrune(_ context.Context, _ string) (string, error) {
	m.pruneCalled = true
	if m.pruneErr != nil {
		return "", m.pruneErr
	}
	return "pruning /path/to/wt", nil
}

func (m *mockGitRunner) StatusPorcelain(_ context.Context, wtPath string) (string, error) {
	if m.statusErr != nil {
		if err, ok := m.statusErr[wtPath]; ok {
			return "", err
		}
	}
	if out, ok := m.statusOutputs[wtPath]; ok {
		return out, nil
	}
	return "", nil
}

func TestCleanupWorktrees_Unit(t *testing.T) {
	tempDir := t.TempDir()
	mainDir := filepath.Join(tempDir, "repo")
	wtOld := filepath.Join(tempDir, "wt-old")
	wtNew := filepath.Join(tempDir, "wt-new")
	wtDirty := filepath.Join(tempDir, "wt-dirty")
	wtOtherBranch := filepath.Join(tempDir, "wt-other")

	for _, dir := range []string{mainDir, wtOld, wtNew, wtDirty, wtOtherBranch} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	// Main repo has .git directory
	if err := os.MkdirAll(filepath.Join(mainDir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir main .git: %v", err)
	}

	fixedNow := time.Date(2026, 8, 29, 18, 0, 0, 0, time.UTC)
	mockClock := func() time.Time { return fixedNow }

	// Set timestamps: wtOld and wtDirty are 2 hours old, wtNew is 10 minutes old
	twoHoursAgo := fixedNow.Add(-2 * time.Hour)
	tenMinsAgo := fixedNow.Add(-10 * time.Minute)

	if err := os.Chtimes(wtOld, twoHoursAgo, twoHoursAgo); err != nil {
		t.Fatalf("chtimes wtOld: %v", err)
	}
	if err := os.Chtimes(wtDirty, twoHoursAgo, twoHoursAgo); err != nil {
		t.Fatalf("chtimes wtDirty: %v", err)
	}
	if err := os.Chtimes(wtNew, tenMinsAgo, tenMinsAgo); err != nil {
		t.Fatalf("chtimes wtNew: %v", err)
	}
	if err := os.Chtimes(wtOtherBranch, twoHoursAgo, twoHoursAgo); err != nil {
		t.Fatalf("chtimes wtOtherBranch: %v", err)
	}

	porcelain := fmt.Sprintf(`worktree %s
HEAD 1111111111111111111111111111111111111111
branch refs/heads/agy/sup-main-sub-0

worktree %s
HEAD 2222222222222222222222222222222222222222
branch refs/heads/agy/sup-old-sub-1

worktree %s
HEAD 3333333333333333333333333333333333333333
branch refs/heads/agy/sup-new-sub-2

worktree %s
HEAD 4444444444444444444444444444444444444444
branch refs/heads/agy/sup-dirty-sub-3

worktree %s
HEAD 5555555555555555555555555555555555555555
branch refs/heads/feat/some-feature
`, mainDir, wtOld, wtNew, wtDirty, wtOtherBranch)

	t.Run("dry-run does not remove or prune", func(t *testing.T) {
		runner := &mockGitRunner{
			porcelainOutput: porcelain,
			statusOutputs: map[string]string{
				wtDirty: " M modified.go\n",
			},
		}

		var buf bytes.Buffer
		report, err := CleanupWorktrees(context.Background(), CleanupOptions{
			RepoDir:   mainDir,
			OlderThan: 1 * time.Hour,
			DryRun:    true,
			Clock:     mockClock,
			Runner:    runner,
			Writer:    &buf,
		})
		if err != nil {
			t.Fatalf("CleanupWorktrees dry-run failed: %v", err)
		}

		if len(runner.removedPaths) != 0 {
			t.Fatalf("dry-run should not call remove, got %v", runner.removedPaths)
		}
		if runner.pruneCalled {
			t.Fatalf("dry-run should not call prune")
		}
		if len(report.Removed) != 1 || !samePath(report.Removed[0].Path, wtOld) {
			t.Fatalf("expected wtOld to be candidate, got %+v", report.Removed)
		}
		if !strings.Contains(buf.String(), "[dry-run]") {
			t.Fatalf("expected output to contain [dry-run], got %s", buf.String())
		}
	})

	t.Run("real run removes only clean old subagent worktrees", func(t *testing.T) {
		runner := &mockGitRunner{
			porcelainOutput: porcelain,
			statusOutputs: map[string]string{
				wtDirty: " M modified.go\n",
			},
		}

		var buf bytes.Buffer
		report, err := CleanupWorktrees(context.Background(), CleanupOptions{
			RepoDir:   mainDir,
			OlderThan: 1 * time.Hour,
			DryRun:    false,
			Clock:     mockClock,
			Runner:    runner,
			Writer:    &buf,
		})
		if err != nil {
			t.Fatalf("CleanupWorktrees failed: %v", err)
		}

		// Verify wtOld was removed
		if len(runner.removedPaths) != 1 || !samePath(runner.removedPaths[0], wtOld) {
			t.Fatalf("expected removedPaths = [%s], got %v", wtOld, runner.removedPaths)
		}
		if !runner.pruneCalled {
			t.Fatalf("expected prune to be called")
		}
		if len(report.Removed) != 1 || !samePath(report.Removed[0].Path, wtOld) {
			t.Fatalf("expected report.Removed = [%s], got %+v", wtOld, report.Removed)
		}

		// Verify main was skipped
		foundMainSkipped := false
		for _, s := range report.Skipped {
			if samePath(s.Path, mainDir) && s.Reason == "main worktree" {
				foundMainSkipped = true
			}
		}
		if !foundMainSkipped {
			t.Fatalf("expected main worktree to be skipped with reason 'main worktree'")
		}

		// Verify dirty was skipped
		foundDirtySkipped := false
		for _, s := range report.Skipped {
			if samePath(s.Path, wtDirty) && s.Reason == "uncommitted changes present" {
				foundDirtySkipped = true
			}
		}
		if !foundDirtySkipped {
			t.Fatalf("expected dirty worktree to be skipped with reason 'uncommitted changes present'")
		}

		// Verify young wtNew was skipped
		foundNewSkipped := false
		for _, s := range report.Skipped {
			if samePath(s.Path, wtNew) {
				foundNewSkipped = true
			}
		}
		if !foundNewSkipped {
			t.Fatalf("expected wtNew to be skipped because of age")
		}
	})
}
