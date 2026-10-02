package controlplane

// coverage_gap_test.go — closes long-standing 0% holes in the controlplane
// package (checkpoint/resume, requeue, contract validation, session quota,
// rate-limit error identity) so the coverage ratchet (D-06) reflects the
// real surface.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func submitGapTask(t *testing.T, store *Store, key string) *Task {
	t.Helper()
	task, err := store.SubmitTask(context.Background(), SubmitTaskRequest{
		IdempotencyKey: key,
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{testScopeDir},
		Payload:        json.RawMessage(`{"prompt":"gap test"}`),
	})
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	return task
}

func TestIsRetryable_Classification(t *testing.T) {
	if !ClassRetryable.IsRetryable() {
		t.Fatal("ClassRetryable must report retryable")
	}
	if ClassNotRetryable.IsRetryable() {
		t.Fatal("ClassNotRetryable must not report retryable")
	}
	if RetryClass("garbage").IsRetryable() {
		t.Fatal("unknown class must not report retryable")
	}
}

func TestRequeueResult_RefusesNonTerminalSource(t *testing.T) {
	store, _ := newTestStore(t)
	task := submitGapTask(t, store, "gap-requeue-1")
	// QUEUED is not WORKER_COMPLETED: RequeueResult must refuse with a
	// message naming the offending state.
	err := store.RequeueResult(context.Background(), task.TaskID, "sup-1", "needs another pass")
	if err == nil {
		t.Fatal("RequeueResult on non-WORKER_COMPLETED task must fail")
	}
}

func TestCheckpointResume_StateGuards(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	task := submitGapTask(t, store, "gap-ckpt-1")
	if _, err := store.ClaimTask(ctx, "gap-worker", 600); err != nil {
		t.Fatalf("claim: %v", err)
	}
	claimed, err := store.GetTask(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if claimed.State != StateLeased {
		t.Fatalf("expected LEASED, got %s", claimed.State)
	}
	if claimed.LeaseToken == nil {
		t.Fatal("claimed task must carry a lease token")
	}
	// LEASED is not RUNNING: checkpoint must refuse.
	if _, err := store.CheckpointTask(ctx, task.TaskID, "gap-worker", *claimed.LeaseToken, &CheckpointData{}); err == nil {
		t.Fatal("CheckpointTask on non-RUNNING task must fail")
	}
	// No checkpoint exists: resume must fail cleanly, not panic.
	if _, err := store.ResumeFromCheckpoint(ctx, task.TaskID, "gap-worker", *claimed.LeaseToken, 600); err == nil {
		t.Fatal("ResumeFromCheckpoint without a checkpoint must fail")
	}
}

func TestValidateContract_Persists(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	task := submitGapTask(t, store, "gap-contract-1")
	cv := ContractValidation{
		Valid:          false,
		PathViolations: []string{"../escape"},
		ValidatedBy:    "gap-test",
	}
	if err := store.ValidateContract(ctx, task.TaskID, cv); err != nil {
		t.Fatalf("ValidateContract: %v", err)
	}
	got, err := store.GetTask(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ContractValidation == nil || len(got.ContractValidation.PathViolations) != 1 {
		t.Fatalf("contract validation not persisted: %+v", got.ContractValidation)
	}
}

func TestSessionQuota_RoundTrip(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	// Defaults when unset.
	q, err := store.GetSessionQuota(ctx, "gap-sess-q")
	if err != nil || q == nil {
		t.Fatalf("GetSessionQuota default: %v %+v", err, q)
	}
	if err := store.SetSessionQuota(ctx, SessionQuota{
		SessionID:          "gap-sess-q",
		MaxConcurrentTasks: 2,
		MaxTasksPerHour:    7,
	}); err != nil {
		t.Fatalf("SetSessionQuota: %v", err)
	}
	q2, err := store.GetSessionQuota(ctx, "gap-sess-q")
	if err != nil || q2.MaxConcurrentTasks != 2 || q2.MaxTasksPerHour != 7 {
		t.Fatalf("quota round-trip: %v %+v", err, q2)
	}
	ok, err := store.CheckSessionQuota(ctx, "gap-sess-q")
	if err != nil || !ok {
		t.Fatalf("CheckSessionQuota: %v %v", ok, err)
	}
}

func TestSubmitRateLimit_ErrorIdentity(t *testing.T) {
	target := ErrSubmitRateLimited{Actor: "gap-a", Limit: 5}
	if !errors.Is(ErrSubmitRateLimited{Actor: "other", Limit: 1}, target) {
		t.Fatal("ErrSubmitRateLimited.Is must match by type regardless of field values")
	}
}
