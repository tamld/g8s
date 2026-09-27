package worker

// #427: workspace_write attempts inside one worker process get per-attempt
// worktree isolation — concurrent code-writing attempts sharing a checkout
// destroy each other's uncommitted deliverables (the 3-way drain failure).
// read_only attempts keep the shared checkout (nothing to clobber).

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeWorktreeIsolator struct {
	mu         sync.Mutex
	dir        string
	acquired   int32
	keepFlag   bool
	released   int32
	acquireErr error
}

func (f *fakeWorktreeIsolator) AcquireWorktree(ctx context.Context, taskID string) (string, func(keep bool), error) {
	f.mu.Lock()
	f.acquired++
	err := f.acquireErr
	f.mu.Unlock()
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(f.dir, 0o700); err != nil {
		return "", nil, err
	}
	return f.dir, func(keep bool) {
		f.mu.Lock()
		f.keepFlag = keep
		f.released++
		f.mu.Unlock()
	}, nil
}

func newIsolationEnv(t *testing.T, iso *fakeWorktreeIsolator) *workerEnv {
	t.Helper()
	env := newWorkerEnv(t, nil)
	env.sup = NewSupervisor(env.store, env.runDir, WithRunner(env.runner), WithPollInterval(2*time.Millisecond), WithWorktreeIsolator(iso))
	return env
}

// A workspace_write attempt must run inside the isolator's worktree and the
// worktree must be released with keep=true after the attempt (deliverable
// survival is the point of #427).
func TestWorkspaceWriteAttemptGetsWorktreeIsolation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wt-task")
	iso := &fakeWorktreeIsolator{dir: dir}
	env := newIsolationEnv(t, iso)
	task := submitTask(t, env, "iso-write-1", 1, map[string]any{"permission": "workspace_write"})
	if _, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-iso", LeaseSeconds: 60}); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if iso.acquired != 1 {
		t.Fatalf("isolator must be consulted once for workspace_write, got %d", iso.acquired)
	}
	env.runner.mu.Lock()
	spawnDir := env.runner.spawned[0].Dir
	env.runner.mu.Unlock()
	if spawnDir != dir {
		t.Fatalf("child must spawn in the isolated worktree %q, got %q", dir, spawnDir)
	}
	if iso.released != 1 || !iso.keepFlag {
		t.Fatalf("worktree must be released exactly once with keep=true, got released=%d keep=%v", iso.released, iso.keepFlag)
	}
	if task == nil {
		t.Fatal("unreachable")
	}
}

// read_only attempts keep the shared checkout — the isolator must not be
// consulted (nothing to clobber, no worktree churn).
func TestReadOnlyAttemptSkipsIsolation(t *testing.T) {
	iso := &fakeWorktreeIsolator{dir: filepath.Join(t.TempDir(), "wt-ro")}
	env := newIsolationEnv(t, iso)
	submitTask(t, env, "iso-ro-1", 1, nil)
	if _, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-ro", LeaseSeconds: 60}); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if iso.acquired != 0 {
		t.Fatalf("read_only attempt must not consult the isolator, got %d acquisitions", iso.acquired)
	}
}

// Isolator failure degrades gracefully: the attempt falls back to the
// shared checkout instead of failing the task.
func TestIsolatorFailureFallsBackToSharedCheckout(t *testing.T) {
	iso := &fakeWorktreeIsolator{dir: filepath.Join(t.TempDir(), "wt-err"), acquireErr: errors.New("pool exhausted")}
	env := newIsolationEnv(t, iso)
	task := submitTask(t, env, "iso-err-1", 1, map[string]any{"permission": "workspace_write"})
	if _, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-err", LeaseSeconds: 60}); err != nil {
		t.Fatalf("isolator failure must degrade, not fail: %v", err)
	}
	env.runner.mu.Lock()
	spawnDir := env.runner.spawned[0].Dir
	env.runner.mu.Unlock()
	if spawnDir == iso.dir {
		t.Fatal("fallback must not use the failed worktree dir")
	}
	_ = task
}
