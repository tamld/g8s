package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/tamld/g8s/internal/controlplane"
)

// preStartEnv prepares a worker env, submits a read_only task, and returns the
// claimed-or-claimable task plus the attempt run directory the supervisor will
// use (ClaimTask bumps attempts to 1, so first attempt is attempt-1).
func preStartEnv(t *testing.T, payload map[string]any) (*workerEnv, *controlplane.Task, string) {
	t.Helper()
	env := newWorkerEnv(t, nil)
	task := submitTask(t, env, "pre-start-"+filepath.Base(t.Name()), 1, payload)
	runDir := filepath.Join(env.runDir, task.TaskID, "attempt-1")
	return env, task, runDir
}

// corruptStoredRequest rewrites the stored request_json of a claimed task so
// it stays valid JSON for the claim scan (json_extract) but fails the
// taskRequest struct decode inside RunOnce after ClaimTask.
func corruptStoredRequest(t *testing.T, env *workerEnv, taskID string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(filepath.Dir(env.runDir), "cp.db"))
	if err != nil {
		t.Fatalf("open cp.db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE tasks SET request_json = '{"prompt": 123}' WHERE task_id = ?`, taskID); err != nil {
		t.Fatalf("corrupt request_json: %v", err)
	}
}

// assertTerminalAttempt asserts the stranded-attempt fix (A-4/A-8, #984): the
// task reached a terminal state with the lease released, the result envelope
// carries the typed failure status, and BOTH the task_started and
// attempt_finished task_events were recorded (no attempt drains silently).
func assertTerminalAttempt(t *testing.T, env *workerEnv, taskID, wantStatus string) {
	t.Helper()
	got, err := env.store.GetTask(context.Background(), taskID)
	if err != nil || got == nil {
		t.Fatalf("get task: %v", err)
	}
	if got.State != "WORKER_COMPLETED" {
		t.Fatalf("attempt stranded: state = %q, want WORKER_COMPLETED", got.State)
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
	if resultMap["status"] != wantStatus {
		t.Fatalf("expected status=%q, got %v", wantStatus, resultMap["status"])
	}

	events, err := env.store.Events(context.Background(), taskID)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var started, finished bool
	for _, e := range events {
		switch e.EventType {
		case "task_started":
			started = true
		case "attempt_finished":
			finished = true
		}
	}
	if !started {
		t.Fatalf("expected task_started event, got %+v", events)
	}
	if !finished {
		t.Fatalf("expected attempt_finished event, got %+v", events)
	}
}

func assertNoSpawn(t *testing.T, env *workerEnv) {
	t.Helper()
	env.runner.mu.Lock()
	defer env.runner.mu.Unlock()
	if len(env.runner.spawned) != 0 {
		t.Fatalf("child command must never spawn on pre-start failure, got %d spawns", len(env.runner.spawned))
	}
}

// TestRunOnceInvalidRequestJSONFinishesAttempt: a task whose stored request
// JSON cannot be decoded must NOT strand the claimed attempt (A-4, #984).
func TestRunOnceInvalidRequestJSONFinishesAttempt(t *testing.T) {
	env, task, _ := preStartEnv(t, nil)
	corruptStoredRequest(t, env, task.TaskID)

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-bad-req", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce returned unexpected error: %v", err)
	}
	if final == nil {
		t.Fatal("RunOnce returned nil final task")
	}
	assertTerminalAttempt(t, env, task.TaskID, "task_request_invalid")
	assertNoSpawn(t, env)
}

// TestRunOnceToolPolicySetupFailureFinishesAttempt: a supplied_text task whose
// hook workspace cannot be created must not strand the claimed attempt (A-4).
func TestRunOnceToolPolicySetupFailureFinishesAttempt(t *testing.T) {
	env, task, _ := preStartEnv(t, map[string]any{"permission": "supplied_text"})
	// Platform-robust failure force (#984): a REGULAR FILE at TMPDIR makes
	// os.MkdirTemp fail with ENOTDIR on every OS — chmod-based read-only
	// dirs do not deny access on Windows (POSIX mode bits are not ACLs).
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	// os.TempDir consults TMPDIR on unix but %TMP%/%TEMP% via GetTempPathW
	// on Windows — set all three so the failure force is platform-stable.
	t.Setenv("TMPDIR", blocker)
	t.Setenv("TMP", blocker)
	t.Setenv("TEMP", blocker)

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-hook-fail", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce returned unexpected error: %v", err)
	}
	if final == nil {
		t.Fatal("RunOnce returned nil final task")
	}
	assertTerminalAttempt(t, env, task.TaskID, "tool_policy_setup_failed")
	assertNoSpawn(t, env)
}

// TestRunOnceRunDirFailureFinishesAttempt: failing to create the attempt run
// dir must not strand the claimed attempt (A-4).
func TestRunOnceRunDirFailureFinishesAttempt(t *testing.T) {
	env, task, _ := preStartEnv(t, nil)
	// A regular file where the per-task run dir should be created.
	if err := os.MkdirAll(env.runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(env.runDir, task.TaskID)
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-rundir-fail", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce returned unexpected error: %v", err)
	}
	if final == nil {
		t.Fatal("RunOnce returned nil final task")
	}
	assertTerminalAttempt(t, env, task.TaskID, "run_dir_unavailable")
	assertNoSpawn(t, env)
}

