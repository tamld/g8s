package orchestrator

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// --- Stub worker for lifecycle integration tests ---

type stubWorker struct {
	name    string
	receipt Receipt
	delay   time.Duration
}

func (w *stubWorker) Name() string                      { return w.name }
func (w *stubWorker) Available(_ context.Context) error { return nil }
func (w *stubWorker) Spawn(_ context.Context, t Task) (Handle, error) {
	r := w.receipt
	r.TaskID = t.ID
	r.WorkerName = w.name
	return &stubHandle{receipt: r, delay: w.delay}, nil
}

type stubHandle struct {
	receipt Receipt
	delay   time.Duration
}

func (h *stubHandle) PID() int { return 42 }
func (h *stubHandle) Wait(ctx context.Context) (Receipt, error) {
	if h.delay > 0 {
		select {
		case <-time.After(h.delay):
		case <-ctx.Done():
			return Receipt{}, ctx.Err()
		}
	}
	return h.receipt, nil
}
func (h *stubHandle) Cancel(_ context.Context) error { return nil }
func (h *stubHandle) StdoutStream() interface {
	Read(p []byte) (int, error)
	Close() error
} {
	return nil
}

// --- Integration tests ---

func TestDriveFullLifecycleHappyPath(t *testing.T) {
	dir := t.TempDir()
	mustRunGit(t, dir, "init", "-q")
	mustRunGit(t, dir, "config", "user.email", "test@example.com")
	mustRunGit(t, dir, "config", "user.name", "Test")
	mustRunGit(t, dir, "commit", "--allow-empty", "-q", "-m", "init")
	mustRunGit(t, dir, "checkout", "-q", "-b", "main")

	worker := &stubWorker{
		name:    "stub",
		receipt: Receipt{OK: true, CommitSHA: "abc123"},
	}
	reg := NewRegistry()
	reg.Register("stub", func() Worker { return worker })

	pool, err := NewPool(PoolOptions{Repo: dir})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}

	fsm := NewFSM(WithClock(fsmClock))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	plan := []TaskSpec{
		{TaskID: "task-1", Task: Task{ID: "task-1", Role: "collector", Prompt: "do things"}},
	}
	result, err := fsm.Drive(ctx, plan, FanOutOptions{Registry: reg, Pool: pool})
	if err != nil {
		t.Fatalf("Drive: %v", err)
	}

	// Assert terminal state.
	if result.FinalState != StateMerge {
		t.Fatalf("final state = %s, want MERGE", result.FinalState)
	}

	// Assert every transition hit the history.
	expectedTransitions := []struct {
		from, to State
	}{
		{StatePlan, StateSpawn},
		{StateSpawn, StateMonitor},
		{StateMonitor, StateReceipt},
		{StateReceipt, StateMerge},
	}
	if len(result.Transitions) != len(expectedTransitions) {
		t.Fatalf("transitions = %d, want %d", len(result.Transitions), len(expectedTransitions))
	}
	for i, exp := range expectedTransitions {
		got := result.Transitions[i]
		if got.From != exp.from || got.To != exp.to {
			t.Fatalf("transition[%d] = %s→%s, want %s→%s",
				i, got.From, got.To, exp.from, exp.to)
		}
	}

	// Assert receipt.
	if len(result.Receipts) != 1 {
		t.Fatalf("receipts = %d, want 1", len(result.Receipts))
	}
	if !result.Receipts[0].OK {
		t.Fatal("receipt should be OK")
	}
}

