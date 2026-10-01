package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireRefusesToDestroyLiveWorktree_Deterministic5Retries(t *testing.T) {
	repo := setupGitRepo(t)
	poolRoot := t.TempDir()
	pool, err := NewPool(PoolOptions{Repo: repo, Root: poolRoot})
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	// Plant a live worktree under pool root
	collisionID := "deadbeef"
	plantedDir := filepath.Join(poolRoot, "wt-"+collisionID)
	if err := os.MkdirAll(plantedDir, 0o755); err != nil {
		t.Fatalf("mkdir planted dir: %v", err)
	}
	gitFile := filepath.Join(plantedDir, ".git")
	if err := os.WriteFile(gitFile, []byte("gitdir: /some/repo/.git/worktrees/wt-deadbeef\n"), 0o644); err != nil {
		t.Fatalf("write .git file: %v", err)
	}
	deliverableFile := filepath.Join(plantedDir, "deliverable.txt")
	deliverableContent := []byte("precious uncommitted work")
	if err := os.WriteFile(deliverableFile, deliverableContent, 0o644); err != nil {
		t.Fatalf("write deliverable: %v", err)
	}

	// Force collisionID for all attempts
	origShortIDFn := shortIDFn
	t.Cleanup(func() { shortIDFn = origShortIDFn })
	attempts := 0
	shortIDFn = func() string {
		attempts++
		return collisionID
	}

	ctx := context.Background()
	_, err = pool.Acquire(ctx, "task-collision")
	if err == nil {
		t.Fatal("expected error on live worktree collision, got nil")
	}

	expectedErr := fmt.Sprintf("worktree path collision with a live worktree after 5 attempts: %s", plantedDir)
	if err.Error() != expectedErr {
		t.Fatalf("expected error %q, got %q", expectedErr, err.Error())
	}
	if attempts != 5 {
		t.Errorf("expected 5 attempts, got %d", attempts)
	}

	// Live worktree and deliverable must survive
	if _, statErr := os.Stat(plantedDir); os.IsNotExist(statErr) {
		t.Fatalf("planted worktree directory %s was destroyed", plantedDir)
	}
	if _, statErr := os.Stat(gitFile); os.IsNotExist(statErr) {
		t.Fatalf("planted .git file %s was destroyed", gitFile)
	}
	content, rerr := os.ReadFile(deliverableFile)
	if rerr != nil || string(content) != string(deliverableContent) {
		t.Fatalf("deliverable file corrupted or destroyed: %v", rerr)
	}
}

func TestAcquireRetriesOnCollisionAndSucceeds(t *testing.T) {
	repo := setupGitRepo(t)
	poolRoot := t.TempDir()
	pool, err := NewPool(PoolOptions{Repo: repo, Root: poolRoot})
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	// Plant a live worktree
	collisionID := "deadbeef"
	plantedDir := filepath.Join(poolRoot, "wt-"+collisionID)
	if err := os.MkdirAll(plantedDir, 0o755); err != nil {
		t.Fatalf("mkdir planted dir: %v", err)
	}
	gitFile := filepath.Join(plantedDir, ".git")
	if err := os.WriteFile(gitFile, []byte("gitdir: /fake\n"), 0o644); err != nil {
		t.Fatalf("write .git file: %v", err)
	}
	deliverableFile := filepath.Join(plantedDir, "deliverable.txt")
	deliverableContent := []byte("surviving deliverable")
	if err := os.WriteFile(deliverableFile, deliverableContent, 0o644); err != nil {
		t.Fatalf("write deliverable: %v", err)
	}

	// 1st call collides, 2nd succeeds with "cafebabe"
	origShortIDFn := shortIDFn
	t.Cleanup(func() { shortIDFn = origShortIDFn })
	callCount := 0
	shortIDFn = func() string {
		callCount++
		if callCount == 1 {
			return collisionID
		}
		return "cafebabe"
	}

	ctx := context.Background()
	wt, err := pool.Acquire(ctx, "task-retry-success")
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	defer func() { _ = pool.Release(ctx, wt, false) }()

	if wt.ID != "wt-cafebabe" {
		t.Errorf("expected wt.ID wt-cafebabe, got %s", wt.ID)
	}
	if wt.Path == plantedDir {
		t.Errorf("acquired worktree path equals planted live worktree path")
	}

	// Planted live worktree and deliverable must survive
	if _, statErr := os.Stat(plantedDir); os.IsNotExist(statErr) {
		t.Fatalf("planted worktree was destroyed")
	}
	content, rerr := os.ReadFile(deliverableFile)
	if rerr != nil || string(content) != string(deliverableContent) {
		t.Fatalf("deliverable file corrupted or destroyed: %v", rerr)
	}
}

