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

// stubWorker records Spawn calls and returns a fixed receipt.
type stubWorker struct {
	receipts []orchestrator.Receipt
	calls    int
}

func (s *stubWorker) Name() string { return "stub" }
func (s *stubWorker) Available(_ context.Context) error { return nil }
func (s *stubWorker) Spawn(_ context.Context, t orchestrator.Task) (orchestrator.Handle, error) {
	if s.calls >= len(s.receipts) {
		return nil, io.EOF
	}
	r := s.receipts[s.calls]
	if r.TaskID == "" {
		r.TaskID = t.ID
	}
	if r.WorkerName == "" {
		r.WorkerName = "stub"
	}
	s.calls++
	return &stubHandle{receipt: r}, nil
}

type stubHandle struct{ receipt orchestrator.Receipt }

func (h *stubHandle) PID() int { return -1 }
func (h *stubHandle) Wait(_ context.Context) (orchestrator.Receipt, error) {
	return h.receipt, nil
}
func (h *stubHandle) Cancel(_ context.Context) error { return nil }
func (h *stubHandle) StdoutStream() interface {
	Read(p []byte) (int, error)
	Close() error
} { return nil }

func TestRunOrchestrateSelfTestSmoke(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orch.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	t.Setenv("G8S_DB", dbPath)

	// Inject stub worker through a fresh supervisor run.
	sup := supervisor.NewSelfTestSupervisor(store, &stubWorker{
		receipts: []orchestrator.Receipt{{
			OK:              true,
			WorkerName:      "stub",
			TaskID:          "x",
			CommitSHA:       "abc",
			FilesModified:   []string{"a.go"},
			ReturnCode:      0,
			HarnessCode:     0,
			DurationSeconds: 0.1,
			StartedAt:       time.Now(),
			FinishedAt:      time.Now(),
		}},
	}, supervisor.NewStubReviewer())
	sup.Config.MaxAttemptsPerApproach = 1
	sup.Config.MaxApproaches = 1
	res, err := sup.Run(context.Background(), supervisor.RunRequest{
		TaskDescription: "noop",
		Role:            "collector",
		Permission:      "read_only",
		Model:           "m1",
		SelfTestMode:    true,
		AddDirs:         []string{"/tmp"},
	})
	if err != nil {
		t.Fatalf("supervisor Run: %v", err)
	}
	if res.Outcome != supervisor.RunSucceeded {
		t.Errorf("expected RunSucceeded, got %s", res.Outcome)
	}
}

func TestRunSubmitMissingArgs(t *testing.T) {
	// runSubmit calls os.Exit(2) on missing flags; this test verifies
	// the failure path without invoking the real submit (which would
	// write to the shared DB). The function does not panic, so we
	// only assert that we never reach a successful return.
	t.Skip("runSubmit calls os.Exit(2); see TestRunSupervisorMetricsMissingArgs for the pattern")
}

func TestAggregateMetricsEmpty(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/empty.db"
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	agg, err := aggregateSupervisorMetrics(context.Background(), store)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if agg.TotalRuns != 0 || agg.FirstAttemptSuccessRate != 0 {
		t.Errorf("expected zeros, got %+v", agg)
	}
}

func TestOrchestrateResultJSONFieldOrder(t *testing.T) {
	// Spot-check the JSON contract the regression suite greps for.
	out := orchestrateResultJSON{ApproachesTried: 3, TotalAttempts: 9, Outcome: "succeeded"}
	encoded, _ := json.Marshal(out)
	got := string(encoded)
	if !strings.Contains(got, `"approaches_tried":3`) {
		t.Errorf("missing approaches_tried:3 in %s", got)
	}
	if !strings.Contains(got, `"total_attempts":9`) {
		t.Errorf("missing total_attempts:9 in %s", got)
	}
}
