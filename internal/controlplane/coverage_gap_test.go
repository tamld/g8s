package controlplane

// coverage_gap_test.go — closes long-standing 0% holes in the controlplane
// package (checkpoint/resume, requeue, contract validation, session quota,
// rate-limit error identity) so the coverage ratchet (D-06) reflects the
// real surface.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
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

// #290: CheckpointTask persists worker progress and verifies lease CAS semantics.
func TestCheckpointTask_HappyPathAndErrors(t *testing.T) {
	store, dbPath := newTestStore(t)
	ctx := context.Background()
	task := submitGapTask(t, store, "gap-ckpt-happy-1")

	// Must be RUNNING to checkpoint.
	claimed, err := store.ClaimTask(ctx, "worker-1", 600)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	token := *claimed.LeaseToken
	if !store.StartTask(task.TaskID, "worker-1", token) {
		t.Fatal("StartTask failed")
	}

	ckpt1 := &CheckpointData{
		SourceHashes:     map[string]string{"main.go": "sha256:abc"},
		WorktreePath:     "/tmp/wt-1",
		CheckpointNumber: 1,
		Timestamp:        1000.0,
		WorkerState:      json.RawMessage(`{"step":1}`),
		CompletedSteps:   []string{"init", "compile"},
	}

	updated, err := store.CheckpointTask(ctx, task.TaskID, "worker-1", token, ckpt1)
	if err != nil {
		t.Fatalf("CheckpointTask: %v", err)
	}
	if updated.CheckpointData == nil {
		t.Fatal("expected CheckpointData on updated task")
	}
	if updated.CheckpointData.CheckpointNumber != 1 || updated.CheckpointData.WorktreePath != "/tmp/wt-1" {
		t.Fatalf("unexpected CheckpointData: %+v", updated.CheckpointData)
	}
	if len(updated.CheckpointData.CompletedSteps) != 2 {
		t.Fatalf("expected 2 completed steps, got %d", len(updated.CheckpointData.CompletedSteps))
	}

	// Verify persistence in raw DB (#290).
	rawDB := openRawDB(t, dbPath)
	var rawData sql.NullString
	if err := rawDB.QueryRow("SELECT checkpoint_data FROM tasks WHERE task_id = ?", task.TaskID).Scan(&rawData); err != nil {
		t.Fatalf("raw DB query: %v", err)
	}
	if !rawData.Valid || !strings.Contains(rawData.String, "main.go") {
		t.Fatalf("persisted checkpoint_data column invalid: %s", rawData.String)
	}

	// Second checkpoint succeeds and updates state.
	ckpt2 := &CheckpointData{
		SourceHashes:     map[string]string{"main.go": "sha256:abc", "sub.go": "sha256:def"},
		WorktreePath:     "/tmp/wt-1",
		CheckpointNumber: 2,
		Timestamp:        2000.0,
		WorkerState:      json.RawMessage(`{"step":2}`),
		CompletedSteps:   []string{"init", "compile", "test"},
	}
	updated2, err := store.CheckpointTask(ctx, task.TaskID, "worker-1", token, ckpt2)
	if err != nil {
		t.Fatalf("second CheckpointTask failed: %v", err)
	}
	if updated2.CheckpointData.CheckpointNumber != 2 {
		t.Fatalf("expected CheckpointNumber 2, got %d", updated2.CheckpointData.CheckpointNumber)
	}

	// Error path: wrong lease token returns ErrLeaseLost (#348).
	if _, err := store.CheckpointTask(ctx, task.TaskID, "worker-1", "wrong-token", ckpt2); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expected ErrLeaseLost for wrong token, got: %v", err)
	}

	// Error path: wrong worker returns ErrLeaseLost (#348).
	if _, err := store.CheckpointTask(ctx, task.TaskID, "wrong-worker", token, ckpt2); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expected ErrLeaseLost for wrong worker, got: %v", err)
	}

	// Error path: unknown task returns ErrUnknownTask.
	if _, err := store.CheckpointTask(ctx, "nonexistent-task-id", "worker-1", token, ckpt2); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("expected ErrUnknownTask for unknown task, got: %v", err)
	}
}

