package controlplane

// Red-test for #986 batch-1 (blind audit registry-a.json, A-12):
//
//	CancelTask, RejectResult, RequestEdit, and RequeueResult issue bare
//	BeginTx without the withBusyRetry wrapper every other lifecycle
//	mutation (AcceptResult, AddErrorCall, …) uses. Under a held
//	SQLITE_BUSY write lock they must retry to completion instead of
//	surfacing "database is locked".
//
// Divergence from the parked fix/986-part2-worker implementation
// (ce6227a): the fixture uses the legacy StartTask shim (background
// context) instead of the parked ctx-threaded signature, so this file
// compiles and demonstrates red both before and after the A-6 shim.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// completedFixture drives a task from QUEUED through claim/start/finish
// into WORKER_COMPLETED so the supervisor result ops have valid input.
func completedFixture(t *testing.T, store *Store, key string) *Task {
	t.Helper()
	ctx := context.Background()
	submitFixture(t, store, key, 2)
	claimed, err := store.ClaimTask(ctx, "w-"+key, 60)
	if err != nil || claimed == nil {
		t.Fatalf("claim %s: %v %+v", key, err, claimed)
	}
	if !store.StartTask(claimed.TaskID, "w-"+key, deref(claimed.LeaseToken)) {
		t.Fatalf("start %s: lease lost", key)
	}
	final, err := store.FinishAttempt(claimed.TaskID, "w-"+key, deref(claimed.LeaseToken), FinishAttemptParams{
		Result:  []byte(`{"ok":true,"status":"succeeded"}`),
		Success: true,
	})
	if err != nil {
		t.Fatalf("finish %s: %v", key, err)
	}
	return final
}

func assertBusyRetried(t *testing.T, name string, dbPath string, backoffCalls *atomic.Int32, op func() error, taskID, wantState string, store *Store) {
	t.Helper()
	ctx := context.Background()
	before := backoffCalls.Load()
	runRecoveryProbe(t, dbPath, backoffCalls, func() {
		if err := op(); err != nil {
			t.Fatalf("%s under external write lock: %v (bare BeginTx without withBusyRetry?)", name, err)
		}
	})
	if backoffCalls.Load()-before < 1 {
		t.Fatalf("%s: expected at least one backoff increment, got none (no withBusyRetry)", name)
	}
	got, err := store.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("%s: get task: %v", name, err)
	}
	if got.State != wantState {
		t.Fatalf("%s: state = %s, want %s (retry must reach the committed transition)", name, got.State, wantState)
	}
}

func TestSupervisorResultOpsRetryOnBusy(t *testing.T) {
	origAttempts, origBackoff := busyRetryAttempts, busyBackoff
	t.Cleanup(func() {
		busyRetryAttempts = origAttempts
		busyBackoff = origBackoff
	})

	store, dbPath := newTestStore(t)
	ctx := context.Background()
	store.db.SetMaxOpenConns(1)
	if _, err := store.db.Exec("PRAGMA busy_timeout = 1"); err != nil {
		t.Fatalf("set busy_timeout: %v", err)
	}

	busyRetryAttempts = 5
	var backoffCalls atomic.Int32
	busyBackoff = func(int) time.Duration {
		backoffCalls.Add(1)
		return 0
	}

	// Complete the WORKER_COMPLETED fixtures first: each completedFixture
	// submits and then claims the oldest QUEUED task, so every fixture must
	// run while its own task is the only queued one (queue claims are
	// oldest-first across the whole session-less queue).
	completed := []*Task{
		completedFixture(t, store, "busy-reject"),
		completedFixture(t, store, "busy-edit"),
		completedFixture(t, store, "busy-requeue"),
	}
	cancelTask := submitFixture(t, store, "busy-cancel", 1)

	assertBusyRetried(t, "CancelTask", dbPath, &backoffCalls,
		func() error { return store.CancelTask(ctx, cancelTask.TaskID, "probe") },
		cancelTask.TaskID, StateCancelled, store)

	assertBusyRetried(t, "RejectResult", dbPath, &backoffCalls,
		func() error { return store.RejectResult(ctx, completed[0].TaskID, "sup", "reason", "ctx") },
		completed[0].TaskID, StateSupervisorRejected, store)

	assertBusyRetried(t, "RequestEdit", dbPath, &backoffCalls,
		func() error { return store.RequestEdit(ctx, completed[1].TaskID, "sup", "reason", "ctx") },
		completed[1].TaskID, StateNeedsInfo, store)

	assertBusyRetried(t, "RequeueResult", dbPath, &backoffCalls,
		func() error { return store.RequeueResult(ctx, completed[2].TaskID, "sup", "reason") },
		completed[2].TaskID, StateQueued, store)
}
