package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

func newTestStore(t *testing.T) *controlplane.Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("new controlplane: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// 1. Aggregate over empty store returns zero-value metrics, no error.
func TestAggregateEmptyStore(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	agg, err := Aggregate(store, ctx, AggregateOptions{})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if agg.TotalRuns != 0 {
		t.Errorf("expected TotalRuns=0, got %d", agg.TotalRuns)
	}
	if agg.FirstAttemptSuccessRate != 0 {
		t.Errorf("expected FirstAttemptSuccessRate=0, got %f", agg.FirstAttemptSuccessRate)
	}
	if agg.AvgAttemptsToSuccess != 0 {
		t.Errorf("expected AvgAttemptsToSuccess=0, got %f", agg.AvgAttemptsToSuccess)
	}
	if agg.AvgApproachesToSuccess != 0 {
		t.Errorf("expected AvgApproachesToSuccess=0, got %f", agg.AvgApproachesToSuccess)
	}
	if agg.EscalationRate != 0 {
		t.Errorf("expected EscalationRate=0, got %f", agg.EscalationRate)
	}
	if agg.AvgCycleSeconds != 0 {
		t.Errorf("expected AvgCycleSeconds=0, got %f", agg.AvgCycleSeconds)
	}
}

// 2. Aggregate over 5 rows computes correct averages.
func TestAggregateFiveRows(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Now()

	rows := []struct {
		id                  string
		firstAttemptSuccess bool
		attempts            int
		approaches          int
		escalation          int
		cycleDuration       float64
	}{
		{"sup-1", true, 1, 1, 0, 10.0},
		{"sup-2", false, 2, 1, 0, 20.0},
		{"sup-3", false, 3, 2, 1, 30.0},
		{"sup-4", true, 1, 1, 0, 15.0},
		{"sup-5", false, 4, 3, 1, 25.0},
	}

	for _, r := range rows {
		if err := store.CreateSupervisorTask(ctx, controlplane.SupervisorTaskRow{
			ID:        r.id,
			State:     "succeeded",
			CreatedAt: now,
			UpdatedAt: now,
		}); err != nil {
			t.Fatalf("create task %s: %v", r.id, err)
		}
		if err := store.SaveMetrics(ctx, r.id, controlplane.MetricsRow{
			SupervisorTaskID:     r.id,
			EnvelopeScore:        0.8,
			FirstAttemptSuccess:  r.firstAttemptSuccess,
			AttemptsToSuccess:    r.attempts,
			ApproachesToSuccess:  r.approaches,
			RCAConfidenceAvg:     0.9,
			CycleDurationSeconds: r.cycleDuration,
			EscalationCount:      r.escalation,
			FalseEscalationRate:  0,
		}); err != nil {
			t.Fatalf("save metrics %s: %v", r.id, err)
		}
	}

	agg, err := Aggregate(store, ctx, AggregateOptions{})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}

	if agg.TotalRuns != 5 {
		t.Errorf("expected TotalRuns=5, got %d", agg.TotalRuns)
	}
	// 2 out of 5 were first attempt success -> 0.4
	if math.Abs(agg.FirstAttemptSuccessRate-0.4) > 1e-6 {
		t.Errorf("expected FirstAttemptSuccessRate=0.4, got %f", agg.FirstAttemptSuccessRate)
	}
	// Attempts: 1 + 2 + 3 + 1 + 4 = 11 / 5 = 2.2
	if math.Abs(agg.AvgAttemptsToSuccess-2.2) > 1e-6 {
		t.Errorf("expected AvgAttemptsToSuccess=2.2, got %f", agg.AvgAttemptsToSuccess)
	}
	// Approaches: 1 + 1 + 2 + 1 + 3 = 8 / 5 = 1.6
	if math.Abs(agg.AvgApproachesToSuccess-1.6) > 1e-6 {
		t.Errorf("expected AvgApproachesToSuccess=1.6, got %f", agg.AvgApproachesToSuccess)
	}
	// Escalation: 2 out of 5 -> 0.4
	if math.Abs(agg.EscalationRate-0.4) > 1e-6 {
		t.Errorf("expected EscalationRate=0.4, got %f", agg.EscalationRate)
	}
	// Cycle: 10 + 20 + 30 + 15 + 25 = 100 / 5 = 20.0
	if math.Abs(agg.AvgCycleSeconds-20.0) > 1e-6 {
		t.Errorf("expected AvgCycleSeconds=20.0, got %f", agg.AvgCycleSeconds)
	}
}