// #290, #348: ResumeFromCheckpoint restores a CHECKPOINTED task to RUNNING with a new lease.
func TestResumeFromCheckpoint_HappyPathAndErrors(t *testing.T) {
	store, dbPath := newTestStore(t)
	ctx := context.Background()
	task := submitGapTask(t, store, "gap-resume-happy-1")

	claimed, err := store.ClaimTask(ctx, "worker-1", 600)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	token := *claimed.LeaseToken
	if !store.StartTask(task.TaskID, "worker-1", token) {
		t.Fatal("StartTask failed")
	}

	ckpt := &CheckpointData{
		WorktreePath:     "/tmp/wt-resume",
		CheckpointNumber: 1,
		Timestamp:        1500.0,
	}
	if _, err := store.CheckpointTask(ctx, task.TaskID, "worker-1", token, ckpt); err != nil {
		t.Fatalf("CheckpointTask: %v", err)
	}

	// Pause to StateCheckpointed.
	paused, err := store.PauseTask(task.TaskID, "worker-1", token, StateCheckpointed, json.RawMessage(`{"paused":true}`), "checkpoint pause")
	if err != nil {
		t.Fatalf("PauseTask: %v", err)
	}
	if paused.State != StateCheckpointed {
		t.Fatalf("expected state CHECKPOINTED, got %s", paused.State)
	}

	// Resume from checkpoint: assigns new worker and lease.
	resumed, err := store.ResumeFromCheckpoint(ctx, task.TaskID, "worker-resume", "token-resume-456", 900)
	if err != nil {
		t.Fatalf("ResumeFromCheckpoint: %v", err)
	}
	if resumed.State != StateRunning {
		t.Fatalf("expected state RUNNING, got %s", resumed.State)
	}
	if resumed.LeaseOwner == nil || *resumed.LeaseOwner != "worker-resume" {
		t.Fatalf("expected LeaseOwner worker-resume, got %v", resumed.LeaseOwner)
	}
	if resumed.LeaseToken == nil || *resumed.LeaseToken != "token-resume-456" {
		t.Fatalf("expected LeaseToken token-resume-456, got %v", resumed.LeaseToken)
	}
	if resumed.LeaseExpiresAt == nil || *resumed.LeaseExpiresAt <= 0 {
		t.Fatalf("expected positive LeaseExpiresAt, got %v", resumed.LeaseExpiresAt)
	}

	// Raw DB check confirms RUNNING state and updated lease columns.
	rawDB := openRawDB(t, dbPath)
	var state, leaseOwner, leaseToken string
	if err := rawDB.QueryRow("SELECT state, lease_owner, lease_token FROM tasks WHERE task_id = ?", task.TaskID).
		Scan(&state, &leaseOwner, &leaseToken); err != nil {
		t.Fatalf("raw DB scan: %v", err)
	}
	if state != StateRunning || leaseOwner != "worker-resume" || leaseToken != "token-resume-456" {
		t.Fatalf("persisted row mismatch: state=%s owner=%s token=%s", state, leaseOwner, leaseToken)
	}

	// Error path: wrong state (task is now RUNNING, not CHECKPOINTED).
	if _, err := store.ResumeFromCheckpoint(ctx, task.TaskID, "worker-resume", "token-resume-456", 900); err == nil {
		t.Fatal("ResumeFromCheckpoint on RUNNING task must fail")
	}

	// Error path: unknown task returns ErrUnknownTask.
	if _, err := store.ResumeFromCheckpoint(ctx, "nonexistent-task", "worker-1", "token", 900); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("expected ErrUnknownTask for unknown task, got: %v", err)
	}
}