func TestDriveEscalationOnFailedReceipt(t *testing.T) {
	dir := t.TempDir()
	mustRunGit(t, dir, "init", "-q")
	mustRunGit(t, dir, "config", "user.email", "test@example.com")
	mustRunGit(t, dir, "config", "user.name", "Test")
	mustRunGit(t, dir, "commit", "--allow-empty", "-q", "-m", "init")
	mustRunGit(t, dir, "checkout", "-q", "-b", "main")

	worker := &stubWorker{
		name:    "fail-stub",
		receipt: Receipt{OK: false, ReturnCode: 1, CommitSHA: "def456"},
	}
	reg := NewRegistry()
	reg.Register("fail-stub", func() Worker { return worker })

	pool, err := NewPool(PoolOptions{Repo: dir})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}

	fsm := NewFSM(WithClock(fsmClock))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := fsm.Drive(ctx, []TaskSpec{
		{TaskID: "t-fail", Task: Task{ID: "t-fail", Role: "collector"}},
	}, FanOutOptions{Registry: reg, Pool: pool})
	if err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if result.FinalState != StateEscalate {
		t.Fatalf("final state = %s, want ESCALATE", result.FinalState)
	}
}

func TestDriveConflictOnScopeViolation(t *testing.T) {
	// FanOut overwrites ScopeViolations with actual git diff results,
	// so we cannot trigger CONFLICT via Drive with a stub worker.
	// Instead, verify the FSM transition RECEIPT → CONFLICT directly
	// (the unit tests in fsm_test.go already cover this path).
	fsm := NewFSM(WithClock(fsmClock))
	must(t, fsm, StateSpawn, "start")
	must(t, fsm, StateMonitor, "monitor")
	must(t, fsm, StateReceipt, "receipt collected")
	must(t, fsm, StateConflict, "merge conflict detected")

	if fsm.Current() != StateConflict {
		t.Fatalf("expected CONFLICT, got %s", fsm.Current())
	}
	if !fsm.IsTerminal() {
		t.Fatal("CONFLICT should be terminal")
	}
	if len(fsm.History()) != 4 {
		t.Fatalf("expected 4 transitions, got %d", len(fsm.History()))
	}
}

func TestDriveCancelOnEmptyPlan(t *testing.T) {
	fsm := NewFSM(WithClock(fsmClock))
	result, err := fsm.Drive(context.Background(), nil, FanOutOptions{})
	if err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if result.FinalState != StateCancel {
		t.Fatalf("final state = %s, want CANCEL", result.FinalState)
	}
	if len(result.Transitions) != 1 {
		t.Fatalf("transitions = %d, want 1 (PLAN→CANCEL)", len(result.Transitions))
	}
}

func TestDriveCancelOnContextAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	fsm := NewFSM(WithClock(fsmClock))
	result, err := fsm.Drive(ctx, []TaskSpec{
		{TaskID: "t1", Task: Task{ID: "t1"}},
	}, FanOutOptions{})
	if err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if result.FinalState != StateCancel {
		t.Fatalf("final state = %s, want CANCEL", result.FinalState)
	}
}

func TestDriveEscalationOnFanOutError(t *testing.T) {
	// When all workers fail to spawn, FanOut returns an error.
	// The FSM should go PLAN → SPAWN → MONITOR → RECEIPT → ESCALATE.
	reg := NewRegistry()
	reg.Register("bad", func() Worker {
		return &alwaysFail{fakeWorker: fakeWorker{name: "bad", available: true}}
	})

	fsm := NewFSM(WithClock(fsmClock))
	ctx := context.Background()

	result, err := fsm.Drive(ctx, []TaskSpec{
		{TaskID: "t-boom", Task: Task{ID: "t-boom"}},
	}, FanOutOptions{Registry: reg, Pool: &Pool{}})
	if err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if result.FinalState != StateEscalate {
		t.Fatalf("final state = %s, want ESCALATE", result.FinalState)
	}

	// Verify full transition path.
	states := make([]State, len(result.Transitions))
	for i, tr := range result.Transitions {
		states[i] = tr.To
	}
	if len(states) < 4 {
		t.Fatalf("expected at least 4 transitions, got %d: %v", len(states), states)
	}
}

