package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/orchestrator"
	"github.com/tamld/g8s/internal/supervisor"
)

func withTempDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("new controlplane: %v", err)
	}
	_ = store.Close()
	return dbPath
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()
	fn()
	_ = w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestRunSupervisorMetricsAggregateEmpty(t *testing.T) {
	store, dbPath := newOpenStore(t)
	_ = dbPath

	out := captureStdout(t, func() {
		executeSupervisorMetrics("", true, false, true, supervisor.AggregateOptions{}, store)
	})

	if !strings.Contains(out, `"mode": "aggregate"`) {
		t.Fatalf("expected aggregate mode, got %s", out)
	}
	if !strings.Contains(out, `"total_runs": 0`) {
		t.Fatalf("expected total_runs=0, got %s", out)
	}
}

func TestRunSupervisorMetricsSingleTaskID(t *testing.T) {
	store, _ := newOpenStore(t)
	const supID = "sup-test-single"
	now := time.Now()
	if err := store.CreateSupervisorTask(context.Background(), controlplane.SupervisorTaskRow{
		ID:        supID,
		State:     "succeeded",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := store.SaveMetrics(context.Background(), supID, controlplane.MetricsRow{
		SupervisorTaskID:     supID,
		EnvelopeScore:        0.85,
		FirstAttemptSuccess:  true,
		AttemptsToSuccess:    1,
		ApproachesToSuccess:  1,
		RCAConfidenceAvg:     0.95,
		CycleDurationSeconds: 12.5,
		EscalationCount:      0,
		FalseEscalationRate:  0,
	}); err != nil {
		t.Fatalf("save metrics: %v", err)
	}

	out := captureStdout(t, func() {
		executeSupervisorMetrics(supID, false, false, true, supervisor.AggregateOptions{}, store)
	})

	if !strings.Contains(out, supID) {
		t.Fatalf("expected task id in output, got %s", out)
	}
	if !strings.Contains(out, `"mode": "single"`) {
		t.Fatalf("expected single mode, got %s", out)
	}
	if !strings.Contains(out, `"EnvelopeScore": 0.85`) {
		t.Fatalf("expected EnvelopeScore=0.85, got %s", out)
	}
}

func TestRunSupervisorMetricsSingleTaskIDPlainText(t *testing.T) {
	store, _ := newOpenStore(t)
	const supID = "sup-test-plain"
	now := time.Now()
	if err := store.CreateSupervisorTask(context.Background(), controlplane.SupervisorTaskRow{
		ID:        supID,
		State:     "succeeded",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := store.SaveMetrics(context.Background(), supID, controlplane.MetricsRow{
		SupervisorTaskID:     supID,
		EnvelopeScore:        0.5,
		FirstAttemptSuccess:  false,
		AttemptsToSuccess:    4,
		ApproachesToSuccess:  2,
		RCAConfidenceAvg:     0.7,
		CycleDurationSeconds: 30.0,
		EscalationCount:      1,
		FalseEscalationRate:  0.25,
	}); err != nil {
		t.Fatalf("save metrics: %v", err)
	}

	out := captureStdout(t, func() {
		executeSupervisorMetrics(supID, false, false, false, supervisor.AggregateOptions{}, store)
	})

	if !strings.Contains(out, "task_id="+supID) {
		t.Fatalf("expected task_id= prefix, got %s", out)
	}
	if !strings.Contains(out, "attempts_to_success=4") {
		t.Fatalf("expected attempts_to_success=4, got %s", out)
	}
}

func TestRunSupervisorMetricsJSONStream(t *testing.T) {
	store, _ := newOpenStore(t)
	const supID = "sup-test-stream"
	now := time.Now()
	if err := store.CreateSupervisorTask(context.Background(), controlplane.SupervisorTaskRow{
		ID:        supID,
		State:     "succeeded",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := store.SaveMetrics(context.Background(), supID, controlplane.MetricsRow{
		SupervisorTaskID:     supID,
		EnvelopeScore:        0.9,
		FirstAttemptSuccess:  true,
		AttemptsToSuccess:    1,
		ApproachesToSuccess:  1,
		RCAConfidenceAvg:     0.99,
		CycleDurationSeconds: 4.2,
		EscalationCount:      0,
		FalseEscalationRate:  0,
	}); err != nil {
		t.Fatalf("save metrics: %v", err)
	}

	out := captureStdout(t, func() {
		executeSupervisorMetrics("", false, true, true, supervisor.AggregateOptions{}, store)
	})

	if !strings.Contains(out, `"supervisor_task_id":"sup-test-stream"`) {
		t.Fatalf("expected streamed json object containing supervisor_task_id, got %s", out)
	}
	if !strings.Contains(out, `"first_attempt_success":true`) {
		t.Fatalf("expected first_attempt_success:true in stream, got %s", out)
	}
}

func newOpenStore(t *testing.T) (*controlplane.Store, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := dir + "/test.db"
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("new controlplane: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, dbPath
}

func TestOrchestrateResultJSONRoundtrip(t *testing.T) {
	out := orchestrateResultJSON{
		SupervisorTaskID: "sup-rt-001",
		Outcome:          supervisor.RunSucceeded.String(),
		Verdict:          "pass",
		ApproachesTried:  3,
		TotalAttempts:    9,
		Escalated:        true,
		Escalation: &supervisor.Escalation{
			TaskID:          "sup-rt-001",
			Trigger:         "iteration_cap",
			ApproachesTried: 3,
			TotalAttempts:   9,
		},
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"approaches_tried":3`) {
		t.Fatalf("expected approaches_tried:3, got %s", encoded)
	}
	if !strings.Contains(string(encoded), `"total_attempts":9`) {
		t.Fatalf("expected total_attempts:9, got %s", encoded)
	}
	if !strings.Contains(string(encoded), `"escalation":{`) {
		t.Fatalf("expected escalation object, got %s", encoded)
	}
}

func TestOrchestrateResultJSONNoEscalation(t *testing.T) {
	out := orchestrateResultJSON{
		SupervisorTaskID: "sup-rt-002",
		Outcome:          supervisor.RunSucceeded.String(),
		Verdict:          "pass",
		ApproachesTried:  1,
		TotalAttempts:    1,
		Escalated:        false,
		Escalation:       nil,
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), `"escalation":`) {
		t.Fatalf("expected no escalation key, got %s", encoded)
	}
}

func TestAggregateSupervisorMetricsWithRows(t *testing.T) {
	dbPath := withTempDB(t)
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	for i, id := range []string{"sup-agg-1", "sup-agg-2"} {
		now := time.Now()
		if err := store.CreateSupervisorTask(ctx, controlplane.SupervisorTaskRow{
			ID:        id,
			State:     "succeeded",
			CreatedAt: now,
			UpdatedAt: now,
		}); err != nil {
			t.Fatalf("create task %d: %v", i, err)
		}
		fas := i == 0
		if err := store.SaveMetrics(ctx, id, controlplane.MetricsRow{
			SupervisorTaskID:     id,
			EnvelopeScore:        0.5,
			FirstAttemptSuccess:  fas,
			AttemptsToSuccess:    i + 1,
			ApproachesToSuccess:  1,
			RCAConfidenceAvg:     0.8,
			CycleDurationSeconds: 5.0,
			EscalationCount:      i,
			FalseEscalationRate:  0,
		}); err != nil {
			t.Fatalf("save metrics %d: %v", i, err)
		}
	}

	agg, err := supervisor.Aggregate(store, ctx, supervisor.AggregateOptions{})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if agg.TotalRuns != 2 {
		t.Errorf("expected TotalRuns=2, got %d", agg.TotalRuns)
	}
	if agg.FirstAttemptSuccessRate != 0.5 {
		t.Errorf("expected first_attempt_success_rate=0.5, got %f", agg.FirstAttemptSuccessRate)
	}
	if agg.AvgAttemptsToSuccess != 1.5 {
		t.Errorf("expected avg_attempts_to_success=1.5, got %f", agg.AvgAttemptsToSuccess)
	}
	if agg.EscalationRate != 0.5 {
		t.Errorf("expected escalation_rate=0.5, got %f", agg.EscalationRate)
	}
}

func TestOrchestratePollingFlags(t *testing.T) {
	origCtor := orchestratorWorkerCtor
	defer func() { orchestratorWorkerCtor = origCtor }()
	orchestratorWorkerCtor = func() orchestrator.Worker { return &trackingStubWorker{} }

	dbPath := withTempDB(t)
	t.Setenv("G8S_DB", dbPath)

	out := captureStdout(t, func() {
		runOrchestrate([]string{
			"--self-test",
			"--model", "gemini-3.8-flash-high",
			"--silence-threshold", "5m",
			"--no-poll",
			"--json",
		})
	})

	if !strings.Contains(out, `"outcome"`) {
		t.Fatalf("expected JSON output containing outcome, got %s", out)
	}
}
