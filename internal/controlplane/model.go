// Package controlplane implements the DELTA-03 SQLite-backed task queue with
// compare-and-swap leases and an append-only event log, ported from the Python
// baseline (reference/python/scripts/agy_control_plane.py) under the g8s
// Zero-CGO constitution: modernc.org/sqlite only, injectable deterministic
// clock, and per-operation connection pragmas.
//
// Schema ownership decision (sprint log D2): this package owns ONLY the
// tasks / task_events / control_plane_maintenance tables. Write receipts live
// in internal/receipt and are wired in by higher layers, never duplicated here.
package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// SchemaVersion is the control-plane schema generation written into
// PRAGMA user_version after initialization or migration.
const SchemaVersion = 3

// TaskSchemaVersion tags request payloads submitted to the queue.
const TaskSchemaVersion = "agy.task.v1"

// Task lifecycle states, mirroring TASK_STATES in the Python baseline.
const (
	StateQueued    = "QUEUED"
	StateLeased    = "LEASED"
	StateRunning   = "RUNNING"
	StateNeedsInfo = "NEEDS_INFO"
	StateBlocked   = "BLOCKED"
	StateSucceeded = "SUCCEEDED"
	StateFailed    = "FAILED"
	StateCancelled = "CANCELLED"
)

// TaskStates enumerates every valid lifecycle state.
var TaskStates = []string{
	StateQueued,
	StateLeased,
	StateRunning,
	StateNeedsInfo,
	StateBlocked,
	StateSucceeded,
	StateFailed,
	StateCancelled,
}

// FinalStates is the set of terminal states; tasks in these states are never
// reclaimed, requeued, or counted as active.
var FinalStates = map[string]struct{}{
	StateSucceeded: {},
	StateFailed:    {},
	StateCancelled: {},
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

	// Deduplicated is a transient response flag (never persisted): true when
	// SubmitTask recognized the idempotency key and returned the existing task.
	Deduplicated bool `json:"deduplicated,omitempty"`
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
}

// TaskResult carries the worker outcome recorded by CompleteTask.
type TaskResult struct {
	Result      json.RawMessage `json:"result"`
	ReceiptHash string          `json:"receipt_hash,omitempty"`
}

// TaskFilter narrows ListTasks results; zero value lists every state.
type TaskFilter struct {
	State *string
	Limit int
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
