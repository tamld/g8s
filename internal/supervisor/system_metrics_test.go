package supervisor

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

func TestNewSystemMetricsCollector(t *testing.T) {
	colNil := NewSystemMetricsCollector(nil, nil)
	if colNil == nil {
		t.Fatal("expected non-nil collector")
	}
	if colNil.clock == nil {
		t.Fatal("expected default clock when nil is passed")
	}

	fixedTime := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	colCustom := NewSystemMetricsCollector(nil, func() time.Time { return fixedTime })
	if got := colCustom.clock(); !got.Equal(fixedTime) {
		t.Fatalf("expected custom clock %v, got %v", fixedTime, got)
	}
}

func TestSystemMetricsCollector_ContextCancelled(t *testing.T) {
	col := NewSystemMetricsCollector(nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := col.Collect(ctx); err == nil {
		t.Error("expected error with canceled context in Collect, got nil")
	}

	if _, err := col.CollectWithOptions(ctx, SystemMetricsOptions{}); err == nil {
		t.Error("expected error with canceled context in CollectWithOptions, got nil")
	}
}

func TestSystemMetricsCollector_EmptyStore(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "empty.db")
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	fixedTime := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	col := NewSystemMetricsCollector(store, func() time.Time { return fixedTime })

	m, err := col.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect on empty store returned error: %v", err)
	}

	if !m.CollectedAt.Equal(fixedTime) {
		t.Errorf("expected CollectedAt %v, got %v", fixedTime, m.CollectedAt)
	}
	if m.TasksCompletedTotal != 0 || m.TasksFailedTotal != 0 || m.TasksCancelledTotal != 0 {
		t.Errorf("expected 0 totals, got completed=%d, failed=%d, cancelled=%d",
			m.TasksCompletedTotal, m.TasksFailedTotal, m.TasksCancelledTotal)
	}
	if m.TaskSuccessRate != 0 || m.TaskFailureRate != 0 || m.TaskCancellationRate != 0 {
		t.Errorf("expected 0 rates, got success=%.2f, fail=%.2f, cancel=%.2f",
			m.TaskSuccessRate, m.TaskFailureRate, m.TaskCancellationRate)
	}
}

