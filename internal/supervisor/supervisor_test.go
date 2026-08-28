package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/orchestrator"
)

// stubWorker is an in-process orchestrator.Worker used by every supervisor
// test. It returns scripted receipts from a per-call slice, then a default
// fail receipt when the slice is exhausted. Safe for concurrent use.
type stubWorker struct {
	mu          sync.Mutex
	receipts    []orchestrator.Receipt
	calls       int32
	failSpawn   atomic.Bool
	spawnErrMsg string
}

func newStubWorker(receipts ...orchestrator.Receipt) *stubWorker {
	return &stubWorker{receipts: receipts}
}

func (s *stubWorker) Name() string { return "stub" }

func (s *stubWorker) Available(_ context.Context) error { return nil }

func (s *stubWorker) Spawn(_ context.Context, t orchestrator.Task) (orchestrator.Handle, error) {
	if s.failSpawn.Load() {
		return nil, errors.New(s.spawnErrMsg)
	}
	idx := int(atomic.AddInt32(&s.calls, 1)) - 1
	s.mu.Lock()
	var receipt orchestrator.Receipt
	if idx < len(s.receipts) {
		receipt = s.receipts[idx]
	} else {
		receipt = s.receipts[len(s.receipts)-1]
	}
	s.mu.Unlock()
	if receipt.TaskID == "" {
		receipt.TaskID = t.ID
	}
	if receipt.WorkerName == "" {
		receipt.WorkerName = "stub"
	}
	return &stubHandle{receipt: receipt}, nil
}

func (s *stubWorker) callCount() int { return int(atomic.LoadInt32(&s.calls)) }

type stubHandle struct {
	receipt orchestrator.Receipt
}

func (h *stubHandle) PID() int { return -1 }

func (h *stubHandle) Wait(_ context.Context) (orchestrator.Receipt, error) {
	return h.receipt, nil
}

func (h *stubHandle) Cancel(_ context.Context) error { return nil }

func (h *stubHandle) StdoutStream() interface {
	Read(p []byte) (int, error)
	Close() error
} {
	return nil
}

func goodReceipt(taskID string) orchestrator.Receipt {
	return orchestrator.Receipt{
		OK:              true,
		WorkerName:      "stub",
		TaskID:          taskID,
		CommitSHA:       "abc123",
		FilesModified:   []string{"src/main.go"},
		ReturnCode:      0,
		HarnessCode:     0,
		DurationSeconds: 0.1,
		StartedAt:       time.Now(),
		FinishedAt:      time.Now(),
		ScopeViolations: nil,
	}
}

func failingReceipt(taskID string) orchestrator.Receipt {
	return orchestrator.Receipt{
		OK:              false,
		WorkerName:      "stub",
		TaskID:          taskID,
		CommitSHA:       "deadbeef",
		ReturnCode:      1,
		HarnessCode:     1,
		DurationSeconds: 0.05,
		StartedAt:       time.Now(),
		FinishedAt:      time.Now(),
		ScopeViolations: []string{"out-of-scope-write"},
	}
}

func newSelfTestSupervisorForWorker(store Persistence, worker orchestrator.Worker) *Supervisor {
	sup := NewSelfTestSupervisor(store, worker, NewStubReviewer())
	return sup
}

// ---- Happy path ----

