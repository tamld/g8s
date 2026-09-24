package supervisor

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

func TestResolveTimeBounds(t *testing.T) {
	refTime := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return refTime }

	// 1. Zero options (clock fallback to time.Now)
	s1, u1 := resolveTimeBounds(AggregateOptions{})
	if !s1.IsZero() || !u1.IsZero() {
		t.Errorf("expected zero bounds for empty options, got since=%v, until=%v", s1, u1)
	}

	// 2. Relative time range with clock
	opts := AggregateOptions{
		TimeRange: 2 * time.Hour,
		Clock:     clock,
	}
	s2, u2 := resolveTimeBounds(opts)
	expectedSince := refTime.Add(-2 * time.Hour)
	if !s2.Equal(expectedSince) {
		t.Errorf("expected since %v, got %v", expectedSince, s2)
	}
	if !u2.IsZero() {
		t.Errorf("expected zero until, got %v", u2)
	}

	// 3. Time range with older explicit Since (cutoff is after since -> cutoff wins)
	olderSince := refTime.Add(-5 * time.Hour)
	optsOlder := AggregateOptions{
		TimeRange: 2 * time.Hour,
		Since:     olderSince,
		Clock:     clock,
	}
	s3, _ := resolveTimeBounds(optsOlder)
	if !s3.Equal(expectedSince) {
		t.Errorf("expected cutoff %v to override older since %v, got %v", expectedSince, olderSince, s3)
	}

	// 4. Time range with newer explicit Since (cutoff is before since -> explicit since preserved)
	newerSince := refTime.Add(-1 * time.Hour)
	optsNewer := AggregateOptions{
		TimeRange: 2 * time.Hour,
		Since:     newerSince,
		Clock:     clock,
	}
	s4, _ := resolveTimeBounds(optsNewer)
	if !s4.Equal(newerSince) {
		t.Errorf("expected newer since %v to be preserved, got %v", newerSince, s4)
	}

	// 5. Explicit Until
	explicitUntil := refTime.Add(30 * time.Minute)
	optsUntil := AggregateOptions{
		Until: explicitUntil,
	}
	_, u5 := resolveTimeBounds(optsUntil)
	if !u5.Equal(explicitUntil) {
		t.Errorf("expected until %v, got %v", explicitUntil, u5)
	}
}