// #383, #481: RequeueResult transitions a WORKER_COMPLETED task to QUEUED and logs feedback.
func TestRequeueResult_HappyPath(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	task := submitGapTask(t, store, "gap-requeue-happy-1")

	claimed, err := store.ClaimTask(ctx, "worker-1", 600)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	token := *claimed.LeaseToken
	if !store.StartTask(task.TaskID, "worker-1", token) {
		t.Fatal("StartTask failed")
	}

	// Finish attempt -> transitions to StateWorkerCompleted.
	finished, err := store.FinishAttempt(task.TaskID, "worker-1", token, FinishAttemptParams{
		Success:   false,
		Retryable: true,
		Err:       "transient network failure",
		Result:    json.RawMessage(`{"status":"fail"}`),
	})
	if err != nil {
		t.Fatalf("FinishAttempt: %v", err)
	}
	if finished.State != StateWorkerCompleted {
		t.Fatalf("expected WORKER_COMPLETED, got %s", finished.State)
	}

	// RequeueResult happy path.
	if err := store.RequeueResult(ctx, task.TaskID, "supervisor-alpha", "retry retryable network failure"); err != nil {
		t.Fatalf("RequeueResult: %v", err)
	}

	requeued, err := store.GetTask(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if requeued.State != StateQueued {
		t.Fatalf("expected state QUEUED, got %s", requeued.State)
	}
	if requeued.SupervisorFeedback == nil {
		t.Fatal("expected non-nil SupervisorFeedback")
	}
	if requeued.SupervisorFeedback.Action != "requeue" {
		t.Fatalf("expected action 'requeue', got %s", requeued.SupervisorFeedback.Action)
	}
	if requeued.SupervisorFeedback.Reason != "retry retryable network failure" {
		t.Fatalf("unexpected reason: %s", requeued.SupervisorFeedback.Reason)
	}
	if requeued.SupervisorFeedback.SupervisorID != "supervisor-alpha" {
		t.Fatalf("unexpected supervisor_id: %s", requeued.SupervisorFeedback.SupervisorID)
	}
	if requeued.SupervisorFeedback.Timestamp <= 0 {
		t.Fatalf("expected positive timestamp, got %f", requeued.SupervisorFeedback.Timestamp)
	}

	// Error path: non-existent task.
	if err := store.RequeueResult(ctx, "nonexistent-task", "sup-1", "reason"); err == nil {
		t.Fatal("RequeueResult on non-existent task must fail")
	}
}

// #481: extractActorFromReq extracts actor name with fallback precedence.
func TestExtractActorFromReq(t *testing.T) {
	ptr := func(s string) *string { return &s }

	tests := []struct {
		name        string
		req         SubmitTaskRequest
		requestJSON string
		want        string
	}{
		{
			name:        "json actor present",
			requestJSON: `{"actor":"alice"}`,
			want:        "alice",
		},
		{
			name:        "json actor trimmed",
			requestJSON: `{"actor":"  bob  "}`,
			want:        "bob",
		},
		{
			name:        "json actor whitespace only falls back to orch",
			requestJSON: `{"actor":"   "}`,
			req:         SubmitTaskRequest{OrchestratorID: ptr("orch-1")},
			want:        "orch-1",
		},
		{
			name:        "json actor empty string falls back to orch",
			requestJSON: `{"actor":""}`,
			req:         SubmitTaskRequest{OrchestratorID: ptr("orch-1")},
			want:        "orch-1",
		},
		{
			name:        "json actor non-string falls back to worker",
			requestJSON: `{"actor":123}`,
			req:         SubmitTaskRequest{WorkerName: ptr("worker-1")},
			want:        "worker-1",
		},
		{
			name:        "invalid json falls back to session",
			requestJSON: `not-valid-json`,
			req:         SubmitTaskRequest{SessionID: ptr("sess-1")},
			want:        "sess-1",
		},
		{
			name: "empty json with orchestrator id",
			req:  SubmitTaskRequest{OrchestratorID: ptr("orch-2")},
			want: "orch-2",
		},
		{
			name: "empty json with worker name",
			req:  SubmitTaskRequest{WorkerName: ptr("worker-2")},
			want: "worker-2",
		},
		{
			name: "empty json with session id",
			req:  SubmitTaskRequest{SessionID: ptr("sess-2")},
			want: "sess-2",
		},
		{
			name: "all empty defaults to operator",
			req:  SubmitTaskRequest{},
			want: "operator",
		},
		{
			name: "whitespace orchestrator id falls back to worker",
			req:  SubmitTaskRequest{OrchestratorID: ptr("   "), WorkerName: ptr("worker-3")},
			want: "worker-3",
		},
		{
			name: "whitespace worker name falls back to session",
			req:  SubmitTaskRequest{WorkerName: ptr("   "), SessionID: ptr("sess-3")},
			want: "sess-3",
		},
		{
			name: "whitespace session id falls back to operator",
			req:  SubmitTaskRequest{SessionID: ptr("   ")},
			want: "operator",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractActorFromReq(tt.req, tt.requestJSON)
			if got != tt.want {
				t.Fatalf("extractActorFromReq() = %q, want %q", got, tt.want)
			}
		})
	}

	// Also exercise Store.SetSubmitRateLimitPerHour method.
	store, _ := newTestStore(t)
	store.SetSubmitRateLimitPerHour(25)
	if GetSubmitRateLimitPerHour() != 25 {
		t.Fatalf("expected rate limit 25, got %d", GetSubmitRateLimitPerHour())
	}
	store.SetSubmitRateLimitPerHour(0) // reset
}

