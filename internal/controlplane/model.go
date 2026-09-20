// Package controlplane implements the DELTA-03 SQLite-backed task queue with
// compare-and-swap leases and an append-only event log, ported from the Python
// baseline (reference/python/scripts/agy_control_plane.py) under the g8s
// Zero-CGO constitution: modernc.org/sqlite only, injectable deterministic
// clock, and per-operation connection pragmas.
//
// Schema ownership decision (sprint log D2): this package owns the v3
// tasks / task_events / control_plane_maintenance tables AND the v4
// supervisor_tasks / supervisor_decisions / supervisor_metrics tables
// (added in WU3). Write receipts live in internal/receipt and are wired
// in by higher layers, never duplicated here.
package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/state"
)

// SchemaVersion is the control-plane schema generation written into
// PRAGMA user_version after initialization or migration.
//
// Schema history:
//
//	v3 (WU2 baseline): tasks / task_events / control_plane_maintenance.
//	v4 (WU3 supervisor migration): adds supervisor_tasks / supervisor_decisions
//	    / supervisor_metrics for the internal/supervisor Concern A persistence
//	    layer. All v3 tables are untouched.
//	v5 (DELTA-17 receipt lake wiring): adds orchestrator_id, worktree_id,
//	    worker_name, iter columns and idx_tasks_orchestrator_iter index to
//	    tasks table for evidence lake correlation.
//	v6 (DEBT-XX brief-issue & brief-consume): adds briefs table for
//	    structured dispatch contracts and audit trail.
//	v7 (DEBT-31 pure FSM validator & event log): adds event_log table for
//	    append-only state transition audit trail.
//	v8 (Issue #291): adds session_id to tasks and supervisor_tasks, adds
//	    session_quotas table for per-session quota tracking.
const SchemaVersion = 10

// ErrUnknownSupervisorTask is returned when GetSupervisorTask / UpdateSupervisorTask /
// GetMetrics address a supervisor task id that does not exist.
var ErrUnknownSupervisorTask = errors.New("controlplane: unknown supervisor task")

// ErrUnknownBrief is returned when GetBrief or UpdateBriefStatus address a brief id that does not exist.
var ErrUnknownBrief = errors.New("controlplane: unknown brief")

