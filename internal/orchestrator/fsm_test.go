package orchestrator

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fsmClock returns a clock frozen at a deterministic point for FSM tests.
var fsmClock = func() time.Time { return time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC) }

func TestFSMInitialState(t *testing.T) {
	fsm := NewFSM()
	if got := fsm.Current(); got != StatePlan {
		t.Fatalf("initial state = %q, want PLAN", got)
	}
	if fsm.IsTerminal() {
		t.Fatal("initial state should not be terminal")
	}
	if len(fsm.History()) != 0 {
		t.Fatalf("initial history should be empty, got %d", len(fsm.History()))
	}
}

func TestFSMHappyPath(t *testing.T) {
	// PLAN → SPAWN → MONITOR → RECEIPT → MERGE
	fsm := NewFSM(WithClock(fsmClock))
	steps := []struct {
		target State
		reason string
	}{
		{StateSpawn, "plan validated"},
		{StateMonitor, "workers spawned"},
		{StateReceipt, "all workers finished"},
		{StateMerge, "all receipts OK"},
	}
	for _, step := range steps {
		got, err := fsm.Next(step.target, step.reason)
		if err != nil {
			t.Fatalf("transition to %s: %v", step.target, err)
		}
		if got != step.target {
			t.Fatalf("got %s, want %s", got, step.target)
		}
	}
	if !fsm.IsTerminal() {
		t.Fatal("MERGE should be terminal")
	}
	hist := fsm.History()
	if len(hist) != 4 {
		t.Fatalf("history length = %d, want 4", len(hist))
	}
	// Verify first transition.
	if hist[0].From != StatePlan || hist[0].To != StateSpawn {
		t.Fatalf("hist[0] = %s→%s, want PLAN→SPAWN", hist[0].From, hist[0].To)
	}
}

func TestFSMRetryPath(t *testing.T) {
	// PLAN → SPAWN → MONITOR → SPAWN (retry) → MONITOR → RECEIPT → MERGE
	fsm := NewFSM(WithClock(fsmClock))
	transitions := []State{
		StateSpawn, StateMonitor, StateSpawn, StateMonitor, StateReceipt, StateMerge,
	}
	for _, target := range transitions {
		if _, err := fsm.Next(target, "retry"); err != nil {
			t.Fatalf("transition to %s: %v", target, err)
		}
	}
	if fsm.Current() != StateMerge {
		t.Fatalf("final state = %s, want MERGE", fsm.Current())
	}
	if len(fsm.History()) != 6 {
		t.Fatalf("history length = %d, want 6", len(fsm.History()))
	}
}

func TestFSMEscalationPath(t *testing.T) {
	// PLAN → SPAWN → MONITOR → RECEIPT → ESCALATE
	fsm := NewFSM(WithClock(fsmClock))
	transitions := []State{StateSpawn, StateMonitor, StateReceipt, StateEscalate}
	for _, target := range transitions {
		if _, err := fsm.Next(target, "escalation"); err != nil {
			t.Fatalf("transition to %s: %v", target, err)
		}
	}
	if fsm.Current() != StateEscalate {
		t.Fatalf("final state = %s, want ESCALATE", fsm.Current())
	}
	if !fsm.IsTerminal() {
		t.Fatal("ESCALATE should be terminal")
	}
}

func TestFSMConflictPath(t *testing.T) {
	// PLAN → SPAWN → MONITOR → RECEIPT → CONFLICT
	fsm := NewFSM(WithClock(fsmClock))
	transitions := []State{StateSpawn, StateMonitor, StateReceipt, StateConflict}
	for _, target := range transitions {
		if _, err := fsm.Next(target, "conflict found"); err != nil {
			t.Fatalf("transition to %s: %v", target, err)
		}
	}
	if fsm.Current() != StateConflict {
		t.Fatalf("final state = %s, want CONFLICT", fsm.Current())
	}
	if !fsm.IsTerminal() {
		t.Fatal("CONFLICT should be terminal")
	}
}

func TestFSMCancelFromPlan(t *testing.T) {
	fsm := NewFSM(WithClock(fsmClock))
	if _, err := fsm.Next(StateCancel, "user abort"); err != nil {
		t.Fatalf("cancel from PLAN: %v", err)
	}
	if fsm.Current() != StateCancel {
		t.Fatalf("expected CANCEL, got %s", fsm.Current())
	}
}

func TestFSMCancelFromSpawn(t *testing.T) {
	fsm := NewFSM(WithClock(fsmClock))
	if _, err := fsm.Next(StateSpawn, "start"); err != nil {
		t.Fatal(err)
	}
	if _, err := fsm.Next(StateCancel, "cancelled"); err != nil {
		t.Fatalf("cancel from SPAWN: %v", err)
	}
	if fsm.Current() != StateCancel {
		t.Fatalf("expected CANCEL, got %s", fsm.Current())
	}
}

func TestFSMCancelFromMonitor(t *testing.T) {
	fsm := NewFSM(WithClock(fsmClock))
	must(t, fsm, StateSpawn, "start")
	must(t, fsm, StateMonitor, "monitor")
	if _, err := fsm.Next(StateCancel, "ctx done"); err != nil {
		t.Fatalf("cancel from MONITOR: %v", err)
	}
	if fsm.Current() != StateCancel {
		t.Fatalf("expected CANCEL, got %s", fsm.Current())
	}
}

func TestFSMCancelFromReceipt(t *testing.T) {
	fsm := NewFSM(WithClock(fsmClock))
	must(t, fsm, StateSpawn, "start")
	must(t, fsm, StateMonitor, "monitor")
	must(t, fsm, StateReceipt, "receipt")
	if _, err := fsm.Next(StateCancel, "late cancel"); err != nil {
		t.Fatalf("cancel from RECEIPT: %v", err)
	}
	if fsm.Current() != StateCancel {
		t.Fatalf("expected CANCEL, got %s", fsm.Current())
	}
}

