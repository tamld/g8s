// fsm.go implements the orchestrator lifecycle FSM.
//
// States: PLAN → SPAWN → MONITOR → RECEIPT → MERGE | ESCALATE
// Terminal: MERGE, ESCALATE, CANCEL, CONFLICT — reject further input.
// Cancel is reachable from any non-terminal state.
//
// The FSM is the contract between worker Handle.Wait and receipt
// persistence. Without it, FanOut returns Receipt[] but nothing
// coordinates the next step (merge? escalate? retry?).
package orchestrator

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// State is the FSM state enum.
type State string

const (
	// StatePlan is the initial planning phase where tasks are assembled.
	StatePlan State = "PLAN"
	// StateSpawn is the phase where workers are spawned into worktrees.
	StateSpawn State = "SPAWN"
	// StateMonitor is the phase where spawned workers are actively monitored.
	StateMonitor State = "MONITOR"
	// StateReceipt is the phase where worker receipts are collected and validated.
	StateReceipt State = "RECEIPT"
	// StateMerge is the terminal success state: all receipts OK, ready to merge.
	StateMerge State = "MERGE"
	// StateEscalate is the terminal escalation state: unrecoverable failure.
	StateEscalate State = "ESCALATE"
	// StateCancel is the terminal cancellation state.
	StateCancel State = "CANCEL"
	// StateConflict is the terminal conflict state: merge conflict detected.
	StateConflict State = "CONFLICT"
)

// allStates enumerates every valid state for validation.
var allStates = map[State]struct{}{
	StatePlan: {}, StateSpawn: {}, StateMonitor: {}, StateReceipt: {},
	StateMerge: {}, StateEscalate: {}, StateCancel: {}, StateConflict: {},
}

// terminalStates cannot transition further.
var terminalStates = map[State]struct{}{
	StateMerge: {}, StateEscalate: {}, StateCancel: {}, StateConflict: {},
}

// validTransitions maps each state to its set of valid target states.
// Cancel is allowed from any non-terminal state (added programmatically).
var validTransitions = map[State]map[State]struct{}{
	StatePlan: {
		StateSpawn:  {},
		StateCancel: {},
	},
	StateSpawn: {
		StateMonitor: {},
		StateCancel:  {},
	},
	StateMonitor: {
		StateReceipt: {},
		StateSpawn:   {}, // retry cycle: MONITOR → SPAWN
		StateCancel:  {},
	},
	StateReceipt: {
		StateMerge:    {},
		StateEscalate: {},
		StateConflict: {},
		StateCancel:   {},
	},
	// Terminal states have no outgoing transitions.
	StateMerge:    {},
	StateEscalate: {},
	StateCancel:   {},
	StateConflict: {},
}

// ErrInvalidTransition is returned when a requested state change is not
// allowed by the FSM transition map.
var ErrInvalidTransition = errors.New("invalid FSM transition")

// ErrTerminalState is returned when Next is called on a terminal state.
var ErrTerminalState = errors.New("FSM is in a terminal state")

// Transition records one state change with its timestamp and reason.
type Transition struct {
	From   State
	To     State
	Reason string
	At     time.Time
}

// FSMOption configures an FSM at construction time.
type FSMOption func(*FSM)

// WithClock injects a deterministic clock for testing.
func WithClock(clock func() time.Time) FSMOption {
	return func(f *FSM) {
		f.clock = clock
	}
}

// FSM is a thread-safe finite state machine for the orchestrator lifecycle.
type FSM struct {
	mu      sync.RWMutex
	current State
	history []Transition
	clock   func() time.Time
}

// NewFSM creates an FSM starting in StatePlan.
func NewFSM(opts ...FSMOption) *FSM {
	f := &FSM{
		current: StatePlan,
		clock:   time.Now,
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// Current returns the FSM's current state.
func (f *FSM) Current() State {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.current
}

// Next attempts to transition the FSM to the given target state.
// Returns the new state on success, or ErrInvalidTransition /
// ErrTerminalState on failure.
func (f *FSM) Next(target State, reason string) (State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Validate target is a known state.
	if _, ok := allStates[target]; !ok {
		return f.current, fmt.Errorf("%w: unknown state %q", ErrInvalidTransition, target)
	}

	// Terminal states reject all transitions.
	if _, terminal := terminalStates[f.current]; terminal {
		return f.current, fmt.Errorf("%w: %s is terminal", ErrTerminalState, f.current)
	}

	// Check the transition is in the allowed set.
	allowed, ok := validTransitions[f.current]
	if !ok {
		return f.current, fmt.Errorf("%w: no transitions from %s", ErrInvalidTransition, f.current)
	}
	if _, valid := allowed[target]; !valid {
		return f.current, fmt.Errorf("%w: %s → %s", ErrInvalidTransition, f.current, target)
	}

	t := Transition{
		From:   f.current,
		To:     target,
		Reason: reason,
		At:     f.clock(),
	}
	f.history = append(f.history, t)
	f.current = target
	return f.current, nil
}

// History returns a copy of the transition log.
func (f *FSM) History() []Transition {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]Transition, len(f.history))
	copy(out, f.history)
	return out
}

// IsTerminal reports whether the FSM is in a terminal state.
func (f *FSM) IsTerminal() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	_, terminal := terminalStates[f.current]
	return terminal
}
