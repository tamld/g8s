package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDeliverablePointerRecordedOnPreservedWorktreeWrite verifies requirement 1:
// workspace_write task + fake isolator + attempt succeeds → the result recorded
// via FinishAttempt parses as JSON containing deliverable.mode == "worktree"
// and deliverable.dir == fake worktree dir.
// Also verifies requirement 5 (regression): other result keys like ok remain intact.
func TestDeliverablePointerRecordedOnPreservedWorktreeWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wt-deliv")
	iso := &fakeWorktreeIsolator{dir: dir}
	env := newIsolationEnv(t, iso)

	task := submitTask(t, env, "deliv-success", 1, map[string]any{"permission": "workspace_write"})
	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-deliv", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if final == nil {
		t.Fatal("RunOnce returned nil task")
	}

	got, err := env.store.GetTask(context.Background(), task.TaskID)
	if err != nil || got == nil {
		t.Fatalf("get task: %v", err)
	}

	var resultMap map[string]any
	if err := json.Unmarshal(got.Result, &resultMap); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	// Requirement 5 regression: existing keys like "ok" still present alongside
	if okVal, exists := resultMap["ok"]; !exists || okVal != true {
		t.Fatalf("expected ok=true in result map, got %v", resultMap["ok"])
	}

	// Requirement 1: deliverable.mode == "worktree", deliverable.dir == fake worktree dir
	delivRaw, ok := resultMap["deliverable"]
	if !ok {
		t.Fatalf("expected deliverable key in result, got: %s", string(got.Result))
	}
	delivMap, ok := delivRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected deliverable to be a map, got %T: %v", delivRaw, delivRaw)
	}
	if delivMap["mode"] != "worktree" {
		t.Errorf("expected deliverable.mode == %q, got %q", "worktree", delivMap["mode"])
	}
	if delivMap["dir"] != dir {
		t.Errorf("expected deliverable.dir == %q, got %q", dir, delivMap["dir"])
	}

	// Requirement 0 regression: preserved worktree dir still exists after RunOnce returns
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("preserved worktree dir %s must still exist after RunOnce returns: %v", dir, err)
	}
	if iso.released != 1 || !iso.keepFlag {
		t.Fatalf("worktree must be released exactly once with keep=true, got released=%d keep=%v", iso.released, iso.keepFlag)
	}
}

// TestDeliverablePointerOmittedWhenIsolatorNil verifies requirement 2:
// workspace_write task + isolator nil (legacy shared checkout) → no deliverable key.
func TestDeliverablePointerOmittedWhenIsolatorNil(t *testing.T) {
	env := newWorkerEnv(t, nil) // isolator is nil
	task := submitTask(t, env, "deliv-noiso", 1, map[string]any{"permission": "workspace_write"})
	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-noiso", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if final == nil {
		t.Fatal("RunOnce returned nil task")
	}

	got, err := env.store.GetTask(context.Background(), task.TaskID)
	if err != nil || got == nil {
		t.Fatalf("get task: %v", err)
	}

	var resultMap map[string]any
	if err := json.Unmarshal(got.Result, &resultMap); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if _, exists := resultMap["deliverable"]; exists {
		t.Fatalf("expected no deliverable key when isolator is nil, got: %v", resultMap["deliverable"])
	}
	if okVal, exists := resultMap["ok"]; !exists || okVal != true {
		t.Fatalf("expected ok=true in result map, got %v", resultMap["ok"])
	}
}

// TestDeliverablePointerOmittedForReadOnlyTask verifies requirement 3:
// read_only task + fake isolator present → no deliverable key (isolator only consulted for workspace_write).
func TestDeliverablePointerOmittedForReadOnlyTask(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wt-ro")
	iso := &fakeWorktreeIsolator{dir: dir}
	env := newIsolationEnv(t, iso)

	task := submitTask(t, env, "deliv-ro", 1, map[string]any{"permission": "read_only"})
	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-ro", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if final == nil {
		t.Fatal("RunOnce returned nil task")
	}

	got, err := env.store.GetTask(context.Background(), task.TaskID)
	if err != nil || got == nil {
		t.Fatalf("get task: %v", err)
	}

	var resultMap map[string]any
	if err := json.Unmarshal(got.Result, &resultMap); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if _, exists := resultMap["deliverable"]; exists {
		t.Fatalf("expected no deliverable key for read_only task, got: %v", resultMap["deliverable"])
	}
	if iso.acquired != 0 {
		t.Fatalf("isolator must not be consulted for read_only, acquired=%d", iso.acquired)
	}
}

// TestDeliverablePointerOmittedOnAttemptFailure verifies requirement 4:
// workspace_write + attempt fails → no deliverable key.
func TestDeliverablePointerOmittedOnAttemptFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wt-fail")
	iso := &fakeWorktreeIsolator{dir: dir}
	env := newIsolationEnv(t, iso)

	env.runner.factory = func(opts SpawnOptions) Child {
		child := newFakeChild(1)
		child.finishLater(opts.ResultPath, `{"ok":false,"status":"failed","reason":"test simulated failure"}`, 5*time.Millisecond)
		return child
	}

	task := submitTask(t, env, "deliv-fail", 1, map[string]any{"permission": "workspace_write"})
	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-fail", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if final == nil {
		t.Fatal("RunOnce returned nil task")
	}

	got, err := env.store.GetTask(context.Background(), task.TaskID)
	if err != nil || got == nil {
		t.Fatalf("get task: %v", err)
	}

	var resultMap map[string]any
	if err := json.Unmarshal(got.Result, &resultMap); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if _, exists := resultMap["deliverable"]; exists {
		t.Fatalf("expected no deliverable key on attempt failure, got: %v", resultMap["deliverable"])
	}
	if okVal, exists := resultMap["ok"]; !exists || okVal != false {
		t.Fatalf("expected ok=false in result map, got %v", resultMap["ok"])
	}
}