func TestSystemMetricsCollector_MultiStateLifecycle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "lifecycle.db")

	var clockMu sync.Mutex
	simTime := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return simTime
	}
	advance := func(d time.Duration) {
		clockMu.Lock()
		defer clockMu.Unlock()
		simTime = simTime.Add(d)
	}

	store, err := controlplane.NewControlPlane(dbPath, clock)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	scopeDir := t.TempDir()

	sess1 := "session-alpha"
	sess2 := "session-beta"

	submit := func(key, sess string, maxAttempts int) *controlplane.Task {
		if maxAttempts == 0 {
			maxAttempts = 1
		}
		payload, _ := json.Marshal(map[string]any{"prompt": "do task " + key})
		task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
			IdempotencyKey: key,
			Payload:        payload,
			Model:          "test-model",
			AddDirs:        []string{scopeDir},
			Role:           "collector",
			Permission:     "read_only",
			SessionID:      &sess,
			MaxAttempts:    maxAttempts,
		})
		if err != nil {
			t.Fatalf("SubmitTask %s failed: %v", key, err)
		}
		return task
	}

	// 1. Task Succeeded (session 1)
	t1 := submit("task-succeeded", sess1, 1)
	c1, err := store.ClaimTask(ctx, "worker-1", 60)
	if err != nil {
		t.Fatalf("ClaimTask t1: %v", err)
	}
	if !store.StartTask(c1.TaskID, "worker-1", *c1.LeaseToken) {
		t.Fatal("StartTask t1 failed")
	}
	advance(5 * time.Second)
	if err := store.CompleteTask(ctx, t1.TaskID, controlplane.TaskResult{Result: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	if err := store.AcceptResult(ctx, t1.TaskID, "supervisor-1"); err != nil {
		t.Fatalf("AcceptResult: %v", err)
	}

	// 2. Task Failed (session 1) via lease expiration + exhausted max attempts
	_ = submit("task-failed", sess1, 1)
	c2, err := store.ClaimTask(ctx, "worker-1", 10)
	if err != nil {
		t.Fatalf("ClaimTask t2: %v", err)
	}
	if !store.StartTask(c2.TaskID, "worker-1", *c2.LeaseToken) {
		t.Fatal("StartTask t2 failed")
	}
	advance(20 * time.Second)
	if _, err := store.ReconcileExpired(ctx); err != nil {
		t.Fatalf("ReconcileExpired: %v", err)
	}

	// 3. Task Cancelled (session 2)
	t3 := submit("task-cancelled", sess2, 1)
	advance(1 * time.Second)
	if err := store.CancelTask(ctx, t3.TaskID, "user cancelled"); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}

	// 4. Task Running / Leased (session 2)
	_ = submit("task-running", sess2, 1)
	c4, err := store.ClaimTask(ctx, "worker-2", 60)
	if err != nil {
		t.Fatalf("ClaimTask t4: %v", err)
	}
	if !store.StartTask(c4.TaskID, "worker-2", *c4.LeaseToken) {
		t.Fatal("StartTask t4 failed")
	}

	// 5. Task Queued (session 2)
	_ = submit("task-queued", sess2, 1)

	// Add supervisor task + metrics to check supervisor metrics aggregation
	supTask := controlplane.SupervisorTaskRow{
		ID:           "sup-1",
		State:        "succeeded",
		EnvelopeJSON: "{}",
		ApproachIdx:  0,
		AttemptIdx:   0,
		CreatedAt:    clock().Add(-5 * time.Minute),
		UpdatedAt:    clock(),
	}
	if err := store.CreateSupervisorTask(ctx, supTask); err != nil {
		t.Fatalf("CreateSupervisorTask: %v", err)
	}
	if err := store.SaveMetrics(ctx, supTask.ID, controlplane.MetricsRow{
		SupervisorTaskID:     supTask.ID,
		EnvelopeScore:        1.0,
		FirstAttemptSuccess:  true,
		AttemptsToSuccess:    1,
		ApproachesToSuccess:  1,
		RCAConfidenceAvg:     0.9,
		CycleDurationSeconds: 12.5,
		EscalationCount:      0,
		FalseEscalationRate:  0.0,
	}); err != nil {
		t.Fatalf("SaveMetrics: %v", err)
	}

	col := NewSystemMetricsCollector(store, clock)
	m, err := col.Collect(ctx)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if m.TasksCompletedTotal != 1 {
		t.Errorf("expected TasksCompletedTotal 1, got %d", m.TasksCompletedTotal)
	}
	if m.TasksFailedTotal != 1 {
		t.Errorf("expected TasksFailedTotal 1, got %d", m.TasksFailedTotal)
	}
	if m.TasksCancelledTotal != 1 {
		t.Errorf("expected TasksCancelledTotal 1, got %d", m.TasksCancelledTotal)
	}
	if m.TasksQueuedCurrent != 1 {
		t.Errorf("expected TasksQueuedCurrent 1, got %d", m.TasksQueuedCurrent)
	}
	if m.TasksRunningCurrent != 1 {
		t.Errorf("expected TasksRunningCurrent 1, got %d", m.TasksRunningCurrent)
	}

	expectedRate := 1.0 / 3.0
	if diff := m.TaskSuccessRate - expectedRate; diff < -0.001 || diff > 0.001 {
		t.Errorf("expected TaskSuccessRate ~%.3f, got %.3f", expectedRate, m.TaskSuccessRate)
	}
	if diff := m.TaskFailureRate - expectedRate; diff < -0.001 || diff > 0.001 {
		t.Errorf("expected TaskFailureRate ~%.3f, got %.3f", expectedRate, m.TaskFailureRate)
	}
	if diff := m.TaskCancellationRate - expectedRate; diff < -0.001 || diff > 0.001 {
		t.Errorf("expected TaskCancellationRate ~%.3f, got %.3f", expectedRate, m.TaskCancellationRate)
	}

	if m.AvgQueueLatencySeconds <= 0 {
		t.Errorf("expected AvgQueueLatencySeconds > 0, got %.4f", m.AvgQueueLatencySeconds)
	}
	if m.AvgExecutionSeconds <= 0 {
		t.Errorf("expected AvgExecutionSeconds > 0, got %.4f", m.AvgExecutionSeconds)
	}

	if m.ActiveSessions != 2 {
		t.Errorf("expected ActiveSessions 2, got %d", m.ActiveSessions)
	}
	if m.TasksPerSession != 1.5 { // 3 completed / 2 sessions = 1.5
		t.Errorf("expected TasksPerSession 1.5, got %.2f", m.TasksPerSession)
	}
	if m.ActiveWorkers != 1 { // worker-2 owns running task
		t.Errorf("expected ActiveWorkers 1, got %d", m.ActiveWorkers)
	}
	if m.SupervisorTotalRuns != 1 {
		t.Errorf("expected SupervisorTotalRuns 1, got %d", m.SupervisorTotalRuns)
	}
	if m.SupervisorFirstAttemptSuccessRate != 1.0 {
		t.Errorf("expected SupervisorFirstAttemptSuccessRate 1.0, got %.2f", m.SupervisorFirstAttemptSuccessRate)
	}

	// Test CollectWithOptions
	mOpts, err := col.CollectWithOptions(ctx, SystemMetricsOptions{
		TimeRange: time.Hour,
		SessionID: sess1,
	})
	if err != nil {
		t.Fatalf("CollectWithOptions failed: %v", err)
	}
	if mOpts.TasksCompletedTotal != 1 {
		t.Errorf("expected CollectWithOptions TasksCompletedTotal 1, got %d", mOpts.TasksCompletedTotal)
	}
}