// BriefRow is the durable row stored in the briefs table.
type BriefRow struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	PayloadMD string    `json:"payload_md"`
	DodMD     string    `json:"dod_md"`
	IssuedBy  string    `json:"issued_by"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Status    string    `json:"status"`
}

// SupervisorTaskRow is the durable row written when a supervisor run starts
// and updated when it ends. EnvelopeJSON holds the serialized TaskEnvelope
// so the evidence contract survives a process restart. Field types are kept
// primitive (no time.Time, no json.RawMessage) so this package does not
// import internal/supervisor — the supervisor package owns the typed
// surface and translates via FromRow/ToRow.
type SupervisorTaskRow struct {
	ID           string
	State        string
	EnvelopeJSON string
	ApproachIdx  int
	AttemptIdx   int
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ParentTaskID *string
	// Session ownership and isolation (issue #291)
	SessionID *string
}

// SupervisorDecisionRow is one immutable entry in the supervisor audit
// trail. Kind is free-form ("run_started", "attempt_started",
// "review_verdict", "approach_shift", "escalated", "needs_info", ...);
// PayloadJSON carries the structured detail.
type SupervisorDecisionRow struct {
	ID          string
	TaskID      string
	Kind        string
	PayloadJSON string
	CreatedAt   time.Time
}

// MetricsRow is the post-run telemetry bundle persisted for one supervisor
// task. Eight scalar columns map 1:1 to the §10 contract; JSON encoding is
// the supervisor package's responsibility, not the store's.
type MetricsRow struct {
	SupervisorTaskID     string
	EnvelopeScore        float64
	FirstAttemptSuccess  bool
	AttemptsToSuccess    int
	ApproachesToSuccess  int
	RCAConfidenceAvg     float64
	CycleDurationSeconds float64
	EscalationCount      int
	FalseEscalationRate  float64
}

// TaskSchemaVersion tags request payloads submitted to the queue.
const TaskSchemaVersion = "agy.task.v1"

// Task lifecycle states, mirroring TASK_STATES in the Python baseline and internal/state registry.
const (
	StateQueued             = string(state.TaskStateQueued)
	StateLeased             = string(state.TaskStateLeased)
	StateRunning            = string(state.TaskStateRunning)
	StateNeedsInfo          = string(state.TaskStateNeedsInfo)
	StateBlocked            = string(state.TaskStateBlocked)
	StateWorkerCompleted    = "WORKER_COMPLETED"    // Worker finished, awaiting supervisor acceptance
	StateOutputInvalid      = "OUTPUT_INVALID"      // Worker completed but output failed validation
	StateSupervisorAccepted = "SUPERVISOR_ACCEPTED" // Supervisor accepted the result
	StateSupervisorRejected = "SUPERVISOR_REJECTED" // Supervisor rejected, needs revision
	StateReviewRequired     = "REVIEW_REQUIRED"     // Needs human review before finalization
	StateCheckpointed       = "CHECKPOINTED"        // Task paused at checkpoint for recovery
	StateSucceeded          = string(state.TaskStateSucceeded)
	StateFailed             = string(state.TaskStateFailed)
	StateCancelled          = string(state.TaskStateCancelled)
)

// TaskStates enumerates every valid lifecycle state.
var TaskStates = []string{
	StateQueued,
	StateLeased,
	StateRunning,
	StateNeedsInfo,
	StateBlocked,
	StateWorkerCompleted,
	StateOutputInvalid,
	StateSupervisorAccepted,
	StateSupervisorRejected,
	StateReviewRequired,
	StateCheckpointed,
	StateSucceeded,
	StateFailed,
	StateCancelled,
}

// FinalStates is the set of terminal states; tasks in these states are never
// reclaimed, requeued, or counted as active.
var FinalStates = map[string]struct{}{
	StateSucceeded:          {},
	StateFailed:             {},
	StateCancelled:          {},
	StateSupervisorAccepted: {}, // Accepted by supervisor, ready for finalization
}

// IsValidState reports whether state is a recognized lifecycle state.
func IsValidState(state string) bool {
	for _, s := range TaskStates {
		if s == state {
			return true
		}
	}
	return false
}

// Task is the durable queue record decoded from one row of the tasks table.
type Task struct {
	TaskID          string          `json:"task_id"`
	ParentTaskID    *string         `json:"parent_task_id,omitempty"`
	IdempotencyKey  string          `json:"idempotency_key"`
	SchemaVersion   string          `json:"schema_version"`
	State           string          `json:"state"`
	Priority        int             `json:"priority"`
	Request         json.RawMessage `json:"request"`
	RequestHash     string          `json:"request_hash"`
	Result          json.RawMessage `json:"result,omitempty"`
	ResultHash      *string         `json:"result_hash,omitempty"`
	ReceiptHash     *string         `json:"receipt_hash,omitempty"`
	Attempts        int             `json:"attempts"`
	MaxAttempts     int             `json:"max_attempts"`
	LeaseOwner      *string         `json:"lease_owner,omitempty"`
	LeaseToken      *string         `json:"lease_token,omitempty"`
	LeaseExpiresAt  *float64        `json:"lease_expires_at,omitempty"`
	CancelRequested bool            `json:"cancel_requested"`
	CreatedAt       float64         `json:"created_at"`
	UpdatedAt       float64         `json:"updated_at"`
	CompletedAt     *float64        `json:"completed_at,omitempty"`
	LastError       *string         `json:"last_error,omitempty"`
	OrchestratorID  *string         `json:"orchestrator_id,omitempty"`
	WorktreeID      *string         `json:"worktree_id,omitempty"`
	WorkerName      *string         `json:"worker_name,omitempty"`
	Iter            int             `json:"iter"`
	// Session ownership and isolation (issue #291)
	SessionID *string `json:"session_id,omitempty"`

	// Denial tracking for command lifecycle handling (issue #294)
	Denied       *bool   `json:"denied,omitempty"`
	DenialReason *string `json:"denial_reason,omitempty"`

	// Result acceptance mechanism (issue #295)
	// ErrorCallHistory tracks error calls per worker attempt for debugging
	ErrorCallHistory []ErrorCallRecord `json:"error_call_history,omitempty"`
	// ResultValidation stores schema/content validation results
	ResultValidation *ResultValidation `json:"result_validation,omitempty"`
	// SupervisorFeedback stores supervisor's edit request or additional context
	SupervisorFeedback *SupervisorFeedback `json:"supervisor_feedback,omitempty"`

	// ContractValidation tracks contract compliance (paths, tools, output size, schema)
	ContractValidation *ContractValidation `json:"contract_validation,omitempty"`

	// Checkpoint data for task recovery (issue #290)
	CheckpointData *CheckpointData `json:"checkpoint_data,omitempty"`

	// Deduplicated is a transient response flag (never persisted): true when
	// SubmitTask recognized the idempotency key and returned the existing task.
	Deduplicated bool `json:"deduplicated,omitempty"`
}

// ErrorCallRecord represents one error call in the task's history.
type ErrorCallRecord struct {
	Timestamp  float64 `json:"timestamp"`
	WorkerID   string  `json:"worker_id"`
	Attempt    int     `json:"attempt"`
	ErrorType  string  `json:"error_type"`
	ErrorMsg   string  `json:"error_msg"`
	StackTrace string  `json:"stack_trace,omitempty"`
}

// ResultValidation stores the outcome of result schema/content validation.
type ResultValidation struct {
	Valid         bool              `json:"valid"`
	SchemaErrors  []string          `json:"schema_errors,omitempty"`
	ContentErrors []string          `json:"content_errors,omitempty"`
	ValidatedAt   float64           `json:"validated_at"`
	ValidatedBy   string            `json:"validated_by"`
	Details       map[string]string `json:"details,omitempty"`
}

// SupervisorFeedback carries supervisor's decision on a worker-completed result.
type SupervisorFeedback struct {
	Action        string  `json:"action"` // "accept", "reject", "request_edit"
	Reason        string  `json:"reason"`
	AdditionalCtx string  `json:"additional_context,omitempty"`
	Timestamp     float64 `json:"timestamp"`
	SupervisorID  string  `json:"supervisor_id"`
}

// ContractValidation tracks the outcome of contract validation (file access, tools, output size, schema).
type ContractValidation struct {
	Valid              bool              `json:"valid"`
	PathViolations     []string          `json:"path_violations,omitempty"`
	ToolViolations     []string          `json:"tool_violations,omitempty"`
	OutputSizeViolated bool              `json:"output_size_violated,omitempty"`
	SchemaErrors       []string          `json:"schema_errors,omitempty"`
	ValidatedAt        float64           `json:"validated_at"`
	ValidatedBy        string            `json:"validated_by"`
	Details            map[string]string `json:"details,omitempty"`
}

// CheckpointData stores task state for recovery (issue #290)
type CheckpointData struct {
	// SourceHashes maps file paths to their content hashes at checkpoint time
	SourceHashes map[string]string `json:"source_hashes,omitempty"`
	// WorktreePath is the path to the worktree at checkpoint time
	WorktreePath string `json:"worktree_path,omitempty"`
	// CheckpointNumber increments on each checkpoint
	CheckpointNumber int `json:"checkpoint_number"`
	// Timestamp when checkpoint was created
	Timestamp float64 `json:"timestamp"`
	// WorkerState captures worker-specific state for recovery
	WorkerState json.RawMessage `json:"worker_state,omitempty"`
	// CompletedSteps tracks which steps have been completed
	CompletedSteps []string `json:"completed_steps,omitempty"`
}

// SubmitTaskRequest is the payload accepted by SubmitTask.
type SubmitTaskRequest struct {
	IdempotencyKey  string          `json:"idempotency_key"`
	Priority        int             `json:"priority"`
	MaxAttempts     int             `json:"max_attempts"`
	ParentTaskID    *string         `json:"parent_task_id,omitempty"`
	Payload         json.RawMessage `json:"payload"`
	Role            string          `json:"role,omitempty"`
	Permission      string          `json:"permission,omitempty"`
	Model           string          `json:"model,omitempty"`
	Timeout         string          `json:"timeout,omitempty"`
	AddDirs         []string        `json:"add_dirs,omitempty"`
	SkipPermissions bool            `json:"skip_permissions,omitempty"`
	NoSandbox       bool            `json:"no_sandbox,omitempty"`
	AgyBin          *string         `json:"agy_bin,omitempty"` // custom binary override is rejected in v0.1
	OrchestratorID  *string         `json:"orchestrator_id,omitempty"`
	WorktreeID      *string         `json:"worktree_id,omitempty"`
	WorkerName      *string         `json:"worker_name,omitempty"`
	Iter            int             `json:"iter,omitempty"`
	// Session ownership and isolation (issue #291)
	SessionID *string `json:"session_id,omitempty"`

	// Contract fields for verifiable task execution (issue #289)
	// AllowedPaths restricts file system access to whitelisted paths
	AllowedPaths []string `json:"allowed_paths,omitempty"`
	// AllowedTools restricts executable tools/binaries to whitelisted names
	AllowedTools []string `json:"allowed_tools,omitempty"`
	// MaxOutputSize limits the result JSON size in bytes (default 1MB)
	MaxOutputSize int64 `json:"max_output_size,omitempty"`
	// OutputSchema is a JSON Schema (Draft 7) for validating result structure
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
}

// TaskResult carries the worker outcome recorded by CompleteTask.
type TaskResult struct {
	Result      json.RawMessage `json:"result"`
	ReceiptHash string          `json:"receipt_hash,omitempty"`
}

// TaskFilter narrows ListTasks results; zero value lists every state.
type TaskFilter struct {
	State     *string
	Limit     int
	SessionID *string
}

// BriefFilter narrows ListBriefs results; zero value lists every status.
type BriefFilter struct {
	Status *string
	Limit  int
}

// ControlPlane is the DELTA-03 public contract. Maintenance, reconciliation,
// and event accessors remain concrete-type APIs (judge decision D3): they are
// operational surfaces beyond the minimum delegation interface.
type ControlPlane interface {
	SubmitTask(ctx context.Context, req SubmitTaskRequest) (*Task, error)
	ClaimTask(ctx context.Context, workerID string, leaseDurationSeconds int) (*Task, error)
	RenewHeartbeat(ctx context.Context, taskID string, workerID string, extensionSeconds int) error
	CompleteTask(ctx context.Context, taskID string, result TaskResult) error
	FailTask(ctx context.Context, taskID string, reason string, exitCode int) error
	CancelTask(ctx context.Context, taskID string, reason string) error
	ResumeTask(ctx context.Context, taskID string, resumedPayload json.RawMessage, reason string) (*Task, error)
	GetTask(ctx context.Context, taskID string) (*Task, error)
	ListTasks(ctx context.Context, filter TaskFilter) ([]*Task, error)
	ListChildTasks(ctx context.Context, parentTaskID string) ([]*Task, error)
	GetTaskLineage(ctx context.Context, taskID string) ([]*Task, error)

	// Result acceptance mechanism (issue #295)
	AcceptResult(ctx context.Context, taskID string, supervisorID string) error
	RejectResult(ctx context.Context, taskID string, supervisorID string, reason string, additionalCtx string) error
	RequestEdit(ctx context.Context, taskID string, supervisorID string, reason string, additionalCtx string) error
	AddErrorCall(ctx context.Context, taskID string, record ErrorCallRecord) error
	ValidateResult(ctx context.Context, taskID string, validation ResultValidation) error
	ValidateContract(ctx context.Context, taskID string, validation ContractValidation) error

	// Checkpoint/recovery for long-running tasks (issue #290)
	CheckpointTask(ctx context.Context, taskID, workerID, leaseToken string, checkpoint *CheckpointData) (*Task, error)
	ResumeFromCheckpoint(ctx context.Context, taskID, workerID, leaseToken string, newLeaseSeconds int) (*Task, error)
}

// canonicalJSON serializes value deterministically: map keys sorted
// (encoding/json behavior), no HTML escaping, compact separators. This mirrors
// canonical_json in the Python baseline so content hashes stay reproducible
// across implementations (baseline integrity test, category "integrity").
func canonicalJSON(value any) (string, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

// contentHash returns the SHA-256 hex digest of the canonical JSON encoding.
func contentHash(value any) (string, error) {
	encoded, err := canonicalJSON(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(encoded))
	return hex.EncodeToString(sum[:]), nil
}
