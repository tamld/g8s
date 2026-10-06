// Package ladder implements the quality-ladder failure classification, policy escalation, and telemetry gauges.
package ladder

import (
	"errors"
	"fmt"
)

// Maximum rungs allowed per task lineage (the hard ceiling per rails Q2 / DoD 4).
const MaxLadderRungs = 6

// Common typed errors for ladder progression and refusal.
var (
	// ErrCeilingReached indicates that the ladder has reached the hard ceiling of 6 rungs.
	ErrCeilingReached = errors.New("ladder ceiling reached: hard limit of 6 rungs per task")

	// ErrTokenBudgetExceeded indicates that cumulative lineage tokens exceed the class token budget.
	ErrTokenBudgetExceeded = errors.New("ladder token budget exceeded")

	// ErrNonEffortShape indicates that the failure is not effort-shaped and was routed to HITL immediately.
	ErrNonEffortShape = errors.New("non-effort failure shape: routed to HITL immediately")

	// ErrTaskSucceeded indicates that the task has already passed its checks.
	ErrTaskSucceeded = errors.New("task already succeeded: checks passed")

	// ErrNoRootTask indicates that the task lineage root could not be determined.
	ErrNoRootTask = errors.New("task lineage root not found")
)

// CeilingExceededError provides rich details when the 6-rung ceiling is reached.
type CeilingExceededError struct {
	TaskID    string
	RungCount int
}

func (e *CeilingExceededError) Error() string {
	return fmt.Sprintf("task %s reached ladder ceiling (%d rungs): HITL mandatory", e.TaskID, e.RungCount)
}

func (e *CeilingExceededError) Unwrap() error {
	return ErrCeilingReached
}

// TokenBudgetExceededError provides rich details when the token budget is exhausted.
type TokenBudgetExceededError struct {
	TaskID      string
	Class       string
	TokensUsed  int
	TokenBudget int
}

func (e *TokenBudgetExceededError) Error() string {
	return fmt.Sprintf("task %s (class %s) token budget exceeded: used %d tokens (budget %d): HITL early",
		e.TaskID, e.Class, e.TokensUsed, e.TokenBudget)
}

func (e *TokenBudgetExceededError) Unwrap() error {
	return ErrTokenBudgetExceeded
}

// NonEffortShapeError provides rich details when a failure is non-effort shaped.
type NonEffortShapeError struct {
	TaskID string
	Shape  FailureShape
	Reason string
}

func (e *NonEffortShapeError) Error() string {
	return fmt.Sprintf("task %s has %s failure: %s (routed to HITL immediately)", e.TaskID, e.Shape, e.Reason)
}

func (e *NonEffortShapeError) Unwrap() error {
	return ErrNonEffortShape
}