func TestSystemMetrics_ExportPrometheus(t *testing.T) {
	m := SystemMetrics{
		TasksCompletedTotal:               10,
		TasksFailedTotal:                  2,
		TasksCancelledTotal:               1,
		TasksQueuedCurrent:                3,
		TasksRunningCurrent:               4,
		TaskSuccessRate:                   0.7692,
		TaskFailureRate:                   0.1538,
		AvgQueueLatencySeconds:            1.2345,
		AvgExecutionSeconds:               5.6789,
		ActiveWorkers:                     3,
		ActiveSessions:                    2,
		SupervisorTotalRuns:               8,
		SupervisorFirstAttemptSuccessRate: 0.8750,
		SupervisorEscalationRate:          0.1250,
		CollectedAt:                       time.Now(),
	}

	prom := m.ExportPrometheus()

	expectedSubstrings := []string{
		"# HELP g8s_tasks_completed_total Total number of tasks completed\n# TYPE g8s_tasks_completed_total counter\ng8s_tasks_completed_total 10",
		"# HELP g8s_tasks_failed_total Total number of tasks failed\n# TYPE g8s_tasks_failed_total counter\ng8s_tasks_failed_total 2",
		"# HELP g8s_tasks_cancelled_total Total number of tasks cancelled\n# TYPE g8s_tasks_cancelled_total counter\ng8s_tasks_cancelled_total 1",
		"# HELP g8s_tasks_queued_current Current number of queued tasks\n# TYPE g8s_tasks_queued_current gauge\ng8s_tasks_queued_current 3",
		"# HELP g8s_tasks_running_current Current number of running tasks\n# TYPE g8s_tasks_running_current gauge\ng8s_tasks_running_current 4",
		"# HELP g8s_task_success_rate Task success rate (0-1)\n# TYPE g8s_task_success_rate gauge\ng8s_task_success_rate 0.7692",
		"# HELP g8s_task_failure_rate Task failure rate (0-1)\n# TYPE g8s_task_failure_rate gauge\ng8s_task_failure_rate 0.1538",
		"# HELP g8s_avg_queue_latency_seconds Average queue latency in seconds\n# TYPE g8s_avg_queue_latency_seconds gauge\ng8s_avg_queue_latency_seconds 1.2345",
		"# HELP g8s_avg_execution_seconds Average execution time in seconds\n# TYPE g8s_avg_execution_seconds gauge\ng8s_avg_execution_seconds 5.6789",
		"# HELP g8s_active_workers Number of active workers\n# TYPE g8s_active_workers gauge\ng8s_active_workers 3",
		"# HELP g8s_active_sessions Number of active sessions\n# TYPE g8s_active_sessions gauge\ng8s_active_sessions 2",
		"# HELP g8s_supervisor_total_runs Total number of supervisor runs\n# TYPE g8s_supervisor_total_runs counter\ng8s_supervisor_total_runs 8",
		"# HELP g8s_supervisor_first_attempt_success_rate First attempt success rate\n# TYPE g8s_supervisor_first_attempt_success_rate gauge\ng8s_supervisor_first_attempt_success_rate 0.8750",
		"# HELP g8s_supervisor_escalation_rate Supervisor escalation rate\n# TYPE g8s_supervisor_escalation_rate gauge\ng8s_supervisor_escalation_rate 0.1250",
	}

	for _, sub := range expectedSubstrings {
		if !strings.Contains(prom, sub) {
			t.Errorf("ExportPrometheus missing expected block:\n%s\n\nFull output:\n%s", sub, prom)
		}
	}
}
