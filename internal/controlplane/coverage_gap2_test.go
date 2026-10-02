package controlplane

// coverage_gap2_test.go — closes uncovered session-scoped methods, supervisor
// maintenance/escalation, and task lifecycle pause/resume edges to raise package
// coverage to the >= 85% ratchet target (D-06).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func submitSessionTask(t *testing.T, store *Store, key, sessionID string, parentID *string) *Task {
	t.Helper()
	task, err := store.SubmitTask(context.Background(), SubmitTaskRequest{
		IdempotencyKey: key,
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{testScopeDir},
		Payload:        json.RawMessage(`{"prompt":"session task"}`),
		SessionID:      &sessionID,
		ParentTaskID:   parentID,
	})
	if err != nil {
		t.Fatalf("SubmitTask with session: %v", err)
	}
	return task
}

// #481: TestSessionScopedTasks verifies session-filtered query, lineage, claim, and active count.
func TestSessionScopedTasks(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	// Clock method coverage
	if store.Clock().IsZero() {
		t.Fatal("store.Clock() returned zero time")
	}

	// 1. GetTaskInSession
	t1 := submitSessionTask(t, store, "sess-task-1", "sess-alpha", nil)
	got, err := store.GetTaskInSession(ctx, t1.TaskID, "sess-alpha")
	if err != nil || got == nil || got.TaskID != t1.TaskID {
		t.Fatalf("GetTaskInSession match failed: got %+v, err %v", got, err)
	}
	if gotWrong, err := store.GetTaskInSession(ctx, t1.TaskID, "sess-beta"); err != nil || gotWrong != nil {
		t.Fatalf("expected nil for mismatched session, got %v (err %v)", gotWrong, err)
	}
	if gotNone, err := store.GetTaskInSession(ctx, "nonexistent-task", "sess-alpha"); err != nil || gotNone != nil {
		t.Fatalf("expected nil for unknown task, got %v (err %v)", gotNone, err)
	}

	// 2. ListChildTasksInSession
	if _, err := store.ListChildTasksInSession(ctx, "", "sess-alpha"); err == nil {
		t.Fatal("ListChildTasksInSession with empty parent_task_id must fail")
	}
	if _, err := store.ListChildTasksInSession(ctx, t1.TaskID, ""); err == nil {
		t.Fatal("ListChildTasksInSession with empty session_id must fail")
	}

	c1 := submitSessionTask(t, store, "sess-child-1", "sess-alpha", &t1.TaskID)
	c2 := submitSessionTask(t, store, "sess-child-2", "sess-alpha", &t1.TaskID)
	_ = submitSessionTask(t, store, "sess-child-3", "sess-beta", &t1.TaskID)

	childrenAlpha, err := store.ListChildTasksInSession(ctx, t1.TaskID, "sess-alpha")
	if err != nil {
		t.Fatalf("ListChildTasksInSession alpha: %v", err)
	}
	if len(childrenAlpha) != 2 || childrenAlpha[0].TaskID != c1.TaskID || childrenAlpha[1].TaskID != c2.TaskID {
		t.Fatalf("unexpected child tasks in sess-alpha: %+v", childrenAlpha)
	}

	childrenBeta, err := store.ListChildTasksInSession(ctx, t1.TaskID, "sess-beta")
	if err != nil || len(childrenBeta) != 1 {
		t.Fatalf("ListChildTasksInSession beta: %+v, err %v", childrenBeta, err)
	}

	childrenUnused, err := store.ListChildTasksInSession(ctx, t1.TaskID, "sess-unused")
	if err != nil || len(childrenUnused) != 0 {
		t.Fatalf("ListChildTasksInSession unused: %+v, err %v", childrenUnused, err)
	}

	// 3. GetTaskLineageInSession
	if lin, err := store.GetTaskLineageInSession(ctx, "", "sess-alpha"); err != nil || lin != nil {
		t.Fatalf("empty taskID must return nil, nil: %v, %+v", err, lin)
	}
	if lin, err := store.GetTaskLineageInSession(ctx, t1.TaskID, ""); err != nil || lin != nil {
		t.Fatalf("empty sessionID must return nil, nil: %v, %+v", err, lin)
	}

	grandchild := submitSessionTask(t, store, "sess-grandchild-1", "sess-alpha", &c1.TaskID)
	lineage, err := store.GetTaskLineageInSession(ctx, grandchild.TaskID, "sess-alpha")
	if err != nil {
		t.Fatalf("GetTaskLineageInSession: %v", err)
	}
	// Lineage order is depth DESC: t1 (root, depth 2), c1 (child, depth 1), grandchild (depth 0)
	if len(lineage) != 3 {
		t.Fatalf("expected 3 lineage tasks, got %d: %+v", len(lineage), lineage)
	}
	if lineage[0].TaskID != t1.TaskID || lineage[1].TaskID != c1.TaskID || lineage[2].TaskID != grandchild.TaskID {
		t.Fatalf("unexpected lineage ordering: %+v", lineage)
	}

	// 4. ClaimTaskInSession validation & execution
	if _, err := store.ClaimTaskInSession(ctx, "", "sess-gamma", 300); err == nil {
		t.Fatal("empty worker_id must fail")
	}
	if _, err := store.ClaimTaskInSession(ctx, "w-1", "", 300); err == nil {
		t.Fatal("empty session_id must fail")
	}
	if _, err := store.ClaimTaskInSession(ctx, "w-1", "sess-gamma", 0); err == nil {
		t.Fatal("non-positive lease seconds must fail")
	}

	tGamma := submitSessionTask(t, store, "sess-gamma-1", "sess-gamma", nil)
	claimedGamma, err := store.ClaimTaskInSession(ctx, "worker-gamma", "sess-gamma", 300)
	if err != nil {
		t.Fatalf("ClaimTaskInSession: %v", err)
	}
	if claimedGamma == nil || claimedGamma.TaskID != tGamma.TaskID {
		t.Fatalf("unexpected claimed task: %+v", claimedGamma)
	}
	if claimedGamma.State != StateLeased || claimedGamma.LeaseOwner == nil || *claimedGamma.LeaseOwner != "worker-gamma" {
		t.Fatalf("claimed task not leased to worker-gamma: %+v", claimedGamma)
	}

	// Claiming again when none available returns nil, nil
	noneLeft, err := store.ClaimTaskInSession(ctx, "worker-gamma", "sess-gamma", 300)
	if err != nil || noneLeft != nil {
		t.Fatalf("expected nil task when none queued: %+v, err %v", noneLeft, err)
	}

	// 5. ActiveTaskCountInSession
	activeGamma, err := store.ActiveTaskCountInSession(ctx, "sess-gamma")
	if err != nil {
		t.Fatalf("ActiveTaskCountInSession gamma: %v", err)
	}
	if activeGamma != 1 {
		t.Fatalf("expected 1 active task in gamma, got %d", activeGamma)
	}

	activeUnused, err := store.ActiveTaskCountInSession(ctx, "sess-unused")
	if err != nil || activeUnused != 0 {
		t.Fatalf("expected 0 active tasks in unused session, got %d, err %v", activeUnused, err)
	}
}