func TestAggregate_ValidationAndContext(t *testing.T) {
	ctx := context.Background()

	// 1. Nil store
	if _, err := Aggregate(nil, ctx, AggregateOptions{}); err == nil {
		t.Error("expected error with nil store, got nil")
	}

	// 2. Canceled context before run
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()

	dbPath := filepath.Join(t.TempDir(), "ctx_test.db")
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if _, err := Aggregate(store, canceledCtx, AggregateOptions{}); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestAggregate_CalculationsAndFiltering(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "aggregate_test.db")
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	refTime := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

	// Seed supervisor tasks
	tasks := []struct {
		id        string
		createdAt time.Time
		hasMetric bool
		metric    controlplane.MetricsRow
	}{
		{
			id:        "task-1",
			createdAt: refTime.Add(-2 * time.Hour),
			hasMetric: true,
			metric: controlplane.MetricsRow{
				SupervisorTaskID:     "task-1",
				EnvelopeScore:        0.9,
				FirstAttemptSuccess:  true,
				AttemptsToSuccess:    1,
				ApproachesToSuccess:  1,
				CycleDurationSeconds: 10.0,
				EscalationCount:      0,
			},
		},
		{
			id:        "task-2",
			createdAt: refTime.Add(-1 * time.Hour),
			hasMetric: true,
			metric: controlplane.MetricsRow{
				SupervisorTaskID:     "task-2",
				EnvelopeScore:        0.8,
				FirstAttemptSuccess:  false,
				AttemptsToSuccess:    3,
				ApproachesToSuccess:  2,
				CycleDurationSeconds: 30.0,
				EscalationCount:      1,
			},
		},
		{
			id:        "task-3",
			createdAt: refTime.Add(-30 * time.Minute),
			hasMetric: true,
			metric: controlplane.MetricsRow{
				SupervisorTaskID:     "task-3",
				EnvelopeScore:        0.95,
				FirstAttemptSuccess:  true,
				AttemptsToSuccess:    1,
				ApproachesToSuccess:  1,
				CycleDurationSeconds: 20.0,
				EscalationCount:      0,
			},
		},
		{
			id:        "task-4-nometrics",
			createdAt: refTime.Add(-10 * time.Minute),
			hasMetric: false,
		},
	}

	for _, tc := range tasks {
		st := controlplane.SupervisorTaskRow{
			ID:           tc.id,
			State:        "succeeded",
			EnvelopeJSON: "{}",
			ApproachIdx:  0,
			AttemptIdx:   0,
			CreatedAt:    tc.createdAt,
			UpdatedAt:    tc.createdAt,
		}
		if err := store.CreateSupervisorTask(ctx, st); err != nil {
			t.Fatalf("CreateSupervisorTask %s: %v", tc.id, err)
		}
		if tc.hasMetric {
			if err := store.SaveMetrics(ctx, tc.id, tc.metric); err != nil {
				t.Fatalf("SaveMetrics %s: %v", tc.id, err)
			}
		}
	}

	// 1. Aggregate without filters
	agg, err := Aggregate(store, ctx, AggregateOptions{})
	if err != nil {
		t.Fatalf("Aggregate failed: %v", err)
	}

	if agg.TotalRuns != 3 {
		t.Fatalf("expected 3 total runs, got %d", agg.TotalRuns)
	}
	expectedFirstRate := 2.0 / 3.0
	if diff := agg.FirstAttemptSuccessRate - expectedFirstRate; diff < -0.001 || diff > 0.001 {
		t.Errorf("expected FirstAttemptSuccessRate ~%.3f, got %.3f", expectedFirstRate, agg.FirstAttemptSuccessRate)
	}
	expectedAvgAttempts := 5.0 / 3.0
	if diff := agg.AvgAttemptsToSuccess - expectedAvgAttempts; diff < -0.001 || diff > 0.001 {
		t.Errorf("expected AvgAttemptsToSuccess ~%.3f, got %.3f", expectedAvgAttempts, agg.AvgAttemptsToSuccess)
	}
	expectedAvgApproaches := 4.0 / 3.0
	if diff := agg.AvgApproachesToSuccess - expectedAvgApproaches; diff < -0.001 || diff > 0.001 {
		t.Errorf("expected AvgApproachesToSuccess ~%.3f, got %.3f", expectedAvgApproaches, agg.AvgApproachesToSuccess)
	}
	expectedEscalationRate := 1.0 / 3.0
	if diff := agg.EscalationRate - expectedEscalationRate; diff < -0.001 || diff > 0.001 {
		t.Errorf("expected EscalationRate ~%.3f, got %.3f", expectedEscalationRate, agg.EscalationRate)
	}
	expectedAvgCycle := 60.0 / 3.0
	if diff := agg.AvgCycleSeconds - expectedAvgCycle; diff < -0.001 || diff > 0.001 {
		t.Errorf("expected AvgCycleSeconds ~%.3f, got %.3f", expectedAvgCycle, agg.AvgCycleSeconds)
	}

	// 2. Filter by TimeRange (e.g. within last 45 minutes)
	aggTime, err := Aggregate(store, ctx, AggregateOptions{
		TimeRange: 45 * time.Minute,
		Clock:     func() time.Time { return refTime },
	})
	if err != nil {
		t.Fatalf("Aggregate with TimeRange failed: %v", err)
	}
	if aggTime.TotalRuns != 1 {
		t.Errorf("expected 1 run in last 45 min, got %d", aggTime.TotalRuns)
	}

	// 3. Filter with Since / Until window that selects only task-2
	aggWindow, err := Aggregate(store, ctx, AggregateOptions{
		Since: refTime.Add(-90 * time.Minute),
		Until: refTime.Add(-45 * time.Minute),
	})
	if err != nil {
		t.Fatalf("Aggregate with Since/Until failed: %v", err)
	}
	if aggWindow.TotalRuns != 1 {
		t.Fatalf("expected 1 run in window, got %d", aggWindow.TotalRuns)
	}
	if aggWindow.EscalationRate != 1.0 {
		t.Errorf("expected EscalationRate 1.0, got %.2f", aggWindow.EscalationRate)
	}

	// 4. Filter with WorkerName that does not match
	aggWorker, err := Aggregate(store, ctx, AggregateOptions{
		WorkerName: "non-existent-worker",
	})
	if err != nil {
		t.Fatalf("Aggregate with WorkerName failed: %v", err)
	}
	if aggWorker.TotalRuns != 0 {
		t.Errorf("expected 0 runs for non-existent worker, got %d", aggWorker.TotalRuns)
	}
}

