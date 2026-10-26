package cleanup

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitT runs git in dir with a hermetic identity and fails the test on error.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out.String())
	}
	return strings.TrimSpace(out.String())
}

func commitFile(t *testing.T, dir, name, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", name)
	gitT(t, dir, "commit", "-q", "-m", msg)
}

func branchExists(t *testing.T, dir, branch string) bool {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return cmd.Run() == nil
}

// orphanBranchFixture builds a repo reproducing the DELTA-40 R4 field state:
//
//   - agy/sup-merged:    tip is an ancestor of main (fast-forward merged)
//   - agy/sup-squashed:  commit cherry-picked onto main (patch-equivalent)
//   - agy/sup-unmerged:  unique commit never merged — MUST survive
//   - feature/keep:      merged but not matching agy/sup-* — MUST survive
//   - HEAD checked out on "lagging", a branch behind main, so `git branch -d`
//     (judged against HEAD) refuses the merged branches.
func orphanBranchFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "-b", "main")
	commitFile(t, dir, "a.txt", "a\n", "base")
	gitT(t, dir, "branch", "lagging")

	gitT(t, dir, "checkout", "-q", "-b", "agy/sup-merged")
	commitFile(t, dir, "m.txt", "m\n", "merged work")
	gitT(t, dir, "checkout", "-q", "main")
	gitT(t, dir, "merge", "-q", "--ff-only", "agy/sup-merged")

	gitT(t, dir, "checkout", "-q", "-b", "agy/sup-squashed")
	commitFile(t, dir, "s.txt", "s\n", "squashed work")
	sha := gitT(t, dir, "rev-parse", "HEAD")
	gitT(t, dir, "checkout", "-q", "main")
	commitFile(t, dir, "other.txt", "o\n", "unrelated main commit")
	gitT(t, dir, "cherry-pick", sha)

	gitT(t, dir, "checkout", "-q", "-b", "agy/sup-unmerged", "lagging")
	commitFile(t, dir, "u.txt", "u\n", "unmerged work")

	gitT(t, dir, "checkout", "-q", "-b", "feature/keep", "main")
	gitT(t, dir, "checkout", "-q", "lagging")
	return dir
}

func TestOrphanBranchSweepMergedAgainstBaseNotHEAD(t *testing.T) {
	cases := []struct {
		name    string
		dryRun  bool
		wantDel map[string]bool // branch -> expected deleted (apply) / listed (dry-run)
	}{
		{
			name:   "dry-run lists merged and patch-equivalent only",
			dryRun: true,
			wantDel: map[string]bool{
				"agy/sup-merged": true, "agy/sup-squashed": true,
				"agy/sup-unmerged": false, "feature/keep": false,
			},
		},
		{
			name:   "apply deletes merged and patch-equivalent, keeps unmerged",
			dryRun: false,
			wantDel: map[string]bool{
				"agy/sup-merged": true, "agy/sup-squashed": true,
				"agy/sup-unmerged": false, "feature/keep": false,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := orphanBranchFixture(t)
			report, err := RunCleanupSweep(context.Background(), CleanupConfig{
				RepoDir:   dir,
				Targets:   []string{TargetOrphanBranch},
				DryRun:    tc.dryRun,
				GitRunner: &DefaultCleanupGitRunner{},
			})
			if err != nil {
				t.Fatalf("RunCleanupSweep: %v", err)
			}
			listed := map[string]CleanupItem{}
			for _, it := range report.Items {
				listed[it.ID] = it
			}
			for branch, want := range tc.wantDel {
				it, ok := listed[branch]
				if ok != want {
					t.Errorf("%s listed=%v want %v (items=%+v)", branch, ok, want, report.Items)
					continue
				}
				if tc.dryRun {
					if ok && it.Action != "would_delete" {
						t.Errorf("%s action=%q want would_delete", branch, it.Action)
					}
					if !branchExists(t, dir, branch) {
						t.Errorf("dry-run must not delete %s", branch)
					}
					continue
				}
				if ok && it.Action != "deleted" {
					t.Errorf("%s action=%q err=%q want deleted", branch, it.Action, it.Error)
				}
				if exists := branchExists(t, dir, branch); exists == want {
					t.Errorf("%s exists=%v after apply, want deleted=%v", branch, exists, want)
				}
			}
		})
	}
}

func TestBranchMergeStateClassification(t *testing.T) {
	dir := orphanBranchFixture(t)
	g := &DefaultCleanupGitRunner{}
	cases := []struct {
		branch string
		want   string
	}{
		{"agy/sup-merged", MergeStateAncestor},
		{"agy/sup-squashed", MergeStatePatchEquivalent},
		{"agy/sup-unmerged", MergeStateUnmerged},
		{"lagging", MergeStateAncestor},
	}
	for _, tc := range cases {
		t.Run(tc.branch, func(t *testing.T) {
			state, base, err := g.BranchMergeState(context.Background(), dir, tc.branch)
			if err != nil {
				t.Fatalf("BranchMergeState: %v", err)
			}
			if base != "main" {
				t.Errorf("base=%q want main", base)
			}
			if state != tc.want {
				t.Errorf("state=%q want %q", state, tc.want)
			}
		})
	}
	if _, _, err := g.BranchMergeState(context.Background(), dir, "does-not-exist"); err == nil {
		t.Error("missing branch must return an error (never classified as merged)")
	}
}

func TestIntegrationBaseFallbacks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for _, tc := range []struct{ branch, want string }{{"master", "master"}, {"trunk", "HEAD"}} {
		t.Run(tc.branch, func(t *testing.T) {
			dir := t.TempDir()
			gitT(t, dir, "init", "-q", "-b", tc.branch)
			commitFile(t, dir, "a.txt", "a\n", "base")
			if got := integrationBase(context.Background(), dir); got != tc.want {
				t.Errorf("integrationBase=%q want %q", got, tc.want)
			}
		})
	}
}