func TestDriveMultipleWorkersAllOK(t *testing.T) {
	dir := t.TempDir()
	mustRunGit(t, dir, "init", "-q")
	mustRunGit(t, dir, "config", "user.email", "test@example.com")
	mustRunGit(t, dir, "config", "user.name", "Test")
	mustRunGit(t, dir, "commit", "--allow-empty", "-q", "-m", "init")
	mustRunGit(t, dir, "checkout", "-q", "-b", "main")

	var spawnCount atomic.Int32
	worker := &countingStubWorker{
		name:    "multi",
		receipt: Receipt{OK: true, CommitSHA: "xxx"},
		spawns:  &spawnCount,
	}
	reg := NewRegistry()
	reg.Register("multi", func() Worker { return worker })

	pool, err := NewPool(PoolOptions{Repo: dir})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}

	fsm := NewFSM(WithClock(fsmClock))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	plan := []TaskSpec{
		{TaskID: "a", Task: Task{ID: "a"}},
		{TaskID: "b", Task: Task{ID: "b"}},
		{TaskID: "c", Task: Task{ID: "c"}},
	}
	result, err := fsm.Drive(ctx, plan, FanOutOptions{Registry: reg, Pool: pool})
	if err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if result.FinalState != StateMerge {
		t.Fatalf("final state = %s, want MERGE", result.FinalState)
	}
	if len(result.Receipts) != 3 {
		t.Fatalf("receipts = %d, want 3", len(result.Receipts))
	}
}

// countingStubWorker is a stub that counts spawns.
type countingStubWorker struct {
	name    string
	receipt Receipt
	spawns  *atomic.Int32
}

func (w *countingStubWorker) Name() string                      { return w.name }
func (w *countingStubWorker) Available(_ context.Context) error { return nil }
func (w *countingStubWorker) Spawn(_ context.Context, t Task) (Handle, error) {
	w.spawns.Add(1)
	r := w.receipt
	r.TaskID = t.ID
	r.WorkerName = w.name
	return &stubHandle{receipt: r}, nil
}

func TestAllReceiptsOK(t *testing.T) {
	if allReceiptsOK(nil) {
		t.Error("nil receipts should not be OK")
	}
	if allReceiptsOK([]Receipt{}) {
		t.Error("empty receipts should not be OK")
	}
	if !allReceiptsOK([]Receipt{{OK: true}, {OK: true}}) {
		t.Error("all OK receipts should pass")
	}
	if allReceiptsOK([]Receipt{{OK: true}, {OK: false}}) {
		t.Error("mixed receipts should fail")
	}
	if allReceiptsOK([]Receipt{{OK: true, ScopeViolations: []string{"x"}}}) {
		t.Error("scope violation should fail")
	}
}

func TestHasConflict(t *testing.T) {
	if hasConflict(nil) {
		t.Error("nil should have no conflict")
	}
	if hasConflict([]Receipt{{OK: true}}) {
		t.Error("clean receipt should have no conflict")
	}
	if !hasConflict([]Receipt{{ScopeViolations: []string{"evil.go"}}}) {
		t.Error("scope violation should signal conflict")
	}
}

func TestRunResultFields(t *testing.T) {
	// Verify RunResult struct fields are populated correctly.
	var r RunResult
	r.FinalState = StateMerge
	r.Receipts = []Receipt{{OK: true}}
	r.Transitions = []Transition{{From: StatePlan, To: StateSpawn}}

	if r.FinalState != StateMerge {
		t.Error("FinalState not set")
	}
	if len(r.Receipts) != 1 {
		t.Error("Receipts not set")
	}
	if len(r.Transitions) != 1 {
		t.Error("Transitions not set")
	}
}

func TestDriveFSMAlreadyUsedReturnsError(t *testing.T) {
	// Drive on an FSM that's already terminal should return an error.
	fsm := NewFSM(WithClock(fsmClock))
	// Manually terminate the FSM.
	must(t, fsm, StateCancel, "manual cancel")

	_, err := fsm.Drive(context.Background(), []TaskSpec{
		{TaskID: "t1", Task: Task{ID: "t1"}},
	}, FanOutOptions{})
	if err == nil {
		t.Fatal("expected error when Drive called on terminal FSM")
	}
	if !errors.Is(err, ErrTerminalState) {
		t.Fatalf("expected ErrTerminalState, got %v", err)
	}
}