func TestAcquireLoop200NeverDestroysLiveWorktree(t *testing.T) {
	repo := setupGitRepo(t)
	poolRoot := t.TempDir()
	pool, err := NewPool(PoolOptions{Repo: repo, Root: poolRoot})
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	// Plant a live worktree under pool root
	plantedID := "wt-planted0"
	plantedDir := filepath.Join(poolRoot, plantedID)
	if err := os.MkdirAll(plantedDir, 0o755); err != nil {
		t.Fatalf("mkdir planted dir: %v", err)
	}
	gitFile := filepath.Join(plantedDir, ".git")
	if err := os.WriteFile(gitFile, []byte("gitdir: /fake\n"), 0o644); err != nil {
		t.Fatalf("write .git file: %v", err)
	}
	deliverableFile := filepath.Join(plantedDir, "deliverable.txt")
	deliverableContent := []byte("important output")
	if err := os.WriteFile(deliverableFile, deliverableContent, 0o644); err != nil {
		t.Fatalf("write deliverable: %v", err)
	}

	ctx := context.Background()
	for i := 0; i < 200; i++ {
		taskID := fmt.Sprintf("task-loop-%d", i)
		wt, err := pool.Acquire(ctx, taskID)
		if err != nil {
			t.Fatalf("acquire loop %d failed: %v", i, err)
		}
		if wt.Path == plantedDir {
			t.Fatalf("acquire loop %d acquired planted live worktree path: %s", i, wt.Path)
		}
		if err := pool.Release(ctx, wt, false); err != nil {
			t.Fatalf("release loop %d failed: %v", i, err)
		}
	}

	// Assert planted live worktree still exists with deliverable after all acquires
	if _, statErr := os.Stat(plantedDir); os.IsNotExist(statErr) {
		t.Fatalf("planted worktree was destroyed after 200 acquires")
	}
	content, rerr := os.ReadFile(deliverableFile)
	if rerr != nil || string(content) != string(deliverableContent) {
		t.Fatalf("deliverable missing or corrupted after 200 acquires: %v", rerr)
	}
}

func TestAcquireDebrisCollisionRemoved(t *testing.T) {
	repo := setupGitRepo(t)
	poolRoot := t.TempDir()
	pool, err := NewPool(PoolOptions{Repo: repo, Root: poolRoot})
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	// Plant a debris directory (NO .git entry)
	debrisID := "debris01"
	debrisDir := filepath.Join(poolRoot, "wt-"+debrisID)
	if err := os.MkdirAll(debrisDir, 0o755); err != nil {
		t.Fatalf("mkdir debris dir: %v", err)
	}
	debrisFile := filepath.Join(debrisDir, "leftover.log")
	if err := os.WriteFile(debrisFile, []byte("stale leftovers"), 0o644); err != nil {
		t.Fatalf("write debris file: %v", err)
	}

	// Force target ID to debrisID
	origShortIDFn := shortIDFn
	t.Cleanup(func() { shortIDFn = origShortIDFn })
	shortIDFn = func() string {
		return debrisID
	}

	ctx := context.Background()
	wt, err := pool.Acquire(ctx, "debris-task")
	if err != nil {
		t.Fatalf("acquire failed on debris collision: %v", err)
	}
	defer func() { _ = pool.Release(ctx, wt, false) }()

	if wt.Path != debrisDir {
		t.Fatalf("expected wt.Path %s, got %s", debrisDir, wt.Path)
	}

	// Old debris file must be removed (cleanup-on-collision preserved)
	if _, statErr := os.Stat(debrisFile); !os.IsNotExist(statErr) {
		t.Errorf("debris file %s still exists after acquire (expected cleanup-on-collision)", debrisFile)
	}

	// Acquired path must now be a valid worktree with .git
	gitEntry := filepath.Join(wt.Path, ".git")
	if _, statErr := os.Stat(gitEntry); os.IsNotExist(statErr) {
		t.Errorf("new worktree .git entry %s does not exist", gitEntry)
	}
}