func TestFSMTerminalRejectsInput(t *testing.T) {
	terminals := []State{StateMerge, StateEscalate, StateCancel, StateConflict}

	for _, terminal := range terminals {
		t.Run(string(terminal), func(t *testing.T) {
			fsm := NewFSM(WithClock(fsmClock))
			// Navigate to RECEIPT so any terminal is reachable.
			must(t, fsm, StateSpawn, "start")
			must(t, fsm, StateMonitor, "monitor")
			must(t, fsm, StateReceipt, "receipt")

			switch terminal {
			case StateCancel:
				must(t, fsm, StateCancel, "cancel")
			case StateMerge:
				must(t, fsm, StateMerge, "merge")
			case StateEscalate:
				must(t, fsm, StateEscalate, "escalate")
			case StateConflict:
				must(t, fsm, StateConflict, "conflict")
			}

			// Now try every possible target from terminal.
			for target := range allStates {
				_, err := fsm.Next(target, "should fail")
				if err == nil {
					t.Errorf("%s → %s: expected error, got nil", terminal, target)
				}
				if !errors.Is(err, ErrTerminalState) {
					t.Errorf("%s → %s: expected ErrTerminalState, got %v", terminal, target, err)
				}
			}
		})
	}
}

func TestFSMInvalidTransition(t *testing.T) {
	cases := []struct {
		name  string
		from  State
		to    State
		setup []State
	}{
		{"PLAN→MONITOR", StatePlan, StateMonitor, nil},
		{"PLAN→RECEIPT", StatePlan, StateReceipt, nil},
		{"PLAN→MERGE", StatePlan, StateMerge, nil},
		{"PLAN→ESCALATE", StatePlan, StateEscalate, nil},
		{"PLAN→CONFLICT", StatePlan, StateConflict, nil},
		{"SPAWN→RECEIPT", StateSpawn, StateReceipt, []State{StateSpawn}},
		{"SPAWN→MERGE", StateSpawn, StateMerge, []State{StateSpawn}},
		{"SPAWN→PLAN", StateSpawn, StatePlan, []State{StateSpawn}},
		{"MONITOR→MERGE", StateMonitor, StateMerge, []State{StateSpawn, StateMonitor}},
		{"MONITOR→PLAN", StateMonitor, StatePlan, []State{StateSpawn, StateMonitor}},
		{"RECEIPT→SPAWN", StateReceipt, StateSpawn, []State{StateSpawn, StateMonitor, StateReceipt}},
		{"RECEIPT→MONITOR", StateReceipt, StateMonitor, []State{StateSpawn, StateMonitor, StateReceipt}},
		{"RECEIPT→PLAN", StateReceipt, StatePlan, []State{StateSpawn, StateMonitor, StateReceipt}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fsm := NewFSM(WithClock(fsmClock))
			for _, s := range tc.setup {
				must(t, fsm, s, "setup")
			}
			_, err := fsm.Next(tc.to, "should fail")
			if err == nil {
				t.Fatalf("expected error for %s → %s", tc.from, tc.to)
			}
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("expected ErrInvalidTransition, got %v", err)
			}
		})
	}
}

func TestFSMUnknownState(t *testing.T) {
	fsm := NewFSM(WithClock(fsmClock))
	_, err := fsm.Next(State("BOGUS"), "nope")
	if err == nil || !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition for unknown state, got %v", err)
	}
}

func TestFSMHistoryTimestamps(t *testing.T) {
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var calls int
	clock := func() time.Time {
		calls++
		return epoch.Add(time.Duration(calls) * time.Second)
	}
	fsm := NewFSM(WithClock(clock))
	must(t, fsm, StateSpawn, "s")
	must(t, fsm, StateMonitor, "m")

	hist := fsm.History()
	if len(hist) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(hist))
	}
	if !hist[0].At.Before(hist[1].At) {
		t.Fatalf("timestamps should be monotonic: %v >= %v", hist[0].At, hist[1].At)
	}
}

func TestFSMHistoryIsACopy(t *testing.T) {
	fsm := NewFSM(WithClock(fsmClock))
	must(t, fsm, StateSpawn, "start")

	h1 := fsm.History()
	h1[0].Reason = "mutated"

	h2 := fsm.History()
	if h2[0].Reason == "mutated" {
		t.Fatal("History should return a defensive copy")
	}
}

func TestFSMConcurrentAccess(t *testing.T) {
	fsm := NewFSM(WithClock(fsmClock))

	var wg sync.WaitGroup
	const goroutines = 50

	// Hammer Current() concurrently while one goroutine transitions.
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_ = fsm.Current()
			_ = fsm.History()
			_ = fsm.IsTerminal()
		}()
	}
	// Transition should not race with reads.
	_, _ = fsm.Next(StateSpawn, "concurrent")
	wg.Wait()
}

func TestFSMWithDefaultClock(t *testing.T) {
	fsm := NewFSM() // no clock option — uses time.Now
	before := time.Now()
	must(t, fsm, StateSpawn, "default clock")
	after := time.Now()

	hist := fsm.History()
	if hist[0].At.Before(before) || hist[0].At.After(after) {
		t.Fatalf("default clock timestamp %v not between %v and %v", hist[0].At, before, after)
	}
}

// must is a test helper that transitions the FSM or fails the test.
func must(t *testing.T, fsm *FSM, target State, reason string) {
	t.Helper()
	if _, err := fsm.Next(target, reason); err != nil {
		t.Fatalf("transition to %s: %v", target, err)
	}
}
