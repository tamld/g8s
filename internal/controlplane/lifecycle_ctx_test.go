package controlplane

// Red-test for #986 batch-1 (blind audit registry-a.json, A-6, controlplane
// half): StartTask and ExecutionSignal issue bare Begin()/QueryRow() calls
// that ignore the caller's context — a cancelled or expiring context cannot
// abort the promotion/poll, so callers hang on the connection.
//
// Shim design (diverging from the parked fix/986-part2-worker implementation
// ce6227a, which changed StartTask/ExecutionSignal signatures in place and
// rippled into 15 production + 50 test call sites in worker/orchestrator):
//
//	StartTaskContext(ctx, taskID, workerID, leaseToken) bool   — ctx-disciplined implementation
//	StartTask(taskID, workerID, leaseToken) bool               — deprecated thin wrapper (context.Background)
//	ExecutionSignalContext(ctx, taskID, workerID, leaseToken) string
//	ExecutionSignal(taskID, workerID, leaseToken) string       — deprecated thin wrapper (context.Background)
//
// This keeps the TaskLeaseStore interface (and the worker/orchestrator
// packages) compiling untouched while the ctx-disciplined API becomes the
// canonical entry point.

import (
	"context"
	"testing"
)

// TestStartTaskContextPropagatesCancel verifies a cancelled context aborts
// the LEASED -> RUNNING promotion while the legacy StartTask shim still
// promotes with context.Background().
func TestStartTaskContextPropagatesCancel(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	task := submitFixture(t, store, "ctx-start", 1)

	claimed, err := store.ClaimTask(ctx, "w-ctx", 60)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %+v", err, claimed)
	}
	token := deref(claimed.LeaseToken)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if store.StartTaskContext(canceled, task.TaskID, "w-ctx", token) {
		t.Fatal("StartTaskContext must not promote with a cancelled context")
	}

	got, err := store.GetTask(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.State != StateLeased {
		t.Fatalf("state = %s, want %s (cancelled ctx must abort the promotion)", got.State, StateLeased)
	}

	// Legacy shim still works: background context, same promotion path.
	if !store.StartTask(task.TaskID, "w-ctx", token) {
		t.Fatal("legacy StartTask must still promote (background context)")
	}
	got, err = store.GetTask(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("get task after legacy start: %v", err)
	}
	if got.State != StateRunning {
		t.Fatalf("state = %s, want %s after legacy StartTask", got.State, StateRunning)
	}
}

// TestExecutionSignalContextPropagatesCancel verifies a cancelled context
// yields the fail-safe "lease_lost" signal (worker stops) while the legacy
// ExecutionSignal shim still reports "active" for a healthy lease.
func TestExecutionSignalContextPropagatesCancel(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	task := submitFixture(t, store, "ctx-signal", 1)

	claimed, err := store.ClaimTask(ctx, "w-ctx", 60)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %+v", err, claimed)
	}
	token := deref(claimed.LeaseToken)
	if !store.StartTask(task.TaskID, "w-ctx", token) {
		t.Fatal("start: lease lost")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if got := store.ExecutionSignalContext(canceled, task.TaskID, "w-ctx", token); got != "lease_lost" {
		t.Fatalf("ExecutionSignalContext with cancelled ctx = %q, want %q (fail-safe)", got, "lease_lost")
	}

	if got := store.ExecutionSignalContext(ctx, task.TaskID, "w-ctx", token); got != "active" {
		t.Fatalf("ExecutionSignalContext with live ctx = %q, want %q", got, "active")
	}

	if got := store.ExecutionSignal(task.TaskID, "w-ctx", token); got != "active" {
		t.Fatalf("legacy ExecutionSignal = %q, want %q", got, "active")
	}
}
