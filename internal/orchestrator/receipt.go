package orchestrator

import "time"

// Receipt is the worker-emitted result plus orchestrator-side metadata.
// Persisted into the controlplane evidence lake by the orchestrator after
// validation.
type Receipt struct {
	OK              bool
	OrchestratorID  string
	WorktreeID      string
	WorkerName      string
	Iter            int
	TaskID          string
	Branch          string
	CommitSHA       string
	ReturnCode      int
	HarnessCode     int
	DurationSeconds float64
	Stdout          string
	Stderr          string
	LastError       string
	RawStdout       []byte
	Violations      []string
	FilesModified   []string
	ScopeViolations []string
	StartedAt       time.Time
	FinishedAt      time.Time
}

// TaskSpec is one slice of the plan: the task description plus metadata
// the orchestrator uses to key receipts, worktree leases, and evidence correlation.
type TaskSpec struct {
	TaskID         string
	SessionID      string
	Prompt         string
	OrchestratorID string
	WorktreeID     string
	WorkerName     string
	Iter           int
	Task           Task
}
