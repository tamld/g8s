// Package state implements pure FSM transition validation and append-only event logging
// across all g8s lifecycle domains per DEBT-31.
package state

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Subject identifies the domain or entity type managed by an FSM.
type Subject string

// State represents an FSM state string.
type State string

// Event represents an event triggering an FSM transition.
type Event string

// Domain subjects.
const (
	SubjectTask         Subject = "task"
	SubjectOrchestrator Subject = "orchestrator"
	SubjectBrief        Subject = "brief"
	SubjectHeartbeat    Subject = "heartbeat"
	SubjectWorktree     Subject = "worktree"
)

// Task states.
const (
	TaskStateQueued    State = "QUEUED"
	TaskStateLeased    State = "LEASED"
	TaskStateRunning   State = "RUNNING"
	TaskStateNeedsInfo State = "NEEDS_INFO"
	TaskStateBlocked   State = "BLOCKED"
	TaskStateSucceeded State = "SUCCEEDED"
	TaskStateFailed    State = "FAILED"
	TaskStateCancelled State = "CANCELLED"
)

// Task events.
const (
	TaskEventClaim       Event = "claim"
	TaskEventStart       Event = "start"
	TaskEventNeedsInfo   Event = "needs_info"
	TaskEventProvideInfo Event = "provide_info"
	TaskEventBlock       Event = "block"
	TaskEventUnblock     Event = "unblock"
	TaskEventSucceed     Event = "succeed"
	TaskEventFail        Event = "fail"
	TaskEventCancel      Event = "cancel"
	TaskEventRequeue     Event = "requeue"
)

// Orchestrator states.
const (
	OrchestratorStatePlan     State = "PLAN"
	OrchestratorStateSpawn    State = "SPAWN"
	OrchestratorStateMonitor  State = "MONITOR"
	OrchestratorStateReceipt  State = "RECEIPT"
	OrchestratorStateMerge    State = "MERGE"
	OrchestratorStateEscalate State = "ESCALATE"
	OrchestratorStateCancel   State = "CANCEL"
	OrchestratorStateConflict State = "CONFLICT"
)

// Orchestrator events.
const (
	OrchestratorEventSpawn    Event = "spawn"
	OrchestratorEventMonitor  Event = "monitor"
	OrchestratorEventReceipt  Event = "receipt"
	OrchestratorEventRetry    Event = "retry"
	OrchestratorEventMerge    Event = "merge"
	OrchestratorEventEscalate Event = "escalate"
	OrchestratorEventConflict Event = "conflict"
	OrchestratorEventCancel   Event = "cancel"
)

// Brief states.
const (
	BriefStateActive   State = "active"
	BriefStateConsumed State = "consumed"
	BriefStateExpired  State = "expired"
)

// Brief events.
const (
	BriefEventConsume Event = "consume"
	BriefEventExpire  Event = "expire"
)

// Heartbeat states.
const (
	HeartbeatStateRunning  State = "running"
	HeartbeatStateIdle     State = "idle"
	HeartbeatStateFinished State = "finished"
	HeartbeatStateFailed   State = "failed"
)

// Heartbeat events.
const (
	HeartbeatEventPause  Event = "pause"
	HeartbeatEventResume Event = "resume"
	HeartbeatEventFinish Event = "finish"
	HeartbeatEventFail   Event = "fail"
)

// Transition defines a valid state transition rule for a Subject.
type Transition struct {
	Subject   Subject
	From, To  State
	Event     Event
	Predicate func(any) error
}

// Errors returned by transition validation.
var (
	ErrInvalidTransition = errors.New("invalid state transition")
	ErrPredicateFailed   = errors.New("transition predicate failed")
	ErrSubjectNotFound   = errors.New("unknown subject in state registry")
)

var (
	registryMu sync.RWMutex
	// Registry maps each Subject to its registered Transitions.
	Registry = map[Subject][]Transition{}
)