// TestRunOncePromptWriteFailureFinishesAttempt: failing to persist prompt.txt
// must not strand the claimed attempt (A-4).
func TestRunOncePromptWriteFailureFinishesAttempt(t *testing.T) {
	env, task, runDir := preStartEnv(t, nil)
	if err := os.MkdirAll(filepath.Join(runDir, "prompt.txt"), 0o700); err != nil {
		t.Fatal(err)
	}

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-prompt-fail", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce returned unexpected error: %v", err)
	}
	if final == nil {
		t.Fatal("RunOnce returned nil final task")
	}
	assertTerminalAttempt(t, env, task.TaskID, "prompt_write_failed")
	assertNoSpawn(t, env)
}

// TestRunOnceStdoutCaptureFailureFinishesAttempt: failing to open the stdout
// capture file must not strand the claimed attempt (A-4).
func TestRunOnceStdoutCaptureFailureFinishesAttempt(t *testing.T) {
	env, task, runDir := preStartEnv(t, nil)
	if err := os.MkdirAll(filepath.Join(runDir, "worker.stdout"), 0o700); err != nil {
		t.Fatal(err)
	}

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-stdout-fail", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce returned unexpected error: %v", err)
	}
	if final == nil {
		t.Fatal("RunOnce returned nil final task")
	}
	assertTerminalAttempt(t, env, task.TaskID, "stdout_capture_unavailable")
	assertNoSpawn(t, env)
}

// TestRunOnceStderrCaptureFailureFinishesAttempt: failing to open the stderr
// capture file must not strand the claimed attempt (A-4).
func TestRunOnceStderrCaptureFailureFinishesAttempt(t *testing.T) {
	env, task, runDir := preStartEnv(t, nil)
	if err := os.MkdirAll(filepath.Join(runDir, "worker.stderr"), 0o700); err != nil {
		t.Fatal(err)
	}

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-stderr-fail", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce returned unexpected error: %v", err)
	}
	if final == nil {
		t.Fatal("RunOnce returned nil final task")
	}
	assertTerminalAttempt(t, env, task.TaskID, "stderr_capture_unavailable")
	assertNoSpawn(t, env)
}

// validationFailingCP wraps a WorkerControlPlane and fails validation-result
// persistence, simulating a mid-collect store failure (A-8, #984).
type validationFailingCP struct {
	WorkerControlPlane
	validateErr   error
	resultCalls   atomic.Int32
	contractCalls atomic.Int32
}

func (f *validationFailingCP) ValidateResult(ctx context.Context, taskID string, validation controlplane.ResultValidation) error {
	f.resultCalls.Add(1)
	return f.validateErr
}

func (f *validationFailingCP) ValidateContract(ctx context.Context, taskID string, validation controlplane.ContractValidation) error {
	f.contractCalls.Add(1)
	return f.validateErr
}

// TestCollectValidationPersistFailureStillFinishesAttempt: when persisting the
// post-run validation fails, collect must still FinishAttempt (best-effort
// persist) instead of stranding the attempt in RUNNING (A-8).
func TestCollectValidationPersistFailureStillFinishesAttempt(t *testing.T) {
	env := newWorkerEnv(t, nil)
	task := submitTask(t, env, "collect-persist-fail", 1, nil)
	failing := &validationFailingCP{
		WorkerControlPlane: env.store,
		validateErr:        errors.New("simulated validation persist failure"),
	}
	env.sup.cp = failing

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-collect-persist", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce returned unexpected error: %v", err)
	}
	if final == nil {
		t.Fatal("RunOnce returned nil final task")
	}
	if failing.resultCalls.Load() != 1 {
		t.Fatalf("expected exactly 1 best-effort ValidateResult call, got %d", failing.resultCalls.Load())
	}
	if failing.contractCalls.Load() != 1 {
		t.Fatalf("expected exactly 1 best-effort ValidateContract call, got %d", failing.contractCalls.Load())
	}

	got, err := env.store.GetTask(context.Background(), task.TaskID)
	if err != nil || got == nil {
		t.Fatalf("get task: %v", err)
	}
	if got.State != "WORKER_COMPLETED" {
		t.Fatalf("attempt stranded: state = %q, want WORKER_COMPLETED", got.State)
	}
	if got.LeaseToken != nil || got.LeaseOwner != nil {
		t.Fatal("lease leak after collect persist failure")
	}
	var resultMap map[string]any
	if err := json.Unmarshal(got.Result, &resultMap); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if resultMap["ok"] != true {
		t.Fatalf("child succeeded; persist failure must not rewrite the outcome, got %v", resultMap["ok"])
	}
	events, err := env.store.Events(context.Background(), task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	finished := false
	for _, e := range events {
		if e.EventType == "attempt_finished" {
			finished = true
		}
	}
	if !finished {
		t.Fatalf("expected attempt_finished event, got %+v", events)
	}
}