// #420: ListSupervisorDecisions retrieves audit decisions in ascending timestamp order.
func TestListSupervisorDecisions(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	// Error path: empty task_id
	if _, err := store.ListSupervisorDecisions(ctx, ""); err == nil {
		t.Fatal("ListSupervisorDecisions with empty taskID must fail")
	}

	baseTime := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	if err := store.CreateSupervisorTask(ctx, SupervisorTaskRow{
		ID:           "task-sup-list",
		State:        "RUNNING",
		EnvelopeJSON: "{}",
	}); err != nil {
		t.Fatalf("CreateSupervisorTask task-sup-list: %v", err)
	}
	if err := store.CreateSupervisorTask(ctx, SupervisorTaskRow{
		ID:           "other-task",
		State:        "RUNNING",
		EnvelopeJSON: "{}",
	}); err != nil {
		t.Fatalf("CreateSupervisorTask other-task: %v", err)
	}

	dec1 := SupervisorDecisionRow{
		ID:          "dec-1",
		TaskID:      "task-sup-list",
		Kind:        "action_approve",
		PayloadJSON: `{"reason":"step approved"}`,
		CreatedAt:   baseTime,
	}
	dec2 := SupervisorDecisionRow{
		ID:          "dec-2",
		TaskID:      "task-sup-list",
		Kind:        "action_complete",
		PayloadJSON: `{"reason":"all steps done"}`,
		CreatedAt:   baseTime.Add(5 * time.Second),
	}
	decOther := SupervisorDecisionRow{
		ID:          "dec-other",
		TaskID:      "other-task",
		Kind:        "action_other",
		PayloadJSON: `{}`,
		CreatedAt:   baseTime.Add(2 * time.Second),
	}

	if err := store.AppendDecision(ctx, dec2); err != nil {
		t.Fatalf("AppendDecision dec2: %v", err)
	}
	if err := store.AppendDecision(ctx, dec1); err != nil {
		t.Fatalf("AppendDecision dec1: %v", err)
	}
	if err := store.AppendDecision(ctx, decOther); err != nil {
		t.Fatalf("AppendDecision decOther: %v", err)
	}

	list, err := store.ListSupervisorDecisions(ctx, "task-sup-list")
	if err != nil {
		t.Fatalf("ListSupervisorDecisions: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 decisions, got %d", len(list))
	}

	// Must be ordered by created_at ASC: dec1 then dec2
	if list[0].ID != "dec-1" || list[0].Kind != "action_approve" {
		t.Fatalf("unexpected first decision: %+v", list[0])
	}
	if list[1].ID != "dec-2" || list[1].Kind != "action_complete" {
		t.Fatalf("unexpected second decision: %+v", list[1])
	}
	if list[0].PayloadJSON != `{"reason":"step approved"}` {
		t.Fatalf("unexpected payload JSON: %s", list[0].PayloadJSON)
	}
}