func init() {
	registerDefaultTransitions()
}

// Register adds one or more transitions to the global Registry in a thread-safe manner.
func Register(transitions ...Transition) {
	registryMu.Lock()
	defer registryMu.Unlock()
	for _, t := range transitions {
		Registry[t.Subject] = append(Registry[t.Subject], t)
	}
}

// ResetRegistry clears and reloads default transitions (primarily for testing).
func ResetRegistry() {
	registryMu.Lock()
	Registry = map[Subject][]Transition{}
	registryMu.Unlock()
	registerDefaultTransitions()
}

// Apply is a pure transition validator. It finds the matching transition for the given
// subject, source state, and event. If found and the optional Predicate succeeds,
// it returns the target state (To). Otherwise, it returns an error with no side effects.
func Apply(s Subject, from State, event Event, data any, at time.Time) (State, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	transitions, ok := Registry[s]
	if !ok {
		return from, fmt.Errorf("%w: %s", ErrSubjectNotFound, s)
	}

	for _, t := range transitions {
		if t.From == from && t.Event == event {
			if t.Predicate != nil {
				if err := t.Predicate(data); err != nil {
					return from, fmt.Errorf("%w: %s: %w", ErrPredicateFailed, s, err)
				}
			}
			return t.To, nil
		}
	}

	return from, fmt.Errorf("%w: [%s] %s --(%s)--> ?", ErrInvalidTransition, s, from, event)
}

// ValidTransitions returns a slice of valid transitions for the subject and starting state.
func ValidTransitions(s Subject, from State) []Transition {
	registryMu.RLock()
	defer registryMu.RUnlock()

	var out []Transition
	for _, t := range Registry[s] {
		if t.From == from {
			out = append(out, t)
		}
	}
	return out
}