// #420: TestSessionScopedSupervisorTasks exercises supervisor session lookups and listings.
func TestSessionScopedSupervisorTasks(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	// 1. GetSupervisorTaskInSession validation & lookup
	if _, err := store.GetSupervisorTaskInSession(ctx, "", "sess-sup-1"); err == nil {
		t.Fatal("empty id must fail")
	}
	if _, err := store.GetSupervisorTaskInSession(ctx, "st-1", ""); err == nil {
		t.Fatal("empty session_id must fail")
	}

	sessID := "sess-sup-1"
	st1 := SupervisorTaskRow{
		ID:           "st-sess-1",
		State:        "RUNNING",
		EnvelopeJSON: `{"task":"one"}`,
		SessionID:    &sessID,
	}
	if err := store.CreateSupervisorTask(ctx, st1); err != nil {
		t.Fatalf("CreateSupervisorTask st1: %v", err)
	}

	got, err := store.GetSupervisorTaskInSession(ctx, "st-sess-1", "sess-sup-1")
	if err != nil || got.ID != "st-sess-1" {
		t.Fatalf("GetSupervisorTaskInSession match failed: %+v, err %v", got, err)
	}
	if _, err := store.GetSupervisorTaskInSession(ctx, "st-sess-1", "sess-other"); !errors.Is(err, ErrUnknownSupervisorTask) {
		t.Fatalf("expected ErrUnknownSupervisorTask for wrong session, got %v", err)
	}

	// 2. ListSupervisorTasksInSession
	if _, err := store.ListSupervisorTasksInSession(ctx, ""); err == nil {
		t.Fatal("ListSupervisorTasksInSession with empty session_id must fail")
	}

	st2 := SupervisorTaskRow{
		ID:           "st-sess-2",
		State:        "RUNNING",
		EnvelopeJSON: `{"task":"two"}`,
		SessionID:    &sessID,
	}
	if err := store.CreateSupervisorTask(ctx, st2); err != nil {
		t.Fatalf("CreateSupervisorTask st2: %v", err)
	}

	sessOther := "sess-sup-2"
	st3 := SupervisorTaskRow{
		ID:           "st-sess-3",
		State:        "COMPLETED",
		EnvelopeJSON: `{"task":"three"}`,
		SessionID:    &sessOther,
	}
	if err := store.CreateSupervisorTask(ctx, st3); err != nil {
		t.Fatalf("CreateSupervisorTask st3: %v", err)
	}

	listSess1, err := store.ListSupervisorTasksInSession(ctx, "sess-sup-1")
	if err != nil || len(listSess1) != 2 {
		t.Fatalf("ListSupervisorTasksInSession sess-sup-1: %d rows (err: %v)", len(listSess1), err)
	}

	listSess2, err := store.ListSupervisorTasksInSession(ctx, "sess-sup-2")
	if err != nil || len(listSess2) != 1 {
		t.Fatalf("ListSupervisorTasksInSession sess-sup-2: %d rows (err: %v)", len(listSess2), err)
	}

	listEmpty, err := store.ListSupervisorTasksInSession(ctx, "sess-empty")
	if err != nil || len(listEmpty) != 0 {
		t.Fatalf("ListSupervisorTasksInSession sess-empty: %d rows (err: %v)", len(listEmpty), err)
	}
}

// #420: TestSupervisorMetricsAndEscalation covers metrics update, false-escalation rate and supervisor task update.
func TestSupervisorMetricsAndEscalation(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	// UpdateFalseEscalationRate validation
	if err := store.UpdateFalseEscalationRate(ctx, "", true); err == nil {
		t.Fatal("empty supervisorTaskID must fail")
	}
	if err := store.UpdateFalseEscalationRate(ctx, "nonexistent-task", true); !errors.Is(err, ErrUnknownSupervisorTask) {
		t.Fatalf("expected ErrUnknownSupervisorTask, got %v", err)
	}

	st := SupervisorTaskRow{
		ID:           "st-metrics-1",
		State:        "RUNNING",
		EnvelopeJSON: `{"run":1}`,
	}
	if err := store.CreateSupervisorTask(ctx, st); err != nil {
		t.Fatalf("CreateSupervisorTask: %v", err)
	}

	initialMetrics := MetricsRow{
		SupervisorTaskID:     "st-metrics-1",
		EnvelopeScore:        0.9,
		FirstAttemptSuccess:  true,
		AttemptsToSuccess:    1,
		ApproachesToSuccess:  1,
		RCAConfidenceAvg:     0.85,
		CycleDurationSeconds: 45.0,
		EscalationCount:      4,
		FalseEscalationRate:  0.25, // 1 false out of 4
	}
	if err := store.SaveMetrics(ctx, "st-metrics-1", initialMetrics); err != nil {
		t.Fatalf("SaveMetrics: %v", err)
	}

	// Record a false escalation: count rises to 2, new rate = 2/4 = 0.5
	if err := store.UpdateFalseEscalationRate(ctx, "st-metrics-1", true); err != nil {
		t.Fatalf("UpdateFalseEscalationRate true: %v", err)
	}
	m1, err := store.GetMetrics(ctx, "st-metrics-1")
	if err != nil {
		t.Fatalf("GetMetrics: %v", err)
	}
	if m1.FalseEscalationRate != 0.5 {
		t.Fatalf("expected rate 0.5 after true, got %f", m1.FalseEscalationRate)
	}

	// Record a true (not false) escalation: false count stays 2, rate = 2/4 = 0.5
	if err := store.UpdateFalseEscalationRate(ctx, "st-metrics-1", false); err != nil {
		t.Fatalf("UpdateFalseEscalationRate false: %v", err)
	}
	m2, err := store.GetMetrics(ctx, "st-metrics-1")
	if err != nil {
		t.Fatalf("GetMetrics: %v", err)
	}
	if m2.FalseEscalationRate != 0.5 {
		t.Fatalf("expected rate 0.5 after false, got %f", m2.FalseEscalationRate)
	}

	// Zero escalation count branch
	stZero := SupervisorTaskRow{
		ID:           "st-metrics-zero",
		State:        "RUNNING",
		EnvelopeJSON: `{}`,
	}
	if err := store.CreateSupervisorTask(ctx, stZero); err != nil {
		t.Fatalf("CreateSupervisorTask zero: %v", err)
	}
	if err := store.SaveMetrics(ctx, "st-metrics-zero", MetricsRow{
		SupervisorTaskID:    "st-metrics-zero",
		EscalationCount:     0,
		FalseEscalationRate: 0.0,
	}); err != nil {
		t.Fatalf("SaveMetrics zero: %v", err)
	}
	if err := store.UpdateFalseEscalationRate(ctx, "st-metrics-zero", true); err != nil {
		t.Fatalf("UpdateFalseEscalationRate on zero: %v", err)
	}

	// UpdateSupervisorTask
	if err := store.UpdateSupervisorTask(ctx, SupervisorTaskRow{}); err == nil {
		t.Fatal("empty id must fail")
	}
	if err := store.UpdateSupervisorTask(ctx, SupervisorTaskRow{ID: "nonexistent", State: "COMPLETED"}); !errors.Is(err, ErrUnknownSupervisorTask) {
		t.Fatalf("expected ErrUnknownSupervisorTask, got %v", err)
	}

	st.State = "COMPLETED"
	st.ApproachIdx = 2
	st.AttemptIdx = 3
	st.EnvelopeJSON = `{"run":1,"finished":true}`
	if err := store.UpdateSupervisorTask(ctx, st); err != nil {
		t.Fatalf("UpdateSupervisorTask: %v", err)
	}
	stGot, err := store.GetSupervisorTask(ctx, st.ID)
	if err != nil {
		t.Fatalf("GetSupervisorTask: %v", err)
	}
	if stGot.State != "COMPLETED" || stGot.ApproachIdx != 2 || stGot.AttemptIdx != 3 {
		t.Fatalf("updated task mismatch: %+v", stGot)
	}
}

