package supervisor

import (
	"time"
)

// SupervisorTask is the typed surface the controlplane's *Store methods
// return. The struct is exported so both controlplane and supervisor can see
// it (no cycle) but type conversions are performed inside the store layer.
type SupervisorTask struct {
	ID           string
	State        string
	EnvelopeJSON string
	ApproachIdx  int
	AttemptIdx   int
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ParentTaskID *string
}

// SupervisorDecision is the auditor's immutable entry.
type SupervisorDecision struct {
	ID          string
	TaskID      string
	Kind        string
	PayloadJSON string
	CreatedAt   time.Time
}