func registerDefaultTransitions() {
	// Task transitions
	Register(
		Transition{Subject: SubjectTask, From: TaskStateQueued, To: TaskStateLeased, Event: TaskEventClaim},
		Transition{Subject: SubjectTask, From: TaskStateLeased, To: TaskStateRunning, Event: TaskEventStart},
		Transition{Subject: SubjectTask, From: TaskStateRunning, To: TaskStateNeedsInfo, Event: TaskEventNeedsInfo},
		Transition{Subject: SubjectTask, From: TaskStateNeedsInfo, To: TaskStateRunning, Event: TaskEventProvideInfo},
		Transition{Subject: SubjectTask, From: TaskStateNeedsInfo, To: TaskStateQueued, Event: TaskEventProvideInfo},
		Transition{Subject: SubjectTask, From: TaskStateRunning, To: TaskStateBlocked, Event: TaskEventBlock},
		Transition{Subject: SubjectTask, From: TaskStateBlocked, To: TaskStateRunning, Event: TaskEventUnblock},
		Transition{Subject: SubjectTask, From: TaskStateBlocked, To: TaskStateQueued, Event: TaskEventUnblock},
		Transition{Subject: SubjectTask, From: TaskStateRunning, To: TaskStateSucceeded, Event: TaskEventSucceed},
		Transition{Subject: SubjectTask, From: TaskStateRunning, To: TaskStateFailed, Event: TaskEventFail},
		Transition{Subject: SubjectTask, From: TaskStateLeased, To: TaskStateFailed, Event: TaskEventFail},
		Transition{Subject: SubjectTask, From: TaskStateQueued, To: TaskStateFailed, Event: TaskEventFail},
		Transition{Subject: SubjectTask, From: TaskStateNeedsInfo, To: TaskStateFailed, Event: TaskEventFail},
		Transition{Subject: SubjectTask, From: TaskStateBlocked, To: TaskStateFailed, Event: TaskEventFail},
		Transition{Subject: SubjectTask, From: TaskStateQueued, To: TaskStateCancelled, Event: TaskEventCancel},
		Transition{Subject: SubjectTask, From: TaskStateLeased, To: TaskStateCancelled, Event: TaskEventCancel},
		Transition{Subject: SubjectTask, From: TaskStateRunning, To: TaskStateCancelled, Event: TaskEventCancel},
		Transition{Subject: SubjectTask, From: TaskStateNeedsInfo, To: TaskStateCancelled, Event: TaskEventCancel},
		Transition{Subject: SubjectTask, From: TaskStateBlocked, To: TaskStateCancelled, Event: TaskEventCancel},
		Transition{Subject: SubjectTask, From: TaskStateLeased, To: TaskStateQueued, Event: TaskEventRequeue},
		Transition{Subject: SubjectTask, From: TaskStateRunning, To: TaskStateQueued, Event: TaskEventRequeue},
	)

	// Orchestrator transitions
	Register(
		Transition{Subject: SubjectOrchestrator, From: OrchestratorStatePlan, To: OrchestratorStateSpawn, Event: OrchestratorEventSpawn},
		Transition{Subject: SubjectOrchestrator, From: OrchestratorStateSpawn, To: OrchestratorStateMonitor, Event: OrchestratorEventMonitor},
		Transition{Subject: SubjectOrchestrator, From: OrchestratorStateMonitor, To: OrchestratorStateReceipt, Event: OrchestratorEventReceipt},
		Transition{Subject: SubjectOrchestrator, From: OrchestratorStateMonitor, To: OrchestratorStateSpawn, Event: OrchestratorEventRetry},
		Transition{Subject: SubjectOrchestrator, From: OrchestratorStateReceipt, To: OrchestratorStateMerge, Event: OrchestratorEventMerge},
		Transition{Subject: SubjectOrchestrator, From: OrchestratorStateReceipt, To: OrchestratorStateEscalate, Event: OrchestratorEventEscalate},
		Transition{Subject: SubjectOrchestrator, From: OrchestratorStateReceipt, To: OrchestratorStateConflict, Event: OrchestratorEventConflict},
		Transition{Subject: SubjectOrchestrator, From: OrchestratorStatePlan, To: OrchestratorStateCancel, Event: OrchestratorEventCancel},
		Transition{Subject: SubjectOrchestrator, From: OrchestratorStateSpawn, To: OrchestratorStateCancel, Event: OrchestratorEventCancel},
		Transition{Subject: SubjectOrchestrator, From: OrchestratorStateMonitor, To: OrchestratorStateCancel, Event: OrchestratorEventCancel},
		Transition{Subject: SubjectOrchestrator, From: OrchestratorStateReceipt, To: OrchestratorStateCancel, Event: OrchestratorEventCancel},
	)

	// Brief transitions
	Register(
		Transition{Subject: SubjectBrief, From: BriefStateActive, To: BriefStateConsumed, Event: BriefEventConsume},
		Transition{Subject: SubjectBrief, From: BriefStateActive, To: BriefStateExpired, Event: BriefEventExpire},
	)

	// Heartbeat transitions
	Register(
		Transition{Subject: SubjectHeartbeat, From: HeartbeatStateRunning, To: HeartbeatStateIdle, Event: HeartbeatEventPause},
		Transition{Subject: SubjectHeartbeat, From: HeartbeatStateIdle, To: HeartbeatStateRunning, Event: HeartbeatEventResume},
		Transition{Subject: SubjectHeartbeat, From: HeartbeatStateRunning, To: HeartbeatStateFinished, Event: HeartbeatEventFinish},
		Transition{Subject: SubjectHeartbeat, From: HeartbeatStateIdle, To: HeartbeatStateFinished, Event: HeartbeatEventFinish},
		Transition{Subject: SubjectHeartbeat, From: HeartbeatStateRunning, To: HeartbeatStateFailed, Event: HeartbeatEventFail},
		Transition{Subject: SubjectHeartbeat, From: HeartbeatStateIdle, To: HeartbeatStateFailed, Event: HeartbeatEventFail},
	)
}
