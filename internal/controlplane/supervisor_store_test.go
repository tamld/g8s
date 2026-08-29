package controlplane

import (
	"context"
	"strings"
	"testing"
	"time"
)

func newTestControlPlane(t *testing.T, clock func() time.Time) *Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := dir + "/test.db"
	store, err := NewControlPlane(dbPath, clock)
	if err != nil {
		t.Fatalf("new controlplane: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestSupervisorStoreLifecycle(t *testing.T) {
	now := time.Now()
	store := newTestControlPlane(t, func() time.Time { return now })

	ctx := context.Background()
	row := SupervisorTaskRow{
		ID:           "sup-lifecycle-1",
		State:        "running",
		EnvelopeJSON: `{"x":1}`,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := store.CreateSupervisorTask(ctx, row); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := store.GetSupervisorTask(ctx, "sup-lifecycle-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != "running" {
		t.Errorf("expected state=running, got %q", got.State)
	}
	if got.EnvelopeJSON != `{"x":1}` {
		t.Errorf("expected envelope json passthrough, got %q", got.EnvelopeJSON)
	}

	row.State = "succeeded"
	if err := store.UpdateSupervisorTask(ctx, row); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = store.GetSupervisorTask(ctx, "sup-lifecycle-1")
	if got.State != "succeeded" {
		t.Errorf("expected state=succeeded, got %q", got.State)
	}

	rows, err := store.ListSupervisorTasks(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(rows))
	}
}

func TestSupervisorStoreDuplicateID(t *testing.T) {
	now := time.Now()
	store := newTestControlPlane(t, func() time.Time { return now })
	ctx := context.Background()

	if err := store.CreateSupervisorTask(ctx, SupervisorTaskRow{
		ID:        "dup",
		State:     "running",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	err := store.CreateSupervisorTask(ctx, SupervisorTaskRow{
		ID:        "dup",
		State:     "running",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("expected UNIQUE constraint error, got %v", err)
	}
}

func TestSupervisorStoreAppendDecision(t *testing.T) {
	now := time.Now()
	store := newTestControlPlane(t, func() time.Time { return now })
	ctx := context.Background()

	if err := store.CreateSupervisorTask(ctx, SupervisorTaskRow{
		ID:        "sup-decision-1",
		State:     "running",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.AppendDecision(ctx, SupervisorDecisionRow{
		TaskID:      "sup-decision-1",
		Kind:        "run_started",
		PayloadJSON: `{"a":1}`,
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := store.AppendDecision(ctx, SupervisorDecisionRow{
		TaskID:      "sup-decision-1",
		Kind:        "review_verdict",
		PayloadJSON: `{"a":2}`,
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("append 2: %v", err)
	}
}

func TestSupervisorStoreMetricsRoundTrip(t *testing.T) {
	now := time.Now()
	store := newTestControlPlane(t, func() time.Time { return now })
	ctx := context.Background()

	if err := store.CreateSupervisorTask(ctx, SupervisorTaskRow{
		ID:        "sup-metrics-1",
		State:     "succeeded",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	want := MetricsRow{
		SupervisorTaskID:     "sup-metrics-1",
		EnvelopeScore:        0.75,
		FirstAttemptSuccess:  true,
		AttemptsToSuccess:    2,
		ApproachesToSuccess:  1,
		RCAConfidenceAvg:     0.85,
		CycleDurationSeconds: 10.5,
		EscalationCount:      0,
		FalseEscalationRate:  0.1,
	}
	if err := store.SaveMetrics(ctx, "sup-metrics-1", want); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := store.GetMetrics(ctx, "sup-metrics-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.EnvelopeScore != want.EnvelopeScore {
		t.Errorf("EnvelopeScore: want %f, got %f", want.EnvelopeScore, got.EnvelopeScore)
	}
	if got.FirstAttemptSuccess != want.FirstAttemptSuccess {
		t.Errorf("FirstAttemptSuccess: want %t, got %t", want.FirstAttemptSuccess, got.FirstAttemptSuccess)
	}
	if got.AttemptsToSuccess != want.AttemptsToSuccess {
		t.Errorf("AttemptsToSuccess: want %d, got %d", want.AttemptsToSuccess, got.AttemptsToSuccess)
	}
}

func TestSupervisorStoreMetricsOverwrite(t *testing.T) {
	now := time.Now()
	store := newTestControlPlane(t, func() time.Time { return now })
	ctx := context.Background()

	if err := store.CreateSupervisorTask(ctx, SupervisorTaskRow{
		ID:        "sup-metrics-overwrite",
		State:     "succeeded",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	first := MetricsRow{SupervisorTaskID: "sup-metrics-overwrite", EnvelopeScore: 0.5, AttemptsToSuccess: 1}
	if err := store.SaveMetrics(ctx, "sup-metrics-overwrite", first); err != nil {
		t.Fatalf("save first: %v", err)
	}
	second := MetricsRow{SupervisorTaskID: "sup-metrics-overwrite", EnvelopeScore: 0.9, AttemptsToSuccess: 5}
	if err := store.SaveMetrics(ctx, "sup-metrics-overwrite", second); err != nil {
		t.Fatalf("save second: %v", err)
	}
	got, err := store.GetMetrics(ctx, "sup-metrics-overwrite")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.EnvelopeScore != 0.9 || got.AttemptsToSuccess != 5 {
		t.Errorf("overwrite failed: got %+v", got)
	}
}

func TestSupervisorStoreGetMissing(t *testing.T) {
	now := time.Now()
	store := newTestControlPlane(t, func() time.Time { return now })
	_, err := store.GetSupervisorTask(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing task")
	}
	if !strings.Contains(err.Error(), "unknown supervisor task") {
		t.Errorf("expected ErrUnknownSupervisorTask, got %v", err)
	}
}

func TestSupervisorStoreGetMetricsMissing(t *testing.T) {
	now := time.Now()
	store := newTestControlPlane(t, func() time.Time { return now })
	_, err := store.GetMetrics(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing metrics")
	}
}
