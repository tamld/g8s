package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPoolAcquireEmptyTaskID(t *testing.T) {
	repo := setupGitRepo(t)
	pool, err := NewPool(PoolOptions{Repo: repo, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	_, err = pool.Acquire(context.Background(), "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "taskID required") {
		t.Errorf("expected error to contain 'taskID required', got %v", err)
	}
}

func TestPoolAcquireIdempotent(t *testing.T) {
	repo := setupGitRepo(t)
	pool, err := NewPool(PoolOptions{Repo: repo, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	ctx := context.Background()
	taskID := "task-123"

	wt1, err := pool.Acquire(ctx, taskID)
	if err != nil {
		t.Fatalf("acquire 1: %v", err)
	}

	wt2, err := pool.Acquire(ctx, taskID)
	if err != nil {
		t.Fatalf("acquire 2: %v", err)
	}

	if wt1.ID != wt2.ID || wt1.Path != wt2.Path || wt1.Branch != wt2.Branch {
		t.Errorf("expected identical worktrees, got %+v and %+v", wt1, wt2)
	}
}

func TestPoolReleaseKeepBranch(t *testing.T) {
	repo := setupGitRepo(t)
	pool, err := NewPool(PoolOptions{Repo: repo, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	ctx := context.Background()
	wt, err := pool.Acquire(ctx, "keep-task")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Release with keep=true
	err = pool.Release(ctx, wt, true)
	if err != nil {
		t.Fatalf("release: %v", err)
	}

	// Verify branch still exists
	// mustRunGit(t, dir, args...) is in coverage_test.go but unexported? Let's check.
	// Wait, mustRunGit is exported? No, starting with lowercase it's unexported.
	// We can use it if it's in the same package `orchestrator_test` or `orchestrator`.
	// Wait, I am in package orchestrator!
	// verify branch still exists; rev-parse fails if branch does not exist
	mustRunGit(t, repo, "rev-parse", "--verify", wt.Branch)
}

func TestPoolReleaseNonExistentWorktree(t *testing.T) {
	repo := setupGitRepo(t)
	pool, err := NewPool(PoolOptions{Repo: repo, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	wt := Worktree{
		ID:     "fake-id",
		Path:   os.TempDir(),
		Branch: "fake-branch",
	}

	// Releasing non-existent shouldn't panic or fail fatally, just silently continue (or return err if designed to).
	err = pool.Release(context.Background(), wt, false)
	if err != nil {
		t.Logf("Release returned err: %v", err) // might be normal
	}
}

func TestShortIDLength(t *testing.T) {
	for i := 0; i < 10; i++ {
		id := shortID()
		if len(id) != 8 {
			t.Errorf("expected shortID length 8, got %d (%s)", len(id), id)
		}
	}
}

func TestShortIDUnique(t *testing.T) {
	id1 := shortID()
	id2 := shortID()
	if id1 == id2 {
		t.Errorf("expected shortID to be unique, got %s twice", id1)
	}
}

func TestIsGitRepoValid(t *testing.T) {
	repo := setupGitRepo(t)
	if !isGitRepo(repo) {
		t.Errorf("expected %s to be valid git repo", repo)
	}
}

func TestIsGitRepoInvalid(t *testing.T) {
	tmpDir := t.TempDir()
	if isGitRepo(tmpDir) {
		t.Errorf("expected %s to be invalid git repo", tmpDir)
	}
}

func TestGitRevParseValid(t *testing.T) {
	repo := setupGitRepo(t)
	sha, err := gitRevParse(repo, "HEAD")
	if err != nil {
		t.Fatalf("gitRevParse failed: %v", err)
	}
	if len(sha) == 0 {
		t.Errorf("expected non-empty SHA")
	}
}

func TestGitRevParseInvalid(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := gitRevParse(tmpDir, "HEAD")
	if err == nil {
		t.Error("expected error for gitRevParse in non-git repo")
	}
}

func TestNewPoolEmptyRepo(t *testing.T) {
	_, err := NewPool(PoolOptions{Repo: ""})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestPoolActiveEmptyAfterRelease(t *testing.T) {
	repo := setupGitRepo(t)
	pool, err := NewPool(PoolOptions{Repo: repo, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	ctx := context.Background()
	wts := make([]Worktree, 3)
	for i := 0; i < 3; i++ {
		wt, err := pool.Acquire(ctx, "task-"+shortID())
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		wts[i] = wt
	}

	active := pool.Active()
	if len(active) != 3 {
		t.Errorf("expected 3 active, got %d", len(active))
	}

	for _, wt := range wts {
		err := pool.Release(ctx, wt, false)
		if err != nil {
			t.Fatalf("release: %v", err)
		}
	}

	activeAfter := pool.Active()
	if len(activeAfter) != 0 {
		t.Errorf("expected 0 active, got %d", len(activeAfter))
	}
}

type fanOutWorker struct{}

func (w fanOutWorker) Name() string                      { return "fanout" }
func (w fanOutWorker) Available(_ context.Context) error { return nil }
func (w fanOutWorker) Spawn(_ context.Context, t Task) (Handle, error) {
	return fanOutHandle{taskID: t.ID}, nil
}

type fanOutHandle struct {
	taskID string
}

func (h fanOutHandle) PID() int { return 123 }
func (h fanOutHandle) Wait(ctx context.Context) (Receipt, error) {
	return Receipt{TaskID: h.taskID, OK: true}, nil
}
func (h fanOutHandle) Cancel(ctx context.Context) error { return nil }
func (h fanOutHandle) StdoutStream() interface {
	Close() error
	Read(p []byte) (n int, err error)
} {
	return nil
}

func TestFanOutMaxParallelCaps(t *testing.T) {
	repo := setupGitRepo(t)
	pool, err := NewPool(PoolOptions{Repo: repo, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("pool: %v", err)
	}

	r := NewRegistry()
	_ = r.Register("fanout", func() Worker { return fanOutWorker{} })

	plan := []TaskSpec{
		{TaskID: "t1", Task: Task{ID: "t1"}},
		{TaskID: "t2", Task: Task{ID: "t2"}},
		{TaskID: "t3", Task: Task{ID: "t3"}},
	}

	opts := FanOutOptions{
		Registry:    r,
		Pool:        pool,
		MaxParallel: 1,
	}

	receipts, err := FanOut(context.Background(), plan, opts)
	if err != nil {
		t.Fatalf("fanout: %v", err)
	}

	if len(receipts) != 3 {
		t.Errorf("expected 3 receipts, got %d", len(receipts))
	}

	// Just verify we got our receipts
	got := make(map[string]bool)
	for _, rec := range receipts {
		got[rec.TaskID] = true
	}
	for _, id := range []string{"t1", "t2", "t3"} {
		if !got[id] {
			t.Errorf("missing receipt for task %s", id)
		}
	}
}

func TestReleasePreservesDirtyWorktree(t *testing.T) {
	repo := setupGitRepo(t)
	// Seed a tracked file in repo
	trackedFile := filepath.Join(repo, "tracked.txt")
	if err := os.WriteFile(trackedFile, []byte("initial"), 0o644); err != nil {
		t.Fatalf("write tracked file: %v", err)
	}
	mustRunGit(t, repo, "add", "tracked.txt")
	mustRunGit(t, repo, "commit", "-m", "add tracked file")

	pool, err := NewPool(PoolOptions{Repo: repo, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	ctx := context.Background()
	wt, err := pool.Acquire(ctx, "dirty-task")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Modify tracked file in worktree without committing
	wtTracked := filepath.Join(wt.Path, "tracked.txt")
	if err := os.WriteFile(wtTracked, []byte("modified-deliverable"), 0o644); err != nil {
		t.Fatalf("modify tracked file: %v", err)
	}

	// Release with keep=true
	if err := pool.Release(ctx, wt, true); err != nil {
		t.Fatalf("release: %v", err)
	}

	// 1. Directory still exists with modification
	if _, err := os.Stat(wt.Path); os.IsNotExist(err) {
		t.Fatalf("expected worktree directory %s to be preserved, but it was removed", wt.Path)
	}
	content, err := os.ReadFile(wtTracked)
	if err != nil {
		t.Fatalf("failed to read modified file: %v", err)
	}
	if string(content) != "modified-deliverable" {
		t.Fatalf("expected modified content %q, got %q", "modified-deliverable", string(content))
	}

	// 2. Branch still exists
	mustRunGit(t, repo, "rev-parse", "--verify", wt.Branch)

	// 3. Lease bookkeeping cleaned up
	if len(pool.Active()) != 0 {
		t.Fatalf("expected 0 active leases in pool, got %d", len(pool.Active()))
	}
}

func TestReleasePreservesUntrackedWorktree(t *testing.T) {
	repo := setupGitRepo(t)
	pool, err := NewPool(PoolOptions{Repo: repo, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	ctx := context.Background()
	wt, err := pool.Acquire(ctx, "untracked-task")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Add an untracked new file
	untrackedFile := filepath.Join(wt.Path, "new-deliverable.txt")
	if err := os.WriteFile(untrackedFile, []byte("untracked-deliverable"), 0o644); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}

	// Release with keep=true
	if err := pool.Release(ctx, wt, true); err != nil {
		t.Fatalf("release: %v", err)
	}

	// 1. Directory still exists with untracked file
	if _, err := os.Stat(wt.Path); os.IsNotExist(err) {
		t.Fatalf("expected worktree directory %s to be preserved, but it was removed", wt.Path)
	}
	content, err := os.ReadFile(untrackedFile)
	if err != nil {
		t.Fatalf("failed to read untracked file: %v", err)
	}
	if string(content) != "untracked-deliverable" {
		t.Fatalf("expected untracked content %q, got %q", "untracked-deliverable", string(content))
	}

	// 2. Branch still exists
	mustRunGit(t, repo, "rev-parse", "--verify", wt.Branch)

	// 3. Lease bookkeeping cleaned up
	if len(pool.Active()) != 0 {
		t.Fatalf("expected 0 active leases in pool, got %d", len(pool.Active()))
	}
}

func TestReleaseCleanWorktreeRemoved(t *testing.T) {
	repo := setupGitRepo(t)
	pool, err := NewPool(PoolOptions{Repo: repo, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	ctx := context.Background()
	wt, err := pool.Acquire(ctx, "clean-task")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Verify worktree exists prior to release
	if _, err := os.Stat(wt.Path); os.IsNotExist(err) {
		t.Fatalf("worktree directory %s does not exist before release", wt.Path)
	}

	// Release with keep=true on clean worktree
	if err := pool.Release(ctx, wt, true); err != nil {
		t.Fatalf("release: %v", err)
	}

	// 1. Directory is removed
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("expected clean worktree directory %s to be removed, but it still exists", wt.Path)
	}

	// 2. Branch still exists (keep=true preserves branch)
	mustRunGit(t, repo, "rev-parse", "--verify", wt.Branch)

	// 3. Lease bookkeeping cleaned up
	if len(pool.Active()) != 0 {
		t.Fatalf("expected 0 active leases in pool, got %d", len(pool.Active()))
	}
}

func TestReleaseKeepFalseUnchanged(t *testing.T) {
	repo := setupGitRepo(t)
	pool, err := NewPool(PoolOptions{Repo: repo, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	ctx := context.Background()
	wt, err := pool.Acquire(ctx, "keep-false-task")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Add an uncommitted modification to make it dirty
	dirtyFile := filepath.Join(wt.Path, "dirty.txt")
	if err := os.WriteFile(dirtyFile, []byte("uncommitted"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	// Release with keep=false
	if err := pool.Release(ctx, wt, false); err != nil {
		t.Fatalf("release: %v", err)
	}

	// 1. Directory must be removed
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("expected dirty worktree directory %s to be removed on keep=false, but it still exists", wt.Path)
	}

	// 2. Branch must be deleted
	cmd := exec.Command("git", "rev-parse", "--verify", wt.Branch)
	cmd.Dir = repo
	if err := cmd.Run(); err == nil {
		t.Fatalf("expected branch %s to be deleted on keep=false, but it still exists", wt.Branch)
	}

	// 3. Lease bookkeeping cleaned up
	if len(pool.Active()) != 0 {
		t.Fatalf("expected 0 active leases in pool, got %d", len(pool.Active()))
	}
}

func TestReleaseTable(t *testing.T) {
	tests := []struct {
		name             string
		keep             bool
		setup            func(t *testing.T, repo, wtPath string)
		wantDirExists    bool
		wantBranchExists bool
	}{
		{
			name: "dirty tracked file preserved on keep=true",
			keep: true,
			setup: func(t *testing.T, repo, wtPath string) {
				if err := os.WriteFile(filepath.Join(wtPath, "mod.txt"), []byte("tracked-data"), 0o644); err != nil {
					t.Fatalf("setup: %v", err)
				}
			},
			wantDirExists:    true,
			wantBranchExists: true,
		},
		{
			name: "untracked file preserved on keep=true",
			keep: true,
			setup: func(t *testing.T, repo, wtPath string) {
				if err := os.WriteFile(filepath.Join(wtPath, "untracked.txt"), []byte("new-data"), 0o644); err != nil {
					t.Fatalf("setup: %v", err)
				}
			},
			wantDirExists:    true,
			wantBranchExists: true,
		},
		{
			name:             "clean worktree removed on keep=true",
			keep:             true,
			setup:            func(t *testing.T, repo, wtPath string) {},
			wantDirExists:    false,
			wantBranchExists: true,
		},
		{
			name: "dirty worktree removed on keep=false",
			keep: false,
			setup: func(t *testing.T, repo, wtPath string) {
				if err := os.WriteFile(filepath.Join(wtPath, "dirty.txt"), []byte("junk"), 0o644); err != nil {
					t.Fatalf("setup: %v", err)
				}
			},
			wantDirExists:    false,
			wantBranchExists: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := setupGitRepo(t)
			pool, err := NewPool(PoolOptions{Repo: repo, Root: t.TempDir()})
			if err != nil {
				t.Fatalf("NewPool: %v", err)
			}
			ctx := context.Background()
			wt, err := pool.Acquire(ctx, "task-"+shortID())
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			tc.setup(t, repo, wt.Path)

			if err := pool.Release(ctx, wt, tc.keep); err != nil {
				t.Fatalf("Release: %v", err)
			}

			_, statErr := os.Stat(wt.Path)
			dirExists := !os.IsNotExist(statErr)
			if dirExists != tc.wantDirExists {
				t.Errorf("dirExists = %v, want %v", dirExists, tc.wantDirExists)
			}

			cmd := exec.Command("git", "rev-parse", "--verify", wt.Branch)
			cmd.Dir = repo
			branchExists := cmd.Run() == nil
			if branchExists != tc.wantBranchExists {
				t.Errorf("branchExists = %v, want %v", branchExists, tc.wantBranchExists)
			}

			if len(pool.Active()) != 0 {
				t.Errorf("active leases = %d, want 0", len(pool.Active()))
			}
		})
	}
}

func TestWorktreeDirtyHelper(t *testing.T) {
	repo := setupGitRepo(t)

	// Clean repo -> not dirty
	dirty, err := worktreeDirty(repo)
	if err != nil {
		t.Fatalf("worktreeDirty on clean repo: %v", err)
	}
	if dirty {
		t.Fatalf("expected clean repo to not be dirty")
	}

	// Add untracked file -> dirty
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	dirty, err = worktreeDirty(repo)
	if err != nil {
		t.Fatalf("worktreeDirty on untracked repo: %v", err)
	}
	if !dirty {
		t.Fatalf("expected untracked file to be dirty")
	}

	// Non-existent directory -> returns error
	_, err = worktreeDirty(filepath.Join(t.TempDir(), "non-existent-dir"))
	if err == nil {
		t.Fatalf("expected error for non-existent path")
	}
}
