package worker

// #427: workspace_write attempts inside one worker process get per-attempt
// worktree isolation — concurrent code-writing attempts sharing a checkout
// destroy each other's uncommitted deliverables (the 3-way drain failure).
// read_only attempts keep the shared checkout (nothing to clobber).

import (
	"context"
	"encoding/json"
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
		return "", func(keep bool) {
			f.mu.Lock()
			f.keepFlag = keep
			f.released++
			f.mu.Unlock()
		}, err
	}
	if f.dir != "" {
		if err := os.MkdirAll(f.dir, 0o700); err != nil {
			return "", nil, err
		}
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

// #656: Isolator failure must fail closed: structured isolation_unavailable failure,
// child command is never invoked, attempt terminally finished without lease leak.
func TestIsolatorFailureFailsClosed(t *testing.T) {
	iso := &fakeWorktreeIsolator{dir: filepath.Join(t.TempDir(), "wt-err"), acquireErr: errors.New("pool exhausted")}
	env := newIsolationEnv(t, iso)
	task := submitTask(t, env, "iso-err-1", 1, map[string]any{"permission": "workspace_write"})
	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-err", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	if final == nil {
		t.Fatal("RunOnce returned nil final task")
	}

	env.runner.mu.Lock()
	spawnCount := len(env.runner.spawned)
	env.runner.mu.Unlock()
	if spawnCount != 0 {
		t.Fatalf("child runner must NEVER be spawned when isolation fails, got %d spawns", spawnCount)
	}

	got, err := env.store.GetTask(context.Background(), task.TaskID)
	if err != nil || got == nil {
		t.Fatalf("get task: %v", err)
	}
	if got.State != "WORKER_COMPLETED" {
		t.Fatalf("expected task state WORKER_COMPLETED, got %q", got.State)
	}
	if got.LeaseToken != nil {
		t.Fatalf("lease leak: expected nil LeaseToken, got %v", *got.LeaseToken)
	}
	if got.LeaseOwner != nil {
		t.Fatalf("lease leak: expected nil LeaseOwner, got %v", *got.LeaseOwner)
	}

	var resultMap map[string]any
	if err := json.Unmarshal(got.Result, &resultMap); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if resultMap["ok"] != false {
		t.Fatalf("expected ok=false, got %v", resultMap["ok"])
	}
	if resultMap["status"] != "isolation_unavailable" {
		t.Fatalf("expected status=isolation_unavailable, got %v", resultMap["status"])
	}
	if iso.released != 1 || iso.keepFlag {
		t.Fatalf("expected release(false) called exactly once on failure, got released=%d keep=%v", iso.released, iso.keepFlag)
	}
}

// #678: When a workspace_write attempt fails or reports [API Error:] on stderr with empty output,
// the worktree must be released with keep=false so wt/<task-id> branches do not leak.
func TestWorkspaceWriteFailedAttemptReleasesWorktreeWithKeepFalse(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wt-failed-task")
	iso := &fakeWorktreeIsolator{dir: dir}
	env := newIsolationEnv(t, iso)
	env.runner.factory = func(opts SpawnOptions) Child {
		if opts.Stderr != nil {
			_, _ = opts.Stderr.Write([]byte("[API Error: 404 No active credentials for provider: openai]\n"))
		}
		child := newFakeChild(0)
		child.finishLater(opts.ResultPath, `{"ok":true,"exit_code":0}`, 5*time.Millisecond)
		return child
	}
	task := submitTask(t, env, "iso-write-api-err", 1, map[string]any{"permission": "workspace_write"})
	if _, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-iso-fail", LeaseSeconds: 60}); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if iso.released != 1 || iso.keepFlag {
		t.Fatalf("failed workspace_write attempt must call release(false) to prune wt/<task-id>, got released=%d keep=%v", iso.released, iso.keepFlag)
	}
	got, err := env.store.GetTask(context.Background(), task.TaskID)
	if err != nil || got == nil {
		t.Fatalf("GetTask: %v", err)
	}
	var resultMap map[string]any
	if err := json.Unmarshal(got.Result, &resultMap); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if resultMap["ok"] != false {
		t.Fatalf("expected ok=false on silent [API Error:], got %v", resultMap["ok"])
	}
}