func TestStreamMetrics(t *testing.T) {
	ctx := context.Background()

	// 1. Nil store
	err := StreamMetrics(nil, ctx, AggregateOptions{}, func(item TaskMetricsItem) error { return nil })
	if err == nil {
		t.Error("expected error with nil store, got nil")
	}

	// 2. Nil callback fn
	dbPath := filepath.Join(t.TempDir(), "stream_test.db")
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	err = StreamMetrics(store, ctx, AggregateOptions{}, nil)
	if err == nil {
		t.Error("expected error with nil fn, got nil")
	}

	// 3. Canceled context before call
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	err = StreamMetrics(store, canceledCtx, AggregateOptions{}, func(item TaskMetricsItem) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	// 4. Seed supervisor task and stream successfully
	st := controlplane.SupervisorTaskRow{
		ID:           "stream-task-1",
		State:        "succeeded",
		EnvelopeJSON: "{}",
		ApproachIdx:  0,
		AttemptIdx:   0,
		CreatedAt:    time.Now().Add(-5 * time.Minute),
		UpdatedAt:    time.Now(),
	}
	if err := store.CreateSupervisorTask(ctx, st); err != nil {
		t.Fatalf("CreateSupervisorTask: %v", err)
	}
	if err := store.SaveMetrics(ctx, st.ID, controlplane.MetricsRow{
		SupervisorTaskID:     st.ID,
		EnvelopeScore:        0.95,
		FirstAttemptSuccess:  true,
		AttemptsToSuccess:    1,
		ApproachesToSuccess:  1,
		RCAConfidenceAvg:     0.88,
		CycleDurationSeconds: 15.5,
		EscalationCount:      0,
		FalseEscalationRate:  0.0,
	}); err != nil {
		t.Fatalf("SaveMetrics: %v", err)
	}

	var items []TaskMetricsItem
	err = StreamMetrics(store, ctx, AggregateOptions{}, func(item TaskMetricsItem) error {
		items = append(items, item)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamMetrics failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 streamed item, got %d", len(items))
	}
	if items[0].SupervisorTaskID != "stream-task-1" {
		t.Errorf("expected task ID stream-task-1, got %s", items[0].SupervisorTaskID)
	}
	if items[0].EnvelopeScore != 0.95 || items[0].CycleDurationSeconds != 15.5 {
		t.Errorf("streamed metrics item fields mismatch: %+v", items[0])
	}

	// 5. Stream with callback returning error aborts stream
	sentinelErr := errors.New("abort stream")
	err = StreamMetrics(store, ctx, AggregateOptions{}, func(item TaskMetricsItem) error {
		return sentinelErr
	})
	if !errors.Is(err, sentinelErr) {
		t.Errorf("expected sentinelErr, got %v", err)
	}
}