// 3. Filter by time_range (last 1h, last 24h) works.
func TestAggregateFilterTimeRange(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	fixedNow := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fixedNow }

	// Task 1: 30 minutes ago (within 1h and within 24h)
	t1Time := fixedNow.Add(-30 * time.Minute)
	// Task 2: 3 hours ago (outside 1h, within 24h)
	t2Time := fixedNow.Add(-3 * time.Hour)
	// Task 3: 48 hours ago (outside 1h and outside 24h)
	t3Time := fixedNow.Add(-48 * time.Hour)

	tasks := []struct {
		id        string
		createdAt time.Time
		attempts  int
	}{
		{"sup-recent", t1Time, 1},
		{"sup-today", t2Time, 2},
		{"sup-old", t3Time, 3},
	}

	for _, task := range tasks {
		if err := store.CreateSupervisorTask(ctx, controlplane.SupervisorTaskRow{
			ID:        task.id,
			State:     "succeeded",
			CreatedAt: task.createdAt,
			UpdatedAt: task.createdAt,
		}); err != nil {
			t.Fatalf("create task %s: %v", task.id, err)
		}
		if err := store.SaveMetrics(ctx, task.id, controlplane.MetricsRow{
			SupervisorTaskID:     task.id,
			EnvelopeScore:        1.0,
			FirstAttemptSuccess:  true,
			AttemptsToSuccess:    task.attempts,
			ApproachesToSuccess:  1,
			RCAConfidenceAvg:     1.0,
			CycleDurationSeconds: 10.0,
			EscalationCount:      0,
		}); err != nil {
			t.Fatalf("save metrics %s: %v", task.id, err)
		}
	}

	// Test last 1h
	agg1h, err := Aggregate(store, ctx, AggregateOptions{
		TimeRange: 1 * time.Hour,
		Clock:     clock,
	})
	if err != nil {
		t.Fatalf("aggregate 1h: %v", err)
	}
	if agg1h.TotalRuns != 1 {
		t.Errorf("expected 1 task in last 1h, got %d", agg1h.TotalRuns)
	}
	if agg1h.AvgAttemptsToSuccess != 1.0 {
		t.Errorf("expected AvgAttemptsToSuccess=1.0, got %f", agg1h.AvgAttemptsToSuccess)
	}

	// Test last 24h
	agg24h, err := Aggregate(store, ctx, AggregateOptions{
		TimeRange: 24 * time.Hour,
		Clock:     clock,
	})
	if err != nil {
		t.Fatalf("aggregate 24h: %v", err)
	}
	if agg24h.TotalRuns != 2 {
		t.Errorf("expected 2 tasks in last 24h, got %d", agg24h.TotalRuns)
	}
	// Average attempts for 1 and 2 = 1.5
	if agg24h.AvgAttemptsToSuccess != 1.5 {
		t.Errorf("expected AvgAttemptsToSuccess=1.5, got %f", agg24h.AvgAttemptsToSuccess)
	}

	// Test all time (no TimeRange filter)
	aggAll, err := Aggregate(store, ctx, AggregateOptions{
		Clock: clock,
	})
	if err != nil {
		t.Fatalf("aggregate all: %v", err)
	}
	if aggAll.TotalRuns != 3 {
		t.Errorf("expected 3 tasks in all time, got %d", aggAll.TotalRuns)
	}
}

