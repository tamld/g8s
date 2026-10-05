package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/receipt"
)

// TestReleasePreservesGitignoredProtectedDeliverable verifies that when a worktree's only change
// is a gitignored receipt-scoped file, Release(keep=true, protected=[that path]) PRESERVES it
// (issue #551). Before the fix, git status --porcelain reports clean because the file is gitignored,
// causing Release to remove the worktree and delete the branch.
func TestReleasePreservesGitignoredProtectedDeliverable(t *testing.T) {
	repo := setupGitRepo(t)
	// Seed a .gitignore in the repository ignoring .g8s/
	gitignorePath := filepath.Join(repo, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte(".g8s/\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	mustRunGit(t, repo, "add", ".gitignore")
	mustRunGit(t, repo, "commit", "-m", "ignore .g8s")

	pool, err := NewPool(PoolOptions{Repo: repo, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	ctx := context.Background()
	wt, err := pool.Acquire(ctx, "gitignored-deliverable-task")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Write gitignored receipt-scoped deliverable inside the worktree
	g8sDir := filepath.Join(wt.Path, ".g8s")
	if err := os.MkdirAll(g8sDir, 0o755); err != nil {
		t.Fatalf("mkdir .g8s: %v", err)
	}
	deliverablePath := filepath.Join(g8sDir, "agent-models.yml")
	content := []byte("models:\n  default: gemini-3.8-flash-high\n")
	if err := os.WriteFile(deliverablePath, content, 0o644); err != nil {
		t.Fatalf("write deliverable: %v", err)
	}

	// Verify git status --porcelain is clean (dirty == false) because .g8s/ is gitignored
	dirty, err := worktreeDirty(wt.Path)
	if err != nil {
		t.Fatalf("worktreeDirty: %v", err)
	}
	if dirty {
		t.Fatal("expected worktree to be git-clean because .g8s/ is gitignored")
	}

	// Release with keep=true and protected=[.g8s/agent-models.yml]
	if err := pool.Release(ctx, wt, true, ".g8s/agent-models.yml"); err != nil {
		t.Fatalf("release: %v", err)
	}

	// 1. Directory must still exist with the deliverable file intact
	if _, err := os.Stat(wt.Path); os.IsNotExist(err) {
		t.Fatalf("expected worktree directory %s to be preserved, but it was removed", wt.Path)
	}
	readBack, err := os.ReadFile(deliverablePath)
	if err != nil {
		t.Fatalf("failed to read deliverable file: %v", err)
	}
	if string(readBack) != string(content) {
		t.Fatalf("deliverable content corrupted: got %q, want %q", string(readBack), string(content))
	}

	// 2. Branch must still exist
	mustRunGit(t, repo, "rev-parse", "--verify", wt.Branch)

	// 3. Lease bookkeeping cleaned up
	if len(pool.Active()) != 0 {
		t.Fatalf("expected 0 active leases in pool, got %d", len(pool.Active()))
	}
}

// TestReleaseKeepFalseRemovesEvenWithProtectedDeliverable verifies that keep=false still removes
// the worktree and deletes the branch regardless of whether protected deliverables exist.
func TestReleaseKeepFalseRemovesEvenWithProtectedDeliverable(t *testing.T) {
	repo := setupGitRepo(t)
	gitignorePath := filepath.Join(repo, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte(".g8s/\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	mustRunGit(t, repo, "add", ".gitignore")
	mustRunGit(t, repo, "commit", "-m", "ignore .g8s")

	pool, err := NewPool(PoolOptions{Repo: repo, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	ctx := context.Background()
	wt, err := pool.Acquire(ctx, "keep-false-task")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	g8sDir := filepath.Join(wt.Path, ".g8s")
	if err := os.MkdirAll(g8sDir, 0o755); err != nil {
		t.Fatalf("mkdir .g8s: %v", err)
	}
	deliverablePath := filepath.Join(g8sDir, "agent-models.yml")
	if err := os.WriteFile(deliverablePath, []byte("data"), 0o644); err != nil {
		t.Fatalf("write deliverable: %v", err)
	}

	// Release with keep=false
	if err := pool.Release(ctx, wt, false, ".g8s/agent-models.yml"); err != nil {
		t.Fatalf("release: %v", err)
	}

	// Directory must be removed
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("expected worktree directory %s to be removed for keep=false", wt.Path)
	}
}

// TestReleaseGitDirtyPreservesRegardlessOfProtected verifies that a git-dirty worktree
// continues to be preserved when keep=true even if no protected paths are given.
func TestReleaseGitDirtyPreservesRegardlessOfProtected(t *testing.T) {
	repo := setupGitRepo(t)
	pool, err := NewPool(PoolOptions{Repo: repo, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	ctx := context.Background()
	wt, err := pool.Acquire(ctx, "dirty-preserves-task")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Write an untracked file so git status is dirty
	untracked := filepath.Join(wt.Path, "dirty.txt")
	if err := os.WriteFile(untracked, []byte("dirty content"), 0o644); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}

	dirty, err := worktreeDirty(wt.Path)
	if err != nil {
		t.Fatalf("worktreeDirty: %v", err)
	}
	if !dirty {
		t.Fatal("expected worktree to be dirty")
	}

	// Release with keep=true, protected=[absent path]
	if err := pool.Release(ctx, wt, true, "nonexistent/path.txt"); err != nil {
		t.Fatalf("release: %v", err)
	}

	// Directory must still exist
	if _, err := os.Stat(wt.Path); os.IsNotExist(err) {
		t.Fatalf("expected dirty worktree %s to be preserved", wt.Path)
	}
}

// TestReleaseProtectedAbsentAndCleanRemoves verifies that if a worktree is git-clean
// and declared protected paths do not exist on disk, the worktree is removed on keep=true.
func TestReleaseProtectedAbsentAndCleanRemoves(t *testing.T) {
	repo := setupGitRepo(t)
	pool, err := NewPool(PoolOptions{Repo: repo, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	ctx := context.Background()
	wt, err := pool.Acquire(ctx, "absent-clean-task")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	dirty, err := worktreeDirty(wt.Path)
	if err != nil {
		t.Fatalf("worktreeDirty: %v", err)
	}
	if dirty {
		t.Fatal("expected worktree to be clean")
	}

	// Release with keep=true, protected=[nonexistent]
	if err := pool.Release(ctx, wt, true, ".g8s/agent-models.yml"); err != nil {
		t.Fatalf("release: %v", err)
	}

	// Directory must be removed
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("expected clean worktree with absent protected deliverable %s to be removed", wt.Path)
	}
}

// TestReleasePreservesGlobProtectedDeliverable verifies that glob entries like
// ./internal/verifier/* count as protected if the literal prefix directory exists.
func TestReleasePreservesGlobProtectedDeliverable(t *testing.T) {
	repo := setupGitRepo(t)
	gitignorePath := filepath.Join(repo, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte(".g8s/\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	mustRunGit(t, repo, "add", ".gitignore")
	mustRunGit(t, repo, "commit", "-m", "ignore .g8s")

	pool, err := NewPool(PoolOptions{Repo: repo, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	ctx := context.Background()
	wt, err := pool.Acquire(ctx, "glob-task")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Create directory matching the literal prefix of ./.g8s/*
	g8sDir := filepath.Join(wt.Path, ".g8s")
	if err := os.MkdirAll(g8sDir, 0o755); err != nil {
		t.Fatalf("mkdir .g8s: %v", err)
	}
	if err := os.WriteFile(filepath.Join(g8sDir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	// Release with keep=true and glob protected path
	if err := pool.Release(ctx, wt, true, "./.g8s/*"); err != nil {
		t.Fatalf("release: %v", err)
	}

	// Directory must still exist
	if _, err := os.Stat(wt.Path); os.IsNotExist(err) {
		t.Fatalf("expected worktree directory %s to be preserved for glob protected path, but it was removed", wt.Path)
	}
}

type fanOutDeliverableWorker struct {
	relPath string
	data    string
}

func (w fanOutDeliverableWorker) Name() string                      { return "deliverable-writer" }
func (w fanOutDeliverableWorker) Available(_ context.Context) error { return nil }
func (w fanOutDeliverableWorker) Spawn(_ context.Context, t Task) (Handle, error) {
	target := filepath.Join(t.Worktree.Path, w.relPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(target, []byte(w.data), 0o644); err != nil {
		return nil, err
	}
	return fanOutHandle{taskID: t.ID}, nil
}

// TestFanOutPreservesReceiptScopedDeliverable verifies the orchestrator FanOut path
// threads AllowedFiles to Pool.Release so a task with only gitignored receipt deliverables
// has its worktree preserved when OK=true.
func TestFanOutPreservesReceiptScopedDeliverable(t *testing.T) {
	repo := setupGitRepo(t)
	gitignorePath := filepath.Join(repo, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte(".g8s/\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	mustRunGit(t, repo, "add", ".gitignore")
	mustRunGit(t, repo, "commit", "-m", "ignore .g8s")

	pool, err := NewPool(PoolOptions{Repo: repo, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	r := NewRegistry()
	_ = r.Register("deliverable-writer", func() Worker {
		return fanOutDeliverableWorker{
			relPath: ".g8s/agent-models.yml",
			data:    "model: gemini-3.8-flash-high",
		}
	})

	taskID := "task-deliverable-551"
	plan := []TaskSpec{
		{
			TaskID: taskID,
			Task: Task{
				ID:           taskID,
				AllowedFiles: []string{".g8s/agent-models.yml"},
			},
		},
	}

	opts := FanOutOptions{
		Registry:    r,
		Pool:        pool,
		MaxParallel: 1,
	}

	receipts, err := FanOut(context.Background(), plan, opts)
	if err != nil {
		t.Fatalf("FanOut: %v", err)
	}
	if len(receipts) != 1 || !receipts[0].OK {
		t.Fatalf("expected successful receipt, got %+v", receipts)
	}

	// Verify the worktree directory was preserved on disk
	wtPath := filepath.Join(pool.root, "wt-"+receipts[0].WorktreeID)
	// Check if worktree directory exists under pool.root
	entries, err := os.ReadDir(pool.root)
	if err != nil {
		t.Fatalf("read pool root: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 preserved worktree in %s, got %d", pool.root, len(entries))
	}
	preservedDir := filepath.Join(pool.root, entries[0].Name())
	deliverableFile := filepath.Join(preservedDir, ".g8s", "agent-models.yml")
	content, err := os.ReadFile(deliverableFile)
	if err != nil {
		t.Fatalf("failed to read deliverable from preserved worktree: %v", err)
	}
	if string(content) != "model: gemini-3.8-flash-high" {
		t.Fatalf("deliverable content mismatch: %q", string(content))
	}
	_ = wtPath
}

// TestFanOutPreservesReceiptScopedDeliverable_ViaReceiptLookup verifies that if
// task.AllowedFiles is empty, FanOut resolves AllowedPaths from receipts.db using
// task.ReceiptID so the worktree is preserved.
func TestFanOutPreservesReceiptScopedDeliverable_ViaReceiptLookup(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("G8S_STATE_DIR", stateDir)

	rcDbPath := filepath.Join(stateDir, "receipts.db")
	mgr, err := receipt.NewReceiptManager(rcDbPath, nil)
	if err != nil {
		t.Fatalf("NewReceiptManager: %v", err)
	}
	defer mgr.Close()

	rc, err := mgr.IssueReceipt("brain", []string{".g8s/agent-models.yml"}, time.Hour,
		receipt.WithCanonicalEnvelope(&receipt.CanonicalEnvelope{
			SchemaURI:      "g8s://envelope/write-receipt/v1",
			FieldOrder:     []string{"receipt_id", "issuer", "allowed_paths", "expires_at"},
			RequiredFields: []string{"receipt_id", "issuer"},
		}),
		receipt.WithRuleGraph(&receipt.RuleGraphSnapshot{
			RulesetVersion: "v1",
			PipelineDigest: "test-digest",
		}),
	)
	if err != nil {
		t.Fatalf("IssueReceipt: %v", err)
	}

	repo := setupGitRepo(t)
	gitignorePath := filepath.Join(repo, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte(".g8s/\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	mustRunGit(t, repo, "add", ".gitignore")
	mustRunGit(t, repo, "commit", "-m", "ignore .g8s")

	pool, err := NewPool(PoolOptions{Repo: repo, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	r := NewRegistry()
	_ = r.Register("deliverable-writer", func() Worker {
		return fanOutDeliverableWorker{
			relPath: ".g8s/agent-models.yml",
			data:    "model: gemini-3.8-flash-high",
		}
	})

	taskID := "task-lookup-551"
	plan := []TaskSpec{
		{
			TaskID: taskID,
			Task: Task{
				ID:        taskID,
				ReceiptID: rc.ReceiptID,
				// AllowedFiles intentionally omitted: should resolve from receipt_id
			},
		},
	}

	opts := FanOutOptions{
		Registry:    r,
		Pool:        pool,
		MaxParallel: 1,
	}

	receipts, err := FanOut(context.Background(), plan, opts)
	if err != nil {
		t.Fatalf("FanOut: %v", err)
	}
	if len(receipts) != 1 || !receipts[0].OK {
		t.Fatalf("expected successful receipt, got %+v", receipts)
	}

	// Verify worktree preserved
	entries, err := os.ReadDir(pool.root)
	if err != nil {
		t.Fatalf("read pool root: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 preserved worktree in %s, got %d", pool.root, len(entries))
	}
	preservedDir := filepath.Join(pool.root, entries[0].Name())
	deliverableFile := filepath.Join(preservedDir, ".g8s", "agent-models.yml")
	content, err := os.ReadFile(deliverableFile)
	if err != nil {
		t.Fatalf("failed to read deliverable: %v", err)
	}
	if string(content) != "model: gemini-3.8-flash-high" {
		t.Fatalf("deliverable content mismatch: %q", string(content))
	}
}
