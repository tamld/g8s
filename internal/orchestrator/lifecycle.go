// lifecycle.go wires the FSM to FanOut, driving the full orchestrator
// lifecycle: PLAN → SPAWN → MONITOR → RECEIPT → MERGE | ESCALATE.
//
// Drive is the top-level entry point: it accepts a plan of TaskSpecs,
// runs them through FanOut, collects receipts, and terminates the FSM
// in MERGE (all OK), ESCALATE (any failure), or CANCEL (context cancelled).
package orchestrator

import (
	"context"
	"fmt"
)

// RunResult is the orchestrator-level outcome of Drive. It wraps the
// receipts and FSM terminal state without coupling to supervisor types.
type RunResult struct {
	// FinalState is the terminal FSM state (MERGE, ESCALATE, CANCEL, CONFLICT).
	FinalState State
	// Receipts are the per-worker receipts collected during the run.
	Receipts []Receipt
	// Transitions is a copy of the FSM history at completion.
	Transitions []Transition
}

// Drive runs the full orchestrator lifecycle for a plan of tasks:
//
//  1. PLAN: validate input and prepare.
//  2. SPAWN → MONITOR: FanOut spawns workers and waits for receipts.
//  3. RECEIPT: collect and evaluate receipts.
//  4. Terminal: MERGE (all OK), ESCALATE (any failure), CANCEL (ctx cancelled).
//
// If ctx is cancelled at any point, the FSM transitions to CANCEL.
// Drive never returns an error for normal terminal states; errors are
// reserved for infrastructure failures (nil registry, nil pool, etc.).
func (f *FSM) Drive(ctx context.Context, plan []TaskSpec, opts FanOutOptions) (RunResult, error) {
	result := RunResult{}

	// --- PLAN phase ---
	// FSM starts in StatePlan. Validate inputs.
	if len(plan) == 0 {
		if _, err := f.Next(StateCancel, "empty plan"); err != nil {
			return result, fmt.Errorf("fsm: cancel on empty plan: %w", err)
		}
		result.FinalState = f.Current()
		result.Transitions = f.History()
		return result, nil
	}

	// Check context before proceeding.
	if err := ctx.Err(); err != nil {
		if _, ferr := f.Next(StateCancel, "context cancelled before spawn"); ferr != nil {
			return result, fmt.Errorf("fsm: cancel: %w", ferr)
		}
		result.FinalState = f.Current()
		result.Transitions = f.History()
		return result, nil
	}

	// --- SPAWN phase ---
	if _, err := f.Next(StateSpawn, "plan validated; spawning workers"); err != nil {
		return result, fmt.Errorf("fsm: plan→spawn: %w", err)
	}

	// --- MONITOR phase ---
	if _, err := f.Next(StateMonitor, "workers spawned; monitoring"); err != nil {
		return result, fmt.Errorf("fsm: spawn→monitor: %w", err)
	}

	// Execute FanOut (blocks until all workers finish or ctx fires).
	receipts, fanoutErr := FanOut(ctx, plan, opts)

	// Check for context cancellation.
	if ctx.Err() != nil {
		if _, ferr := f.Next(StateCancel, "context cancelled during monitor"); ferr != nil {
			return result, fmt.Errorf("fsm: cancel during monitor: %w", ferr)
		}
		result.FinalState = f.Current()
		result.Receipts = receipts
		result.Transitions = f.History()
		return result, nil
	}

	// --- RECEIPT phase ---
	if _, err := f.Next(StateReceipt, "fan-out complete; evaluating receipts"); err != nil {
		return result, fmt.Errorf("fsm: monitor→receipt: %w", err)
	}
	result.Receipts = receipts

	// Determine terminal state based on receipts.
	if fanoutErr != nil {
		// All workers failed to spawn.
		if _, err := f.Next(StateEscalate, fmt.Sprintf("fan-out error: %v", fanoutErr)); err != nil {
			return result, fmt.Errorf("fsm: receipt→escalate: %w", err)
		}
		result.FinalState = f.Current()
		result.Transitions = f.History()
		return result, nil
	}

	if allReceiptsOK(receipts) {
		if _, err := f.Next(StateMerge, "all receipts OK"); err != nil {
			return result, fmt.Errorf("fsm: receipt→merge: %w", err)
		}
	} else if hasConflict(receipts) {
		if _, err := f.Next(StateConflict, "merge conflict detected"); err != nil {
			return result, fmt.Errorf("fsm: receipt→conflict: %w", err)
		}
	} else {
		if _, err := f.Next(StateEscalate, "one or more receipts failed"); err != nil {
			return result, fmt.Errorf("fsm: receipt→escalate: %w", err)
		}
	}

	result.FinalState = f.Current()
	result.Transitions = f.History()
	return result, nil
}

// allReceiptsOK reports true if every receipt has OK=true and no scope violations.
func allReceiptsOK(receipts []Receipt) bool {
	if len(receipts) == 0 {
		return false
	}
	for _, r := range receipts {
		if !r.OK || len(r.ScopeViolations) > 0 {
			return false
		}
	}
	return true
}

// hasConflict reports true if any receipt has scope violations (a proxy
// for merge conflicts at the orchestrator layer).
func hasConflict(receipts []Receipt) bool {
	for _, r := range receipts {
		if len(r.ScopeViolations) > 0 {
			return true
		}
	}
	return false
}