// 4. Filter by worker_name works.
func TestAggregateFilterWorkerName(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Now()

	// Create supervisor task 1 (agy worker recorded via task row)
	supAgy1 := "sup-agy-1"
	if err := store.CreateSupervisorTask(ctx, controlplane.SupervisorTaskRow{
		ID:        supAgy1,
		State:     "succeeded",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create supAgy1: %v", err)
	}
	if err := store.SaveMetrics(ctx, supAgy1, controlplane.MetricsRow{
		SupervisorTaskID:     supAgy1,
		EnvelopeScore:        0.8,
		FirstAttemptSuccess:  true,
		AttemptsToSuccess:    1,
		ApproachesToSuccess:  1,
		RCAConfidenceAvg:     0.9,
		CycleDurationSeconds: 10.0,
		EscalationCount:      0,
	}); err != nil {
		t.Fatalf("save metrics supAgy1: %v", err)
	}
	// Submit a task with worker_name = "agy" referencing supAgy1
	workerAgy := "agy"
	if _, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "k-sup-agy-1",
		Priority:       0,
		MaxAttempts:    1,
		Payload:        json.RawMessage(`{"prompt":"p1"}`),
		Model:          "claude-3-5-sonnet",
		AddDirs:        []string{t.TempDir()},
		OrchestratorID: &supAgy1,
		WorkerName:     &workerAgy,
	}); err != nil {
		t.Fatalf("submit task for supAgy1: %v", err)
	}

	// Create supervisor task 2 (codex worker recorded via decision row)
	const supCodex = "sup-codex-1"
	if err := store.CreateSupervisorTask(ctx, controlplane.SupervisorTaskRow{
		ID:        supCodex,
		State:     "succeeded",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create supCodex: %v", err)
	}
	if err := store.SaveMetrics(ctx, supCodex, controlplane.MetricsRow{
		SupervisorTaskID:     supCodex,
		EnvelopeScore:        0.7,
		FirstAttemptSuccess:  false,
		AttemptsToSuccess:    3,
		ApproachesToSuccess:  2,
		RCAConfidenceAvg:     0.8,
		CycleDurationSeconds: 30.0,
		EscalationCount:      1,
	}); err != nil {
		t.Fatalf("save metrics supCodex: %v", err)
	}
	if err := store.AppendDecision(ctx, controlplane.SupervisorDecisionRow{
		TaskID:      supCodex,
		Kind:        "run_started",
		PayloadJSON: `{"worker_name":"codex","role":"scout"}`,
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("append decision supCodex: %v", err)
	}

	// Create supervisor task 3 (agy worker recorded via envelope json)
	const supAgy2 = "sup-agy-2"
	if err := store.CreateSupervisorTask(ctx, controlplane.SupervisorTaskRow{
		ID:           supAgy2,
		State:        "succeeded",
		EnvelopeJSON: `{"worker_name":"agy"}`,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("create supAgy2: %v", err)
	}
	if err := store.SaveMetrics(ctx, supAgy2, controlplane.MetricsRow{
		SupervisorTaskID:     supAgy2,
		EnvelopeScore:        0.9,
		FirstAttemptSuccess:  true,
		AttemptsToSuccess:    1,
		ApproachesToSuccess:  1,
		RCAConfidenceAvg:     1.0,
		CycleDurationSeconds: 12.0,
		EscalationCount:      0,
	}); err != nil {
		t.Fatalf("save metrics supAgy2: %v", err)
	}

	// Filter by worker_name = "agy"
	aggAgy, err := Aggregate(store, ctx, AggregateOptions{WorkerName: "agy"})
	if err != nil {
		t.Fatalf("aggregate agy: %v", err)
	}
	if aggAgy.TotalRuns != 2 {
		t.Errorf("expected 2 agy runs, got %d", aggAgy.TotalRuns)
	}
	if aggAgy.FirstAttemptSuccessRate != 1.0 {
		t.Errorf("expected FirstAttemptSuccessRate=1.0 for agy, got %f", aggAgy.FirstAttemptSuccessRate)
	}

	// Filter by worker_name = "codex"
	aggCodex, err := Aggregate(store, ctx, AggregateOptions{WorkerName: "codex"})
	if err != nil {
		t.Fatalf("aggregate codex: %v", err)
	}
	if aggCodex.TotalRuns != 1 {
		t.Errorf("expected 1 codex run, got %d", aggCodex.TotalRuns)
	}
	if aggCodex.AvgAttemptsToSuccess != 3.0 {
		t.Errorf("expected AvgAttemptsToSuccess=3.0 for codex, got %f", aggCodex.AvgAttemptsToSuccess)
	}
	if aggCodex.EscalationRate != 1.0 {
		t.Errorf("expected EscalationRate=1.0 for codex, got %f", aggCodex.EscalationRate)
	}

	// Filter by non-existent worker
	aggNone, err := Aggregate(store, ctx, AggregateOptions{WorkerName: "nonexistent"})
	if err != nil {
		t.Fatalf("aggregate nonexistent: %v", err)
	}
	if aggNone.TotalRuns != 0 {
		t.Errorf("expected 0 runs for nonexistent worker, got %d", aggNone.TotalRuns)
	}
}

func TestStreamMetricsAndFiltering(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Now()

	for _, id := range []string{"sup-s1", "sup-s2"} {
		if err := store.CreateSupervisorTask(ctx, controlplane.SupervisorTaskRow{
			ID:        id,
			State:     "succeeded",
			CreatedAt: now,
			UpdatedAt: now,
		}); err != nil {
			t.Fatalf("create task %s: %v", id, err)
		}
		if err := store.SaveMetrics(ctx, id, controlplane.MetricsRow{
			SupervisorTaskID:     id,
			EnvelopeScore:        0.8,
			FirstAttemptSuccess:  true,
			AttemptsToSuccess:    1,
			ApproachesToSuccess:  1,
			RCAConfidenceAvg:     0.9,
			CycleDurationSeconds: 5.0,
			EscalationCount:      0,
		}); err != nil {
			t.Fatalf("save metrics %s: %v", id, err)
		}
	}

	var streamed []TaskMetricsItem
	err := StreamMetrics(store, ctx, AggregateOptions{}, func(item TaskMetricsItem) error {
		streamed = append(streamed, item)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamMetrics: %v", err)
	}
	if len(streamed) != 2 {
		t.Fatalf("expected 2 streamed items, got %d", len(streamed))
	}
	if streamed[0].SupervisorTaskID != "sup-s1" || streamed[1].SupervisorTaskID != "sup-s2" {
		t.Errorf("streamed task IDs mismatch: %+v", streamed)
	}

	// Test early cancellation / error propagation in stream callback
	expectedErr := errors.New("abort stream")
	err = StreamMetrics(store, ctx, AggregateOptions{}, func(item TaskMetricsItem) error {
		return expectedErr
	})
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestOptimizerValidationAndErrors(t *testing.T) {
	ctx := context.Background()

	// Nil store
	if _, err := Aggregate(nil, ctx, AggregateOptions{}); err == nil {
		t.Errorf("expected error with nil store in Aggregate")
	}
	if err := StreamMetrics(nil, ctx, AggregateOptions{}, func(TaskMetricsItem) error { return nil }); err == nil {
		t.Errorf("expected error with nil store in StreamMetrics")
	}
	store := newTestStore(t)
	if err := StreamMetrics(store, ctx, AggregateOptions{}, nil); err == nil {
		t.Errorf("expected error with nil fn in StreamMetrics")
	}

	// Canceled context
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Aggregate(store, canceledCtx, AggregateOptions{}); err == nil {
		t.Errorf("expected error with canceled context in Aggregate")
	}
	if err := StreamMetrics(store, canceledCtx, AggregateOptions{}, func(TaskMetricsItem) error { return nil }); err == nil {
		t.Errorf("expected error with canceled context in StreamMetrics")
	}

	// StubOptimizer
	opt := NewStubOptimizer()
	cfg := SupervisorConfig{MaxAttemptsPerApproach: 5, MaxApproaches: 4}
	proposed := opt.Propose(cfg, nil)
	if proposed != cfg {
		t.Errorf("StubOptimizer changed config: %+v != %+v", proposed, cfg)
	}
}

func TestListSupervisorDecisionsAndWorkerLookup(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Now()

	// ListSupervisorDecisions validation
	if _, err := store.ListSupervisorDecisions(ctx, ""); err == nil {
		t.Errorf("expected error for empty task_id")
	}
	if _, err := store.GetSupervisorTaskWorker(ctx, ""); err == nil {
		t.Errorf("expected error for empty supervisor_task_id")
	}

	const taskID = "sup-dec-test"
	if err := store.CreateSupervisorTask(ctx, controlplane.SupervisorTaskRow{
		ID:        taskID,
		State:     "running",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create supervisor task: %v", err)
	}

	// No decisions initially
	decs, err := store.ListSupervisorDecisions(ctx, taskID)
	if err != nil {
		t.Fatalf("ListSupervisorDecisions: %v", err)
	}
	if len(decs) != 0 {
		t.Errorf("expected 0 decisions, got %d", len(decs))
	}

	// Append decisions
	if err := store.AppendDecision(ctx, controlplane.SupervisorDecisionRow{
		TaskID:      taskID,
		Kind:        "run_started",
		PayloadJSON: `{"request":{"worker_name":"worker-agent"}}`,
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("append decision 1: %v", err)
	}
	if err := store.AppendDecision(ctx, controlplane.SupervisorDecisionRow{
		TaskID:      taskID,
		Kind:        "review_verdict",
		PayloadJSON: `{"receipt":{"WorkerName":"worker-agent"}}`,
		CreatedAt:   now.Add(time.Second),
	}); err != nil {
		t.Fatalf("append decision 2: %v", err)
	}

	decs, err = store.ListSupervisorDecisions(ctx, taskID)
	if err != nil {
		t.Fatalf("ListSupervisorDecisions: %v", err)
	}
	if len(decs) != 2 {
		t.Fatalf("expected 2 decisions, got %d", len(decs))
	}
	if decs[0].Kind != "run_started" || decs[1].Kind != "review_verdict" {
		t.Errorf("decisions order/kind mismatch: %+v", decs)
	}

	// Worker lookup
	worker, err := store.GetSupervisorTaskWorker(ctx, taskID)
	if err != nil {
		t.Fatalf("GetSupervisorTaskWorker: %v", err)
	}
	if worker != "worker-agent" {
		t.Errorf("expected worker 'worker-agent', got %q", worker)
	}

	// Worker lookup for task with no worker returns ""
	const noWorkerTask = "sup-no-worker"
	if err := store.CreateSupervisorTask(ctx, controlplane.SupervisorTaskRow{
		ID:           noWorkerTask,
		State:        "succeeded",
		EnvelopeJSON: "{}",
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("create noWorkerTask: %v", err)
	}
	w, err := store.GetSupervisorTaskWorker(ctx, noWorkerTask)
	if err != nil {
		t.Fatalf("GetSupervisorTaskWorker noWorkerTask: %v", err)
	}
	if w != "" {
		t.Errorf("expected empty worker name, got %q", w)
	}
}

func TestStreamMetricsAndAggregateAdvancedBounds(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	t0 := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 29, 11, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)

	// Create task 1 with metrics and worker "agy"
	if err := store.CreateSupervisorTask(ctx, controlplane.SupervisorTaskRow{
		ID:           "sup-adv-1",
		State:        "succeeded",
		EnvelopeJSON: `{"worker_name":"agy"}`,
		CreatedAt:    t0,
		UpdatedAt:    t0,
	}); err != nil {
		t.Fatalf("create task 1: %v", err)
	}
	if err := store.SaveMetrics(ctx, "sup-adv-1", controlplane.MetricsRow{
		SupervisorTaskID:     "sup-adv-1",
		EnvelopeScore:        0.9,
		FirstAttemptSuccess:  true,
		AttemptsToSuccess:    1,
		ApproachesToSuccess:  1,
		RCAConfidenceAvg:     0.9,
		CycleDurationSeconds: 10.0,
		EscalationCount:      0,
	}); err != nil {
		t.Fatalf("save metrics 1: %v", err)
	}

	// Create task 2 with metrics and worker "codex"
	if err := store.CreateSupervisorTask(ctx, controlplane.SupervisorTaskRow{
		ID:           "sup-adv-2",
		State:        "succeeded",
		EnvelopeJSON: `{"worker_name":"codex"}`,
		CreatedAt:    t1,
		UpdatedAt:    t1,
	}); err != nil {
		t.Fatalf("create task 2: %v", err)
	}
	if err := store.SaveMetrics(ctx, "sup-adv-2", controlplane.MetricsRow{
		SupervisorTaskID:     "sup-adv-2",
		EnvelopeScore:        0.8,
		FirstAttemptSuccess:  false,
		AttemptsToSuccess:    2,
		ApproachesToSuccess:  1,
		RCAConfidenceAvg:     0.85,
		CycleDurationSeconds: 20.0,
		EscalationCount:      0,
	}); err != nil {
		t.Fatalf("save metrics 2: %v", err)
	}

	// Create task 3 WITHOUT metrics at t2
	if err := store.CreateSupervisorTask(ctx, controlplane.SupervisorTaskRow{
		ID:           "sup-adv-3-no-metrics",
		State:        "running",
		EnvelopeJSON: `{"worker_name":"agy"}`,
		CreatedAt:    t2,
		UpdatedAt:    t2,
	}); err != nil {
		t.Fatalf("create task 3: %v", err)
	}

	// Test StreamMetrics with Until bound (should exclude t2 and t1 if Until is t0)
	var streamedUntil []TaskMetricsItem
	err := StreamMetrics(store, ctx, AggregateOptions{
		Until: t0,
	}, func(item TaskMetricsItem) error {
		streamedUntil = append(streamedUntil, item)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamMetrics Until: %v", err)
	}
	if len(streamedUntil) != 1 || streamedUntil[0].SupervisorTaskID != "sup-adv-1" {
		t.Errorf("expected only sup-adv-1 in Until t0 stream, got %+v", streamedUntil)
	}

	// Test StreamMetrics with Since bound (should exclude t0 if Since is t1)
	var streamedSince []TaskMetricsItem
	err = StreamMetrics(store, ctx, AggregateOptions{
		Since: t1,
	}, func(item TaskMetricsItem) error {
		streamedSince = append(streamedSince, item)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamMetrics Since: %v", err)
	}
	// Task 3 has no metrics, so only task 2 should be emitted
	if len(streamedSince) != 1 || streamedSince[0].SupervisorTaskID != "sup-adv-2" {
		t.Errorf("expected only sup-adv-2 in Since t1 stream, got %+v", streamedSince)
	}

	// Test StreamMetrics with WorkerName filter
	var streamedAgy []TaskMetricsItem
	err = StreamMetrics(store, ctx, AggregateOptions{
		WorkerName: "agy",
	}, func(item TaskMetricsItem) error {
		streamedAgy = append(streamedAgy, item)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamMetrics WorkerName: %v", err)
	}
	if len(streamedAgy) != 1 || streamedAgy[0].SupervisorTaskID != "sup-adv-1" {
		t.Errorf("expected only sup-adv-1 for agy worker stream, got %+v", streamedAgy)
	}

	// Test Aggregate with Until bound
	aggUntil, err := Aggregate(store, ctx, AggregateOptions{
		Until: t0,
	})
	if err != nil {
		t.Fatalf("Aggregate Until: %v", err)
	}
	if aggUntil.TotalRuns != 1 {
		t.Errorf("expected TotalRuns=1 for Until t0, got %d", aggUntil.TotalRuns)
	}

	// Test Aggregate with Since bound
	aggSince, err := Aggregate(store, ctx, AggregateOptions{
		Since: t1,
	})
	if err != nil {
		t.Fatalf("Aggregate Since: %v", err)
	}
	if aggSince.TotalRuns != 1 {
		t.Errorf("expected TotalRuns=1 for Since t1, got %d", aggSince.TotalRuns)
	}
}
