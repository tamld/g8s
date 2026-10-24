package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type fakeRepoWorktreeIsolator struct {
	fakeWorktreeIsolator
	repoMu           sync.Mutex
	repoAcquired     int32
	lastRepoDir      string
	lastTaskID       string
	repoAcquireErr   error
	repoReturnDir    string
	repoReleaseCalls int32
	repoKeepFlag     bool
}

func (f *fakeRepoWorktreeIsolator) AcquireRepoWorktree(ctx context.Context, repoDir, taskID string) (string, func(keep bool), error) {
	f.repoMu.Lock()
	f.repoAcquired++
	f.lastRepoDir = repoDir
	f.lastTaskID = taskID
	err := f.repoAcquireErr
	retDir := f.repoReturnDir
	f.repoMu.Unlock()
	if err != nil {
		return "", func(keep bool) {
			f.repoMu.Lock()
			f.repoReleaseCalls++
			f.repoKeepFlag = keep
			f.repoMu.Unlock()
		}, err
	}
	if retDir != "" {
		if merr := os.MkdirAll(retDir, 0o700); merr != nil {
			return "", nil, merr
		}
	}
	return retDir, func(keep bool) {
		f.repoMu.Lock()
		f.repoReleaseCalls++
		f.repoKeepFlag = keep
		f.repoMu.Unlock()
	}, nil
}

// TestIsolationFailureNilIsolator tests that a workspace_write task fails closed
// when no isolator is configured on the supervisor.
func TestIsolationFailureNilIsolator(t *testing.T) {
	env := newWorkerEnv(t, nil) // isolator is nil
	task := submitTask(t, env, "fail-nil-iso", 1, map[string]any{"permission": "workspace_write"})

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-nil-iso", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce returned unexpected error: %v", err)
	}
	if final == nil {
		t.Fatal("RunOnce returned nil final task")
	}

	env.runner.mu.Lock()
	spawnCount := len(env.runner.spawned)
	env.runner.mu.Unlock()
	if spawnCount != 0 {
		t.Fatalf("child command must NEVER be spawned when isolator is nil, got %d spawns", spawnCount)
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
}

// TestIsolationFailureAcquireError tests that a workspace_write task fails closed
// when AcquireWorktree returns a non-nil error.
func TestIsolationFailureAcquireError(t *testing.T) {
	iso := &fakeWorktreeIsolator{
		dir:        filepath.Join(t.TempDir(), "wt-err"),
		acquireErr: errors.New("insufficient worktree slots"),
	}
	env := newWorkerEnv(t, nil, WithWorktreeIsolator(iso))
	task := submitTask(t, env, "fail-acquire-err", 1, map[string]any{"permission": "workspace_write"})

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-acq-err", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce returned unexpected error: %v", err)
	}
	if final == nil {
		t.Fatal("RunOnce returned nil final task")
	}

	env.runner.mu.Lock()
	spawnCount := len(env.runner.spawned)
	env.runner.mu.Unlock()
	if spawnCount != 0 {
		t.Fatalf("child command must NEVER be spawned when AcquireWorktree fails, got %d spawns", spawnCount)
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

// TestIsolationFailureEmptyDir tests that a workspace_write task fails closed
// when AcquireWorktree returns an empty dir with nil error.
func TestIsolationFailureEmptyDir(t *testing.T) {
	iso := &fakeWorktreeIsolator{
		dir: "", // empty dir returned
	}
	env := newWorkerEnv(t, nil, WithWorktreeIsolator(iso))
	task := submitTask(t, env, "fail-empty-dir", 1, map[string]any{"permission": "workspace_write"})

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-empty-dir", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce returned unexpected error: %v", err)
	}
	if final == nil {
		t.Fatal("RunOnce returned nil final task")
	}

	env.runner.mu.Lock()
	spawnCount := len(env.runner.spawned)
	env.runner.mu.Unlock()
	if spawnCount != 0 {
		t.Fatalf("child command must NEVER be spawned when AcquireWorktree returns empty dir, got %d spawns", spawnCount)
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
		t.Fatalf("expected release(false) called on empty dir failure, got released=%d keep=%v", iso.released, iso.keepFlag)
	}
}

// TestIsolationRepoWorktreeIsolatorSuccess tests successful cross-repo isolated execution.
func TestIsolationRepoWorktreeIsolatorSuccess(t *testing.T) {
	wtDir := filepath.Join(t.TempDir(), "wt-cross-repo")
	repoDir := filepath.Join(t.TempDir(), "external-repo")
	iso := &fakeRepoWorktreeIsolator{
		repoReturnDir: wtDir,
	}
	env := newWorkerEnv(t, nil, WithWorktreeIsolator(iso))
	task := submitTask(t, env, "succ-cross-repo", 1, map[string]any{
		"permission": "workspace_write",
		"add_dirs":   []string{repoDir},
	})

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-repo-succ", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	if final == nil {
		t.Fatal("RunOnce returned nil final task")
	}

	if iso.repoAcquired != 1 {
		t.Fatalf("expected AcquireRepoWorktree called 1 time, got %d", iso.repoAcquired)
	}
	if iso.lastRepoDir != repoDir {
		t.Fatalf("expected repoDir=%q, got %q", repoDir, iso.lastRepoDir)
	}
	if iso.lastTaskID != task.TaskID {
		t.Fatalf("expected taskID=%q, got %q", task.TaskID, iso.lastTaskID)
	}

	env.runner.mu.Lock()
	if len(env.runner.spawned) != 1 {
		t.Fatalf("expected 1 spawned child, got %d", len(env.runner.spawned))
	}
	spawnDir := env.runner.spawned[0].Dir
	env.runner.mu.Unlock()

	if spawnDir != wtDir {
		t.Fatalf("expected child spawned in %q, got %q", wtDir, spawnDir)
	}
	if iso.repoReleaseCalls != 1 || !iso.repoKeepFlag {
		t.Fatalf("expected release(true) called on success, got calls=%d keep=%v", iso.repoReleaseCalls, iso.repoKeepFlag)
	}
}