func TestSupervisorRunHappy(t *testing.T) {
	store := NewStubPersistence()
	worker := newStubWorker(goodReceipt(""))
	sup := newSelfTestSupervisorForWorker(store, worker)
	sup.Config.MaxAttemptsPerApproach = 3
	sup.Config.MaxApproaches = 3

	res, err := sup.Run(context.Background(), RunRequest{
		TaskDescription: "scan src",
		Role:            "scout",
		Permission:      "read_only",
		Model:           "m1",
		AddDirs:         []string{"./src"},
		SelfTestMode:    true,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Outcome != RunSucceeded {
		t.Errorf("expected RunSucceeded, got %s verdict=%q", res.Outcome, res.Verdict)
	}
	if res.AttemptCount != 1 {
		t.Errorf("expected 1 attempt, got %d", res.AttemptCount)
	}
	if worker.callCount() != 1 {
		t.Errorf("expected 1 worker call, got %d", worker.callCount())
	}

	stored, err := store.GetSupervisorTask(context.Background(), res.SupervisorTaskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if stored.State != "succeeded" {
		t.Errorf("expected state=succeeded, got %q", stored.State)
	}
}

// ---- Deterministic fail → escalation ----

func TestSupervisorRunDeterministicFail(t *testing.T) {
	store := NewStubPersistence()
	worker := newStubWorker(failingReceipt(""))
	sup := newSelfTestSupervisorForWorker(store, worker)
	sup.Config.MaxAttemptsPerApproach = 3
	sup.Config.MaxApproaches = 3
	// Inject an RCA with high confidence so the loop burns all 9 attempts
	// before escalating (StubRCA's confidence drops below 0.6 after 4 fails,
	// which would trigger a NEEDS_INFO pause instead of escalation).
	sup.RCAFn = func(ctx context.Context, attempts []AttemptRecord) (RCARecord, error) {
		return RCARecord{
			Symptom:    "scope violation persists",
			RootCause:  "worker mutated outside allowed_paths",
			Confidence: 0.9,
		}, nil
	}

	res, err := sup.Run(context.Background(), RunRequest{
		TaskDescription: "scan src",
		Role:            "scout",
		Permission:      "read_only",
		Model:           "m1",
		AddDirs:         []string{"./src"},
		SelfTestMode:    true,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	if !res.Escalated {
		t.Fatalf("expected Escalated=true, got %+v", res)
	}
	if res.Outcome != RunEscalated {
		t.Errorf("expected RunEscalated, got %s", res.Outcome)
	}
	if res.ApproachCount != 3 {
		t.Errorf("expected ApproachCount=3, got %d", res.ApproachCount)
	}
	if res.AttemptCount != 9 {
		t.Errorf("expected AttemptCount=9, got %d", res.AttemptCount)
	}
	if res.Escalation == nil {
		t.Fatalf("expected non-nil Escalation")
	}
	if res.Escalation.TotalAttempts != 9 {
		t.Errorf("expected escalation.TotalAttempts=9, got %d", res.Escalation.TotalAttempts)
	}
	if worker.callCount() != 9 {
		t.Errorf("expected worker calls=9, got %d", worker.callCount())
	}

	stored, err := store.GetSupervisorTask(context.Background(), res.SupervisorTaskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if stored.State != "escalated" {
		t.Errorf("expected persisted state=escalated, got %q", stored.State)
	}
}

// ---- Revise then pass ----

func TestSupervisorRunReviseThenPass(t *testing.T) {
	// Scope-violation receipts would classify as FAIL and still consume the
	// attempt budget per the deterministic-fail contract. To exercise the
	// Revise-then-Pass loop we use non-scope failures.
	store := NewStubPersistence()
	nonScopeFail := func(taskID string) orchestrator.Receipt {
		return orchestrator.Receipt{
			OK:              false,
			TaskID:          taskID,
			ReturnCode:      1,
			CommitSHA:       "x",
			ScopeViolations: nil,
		}
	}
	worker := newStubWorker(
		nonScopeFail("f1"),
		nonScopeFail("f2"),
		goodReceipt("g3"),
	)
	sup := newSelfTestSupervisorForWorker(store, worker)
	sup.Config.MaxAttemptsPerApproach = 3
	sup.Config.MaxApproaches = 3
	// High-confidence RCA so we never pause mid-loop.
	sup.RCAFn = func(ctx context.Context, attempts []AttemptRecord) (RCARecord, error) {
		return RCARecord{Confidence: 0.9}, nil
	}

	res, err := sup.Run(context.Background(), RunRequest{
		TaskDescription: "scan src",
		Role:            "scout",
		Permission:      "read_only",
		Model:           "m1",
		AddDirs:         []string{"./src"},
		SelfTestMode:    true,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Outcome != RunSucceeded {
		t.Errorf("expected RunSucceeded, got %s verdict=%q", res.Outcome, res.Verdict)
	}
	if res.AttemptCount != 3 {
		t.Errorf("expected AttemptCount=3, got %d", res.AttemptCount)
	}
	if res.ApproachCount != 1 {
		t.Errorf("expected ApproachCount=1, got %d", res.ApproachCount)
	}
}

// ---- Iteration cap ----

func TestSupervisorRunEnforceIterationCap(t *testing.T) {
	store := NewStubPersistence()
	worker := newStubWorker(failingReceipt(""))
	sup := newSelfTestSupervisorForWorker(store, worker)
	sup.Config.MaxAttemptsPerApproach = 3
	sup.Config.MaxApproaches = 3
	// High-confidence RCA keeps the loop approaching-escalating instead of
	// pausing on low-confidence heuristic.
	sup.RCAFn = func(ctx context.Context, attempts []AttemptRecord) (RCARecord, error) {
		return RCARecord{Confidence: 0.9, Symptom: "cap test"}, nil
	}

	res, err := sup.Run(context.Background(), RunRequest{
		TaskDescription: "scan src",
		Role:            "scout",
		Permission:      "read_only",
		Model:           "m1",
		AddDirs:         []string{"./src"},
		SelfTestMode:    true,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if worker.callCount() != 9 {
		t.Errorf("expected exactly 9 worker calls (loop cap), got %d", worker.callCount())
	}
	if !res.Escalated {
		t.Errorf("expected escalation")
	}
}

// ---- Zero-imports-of-worker-binaries assertion ----

func TestSupervisorNeverImportsWorkerBinaries(t *testing.T) {
	// Resolve module root so this test runs from any working directory.
	rootCmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}")
	rootOut, err := rootCmd.Output()
	if err != nil {
		t.Fatalf("go list -m: %v", err)
	}
	moduleRoot := strings.TrimSpace(string(rootOut))
	cmd := exec.Command("go", "list", "-deps", "./internal/supervisor")
	cmd.Dir = moduleRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		l := strings.ToLower(line)
		for _, banned := range []string{"codex", "agy", "gemini", "claude"} {
			if strings.Contains(l, banned) {
				// Allow the orchestrator package itself (it only carries the
				// name as a comment, not as an import). Any path that looks
				// like a worker binary must fail.
				if strings.Contains(l, "github.com/tamld/g8s/internal/orchestrator") {
					continue
				}
				t.Errorf("supervisor deps must not include worker binary package %q (found: %s)", banned, line)
			}
		}
	}
}

// ---- Concurrent supervisor runs ----

func TestConcurrentSupervisorRun(t *testing.T) {
	store := NewStubPersistence()
	worker := newStubWorker(goodReceipt(""))

	var wg sync.WaitGroup
	errCh := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sup := newSelfTestSupervisorForWorker(store, worker)
			sup.Config.MaxAttemptsPerApproach = 3
			sup.Config.MaxApproaches = 3
			_, err := sup.Run(context.Background(), RunRequest{
				TaskDescription: "scan src",
				Role:            "scout",
				Permission:      "read_only",
				Model:           "m1",
				AddDirs:         []string{"./src"},
				SelfTestMode:    true,
			})
			if err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent run error: %v", err)
	}

	tasks, _ := store.ListSupervisorTasks(context.Background())
	if len(tasks) != 4 {
		t.Errorf("expected 4 supervisor tasks persisted, got %d", len(tasks))
	}
}

// ---- Reviewer policy ----

func TestReviewerScopeViolationAlwaysFails(t *testing.T) {
	env := SelectEnvelope(nil)
	r := NewStubReviewer()
	receipt := orchestrator.Receipt{
		OK:              true,
		CommitSHA:       "abc",
		ReturnCode:      0,
		ScopeViolations: []string{"secrets/.env"},
	}
	out := ReviewReceipt(receipt, env, r)
	if out.Verdict != VerdictFail {
		t.Errorf("expected VerdictFail regardless of OK=true, got %s", out.Verdict)
	}
}

// ---- Envelope hints ----

func TestEnvelopeHintsNilVsSet(t *testing.T) {
	if got := SelectEnvelope(nil); got.SRS || got.PRD || got.FSM || got.DnD {
		t.Errorf("nil hints should yield minimal envelope, got %+v", got)
	}
	if got := SelectEnvelope(EnvelopeHints{"DnD": true}); !got.DnD {
		t.Errorf("DnD hint should be honored, got %+v", got)
	}
}

// ---- Approach shift resets attempt ----

func TestApproachShiftResetsAttempt(t *testing.T) {
	store := NewStubPersistence()
	nonScopeFail := func(taskID string) orchestrator.Receipt {
		return orchestrator.Receipt{
			OK:         false,
			TaskID:     taskID,
			ReturnCode: 1,
			CommitSHA:  "x",
		}
	}
	worker := newStubWorker(
		nonScopeFail("a"), nonScopeFail("b"), nonScopeFail("c"), // approach 0
		nonScopeFail("d"), nonScopeFail("e"), nonScopeFail("f"), // approach 1
		goodReceipt("g"), // approach 2
	)
	sup := newSelfTestSupervisorForWorker(store, worker)
	sup.Config.MaxAttemptsPerApproach = 3
	sup.Config.MaxApproaches = 3
	sup.RCAFn = func(ctx context.Context, attempts []AttemptRecord) (RCARecord, error) {
		// Force high confidence so approach shift fires.
		return RCARecord{Confidence: 0.9, Symptom: "rotate", RootCause: "fresh angle"}, nil
	}

	res, err := sup.Run(context.Background(), RunRequest{
		TaskDescription: "scan src",
		Role:            "scout",
		Permission:      "read_only",
		Model:           "m1",
		AddDirs:         []string{"./src"},
		SelfTestMode:    true,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Outcome != RunSucceeded {
		t.Fatalf("expected RunSucceeded, got %s verdict=%q attempts=%d approaches=%d", res.Outcome, res.Verdict, res.AttemptCount, res.ApproachCount)
	}
	if res.AttemptCount != 7 {
		t.Errorf("expected 7 attempts (3+3+1), got %d", res.AttemptCount)
	}
	if res.ApproachCount != 3 {
		t.Errorf("expected 3 approaches, got %d", res.ApproachCount)
	}
}

// ---- Metrics persistence ----

func TestMetricsPersistAcrossRestarts(t *testing.T) {
	store := NewStubPersistence()
	worker := newStubWorker(goodReceipt(""))
	// Shared metrics store across both supervisor instances to simulate the
	// supervisor restarting while metrics persist in the underlying DB.
	metricsStore := NewSQLMetricsStore(time.Now)

	sup1 := newSelfTestSupervisorForWorker(store, worker)
	sup1.MetricsStore = metricsStore
	sup1.Config.MaxAttemptsPerApproach = 3
	sup1.Config.MaxApproaches = 3
	res1, err := sup1.Run(context.Background(), RunRequest{
		TaskDescription: "scan src",
		Role:            "scout",
		Permission:      "read_only",
		Model:           "m1",
		AddDirs:         []string{"./src"},
		SelfTestMode:    true,
	})
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if res1.Outcome != RunSucceeded {
		t.Fatalf("expected first run success, got %s", res1.Outcome)
	}

	// Simulate restart: fresh supervisor struct, same persistence + metrics
	// backing store. Verify metrics survive.
	sup2 := newSelfTestSupervisorForWorker(store, worker)
	sup2.MetricsStore = metricsStore
	sup2.Config.MaxAttemptsPerApproach = 3
	sup2.Config.MaxApproaches = 3
	m, err := sup2.MetricsStore.GetMetrics(context.Background(), res1.SupervisorTaskID)
	if err != nil {
		t.Fatalf("get metrics: %v", err)
	}
	if m.AttemptsToSuccess != 1 {
		t.Errorf("expected AttemptsToSuccess=1 after restart, got %d", m.AttemptsToSuccess)
	}
	if m.EnvelopeScore <= 0 {
		t.Errorf("expected positive envelope score, got %f", m.EnvelopeScore)
	}
}

// ---- RCA low confidence pauses ----

func TestRCALowConfidencePauses(t *testing.T) {
	store := NewStubPersistence()
	nonScopeFail := func(taskID string) orchestrator.Receipt {
		return orchestrator.Receipt{
			OK:         false,
			TaskID:     taskID,
			ReturnCode: 1,
			CommitSHA:  "x",
		}
	}
	worker := newStubWorker(nonScopeFail("a"), nonScopeFail("b"), nonScopeFail("c"))

	sup := newSelfTestSupervisorForWorker(store, worker)
	sup.Config.MaxAttemptsPerApproach = 3
	sup.Config.MaxApproaches = 3
	sup.RCAFn = func(ctx context.Context, attempts []AttemptRecord) (RCARecord, error) {
		// Force low confidence → supervisor pauses (NEEDS_INFO) instead of
		// shifting approach or escalating.
		return RCARecord{Confidence: 0.4, Symptom: "low confidence", RootCause: "heuristic"}, nil
	}

	res, err := sup.Run(context.Background(), RunRequest{
		TaskDescription: "scan src",
		Role:            "scout",
		Permission:      "read_only",
		Model:           "m1",
		AddDirs:         []string{"./src"},
		SelfTestMode:    true,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Outcome != RunFailed {
		t.Errorf("expected RunFailed (pause), got %s verdict=%q", res.Outcome, res.Verdict)
	}
	if res.AttemptCount != 3 {
		t.Errorf("expected 3 attempts before pause, got %d", res.AttemptCount)
	}
	if res.ApproachCount != 1 {
		t.Errorf("expected 1 approach, got %d", res.ApproachCount)
	}
	if res.Escalated {
		t.Errorf("expected NOT escalated when pausing for info")
	}
	if res.Escalation != nil {
		t.Errorf("expected nil Escalation on pause, got %+v", res.Escalation)
	}
}

// Helper used in tests; avoids importing fmt at the test site.
func mustContain(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected %q to contain %q", s, substr)
	}
}

var _ = fmt.Sprintf // keep fmt referenced even if no test uses it