// #481: TestMaintenance_Comprehensive exercises lock acquisition, collision, and release.
func TestMaintenance_Comprehensive(t *testing.T) {
	store, _ := newTestStore(t)

	// Validation
	if _, err := store.BeginMaintenance("", 60); err == nil {
		t.Fatal("empty owner must fail")
	}
	if _, err := store.BeginMaintenance("admin", 0); err == nil {
		t.Fatal("non-positive ttl must fail")
	}

	// Acquisition with zero active tasks
	active, err := store.BeginMaintenance("admin-1", 120)
	if err != nil {
		t.Fatalf("BeginMaintenance: %v", err)
	}
	if active != 0 {
		t.Fatalf("expected 0 active tasks, got %d", active)
	}

	// Second acquisition while held must fail with collision error
	if _, err := store.BeginMaintenance("admin-2", 120); err == nil {
		t.Fatal("concurrent BeginMaintenance must fail")
	}

	// Release by wrong owner returns false
	released, err := store.EndMaintenance("wrong-admin")
	if err != nil || released {
		t.Fatalf("expected (false, nil) for wrong owner, got %v, %v", released, err)
	}

	// Release by valid owner returns true
	released, err = store.EndMaintenance("admin-1")
	if err != nil || !released {
		t.Fatalf("expected (true, nil) for valid owner, got %v, %v", released, err)
	}

	// Releasing again returns false
	released, err = store.EndMaintenance("admin-1")
	if err != nil || released {
		t.Fatalf("expected (false, nil) after already released, got %v, %v", released, err)
	}

	// Acquisition with active tasks > 0
	ctx := context.Background()
	task := submitGapTask(t, store, "gap-maint-active-1")
	if _, err := store.ClaimTask(ctx, "worker-maint", 300); err != nil {
		t.Fatalf("claim: %v", err)
	}
	_ = task
	activeCount, err := store.BeginMaintenance("admin-active", 60)
	if err != nil || activeCount != 1 {
		t.Fatalf("expected activeCount == 1, got %d (err %v)", activeCount, err)
	}
	if ok, err := store.EndMaintenance("admin-active"); err != nil || !ok {
		t.Fatalf("EndMaintenance admin-active failed: %v, %v", ok, err)
	}
}

// #481: TestCompleteAndFailTask covers CompleteTask and FailTask convenience wrappers.
func TestCompleteAndFailTask(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	// Error path: unknown task
	if err := store.CompleteTask(ctx, "nonexistent", TaskResult{Result: json.RawMessage(`{}`)}); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("expected ErrUnknownTask, got %v", err)
	}
	if err := store.FailTask(ctx, "nonexistent", "reason", 1); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("expected ErrUnknownTask, got %v", err)
	}

	// CompleteTask happy path
	task1 := submitGapTask(t, store, "gap-complete-1")
	claimed1, err := store.ClaimTask(ctx, "worker-1", 600)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !store.StartTask(task1.TaskID, "worker-1", *claimed1.LeaseToken) {
		t.Fatal("StartTask failed")
	}

	if err := store.CompleteTask(ctx, task1.TaskID, TaskResult{Result: json.RawMessage(`{"status":"success"}`)}); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	got1, err := store.GetTask(ctx, task1.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got1.State != StateWorkerCompleted {
		t.Fatalf("expected WORKER_COMPLETED, got %s", got1.State)
	}

	// FailTask happy path
	task2 := submitGapTask(t, store, "gap-fail-1")
	claimed2, err := store.ClaimTask(ctx, "worker-2", 600)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !store.StartTask(task2.TaskID, "worker-2", *claimed2.LeaseToken) {
		t.Fatal("StartTask failed")
	}

	if err := store.FailTask(ctx, task2.TaskID, "command crashed", 137); err != nil {
		t.Fatalf("FailTask: %v", err)
	}
	got2, err := store.GetTask(ctx, task2.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got2.State != StateWorkerCompleted {
		t.Fatalf("expected WORKER_COMPLETED, got %s", got2.State)
	}
	if got2.LastError == nil || *got2.LastError != "command crashed (exit code 137)" {
		t.Fatalf("unexpected last error: %v", got2.LastError)
	}
}

// #481: TestPauseAndResumeTask exercises NEEDS_INFO and BLOCKED state pause and resumption.
func TestPauseAndResumeTask(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	// PauseTask invalid pause state
	if _, err := store.PauseTask("any", "w1", "tok", StateRunning, nil, "reason"); err == nil {
		t.Fatal("invalid pauseState must fail")
	}

	// 1. Pause to NEEDS_INFO and Resume
	task := submitGapTask(t, store, "gap-pause-resume-1")
	claimed, err := store.ClaimTask(ctx, "worker-1", 600)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	token := *claimed.LeaseToken
	if !store.StartTask(task.TaskID, "worker-1", token) {
		t.Fatal("StartTask failed")
	}

	pausedInfo, err := store.PauseTask(task.TaskID, "worker-1", token, StateNeedsInfo, json.RawMessage(`{"query":"clarification"}`), "needs user response")
	if err != nil {
		t.Fatalf("PauseTask NEEDS_INFO: %v", err)
	}
	if pausedInfo.State != StateNeedsInfo {
		t.Fatalf("expected NEEDS_INFO, got %s", pausedInfo.State)
	}

	// Resuming with new payload
	resumedTask, err := store.ResumeTask(ctx, task.TaskID, json.RawMessage(`{"prompt":"clarified instructions"}`), "user replied")
	if err != nil {
		t.Fatalf("ResumeTask: %v", err)
	}
	if resumedTask.State != StateQueued || resumedTask.Attempts != 0 {
		t.Fatalf("expected QUEUED with attempts=0, got state=%s attempts=%d", resumedTask.State, resumedTask.Attempts)
	}

	// 2. Pause to BLOCKED and Resume with nil payload (retains existing)
	claimed2, err := store.ClaimTask(ctx, "worker-2", 600)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	token2 := *claimed2.LeaseToken
	if !store.StartTask(task.TaskID, "worker-2", token2) {
		t.Fatal("StartTask failed")
	}

	pausedBlocked, err := store.PauseTask(task.TaskID, "worker-2", token2, StateBlocked, json.RawMessage(`{"blocked":true}`), "waiting for dependency")
	if err != nil {
		t.Fatalf("PauseTask BLOCKED: %v", err)
	}
	if pausedBlocked.State != StateBlocked {
		t.Fatalf("expected BLOCKED, got %s", pausedBlocked.State)
	}

	resumedBlocked, err := store.ResumeTask(ctx, task.TaskID, nil, "dependency unblocked")
	if err != nil {
		t.Fatalf("ResumeTask from BLOCKED: %v", err)
	}
	if resumedBlocked.State != StateQueued {
		t.Fatalf("expected QUEUED, got %s", resumedBlocked.State)
	}

	// Resume error paths
	if _, err := store.ResumeTask(ctx, "nonexistent", nil, "reason"); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("expected ErrUnknownTask, got %v", err)
	}
	if _, err := store.ResumeTask(ctx, task.TaskID, nil, "reason"); err == nil {
		t.Fatal("resuming a QUEUED task must fail")
	}
}