// #420: GetSupervisorTaskWorker resolves worker name across tasks, decisions, and supervisor envelopes.
func TestGetSupervisorTaskWorker(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	// Error path: empty ID
	if _, err := store.GetSupervisorTaskWorker(ctx, ""); err == nil {
		t.Fatal("empty supervisorTaskID must error")
	}

	// Unknown ID returns empty string, no error
	got, err := store.GetSupervisorTaskWorker(ctx, "unknown-sup-id")
	if err != nil {
		t.Fatalf("unexpected error for unknown ID: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty worker name for unknown ID, got %q", got)
	}

	// Path 1: Resolved from tasks table by task_id
	workerNameA := "worker-direct"
	taskA, err := store.SubmitTask(ctx, SubmitTaskRequest{
		IdempotencyKey: "worker-res-1",
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{testScopeDir},
		Payload:        json.RawMessage(`{"prompt":"test"}`),
		WorkerName:     &workerNameA,
	})
	if err != nil {
		t.Fatalf("submit taskA: %v", err)
	}
	w1, err := store.GetSupervisorTaskWorker(ctx, taskA.TaskID)
	if err != nil || w1 != "worker-direct" {
		t.Fatalf("expected 'worker-direct', got %q (err: %v)", w1, err)
	}

	// Path 1b: Resolved from tasks table by parent_task_id
	parentTask := submitGapTask(t, store, "parent-key-1")
	workerNameB := "worker-child"
	_, err = store.SubmitTask(ctx, SubmitTaskRequest{
		IdempotencyKey: "worker-res-2",
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{testScopeDir},
		Payload:        json.RawMessage(`{"prompt":"test"}`),
		ParentTaskID:   &parentTask.TaskID,
		WorkerName:     &workerNameB,
	})
	if err != nil {
		t.Fatalf("submit child task: %v", err)
	}
	w2, err := store.GetSupervisorTaskWorker(ctx, parentTask.TaskID)
	if err != nil || w2 != "worker-child" {
		t.Fatalf("expected 'worker-child' via parent_task_id, got %q (err: %v)", w2, err)
	}

	// Path 2: Resolved from supervisor_decisions table
	decTaskID := "sup-dec-task-1"
	if err := store.CreateSupervisorTask(ctx, SupervisorTaskRow{
		ID:           decTaskID,
		State:        "RUNNING",
		EnvelopeJSON: "{}",
	}); err != nil {
		t.Fatalf("CreateSupervisorTask decTaskID: %v", err)
	}
	if err := store.AppendDecision(ctx, SupervisorDecisionRow{
		TaskID:      decTaskID,
		Kind:        "assign",
		PayloadJSON: `{"worker_name": "worker-from-dec"}`,
	}); err != nil {
		t.Fatalf("AppendDecision: %v", err)
	}
	w3, err := store.GetSupervisorTaskWorker(ctx, decTaskID)
	if err != nil || w3 != "worker-from-dec" {
		t.Fatalf("expected 'worker-from-dec', got %q (err: %v)", w3, err)
	}

	// Path 3: Resolved from supervisor_tasks table envelope
	stID := "sup-st-task-1"
	if err := store.CreateSupervisorTask(ctx, SupervisorTaskRow{
		ID:           stID,
		State:        "RUNNING",
		EnvelopeJSON: `{"request": {"worker_name": "worker-from-st-env"}}`,
	}); err != nil {
		t.Fatalf("CreateSupervisorTask: %v", err)
	}
	w4, err := store.GetSupervisorTaskWorker(ctx, stID)
	if err != nil || w4 != "worker-from-st-env" {
		t.Fatalf("expected 'worker-from-st-env', got %q (err: %v)", w4, err)
	}
}

// #420: extractWorkerNameFromJSON inspects diverse nested and top-level schema variants.
func TestExtractWorkerNameFromJSON(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{name: "invalid json", json: `not-valid-json`, want: ""},
		{name: "empty json object", json: `{}`, want: ""},
		{name: "top-level worker_name", json: `{"worker_name":"worker-1"}`, want: "worker-1"},
		{name: "top-level WorkerName", json: `{"WorkerName":"worker-2"}`, want: "worker-2"},
		{name: "top-level worker", json: `{"worker":"worker-3"}`, want: "worker-3"},
		{name: "receipt worker_name", json: `{"receipt":{"worker_name":"worker-4"}}`, want: "worker-4"},
		{name: "receipt WorkerName", json: `{"receipt":{"WorkerName":"worker-5"}}`, want: "worker-5"},
		{name: "request worker_name", json: `{"request":{"worker_name":"worker-6"}}`, want: "worker-6"},
		{name: "request WorkerName", json: `{"request":{"WorkerName":"worker-7"}}`, want: "worker-7"},
		{name: "request worker", json: `{"request":{"worker":"worker-8"}}`, want: "worker-8"},
		{name: "empty strings fall through", json: `{"worker_name":"","request":{"worker":"worker-9"}}`, want: "worker-9"},
		{name: "non-string ignored", json: `{"worker_name":123,"worker":"worker-10"}`, want: "worker-10"},
		{name: "unrelated keys only", json: `{"foo":"bar","baz":123}`, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractWorkerNameFromJSON(tt.json)
			if got != tt.want {
				t.Fatalf("extractWorkerNameFromJSON(%s) = %q, want %q", tt.json, got, tt.want)
			}
		})
	}
}
