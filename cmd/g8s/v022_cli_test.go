package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFleetGCPrunesMergedBranchesAndPreservesUnmerged(t *testing.T) {
	ws := t.TempDir()
	repo := filepath.Join(ws, "sample-repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
		}
	}

	runGit("init", "-b", "main")
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("# sample\n"), 0o644)
	runGit("add", "README.md")
	runGit("commit", "-m", "initial")

	// Create merged branch
	runGit("checkout", "-b", "feat/merged-branch")
	runGit("checkout", "main")

	// Create unmerged branch with a unique commit
	runGit("checkout", "-b", "feat/unmerged-branch")
	_ = os.WriteFile(filepath.Join(repo, "unmerged.txt"), []byte("work\n"), 0o644)
	runGit("add", "unmerged.txt")
	runGit("commit", "-m", "unmerged work")
	runGit("checkout", "main")

	report := gcFleetRepository(repo, false)
	if len(report.PrunedBranches) != 1 || report.PrunedBranches[0] != "feat/merged-branch" {
		t.Fatalf("expected feat/merged-branch to be pruned, got %+v", report)
	}
	if len(report.SkippedBranches) != 1 || report.SkippedBranches[0] != "feat/unmerged-branch" {
		t.Fatalf("expected feat/unmerged-branch to be preserved, got %+v", report)
	}
}

func TestParseContextKeyValuePairs(t *testing.T) {
	m := parseContextKeyValuePairs("quota_mode=saving,provider=9router")
	if m["quota_mode"] != "saving" || m["provider"] != "9router" {
		t.Fatalf("unexpected parsed map: %+v", m)
	}
}

func TestBatchEditCLIFileExecution(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "one.txt")
	f2 := filepath.Join(dir, "two.txt")
	_ = os.WriteFile(f1, []byte("alpha=1\n"), 0o644)
	_ = os.WriteFile(f2, []byte("beta=1\n"), 0o644)

	batchSpec := `[
		{"path": "` + filepath.ToSlash(f1) + `", "old_string": "alpha=1", "new_string": "alpha=2"},
		{"path": "` + filepath.ToSlash(f2) + `", "old_string": "beta=1", "new_string": "beta=2"}
	]`
	batchPath := filepath.Join(dir, "batch.json")
	_ = os.WriteFile(batchPath, []byte(batchSpec), 0o644)

	res, err := executeBatchEditFromFile(batchPath, dir, nil)
	if err != nil || len(res) != 2 {
		t.Fatalf("executeBatchEditFromFile failed: res=%v err=%v", res, err)
	}
	b1, _ := os.ReadFile(f1)
	b2, _ := os.ReadFile(f2)
	if !strings.Contains(string(b1), "alpha=2") || !strings.Contains(string(b2), "beta=2") {
		t.Fatalf("expected updated files, got %q and %q", string(b1), string(b2))
	}
}