// #481: TestTerminalSignalState ensures signal file classifies all five terminal states.
func TestTerminalSignalState(t *testing.T) {
	tests := []struct {
		state string
		want  bool
	}{
		{state: StateFailed, want: true},
		{state: StateWorkerCompleted, want: true},
		{state: StateSucceeded, want: true},
		{state: StateCancelled, want: true},
		{state: StateNeedsInfo, want: true},
		{state: StateRunning, want: false},
		{state: StateQueued, want: false},
		{state: StateLeased, want: false},
		{state: StateBlocked, want: false},
		{state: StateCheckpointed, want: false},
		{state: "UNKNOWN_STATE", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			got := isTerminalSignalState(tt.state)
			if got != tt.want {
				t.Fatalf("isTerminalSignalState(%s) = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}

// #481: TestRetryBackoff_AllCases exercises all backoff tiers.
func TestRetryBackoff_AllCases(t *testing.T) {
	if got := RetryBackoff(1); got != 5*time.Minute {
		t.Fatalf("RetryBackoff(1) = %v, want 5m", got)
	}
	if got := RetryBackoff(2); got != 20*time.Minute {
		t.Fatalf("RetryBackoff(2) = %v, want 20m", got)
	}
	if got := RetryBackoff(3); got != 60*time.Minute {
		t.Fatalf("RetryBackoff(3) = %v, want 60m", got)
	}
	if got := RetryBackoff(0); got != 60*time.Minute {
		t.Fatalf("RetryBackoff(0) = %v, want 60m", got)
	}
}

// #481: TestSessionQuota_ExceededAndEdgeCases covers concurrent and hourly limits.
func TestSessionQuota_ExceededAndEdgeCases(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	// Empty session ID on SetSessionQuota
	if err := store.SetSessionQuota(ctx, SessionQuota{SessionID: ""}); err == nil {
		t.Fatal("empty session_id must fail")
	}

	// Non-positive limits fallback to defaults (10 and 100)
	if err := store.SetSessionQuota(ctx, SessionQuota{
		SessionID:          "sess-q-defaults",
		MaxConcurrentTasks: 0,
		MaxTasksPerHour:    -5,
	}); err != nil {
		t.Fatalf("SetSessionQuota with defaults: %v", err)
	}
	qDef, err := store.GetSessionQuota(ctx, "sess-q-defaults")
	if err != nil || qDef.MaxConcurrentTasks != 10 || qDef.MaxTasksPerHour != 100 {
		t.Fatalf("unexpected defaults: %+v, err %v", qDef, err)
	}

	// Empty session on CheckSessionQuota
	ok, err := store.CheckSessionQuota(ctx, "")
	if err != nil || !ok {
		t.Fatalf("CheckSessionQuota empty: ok=%v, err=%v", ok, err)
	}

	// Concurrent limit hit
	sessConcurrent := "sess-q-conc"
	if err := store.SetSessionQuota(ctx, SessionQuota{
		SessionID:          sessConcurrent,
		MaxConcurrentTasks: 1,
		MaxTasksPerHour:    100,
	}); err != nil {
		t.Fatalf("SetSessionQuota: %v", err)
	}
	taskC := submitSessionTask(t, store, "sess-conc-task", sessConcurrent, nil)
	if _, err := store.ClaimTaskInSession(ctx, "worker-1", sessConcurrent, 300); err != nil {
		t.Fatalf("claim: %v", err)
	}
	_ = taskC
	ok, err = store.CheckSessionQuota(ctx, sessConcurrent)
	if err != nil || ok {
		t.Fatalf("expected CheckSessionQuota false for active count >= max concurrent, got ok=%v, err=%v", ok, err)
	}

	// Hourly limit hit
	sessHourly := "sess-q-hour"
	if err := store.SetSessionQuota(ctx, SessionQuota{
		SessionID:          sessHourly,
		MaxConcurrentTasks: 10,
		MaxTasksPerHour:    1,
	}); err != nil {
		t.Fatalf("SetSessionQuota: %v", err)
	}
	_ = submitSessionTask(t, store, "sess-hour-task-1", sessHourly, nil)
	ok, err = store.CheckSessionQuota(ctx, sessHourly)
	if err != nil || ok {
		t.Fatalf("expected CheckSessionQuota false for hourly count >= max hourly, got ok=%v, err=%v", ok, err)
	}
}

// #481: TestSessionMethods_Errors covers error branches of session management.
func TestSessionMethods_Errors(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if err := store.HeartbeatSession(ctx, "nonexistent-sess"); !errors.Is(err, ErrSessionNotActive) {
		t.Fatalf("expected ErrSessionNotActive, got %v", err)
	}
	if err := store.MarkSessionDead(ctx, "nonexistent-sess"); !errors.Is(err, ErrSessionNotActive) {
		t.Fatalf("expected ErrSessionNotActive, got %v", err)
	}
	if err := store.AttachWriteReceiptSession(ctx, "", ""); err == nil {
		t.Fatal("empty session_id on AttachWriteReceiptSession must fail")
	}
	if err := store.AttachWriteReceiptSession(ctx, "receipt-nonexistent", "sess-1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows for nonexistent receipt, got %v", err)
	}
}

// #481: TestCancelTask_LifecycleVariations verifies cancellation on running and terminal tasks.
func TestCancelTask_LifecycleVariations(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	// Unknown task returns ErrUnknownTask
	if err := store.CancelTask(ctx, "nonexistent", "reason"); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("expected ErrUnknownTask, got %v", err)
	}

	// Cancel a RUNNING task keeps it running with cancel_requested = 1
	task := submitGapTask(t, store, "gap-cancel-run-1")
	claimed, err := store.ClaimTask(ctx, "worker-1", 600)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !store.StartTask(task.TaskID, "worker-1", *claimed.LeaseToken) {
		t.Fatal("StartTask failed")
	}
	if err := store.CancelTask(ctx, task.TaskID, "user aborted"); err != nil {
		t.Fatalf("CancelTask on RUNNING: %v", err)
	}
	runningGot, err := store.GetTask(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if runningGot.State != StateRunning || !runningGot.CancelRequested {
		t.Fatalf("expected RUNNING with cancel_requested=true, got state=%s cancel=%v", runningGot.State, runningGot.CancelRequested)
	}

	// Cancel a QUEUED task -> transitions to CANCELLED
	queuedTask := submitGapTask(t, store, "gap-cancel-queued-1")
	if err := store.CancelTask(ctx, queuedTask.TaskID, "cancelled queued"); err != nil {
		t.Fatalf("CancelTask on QUEUED: %v", err)
	}
	cancelledGot, err := store.GetTask(ctx, queuedTask.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if cancelledGot.State != StateCancelled {
		t.Fatalf("expected CANCELLED, got %s", cancelledGot.State)
	}

	// Cancel on already CANCELLED task is an idempotent no-op
	if err := store.CancelTask(ctx, queuedTask.TaskID, "repeat cancel"); err != nil {
		t.Fatalf("repeated CancelTask failed: %v", err)
	}
}

// #481: TestConsumeWriteReceipt_LegacyFallback tests fallback receipt resolution.
func TestConsumeWriteReceipt_LegacyFallback(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	// Unknown receipt
	if err := store.ConsumeWriteReceipt(ctx, "nonexistent-receipt", "task-1"); !errors.Is(err, ErrReceiptUnknownOrExpired) {
		t.Fatalf("expected ErrReceiptUnknownOrExpired, got %v", err)
	}

	// Ensure legacy table and insert consumed row
	now := float64(store.Clock().UnixNano()) / 1e9
	if _, err := store.db.Exec(writeReceiptsEnsureDDL); err != nil {
		t.Fatalf("ensure write_receipts: %v", err)
	}
	if _, err := store.db.Exec(
		`INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, created_at, consumed, consumer_task_id)
		 VALUES (?, 'issuer-1', '[]', ?, ?, 1, ?)`, "rcpt-legacy-consumed", now+1000, now, "task-other",
	); err != nil {
		t.Fatalf("insert legacy consumed: %v", err)
	}

	// Consumed by different task -> ErrReceiptAlreadyConsumed
	if err := store.ConsumeWriteReceipt(ctx, "rcpt-legacy-consumed", "task-me"); !errors.Is(err, ErrReceiptAlreadyConsumed) {
		t.Fatalf("expected ErrReceiptAlreadyConsumed, got %v", err)
	}

	// Consumed by same task -> idempotent nil
	if err := store.ConsumeWriteReceipt(ctx, "rcpt-legacy-consumed", "task-other"); err != nil {
		t.Fatalf("expected nil for idempotent consume, got %v", err)
	}

	// Expired receipt
	if _, err := store.db.Exec(
		`INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, created_at, consumed)
		 VALUES (?, 'issuer-1', '[]', ?, ?, 0)`, "rcpt-legacy-expired", now-1000, now,
	); err != nil {
		t.Fatalf("insert legacy expired: %v", err)
	}
	if err := store.ConsumeWriteReceipt(ctx, "rcpt-legacy-expired", "task-me"); !errors.Is(err, ErrReceiptUnknownOrExpired) {
		t.Fatalf("expected ErrReceiptUnknownOrExpired for expired receipt, got %v", err)
	}
}

// #420: TestSupervisorStore_EdgeCases covers validation, auto-ids, and default columns.
func TestSupervisorStore_EdgeCases(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	// CreateSupervisorTask validation
	if err := store.CreateSupervisorTask(ctx, SupervisorTaskRow{State: "RUNNING"}); err == nil {
		t.Fatal("empty id must fail")
	}
	if err := store.CreateSupervisorTask(ctx, SupervisorTaskRow{ID: "st-no-state"}); err == nil {
		t.Fatal("empty state must fail")
	}

	// Create with parent_task_id and zero times (auto-default to clock)
	parentID := "st-parent-1"
	sessID := "st-sess-1"
	st := SupervisorTaskRow{
		ID:           "st-with-parent",
		State:        "RUNNING",
		EnvelopeJSON: `{"parent":true}`,
		ParentTaskID: &parentID,
		SessionID:    &sessID,
	}
	if err := store.CreateSupervisorTask(ctx, st); err != nil {
		t.Fatalf("CreateSupervisorTask: %v", err)
	}

	// GetSupervisorTask validation & lookup
	if _, err := store.GetSupervisorTask(ctx, ""); err == nil {
		t.Fatal("empty id must fail")
	}
	got, err := store.GetSupervisorTask(ctx, "st-with-parent")
	if err != nil {
		t.Fatalf("GetSupervisorTask: %v", err)
	}
	if got.ParentTaskID == nil || *got.ParentTaskID != parentID {
		t.Fatalf("expected ParentTaskID %s, got %v", parentID, got.ParentTaskID)
	}

	// ListSupervisorTasks
	allTasks, err := store.ListSupervisorTasks(ctx)
	if err != nil || len(allTasks) == 0 {
		t.Fatalf("ListSupervisorTasks: %d rows (err %v)", len(allTasks), err)
	}

	// AppendDecision validation & auto-ID / zero-timestamp
	if err := store.AppendDecision(ctx, SupervisorDecisionRow{Kind: "approve"}); err == nil {
		t.Fatal("empty task_id must fail")
	}
	if err := store.AppendDecision(ctx, SupervisorDecisionRow{TaskID: "st-with-parent"}); err == nil {
		t.Fatal("empty kind must fail")
	}
	// Append with empty ID and zero CreatedAt (triggers auto-generated ID and clock default)
	if err := store.AppendDecision(ctx, SupervisorDecisionRow{
		TaskID:      "st-with-parent",
		Kind:        "action_test",
		PayloadJSON: `{}`,
	}); err != nil {
		t.Fatalf("AppendDecision with auto ID: %v", err)
	}

	// SaveMetrics & GetMetrics validation
	if err := store.SaveMetrics(ctx, "", MetricsRow{}); err == nil {
		t.Fatal("empty supervisorTaskID must fail")
	}
	if _, err := store.GetMetrics(ctx, ""); err == nil {
		t.Fatal("empty supervisorTaskID on GetMetrics must fail")
	}
	if _, err := store.GetMetrics(ctx, "nonexistent-metrics-task"); !errors.Is(err, ErrUnknownSupervisorTask) {
		t.Fatalf("expected ErrUnknownSupervisorTask, got %v", err)
	}
}

// #481: TestRedactPayload_AllCases covers nil, empty, invalid, and prompt-containing payloads.
func TestRedactPayload_AllCases(t *testing.T) {
	if err := redactPayload(nil); err != nil {
		t.Fatalf("redactPayload(nil): %v", err)
	}
	empty := ""
	if err := redactPayload(&empty); err != nil {
		t.Fatalf("redactPayload(''): %v", err)
	}
	invalid := "{not-json"
	if err := redactPayload(&invalid); err == nil {
		t.Fatal("expected error on invalid JSON")
	}
	noPrompt := `{"action":"status"}`
	if err := redactPayload(&noPrompt); err != nil {
		t.Fatalf("redactPayload(no prompt): %v", err)
	}
	withPrompt := `{"prompt":"secret information","other":123}`
	if err := redactPayload(&withPrompt); err != nil {
		t.Fatalf("redactPayload(with prompt): %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(withPrompt), &parsed); err != nil {
		t.Fatalf("unmarshal redacted: %v", err)
	}
	if _, exists := parsed["prompt"]; exists {
		t.Fatal("prompt should be redacted")
	}
	if parsed["prompt_redacted"] != true {
		t.Fatal("prompt_redacted should be true")
	}
}

// #481: TestDetermineNextState_AllCases verifies transition outcomes.
func TestDetermineNextState_AllCases(t *testing.T) {
	// Cancel requested overrides all
	cancelTask := &Task{CancelRequested: true}
	if got := determineNextState(cancelTask, FinishAttemptParams{Success: true}); got != StateCancelled {
		t.Fatalf("got %s, want CANCELLED", got)
	}

	// Success
	normalTask := &Task{Attempts: 1, MaxAttempts: 3}
	if got := determineNextState(normalTask, FinishAttemptParams{Success: true}); got != StateSucceeded {
		t.Fatalf("got %s, want SUCCEEDED", got)
	}

	// Retryable with attempts < max_attempts
	if got := determineNextState(normalTask, FinishAttemptParams{Success: false, Retryable: true}); got != StateQueued {
		t.Fatalf("got %s, want QUEUED", got)
	}

	// Retryable with budget exhausted
	exhaustedTask := &Task{Attempts: 3, MaxAttempts: 3}
	if got := determineNextState(exhaustedTask, FinishAttemptParams{Success: false, Retryable: true}); got != StateFailed {
		t.Fatalf("got %s, want FAILED", got)
	}

	// Non-retryable failure
	if got := determineNextState(normalTask, FinishAttemptParams{Success: false, Retryable: false}); got != StateFailed {
		t.Fatalf("got %s, want FAILED", got)
	}
}

// #481: TestErrSubmitRateLimited_IsCases tests pointer, value, and unrelated error matching.
func TestErrSubmitRateLimited_IsCases(t *testing.T) {
	err := ErrSubmitRateLimited{Actor: "test", Limit: 10}
	if !errors.Is(err, &ErrSubmitRateLimited{}) {
		t.Fatal("should match pointer target")
	}
	if !errors.Is(err, ErrSubmitRateLimited{}) {
		t.Fatal("should match value target")
	}
	if errors.Is(err, errors.New("unrelated error")) {
		t.Fatal("should not match unrelated error")
	}
}

// #481: TestExtendedControlPlaneOperations covers PauseTask errors, Heartbeat variations,
// ClaimTaskProvider, and session listing.
func TestExtendedControlPlaneOperations(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	// 1. PauseTask errors: unknown task and stale lease
	if _, err := store.PauseTask("nonexistent-pause", "w1", "tok", StateNeedsInfo, nil, "reason"); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("expected ErrUnknownTask, got %v", err)
	}

	task := submitGapTask(t, store, "gap-pause-err-1")
	claimed, err := store.ClaimTask(ctx, "worker-1", 600)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	token := *claimed.LeaseToken
	if !store.StartTask(task.TaskID, "worker-1", token) {
		t.Fatal("StartTask failed")
	}

	// Stale lease (wrong worker)
	if _, err := store.PauseTask(task.TaskID, "wrong-worker", token, StateNeedsInfo, nil, "reason"); err == nil {
		t.Fatal("expected error on PauseTask with wrong worker")
	}

	// 2. RenewHeartbeat and Heartbeat error cases
	if err := store.RenewHeartbeat(ctx, task.TaskID, "worker-1", 300); err != nil {
		t.Fatalf("RenewHeartbeat: %v", err)
	}
	if err := store.RenewHeartbeat(ctx, task.TaskID, "wrong-worker", 300); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expected ErrLeaseLost on RenewHeartbeat with wrong worker, got %v", err)
	}
	if err := store.Heartbeat(ctx, task.TaskID, "worker-1", "", 300); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expected ErrLeaseLost on Heartbeat with empty token, got %v", err)
	}

	// 3. ClaimTaskProvider
	// Task with provider specified in payload
	_, err = store.SubmitTask(ctx, SubmitTaskRequest{
		IdempotencyKey: "gap-provider-task-1",
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{testScopeDir},
		Payload:        json.RawMessage(`{"prompt":"provider task","provider":"custom-provider"}`),
	})
	if err != nil {
		t.Fatalf("SubmitTask with provider: %v", err)
	}

	// Claim with matching provider
	claimedProv, err := store.ClaimTaskProvider(ctx, "worker-provider", 300, "custom-provider")
	if err != nil || claimedProv == nil {
		t.Fatalf("ClaimTaskProvider matching: %+v, err %v", claimedProv, err)
	}

	// ClaimTaskProvider with empty provider delegates to regular ClaimTask
	tReg := submitGapTask(t, store, "gap-prov-reg-1")
	claimedEmpty, err := store.ClaimTaskProvider(ctx, "worker-reg", 300, "")
	if err != nil || claimedEmpty == nil || claimedEmpty.TaskID != tReg.TaskID {
		t.Fatalf("ClaimTaskProvider empty provider: %+v, err %v", claimedEmpty, err)
	}

	// 4. Session registry: ListActiveSessions and GetSession
	now := time.Now().UTC()
	if err := store.RegisterSession(ctx, Session{
		ID:     "sess-invalid",
		Status: SessionStatusActive,
		Mode:   "invalid_mode",
	}); err == nil {
		t.Fatal("expected error on invalid session mode")
	}

	sess1 := Session{
		ID:           "sess-active-1",
		Status:       SessionStatusActive,
		Mode:         SessionModeInPlace,
		WorktreePath: "/tmp/wt-sess-1",
		StartedAt:    now,
	}
	sessDead := Session{
		ID:           "sess-dead-1",
		Status:       SessionStatusDead,
		Mode:         SessionModeWorktree,
		WorktreePath: "/tmp/wt-sess-dead",
		StartedAt:    now.Add(-10 * time.Minute),
	}
	if err := store.RegisterSession(ctx, sess1); err != nil {
		t.Fatalf("RegisterSession sess1: %v", err)
	}
	if err := store.RegisterSession(ctx, sessDead); err != nil {
		t.Fatalf("RegisterSession sessDead: %v", err)
	}
	if err := store.MarkSessionDead(ctx, sessDead.ID); err != nil {
		t.Fatalf("MarkSessionDead: %v", err)
	}

	activeList, err := store.ListActiveSessions(ctx)
	if err != nil || len(activeList) != 1 || activeList[0].ID != "sess-active-1" {
		t.Fatalf("ListActiveSessions: %+v, err %v", activeList, err)
	}

	gotSess, err := store.GetSession(ctx, "sess-active-1")
	if err != nil || gotSess.ID != "sess-active-1" {
		t.Fatalf("GetSession: %+v, err %v", gotSess, err)
	}
	if _, err := store.GetSession(ctx, "nonexistent-session"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows on unknown session, got %v", err)
	}
}

// #481: TestControlPlaneRemainingGaps closes small uncovered branches in filters, limits, and classification.
func TestControlPlaneRemainingGaps(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	// 1. ClassifyRetryable branches
	if got := ClassifyRetryable(`{"status":"succeeded"}`, ""); got != RetryClassNotRetryable {
		t.Fatalf("expected NotRetryable for succeeded status, got %v", got)
	}
	if got := ClassifyRetryable(`{"result":{"status":"SUCCESS"}}`, ""); got != RetryClassNotRetryable {
		t.Fatalf("expected NotRetryable for SUCCESS result, got %v", got)
	}
	if got := ClassifyRetryable(`{"error":{"code":"E_USAGE"}}`, ""); got != RetryClassNotRetryable {
		t.Fatalf("expected NotRetryable for E_USAGE, got %v", got)
	}
	if got := ClassifyRetryable(`{"error":{"code":"E_INVALID"}}`, ""); got != RetryClassNotRetryable {
		t.Fatalf("expected NotRetryable for E_INVALID, got %v", got)
	}
	if got := ClassifyRetryable(`{"error":{"code":"E_DENIED"}}`, ""); got != RetryClassNotRetryable {
		t.Fatalf("expected NotRetryable for E_DENIED, got %v", got)
	}
	if got := ClassifyRetryable(`{"error":{"code":"E_TIMEOUT"}}`, ""); got != RetryClassRetryable {
		t.Fatalf("expected Retryable for E_TIMEOUT, got %v", got)
	}

	// 2. ActiveTaskCountInSession validation
	if _, err := store.ActiveTaskCountInSession(ctx, ""); err == nil {
		t.Fatal("empty sessionID on ActiveTaskCountInSession must fail")
	}

	// 3. checkExistingTask: duplicate idempotency key with different payload
	_ = submitGapTask(t, store, "gap-idem-dup-1")
	_, err := store.SubmitTask(ctx, SubmitTaskRequest{
		IdempotencyKey: "gap-idem-dup-1",
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{testScopeDir},
		Payload:        json.RawMessage(`{"prompt":"different payload"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "different request") {
		t.Fatalf("expected different request error, got %v", err)
	}

	// 4. UpdateBriefStatus validation & not found
	if err := store.UpdateBriefStatus(ctx, "", "active"); err == nil {
		t.Fatal("empty id must fail")
	}
	if err := store.UpdateBriefStatus(ctx, "brief-1", ""); err == nil {
		t.Fatal("empty status must fail")
	}
	if err := store.UpdateBriefStatus(ctx, "nonexistent-brief", "active"); !errors.Is(err, ErrUnknownBrief) {
		t.Fatalf("expected ErrUnknownBrief, got %v", err)
	}

	// 5. ListTasks error & composite filters
	if _, err := store.ListTasks(ctx, TaskFilter{Limit: -1}); !errors.Is(err, ErrLimitBounds) {
		t.Fatalf("expected ErrLimitBounds for limit < 1, got %v", err)
	}
	if _, err := store.ListTasks(ctx, TaskFilter{Limit: 201}); !errors.Is(err, ErrLimitBounds) {
		t.Fatalf("expected ErrLimitBounds for limit > 200, got %v", err)
	}
	invalidState := "NONEXISTENT_STATE"
	if _, err := store.ListTasks(ctx, TaskFilter{State: &invalidState}); !errors.Is(err, ErrUnknownState) {
		t.Fatalf("expected ErrUnknownState for invalid state, got %v", err)
	}
	validState := StateQueued
	filterSess := "sess-filter-test"
	if _, err := store.ListTasks(ctx, TaskFilter{State: &validState, SessionID: &filterSess}); err != nil {
		t.Fatalf("ListTasks with state and session: %v", err)
	}

	// 6. ListChildTasks validation
	if _, err := store.ListChildTasks(ctx, ""); err == nil {
		t.Fatal("empty parentTaskID must fail")
	}
}

// #465: TestBusyRetryAndErrorClassification tests SQLITE_BUSY recognition and retry loop.
func TestBusyRetryAndErrorClassification(t *testing.T) {
	if isBusyErr(nil) {
		t.Fatal("nil should not be busy err")
	}
	if !isBusyErr(ErrBusy) {
		t.Fatal("ErrBusy should be busy err")
	}
	if !isBusyErr(errors.New("database is locked")) {
		t.Fatal("'database is locked' should be busy err")
	}
	if !isBusyErr(errors.New("database table is locked")) {
		t.Fatal("'database table is locked' should be busy err")
	}
	if !isBusyErr(errors.New("some sqlite_busy error")) {
		t.Fatal("'sqlite_busy' should be busy err")
	}
	if isBusyErr(errors.New("unrelated syntax error")) {
		t.Fatal("syntax error should not be busy err")
	}

	// withBusyRetry success on first try
	if err := withBusyRetry(func() error { return nil }); err != nil {
		t.Fatalf("withBusyRetry: %v", err)
	}

	// withBusyRetry succeeds after retry
	tries := 0
	err := withBusyRetry(func() error {
		tries++
		if tries < 2 {
			return ErrBusy
		}
		return nil
	})
	if err != nil || tries != 2 {
		t.Fatalf("withBusyRetry retry: tries=%d, err=%v", tries, err)
	}
}

// #481: TestSqlitePathEscape_Runtime exercises sqlitePathEscape using the runtime GOOS.
func TestSqlitePathEscape_Runtime(t *testing.T) {
	cur := sqlitePathEscape("/tmp/local.sqlite")
	if cur == "" {
		t.Fatal("sqlitePathEscape returned empty string")
	}
}

// #481: TestLineageAndParentCheck covers valid parent check, child listing, and lineage traversal.
func TestLineageAndParentCheck(t *testing.T) {
	store, dbPath := newTestStore(t)
	ctx := context.Background()

	// Empty taskID on GetTaskLineage
	if lin, err := store.GetTaskLineage(ctx, ""); err != nil || lin != nil {
		t.Fatalf("expected nil for empty taskID, got %v, %v", lin, err)
	}

	// Submit parent
	parent := submitGapTask(t, store, "gap-parent-check-1")

	// Submit child with valid parent
	child, err := store.SubmitTask(ctx, SubmitTaskRequest{
		IdempotencyKey: "gap-child-check-1",
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{testScopeDir},
		Payload:        json.RawMessage(`{"prompt":"child task"}`),
		ParentTaskID:   &parent.TaskID,
	})
	if err != nil {
		t.Fatalf("SubmitTask with valid parent: %v", err)
	}

	// ListChildTasks
	children, err := store.ListChildTasks(ctx, parent.TaskID)
	if err != nil || len(children) != 1 || children[0].TaskID != child.TaskID {
		t.Fatalf("ListChildTasks: %+v, err %v", children, err)
	}

	// GetTaskLineage
	lineage, err := store.GetTaskLineage(ctx, child.TaskID)
	if err != nil || len(lineage) != 2 || lineage[0].TaskID != parent.TaskID || lineage[1].TaskID != child.TaskID {
		t.Fatalf("GetTaskLineage: %+v, err %v", lineage, err)
	}

	// StartTask with cancel_requested = 1 on a LEASED task returns false
	taskToCancel := submitGapTask(t, store, "gap-cancel-start-1")
	claimed, err := store.ClaimTask(ctx, "worker-cancel", 600)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	rawDB := openRawDB(t, dbPath)
	if _, err := rawDB.Exec("UPDATE tasks SET cancel_requested = 1 WHERE task_id = ?", taskToCancel.TaskID); err != nil {
		t.Fatalf("set cancel_requested: %v", err)
	}
	if ok := store.StartTask(taskToCancel.TaskID, "worker-cancel", *claimed.LeaseToken); ok {
		t.Fatal("StartTask should return false when cancel_requested = 1")
	}
}

// #481: TestResubmitTaskAndBuildReceiptEdgeCases covers ResubmitTask error conditions and BuildReceipt inspection.
func TestResubmitTaskAndBuildReceiptEdgeCases(t *testing.T) {
	store, dbPath := newTestStore(t)
	ctx := context.Background()

	// BuildReceipt on nonexistent task
	if _, err := store.BuildReceipt(ctx, "nonexistent"); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("expected ErrUnknownTask on BuildReceipt, got %v", err)
	}

	// BuildReceipt on real task
	task := submitGapTask(t, store, "gap-build-rcpt-1")
	rcpt, err := store.BuildReceipt(ctx, task.TaskID)
	if err != nil || rcpt == nil || rcpt["receipt_hash"] == "" {
		t.Fatalf("BuildReceipt failed: %+v, err %v", rcpt, err)
	}

	// ResubmitTask disabled
	if _, err := store.ResubmitTask(ctx, task.TaskID, ResubmitOpts{Enabled: false}); err == nil {
		t.Fatal("expected error when ResubmitOpts.Enabled is false")
	}

	// ResubmitTask on nonexistent task
	if _, err := store.ResubmitTask(ctx, "nonexistent-task", ResubmitOpts{Enabled: true}); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("expected ErrUnknownTask, got %v", err)
	}

	// ResubmitTask on non-FAILED task (task is currently QUEUED)
	if _, err := store.ResubmitTask(ctx, task.TaskID, ResubmitOpts{Enabled: true}); err == nil {
		t.Fatal("expected error on resubmitting non-FAILED task")
	}

	// ResubmitTask on non-retryable failure
	rawDB := openRawDB(t, dbPath)
	if _, err := rawDB.Exec("UPDATE tasks SET state = 'FAILED', last_error = 'receipt violation' WHERE task_id = ?", task.TaskID); err != nil {
		t.Fatalf("raw DB update: %v", err)
	}
	if _, err := store.ResubmitTask(ctx, task.TaskID, ResubmitOpts{Enabled: true}); err == nil {
		t.Fatal("expected error for non-retryable failure")
	}

	// ResubmitTask with workspace_write requiring fresh receipt
	if _, err := rawDB.Exec("UPDATE tasks SET last_error = 'timeout', request_json = '{\"permission\":\"workspace_write\"}' WHERE task_id = ?", task.TaskID); err != nil {
		t.Fatalf("raw DB update: %v", err)
	}
	if _, err := store.ResubmitTask(ctx, task.TaskID, ResubmitOpts{Enabled: true}); err == nil {
		t.Fatal("expected error when workspace_write lacks fresh receipt_id")
	}

	// ResubmitTask happy path
	if _, err := rawDB.Exec("UPDATE tasks SET last_error = 'execution timed out', request_json = '{\"prompt\":\"retry me\",\"role\":\"collector\"}' WHERE task_id = ?", task.TaskID); err != nil {
		t.Fatalf("raw DB update: %v", err)
	}
	retryID1, err := store.ResubmitTask(ctx, task.TaskID, ResubmitOpts{Enabled: true})
	if err != nil || retryID1 == "" {
		t.Fatalf("ResubmitTask happy path failed: %s, err %v", retryID1, err)
	}

	// Double-fire idempotency: returns the same retryID1 while active
	retryID2, err := store.ResubmitTask(ctx, task.TaskID, ResubmitOpts{Enabled: true})
	if err != nil || retryID2 != retryID1 {
		t.Fatalf("ResubmitTask double-fire idempotency failed: %s != %s, err %v", retryID2, retryID1, err)
	}

	// Budget exceeded check
	if _, err := rawDB.Exec("UPDATE tasks SET state = 'FAILED' WHERE task_id = ?", retryID1); err != nil {
		t.Fatalf("fail first retry child: %v", err)
	}
	if _, err := store.ResubmitTask(ctx, task.TaskID, ResubmitOpts{Enabled: true, MaxPerTask: 1}); err == nil {
		t.Fatal("expected retry budget exceeded error")
	}
}

// #481: TestSupervisorFeedbackAndHashing covers supervisor transitions, error call logging, and hash edge cases.
func TestSupervisorFeedbackAndHashing(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	// 1. canonicalJSON and contentHash error on unmarshalable types
	if _, err := canonicalJSON(make(chan int)); err == nil {
		t.Fatal("expected error on unmarshalable type in canonicalJSON")
	}
	if _, err := contentHash(make(chan int)); err == nil {
		t.Fatal("expected error on unmarshalable type in contentHash")
	}

	// 2. AddErrorCall on nonexistent and valid task
	if err := store.AddErrorCall(ctx, "nonexistent-task", ErrorCallRecord{}); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("expected ErrUnknownTask, got %v", err)
	}

	task := submitGapTask(t, store, "gap-err-call-1")
	rec1 := ErrorCallRecord{
		ErrorType: "transient",
		ErrorMsg:  "net err",
		Timestamp: 100.0,
	}
	rec2 := ErrorCallRecord{
		ErrorType: "fatal",
		ErrorMsg:  "crash",
		Timestamp: 105.0,
	}
	if err := store.AddErrorCall(ctx, task.TaskID, rec1); err != nil {
		t.Fatalf("AddErrorCall 1: %v", err)
	}
	if err := store.AddErrorCall(ctx, task.TaskID, rec2); err != nil {
		t.Fatalf("AddErrorCall 2: %v", err)
	}
	gotTask, err := store.GetTask(ctx, task.TaskID)
	if err != nil || len(gotTask.ErrorCallHistory) != 2 {
		t.Fatalf("expected 2 error call records, got %d (err %v)", len(gotTask.ErrorCallHistory), err)
	}

	// ValidateResult
	if err := store.ValidateResult(ctx, task.TaskID, ResultValidation{
		Valid:       true,
		ValidatedBy: "supervisor-1",
	}); err != nil {
		t.Fatalf("ValidateResult: %v", err)
	}

	// 3. AcceptResult, RejectResult, RequestEdit fail on non-WORKER_COMPLETED task
	if err := store.AcceptResult(ctx, task.TaskID, "sup-1"); err == nil {
		t.Fatal("expected error on AcceptResult for QUEUED task")
	}
	if err := store.RejectResult(ctx, task.TaskID, "sup-1", "bad", "ctx"); err == nil {
		t.Fatal("expected error on RejectResult for QUEUED task")
	}
	if err := store.RequestEdit(ctx, task.TaskID, "sup-1", "edit pls", "ctx"); err == nil {
		t.Fatal("expected error on RequestEdit for QUEUED task")
	}
}