// TestIsolationRepoWorktreeIsolatorFailure tests that RepoWorktreeIsolator acquisition error fails closed.
func TestIsolationRepoWorktreeIsolatorFailure(t *testing.T) {
	repoDir := filepath.Join(t.TempDir(), "external-repo-fail")
	iso := &fakeRepoWorktreeIsolator{
		repoAcquireErr: errors.New("failed to branch external repo"),
	}
	env := newWorkerEnv(t, nil, WithWorktreeIsolator(iso))
	task := submitTask(t, env, "fail-cross-repo", 1, map[string]any{
		"permission": "workspace_write",
		"add_dirs":   []string{repoDir},
	})

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-repo-fail", LeaseSeconds: 60})
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
		t.Fatalf("child command must NEVER be spawned when RepoWorktreeIsolator fails, got %d", spawnCount)
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
	if iso.repoReleaseCalls != 1 || iso.repoKeepFlag {
		t.Fatalf("expected release(false) called on failure, got calls=%d keep=%v", iso.repoReleaseCalls, iso.repoKeepFlag)
	}
}

// TestIsolationRepoWorktreeIsolatorEmptyDir tests that RepoWorktreeIsolator empty dir fails closed.
func TestIsolationRepoWorktreeIsolatorEmptyDir(t *testing.T) {
	repoDir := filepath.Join(t.TempDir(), "external-repo-empty")
	iso := &fakeRepoWorktreeIsolator{
		repoReturnDir: "", // empty dir returned
	}
	env := newWorkerEnv(t, nil, WithWorktreeIsolator(iso))
	task := submitTask(t, env, "empty-cross-repo", 1, map[string]any{
		"permission": "workspace_write",
		"add_dirs":   []string{repoDir},
	})

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-repo-empty", LeaseSeconds: 60})
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
		t.Fatalf("child command must NEVER be spawned when RepoWorktreeIsolator returns empty dir, got %d", spawnCount)
	}

	got, err := env.store.GetTask(context.Background(), task.TaskID)
	if err != nil || got == nil {
		t.Fatalf("get task: %v", err)
	}
	if got.State != "WORKER_COMPLETED" {
		t.Fatalf("expected task state WORKER_COMPLETED, got %q", got.State)
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
	if iso.repoReleaseCalls != 1 || iso.repoKeepFlag {
		t.Fatalf("expected release(false) called on empty dir, got calls=%d keep=%v", iso.repoReleaseCalls, iso.repoKeepFlag)
	}
}

// TestIsolationReadOnlyPreserved verifies that read_only tasks continue normal execution
// without isolator consultation even when the isolator is nil or failing.
func TestIsolationReadOnlyPreserved(t *testing.T) {
	// Sub-test 1: read_only with nil isolator
	envNil := newWorkerEnv(t, nil)
	task1 := submitTask(t, envNil, "ro-nil-iso", 1, map[string]any{"permission": "read_only"})
	final1, err1 := envNil.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-ro-nil", LeaseSeconds: 60})
	if err1 != nil {
		t.Fatalf("RunOnce ro nil: %v", err1)
	}
	if final1 == nil {
		t.Fatal("expected claimed task")
	}
	envNil.runner.mu.Lock()
	if len(envNil.runner.spawned) != 1 {
		t.Fatalf("read_only must spawn child even when isolator is nil, got %d", len(envNil.runner.spawned))
	}
	envNil.runner.mu.Unlock()

	// Sub-test 2: read_only with failing isolator
	isoErr := &fakeWorktreeIsolator{acquireErr: errors.New("failing isolator")}
	envErr := newWorkerEnv(t, nil, WithWorktreeIsolator(isoErr))
	task2 := submitTask(t, envErr, "ro-err-iso", 1, map[string]any{"permission": "read_only"})
	final2, err2 := envErr.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-ro-err", LeaseSeconds: 60})
	if err2 != nil {
		t.Fatalf("RunOnce ro err: %v", err2)
	}
	if final2 == nil {
		t.Fatal("expected claimed task")
	}
	if isoErr.acquired != 0 {
		t.Fatalf("read_only must NOT consult isolator, got %d acquisitions", isoErr.acquired)
	}
	envErr.runner.mu.Lock()
	if len(envErr.runner.spawned) != 1 {
		t.Fatalf("read_only must spawn child, got %d", len(envErr.runner.spawned))
	}
	envErr.runner.mu.Unlock()

	_ = task1
	_ = task2
}
