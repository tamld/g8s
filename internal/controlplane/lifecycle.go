package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/tamld/g8s/internal/harness"
)

// ErrUnknownTask reports operations addressing a task id that does not exist.
// Spec-first deviation from the Python baseline, which returns None silently;
// the DELTA-03 Go contract surfaces typed errors instead.
var ErrUnknownTask = errors.New("unknown task")

// FinishAttemptParams carries the worker verdict for FinishAttempt.
type FinishAttemptParams struct {
	Result    json.RawMessage
	Success   bool
	Retryable bool
	Err       string
}

// TaskEvent is one row of the append-only task_events audit stream.
type TaskEvent struct {
	EventID   int64           `json:"event_id"`
	TaskID    string          `json:"task_id"`
	Timestamp float64         `json:"timestamp"`
	EventType string          `json:"event_type"`
	Actor     string          `json:"actor"`
	Details   json.RawMessage `json:"details,omitempty"`
}

// ReceiptSchemaVersion mirrors RECEIPT_SCHEMA_VERSION in the baseline.
const ReceiptSchemaVersion = "agy.receipt.v1"

// ValidateSubmitRequest enforces the v0.1 submission safety envelope before
// any hashing or persistence: prompt presence, required model, explicit scope
// roots, and the disabled escape hatches (no_sandbox, custom agy binary,
// workspace_write unless the deployment opt-in env var is set). Scope-root
// denial and blocked-prompt checks are delegated to the shared harness.
func ValidateSubmitRequest(req SubmitTaskRequest) error {
	var payload struct {
		Prompt string `json:"prompt"`
	}
	if len(req.Payload) == 0 || json.Unmarshal(req.Payload, &payload) != nil {
		return errors.New("request must be an object")
	}
	if strings.TrimSpace(payload.Prompt) == "" {
		return errors.New("request.prompt is required")
	}

	role := req.Role
	if role == "" {
		role = "collector"
	}
	permission := req.Permission
	if permission == "" {
		permission = "read_only"
	}

	if strings.TrimSpace(req.Model) == "" {
		return errors.New("request.model is required")
	}
	if len(req.AddDirs) == 0 {
		return errors.New("request.add_dirs requires at least one explicit scope root")
	}
	if req.NoSandbox {
		return errors.New("no_sandbox is disabled in control-plane v0.1")
	}
	if req.AgyBin != nil {
		return errors.New("custom agy_bin is disabled in control-plane v0.1")
	}
	if permission == "workspace_write" && os.Getenv("AGY_MCP_ALLOW_WORKSPACE_WRITE") != "1" {
		// #334: the delegated-write path is opt-in; the rejection must name
		// the escape hatch so supervisors do not have to read source to
		// discover it. Receipts remain mandatory for workspace_write
		// regardless of this flag (harness gate).
		return errors.New("workspace_write is disabled by default; set AGY_MCP_ALLOW_WORKSPACE_WRITE=1 in the worker environment to enable delegated writes (a single-use, path-scoped write receipt is still mandatory)")
	}

	// The harness re-validates with the receipt ID carried in the payload
	// (#334): submit.go embeds receipt_id into the payload JSON, so the
	// control-plane re-check must pass it through — an empty receipt here
	// would double-reject every delegated-write submission.
	var receiptEnvelope struct {
		ReceiptID string `json:"receipt_id"`
	}
	_ = json.Unmarshal(req.Payload, &receiptEnvelope)

	return harness.ValidateRequest(payload.Prompt, role, permission, req.AddDirs, req.SkipPermissions, receiptEnvelope.ReceiptID)
}

// redactPayload removes the raw prompt from a request JSON document and
// records its SHA-256 fingerprint plus the redaction flag (containment
// axiom: finished tasks never retain plaintext prompts at rest).
func redactPayload(requestJSON *string) error {
	if requestJSON == nil || *requestJSON == "" {
		return nil
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(*requestJSON), &doc); err != nil {
		return err
	}
	prompt, ok := doc["prompt"]
	if !ok {
		return nil
	}
	delete(doc, "prompt")
	hash, err := contentHash(prompt)
	if err != nil {
		return err
	}
	doc["prompt_hash"] = hash
	doc["prompt_redacted"] = true
	canonical, err := canonicalJSON(doc)
	if err != nil {
		return err
	}
	*requestJSON = canonical
	return nil
}

func eventsTx(tx *sql.Tx, taskID string) ([]map[string]any, error) {
	rows, err := tx.Query(
		`SELECT event_id, task_id, timestamp, event_type, actor, details_json
		 FROM task_events WHERE task_id = ? ORDER BY event_id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []map[string]any{}
	for rows.Next() {
		var (
			eventID     int64
			taskIDCol   string
			timestamp   float64
			eventType   string
			actor       string
			detailsJSON sql.NullString
		)
		if err := rows.Scan(&eventID, &taskIDCol, &timestamp, &eventType, &actor, &detailsJSON); err != nil {
			return nil, err
		}
		details := any(map[string]any{})
		if detailsJSON.Valid && detailsJSON.String != "" {
			if err := json.Unmarshal([]byte(detailsJSON.String), &details); err != nil {
				return nil, err
			}
		}
		events = append(events, map[string]any{
			"event_id":   eventID,
			"task_id":    taskIDCol,
			"timestamp":  timestamp,
			"event_type": eventType,
			"actor":      actor,
			"details":    details,
		})
	}
	return events, rows.Err()
}

// buildReceiptTx assembles the unsigned receipt payload for a task inside an
// open transaction and returns it with the self-referential receipt_hash
// applied. Hash values are computed over canonically sorted key sets; they
// are reproducible within this implementation but intentionally not compared
// against Python baseline hash literals.
func buildReceiptTx(tx *sql.Tx, task *Task) (map[string]any, string, error) {
	events, err := eventsTx(tx, task.TaskID)
	if err != nil {
		return nil, "", err
	}
	eventsCanonical, err := canonicalJSON(events)
	if err != nil {
		return nil, "", err
	}
	eventsHash, err := contentHash(json.RawMessage(eventsCanonical))
	if err != nil {
		return nil, "", err
	}

	payload := map[string]any{
		"schema_version": ReceiptSchemaVersion,
		"task_id":        task.TaskID,
		"state":          task.State,
		"request_hash":   task.RequestHash,
		"events_hash":    eventsHash,
		"attempts":       task.Attempts,
		"created_at":     task.CreatedAt,
		"signed":         false,
	}
	if task.ParentTaskID != nil {
		payload["parent_task_id"] = *task.ParentTaskID
	} else {
		payload["parent_task_id"] = nil
	}
	if task.ResultHash != nil {
		payload["result_hash"] = *task.ResultHash
	} else {
		payload["result_hash"] = nil
	}
	if task.CompletedAt != nil {
		payload["completed_at"] = *task.CompletedAt
	} else {
		payload["completed_at"] = nil
	}

	unsignedCanonical, err := canonicalJSON(payload)
	if err != nil {
		return nil, "", err
	}
	receiptHash, err := contentHash(json.RawMessage(unsignedCanonical))
	if err != nil {
		return nil, "", err
	}
	payload["receipt_hash"] = receiptHash
	return payload, receiptHash, nil
}

// BuildReceipt exposes receipt construction for read-only inspection.
func (s *Store) BuildReceipt(_ context.Context, taskID string) (map[string]any, error) {
	task, err := s.GetTask(context.Background(), taskID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTask, taskID)
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	payload, _, err := buildReceiptTx(tx, task)
	if err != nil {
		return nil, err
	}
	return payload, tx.Commit()
}

const finishStaleLeaseMsg = "lease ownership lost; refusing stale completion"

func checkLeaseOwnership(task *Task, workerID, leaseToken string) error {
	if task.State != StateRunning ||
		task.LeaseOwner == nil || *task.LeaseOwner != workerID ||
		task.LeaseToken == nil || *task.LeaseToken != leaseToken {
		return errors.New(finishStaleLeaseMsg)
	}
	return nil
}

func determineNextState(task *Task, params FinishAttemptParams) string {
	switch {
	case task.CancelRequested:
		return StateCancelled
	case params.Success:
		return StateSucceeded
	case params.Retryable && task.Attempts < task.MaxAttempts:
		return StateQueued
	}
	return StateFailed
}

// FinishAttempt applies the worker verdict for a RUNNING lease with strict
// ownership enforcement. Transitions follow the baseline priority table:
// cancel_requested -> CANCELLED, success -> SUCCEEDED, retryable within
// budget -> QUEUED (prompt retained), otherwise FAILED. Final states get
// prompt redaction and an unsigned receipt hash.
func (s *Store) FinishAttempt(taskID, workerID, leaseToken string, params FinishAttemptParams) (*Task, error) {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	row := tx.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE task_id = ?`, taskID)
	task, err := scanTask(row)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTask, taskID)
	}
	if err := checkLeaseOwnership(task, workerID, leaseToken); err != nil {
		return nil, err
	}

	now := float64(s.clock().UnixNano()) / 1e9
	_ = determineNextState(task, params) // kept for documentation of original logic
	nextState := StateWorkerCompleted

	resultCanonical, err := canonicalJSON(params.Result)
	if err != nil {
		return nil, err
	}
	resultHash, err := contentHash(params.Result)
	if err != nil {
		return nil, err
	}

	// Transition to WorkerCompleted instead of directly to final state
	// Supervisor will then accept/reject the result
	res, err := tx.Exec(
		`UPDATE tasks SET state = ?, result_json = ?, result_hash = ?, updated_at = ?,
		 completed_at = ?, last_error = ?, lease_owner = NULL, lease_token = NULL,
		 lease_expires_at = NULL, receipt_hash = NULL,
		 error_call_history = ?, result_validation = ?, supervisor_feedback = NULL
		 WHERE task_id = ? AND lease_owner = ? AND lease_token = ? AND state = 'RUNNING'`,
		nextState, resultCanonical, resultHash, now,
		now, params.Err, // Use now for completed_at
		"[]", "{}", // empty error_call_history and result_validation for now
		taskID, workerID, leaseToken)
	if err != nil {
		return nil, err
	}
	if affected, err := res.RowsAffected(); err != nil || affected != 1 {
		return nil, errors.New(finishStaleLeaseMsg)
	}

	isRetryable := params.Retryable && !params.Success
	requestJSON := marshalStoredRequest(task.Request)

	fresh := *task
	fresh.State = nextState
	fresh.ResultHash = &resultHash
	fresh.CompletedAt = &now

	if isRetryable {
		// Retryable: keep prompt for next attempt, don't seal receipt
		if err := insertTaskEvent(tx, taskID, "attempt_finished", workerID, map[string]any{
			"next_state":  nextState,
			"success":     params.Success,
			"retryable":   params.Retryable,
			"result_hash": resultHash,
		}, now); err != nil {
			return nil, err
		}
	} else {
		// Non-retryable (success or permanent failure): redact prompt, seal receipt
		if err := redactPayload(&requestJSON); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`UPDATE tasks SET request_json = ? WHERE task_id = ?`, requestJSON, taskID); err != nil {
			return nil, err
		}

		if err := insertTaskEvent(tx, taskID, "attempt_finished", workerID, map[string]any{
			"next_state":  nextState,
			"success":     params.Success,
			"retryable":   params.Retryable,
			"result_hash": resultHash,
		}, now); err != nil {
			return nil, err
		}

		// Build receipt for non-retryable WorkerCompleted state
		// Use the redacted request for receipt
		fresh.Request = json.RawMessage(requestJSON)
		payload, receiptHash, err := buildReceiptTx(tx, &fresh)
		if err != nil {
			return nil, err
		}
		_ = payload
		if _, err := tx.Exec(`UPDATE tasks SET receipt_hash = ? WHERE task_id = ?`, receiptHash, taskID); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetTask(context.Background(), taskID)
}

// marshalStoredRequest re-canonicalizes stored request bytes so redaction can
// operate on a mutable string copy.
func marshalStoredRequest(raw json.RawMessage) string {
	return string(raw)
}

// CompleteTask is the spec-level success completion convenience for the Brain
// tier: it adopts the current lease holder and delegates to FinishAttempt
// (governance axiom 1 - the Brain owns terminal state transitions).
func (s *Store) CompleteTask(ctx context.Context, taskID string, result TaskResult) error {
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("%w: %s", ErrUnknownTask, taskID)
	}
	_, err = s.FinishAttempt(taskID, deref(task.LeaseOwner), deref(task.LeaseToken), FinishAttemptParams{
		Result:  result.Result,
		Success: true,
	})
	return err
}

// FailTask records a non-retryable failure with the observed exit code.
func (s *Store) FailTask(ctx context.Context, taskID, reason string, exitCode int) error {
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("%w: %s", ErrUnknownTask, taskID)
	}
	_, err = s.FinishAttempt(taskID, deref(task.LeaseOwner), deref(task.LeaseToken), FinishAttemptParams{
		Result:  json.RawMessage(`{}`),
		Err:     fmt.Sprintf("%s (exit code %d)", reason, exitCode),
		Success: false,
	})
	return err
}

// CancelTask requests cancellation. QUEUED (and every other non-final,
// non-running state) transitions terminally to CANCELLED; RUNNING tasks stay
// running with cancel_requested set so workers observe ExecutionSignal and
// stop cooperatively.
func (s *Store) CancelTask(_ context.Context, taskID, reason string) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	row := tx.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE task_id = ?`, taskID)
	task, err := scanTask(row)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("%w: %s", ErrUnknownTask, taskID)
	}
	switch task.State {
	case StateSucceeded, StateFailed, StateCancelled:
		return tx.Commit()
	}

	now := float64(s.clock().UnixNano()) / 1e9
	nextState := StateRunning
	if task.State != StateRunning {
		nextState = StateCancelled
	}

	requestJSON := marshalStoredRequest(task.Request)
	if nextState == StateCancelled {
		if err := redactPayload(&requestJSON); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE tasks SET request_json = ? WHERE task_id = ?`, requestJSON, taskID); err != nil {
			return err
		}
	}

	if nextState == StateCancelled {
		if _, err := tx.Exec(
			`UPDATE tasks SET state = 'CANCELLED', cancel_requested = 1, updated_at = ?,
			 completed_at = ?, last_error = ?, lease_owner = NULL, lease_token = NULL,
			 lease_expires_at = NULL WHERE task_id = ?`,
			now, now, reason, taskID); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(
			`UPDATE tasks SET cancel_requested = 1, updated_at = ? WHERE task_id = ?`,
			now, taskID); err != nil {
			return err
		}
	}

	fresh := *task
	fresh.State = nextState
	fresh.CancelRequested = true
	if nextState == StateCancelled {
		fresh.CompletedAt = &now
	}
	if err := insertTaskEvent(tx, taskID, "cancel_requested", "brain", map[string]any{
		"reason":     reason,
		"next_state": nextState,
	}, now); err != nil {
		return err
	}

	if nextState == StateCancelled {
		_, receiptHash, err := buildReceiptTx(tx, &fresh)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE tasks SET receipt_hash = ? WHERE task_id = ?`, receiptHash, taskID); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// PauseTask moves a RUNNING lease into NEEDS_INFO, BLOCKED, or CHECKPOINTED, always redacting
// the prompt and sealing a receipt even though paused states are not final
// (baseline semantics: paused work is frozen evidence).
func (s *Store) PauseTask(taskID, workerID, leaseToken, pauseState string, result json.RawMessage, reason string) (*Task, error) {
	if pauseState != StateNeedsInfo && pauseState != StateBlocked && pauseState != StateCheckpointed {
		return nil, errors.New("pause state must be NEEDS_INFO, BLOCKED, or CHECKPOINTED")
	}

	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	row := tx.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE task_id = ?`, taskID)
	task, err := scanTask(row)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTask, taskID)
	}

	now := float64(s.clock().UnixNano()) / 1e9
	resultCanonical, err := canonicalJSON(result)
	if err != nil {
		return nil, err
	}
	resultHash, err := contentHash(result)
	if err != nil {
		return nil, err
	}

	res, err := tx.Exec(
		`UPDATE tasks SET state = ?, result_json = ?, result_hash = ?, updated_at = ?,
		 last_error = ?, lease_owner = NULL, lease_token = NULL, lease_expires_at = NULL
		 WHERE task_id = ? AND state = 'RUNNING' AND lease_owner = ? AND lease_token = ?`,
		pauseState, resultCanonical, resultHash, now, reason, taskID, workerID, leaseToken)
	if err != nil {
		return nil, err
	}
	if affected, err := res.RowsAffected(); err != nil || affected != 1 {
		return nil, errors.New(finishStaleLeaseMsg)
	}

	requestJSON := marshalStoredRequest(task.Request)
	if err := redactPayload(&requestJSON); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE tasks SET request_json = ? WHERE task_id = ?`, requestJSON, taskID); err != nil {
		return nil, err
	}

	fresh := *task
	fresh.State = pauseState
	fresh.ResultHash = &resultHash
	if err := insertTaskEvent(tx, taskID, "task_paused", workerID, map[string]any{
		"state":       pauseState,
		"reason":      reason,
		"result_hash": resultHash,
	}, now); err != nil {
		return nil, err
	}

	_, receiptHash, err := buildReceiptTx(tx, &fresh)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE tasks SET receipt_hash = ? WHERE task_id = ?`, receiptHash, taskID); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetTask(context.Background(), taskID)
}

// CheckpointTask saves a checkpoint for a RUNNING task, storing source hashes,
// worktree path, and worker state for recovery. Returns the updated task.
func (s *Store) CheckpointTask(ctx context.Context, taskID, workerID, leaseToken string, checkpoint *CheckpointData) (*Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	row := tx.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE task_id = ?`, taskID)
	task, err := scanTask(row)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTask, taskID)
	}

	if task.State != StateRunning {
		return nil, fmt.Errorf("task %s in state %s cannot be checkpointed (must be RUNNING)", taskID, task.State)
	}

	now := float64(s.clock().UnixNano()) / 1e9
	checkpointJSON, err := json.Marshal(checkpoint)
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(
		`UPDATE tasks SET checkpoint_data = ?, updated_at = ? WHERE task_id = ? AND lease_owner = ? AND lease_token = ?`,
		string(checkpointJSON), now, taskID, workerID, leaseToken)
	if err != nil {
		return nil, err
	}

	if err := insertTaskEvent(tx, taskID, "task_checkpointed", workerID, map[string]any{
		"checkpoint_number": checkpoint.CheckpointNumber,
		"timestamp":         checkpoint.Timestamp,
	}, now); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetTask(ctx, taskID)
}

// ResumeFromCheckpoint restores a task from CHECKPOINTED state back to RUNNING.
func (s *Store) ResumeFromCheckpoint(ctx context.Context, taskID, workerID, leaseToken string, newLeaseSeconds int) (*Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	row := tx.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE task_id = ?`, taskID)
	task, err := scanTask(row)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTask, taskID)
	}

	if task.State != StateCheckpointed {
		return nil, fmt.Errorf("task %s in state %s cannot be resumed from checkpoint (must be CHECKPOINTED)", taskID, task.State)
	}

	now := float64(s.clock().UnixNano()) / 1e9
	leaseExpiresAt := now + float64(newLeaseSeconds)

	_, err = tx.Exec(
		`UPDATE tasks SET state = ?, lease_owner = ?, lease_token = ?, lease_expires_at = ?, updated_at = ?
		 WHERE task_id = ? AND state = ?`,
		StateRunning, workerID, leaseToken, leaseExpiresAt, now, taskID, StateCheckpointed)
	if err != nil {
		return nil, err
	}

	if err := insertTaskEvent(tx, taskID, "checkpoint_resumed", workerID, map[string]any{
		"new_lease_seconds": newLeaseSeconds,
	}, now); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetTask(ctx, taskID)
}

// ResumeTask moves a task from NEEDS_INFO or BLOCKED back to QUEUED,
// updating its request payload with resumed inputs and recording a resumption event.
func (s *Store) ResumeTask(ctx context.Context, taskID string, resumedPayload json.RawMessage, reason string) (*Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	row := tx.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE task_id = ?`, taskID)
	task, err := scanTask(row)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTask, taskID)
	}

	if task.State != StateNeedsInfo && task.State != StateBlocked {
		return nil, fmt.Errorf("task %s in state %s cannot be resumed (must be NEEDS_INFO or BLOCKED)", taskID, task.State)
	}

	now := float64(s.clock().UnixNano()) / 1e9

	newPayload := task.Request
	if len(resumedPayload) > 0 && string(resumedPayload) != "null" {
		newPayload = resumedPayload
	}
	reqCanonical, err := canonicalJSON(newPayload)
	if err != nil {
		return nil, err
	}
	reqHash, err := contentHash(newPayload)
	if err != nil {
		return nil, err
	}

	res, err := tx.Exec(
		`UPDATE tasks SET state = 'QUEUED', attempts = 0, request_json = ?, request_hash = ?, result_json = NULL, result_hash = NULL,
		 last_error = NULL, lease_owner = NULL, lease_token = NULL, lease_expires_at = NULL, updated_at = ?
		 WHERE task_id = ? AND (state = 'NEEDS_INFO' OR state = 'BLOCKED')`,
		reqCanonical, reqHash, now, taskID)
	if err != nil {
		return nil, err
	}
	if affected, err := res.RowsAffected(); err != nil || affected != 1 {
		return nil, errors.New("failed to resume task: concurrent state change")
	}

	if err := insertTaskEvent(tx, taskID, "task_resumed", "orchestrator", map[string]any{
		"previous_state": task.State,
		"reason":         reason,
	}, now); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return s.GetTask(ctx, taskID)
}

// BeginMaintenance acquires the singleton maintenance lock, blocking all new
// claims while held. Returns the number of active (LEASED/RUNNING) tasks at
// acquisition time.
func (s *Store) BeginMaintenance(owner string, ttlSeconds float64) (int, error) {
	if strings.TrimSpace(owner) == "" {
		return 0, errors.New("maintenance owner is required")
	}
	if ttlSeconds <= 0 {
		return 0, errors.New("maintenance ttl_seconds must be positive")
	}

	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	now := float64(s.clock().UnixNano()) / 1e9
	var currentExpires sql.NullFloat64
	var currentOwner sql.NullString
	err = tx.QueryRow(`SELECT owner, expires_at FROM control_plane_maintenance WHERE singleton = 1`).
		Scan(&currentOwner, &currentExpires)
	switch {
	case err == sql.ErrNoRows:
		// lock free
	case err != nil:
		return 0, err
	case currentExpires.Valid && currentExpires.Float64 > now:
		return 0, fmt.Errorf("control-plane maintenance is already held by %s", currentOwner.String)
	}

	if _, err := tx.Exec(`DELETE FROM control_plane_maintenance WHERE singleton = 1`); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(
		`INSERT INTO control_plane_maintenance (singleton, owner, expires_at, updated_at)
		 VALUES (1, ?, ?, ?)`, owner, now+ttlSeconds, now); err != nil {
		return 0, err
	}

	var active int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM tasks WHERE state IN ('LEASED', 'RUNNING')`).Scan(&active); err != nil {
		return 0, err
	}
	return active, tx.Commit()
}

// EndMaintenance releases the lock when the owner matches; stale holders are
// rejected and expired locks are simply absent.
func (s *Store) EndMaintenance(owner string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM control_plane_maintenance WHERE singleton = 1 AND owner = ?`, owner)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

// Events returns the full audit trail for one task ordered by insertion.
func (s *Store) Events(_ context.Context, taskID string) ([]TaskEvent, error) {
	rows, err := s.db.Query(
		`SELECT event_id, task_id, timestamp, event_type, actor, details_json
		 FROM task_events WHERE task_id = ? ORDER BY event_id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []TaskEvent{}
	for rows.Next() {
		var (
			e           TaskEvent
			detailsJSON sql.NullString
		)
		if err := rows.Scan(&e.EventID, &e.TaskID, &e.Timestamp, &e.EventType, &e.Actor, &detailsJSON); err != nil {
			return nil, err
		}
		if detailsJSON.Valid && detailsJSON.String != "" {
			e.Details = json.RawMessage(detailsJSON.String)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// AcceptResult transitions a WORKER_COMPLETED task to SUPERVISOR_ACCEPTED.
func (s *Store) AcceptResult(ctx context.Context, taskID string, supervisorID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	row := tx.QueryRow(`SELECT state FROM tasks WHERE task_id = ?`, taskID)
	var state string
	if err := row.Scan(&state); err != nil {
		return err
	}
	if state != StateWorkerCompleted {
		return fmt.Errorf("task %s is not in WORKER_COMPLETED state (current: %s)", taskID, state)
	}

	now := float64(s.clock().UnixNano()) / 1e9
	feedback := SupervisorFeedback{
		Action:       "accept",
		Reason:       "result accepted by supervisor",
		Timestamp:    now,
		SupervisorID: supervisorID,
	}
	feedbackJSON, _ := json.Marshal(feedback)

	_, err = tx.Exec(
		`UPDATE tasks SET state = ?, supervisor_feedback = ?, updated_at = ? WHERE task_id = ?`,
		StateSupervisorAccepted, string(feedbackJSON), now, taskID)
	if err != nil {
		return err
	}

	if err := insertTaskEvent(tx, taskID, "result_accepted", supervisorID, map[string]any{
		"previous_state": state,
	}, now); err != nil {
		return err
	}

	return tx.Commit()
}

// RejectResult transitions a WORKER_COMPLETED task to SUPERVISOR_REJECTED.
func (s *Store) RejectResult(ctx context.Context, taskID string, supervisorID string, reason string, additionalCtx string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	row := tx.QueryRow(`SELECT state FROM tasks WHERE task_id = ?`, taskID)
	var state string
	if err := row.Scan(&state); err != nil {
		return err
	}
	if state != StateWorkerCompleted {
		return fmt.Errorf("task %s is not in WORKER_COMPLETED state (current: %s)", taskID, state)
	}

	now := float64(s.clock().UnixNano()) / 1e9
	feedback := SupervisorFeedback{
		Action:        "reject",
		Reason:        reason,
		AdditionalCtx: additionalCtx,
		Timestamp:     now,
		SupervisorID:  supervisorID,
	}
	feedbackJSON, _ := json.Marshal(feedback)

	_, err = tx.Exec(
		`UPDATE tasks SET state = ?, supervisor_feedback = ?, updated_at = ? WHERE task_id = ?`,
		StateSupervisorRejected, string(feedbackJSON), now, taskID)
	if err != nil {
		return err
	}

	if err := insertTaskEvent(tx, taskID, "result_rejected", supervisorID, map[string]any{
		"previous_state":     state,
		"reason":             reason,
		"additional_context": additionalCtx,
	}, now); err != nil {
		return err
	}

	return tx.Commit()
}

// RequestEdit transitions a WORKER_COMPLETED task to SUPERVISOR_REJECTED with edit request.
func (s *Store) RequestEdit(ctx context.Context, taskID string, supervisorID string, reason string, additionalCtx string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	row := tx.QueryRow(`SELECT state FROM tasks WHERE task_id = ?`, taskID)
	var state string
	if err := row.Scan(&state); err != nil {
		return err
	}
	if state != StateWorkerCompleted {
		return fmt.Errorf("task %s is not in WORKER_COMPLETED state (current: %s)", taskID, state)
	}

	now := float64(s.clock().UnixNano()) / 1e9
	feedback := SupervisorFeedback{
		Action:        "request_edit",
		Reason:        reason,
		AdditionalCtx: additionalCtx,
		Timestamp:     now,
		SupervisorID:  supervisorID,
	}
	feedbackJSON, _ := json.Marshal(feedback)

	// Transition back to NEEDS_INFO so worker can retry with new context
	_, err = tx.Exec(
		`UPDATE tasks SET state = ?, supervisor_feedback = ?, updated_at = ? WHERE task_id = ?`,
		StateNeedsInfo, string(feedbackJSON), now, taskID)
	if err != nil {
		return err
	}

	if err := insertTaskEvent(tx, taskID, "edit_requested", supervisorID, map[string]any{
		"previous_state":     state,
		"reason":             reason,
		"additional_context": additionalCtx,
	}, now); err != nil {
		return err
	}

	return tx.Commit()
}

// RequeueResult transitions a WORKER_COMPLETED task back to QUEUED for retry.
// This is used for automatic retries of retryable failures (timeout, spawn failure).
func (s *Store) RequeueResult(ctx context.Context, taskID string, supervisorID string, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	row := tx.QueryRow(`SELECT state FROM tasks WHERE task_id = ?`, taskID)
	var state string
	if err := row.Scan(&state); err != nil {
		return err
	}
	if state != StateWorkerCompleted {
		return fmt.Errorf("task %s is not in WORKER_COMPLETED state (current: %s)", taskID, state)
	}

	now := float64(s.clock().UnixNano()) / 1e9
	feedback := SupervisorFeedback{
		Action:       "requeue",
		Reason:       reason,
		Timestamp:    now,
		SupervisorID: supervisorID,
	}
	feedbackJSON, _ := json.Marshal(feedback)

	// Transition back to QUEUED for automatic retry
	_, err = tx.Exec(
		`UPDATE tasks SET state = ?, supervisor_feedback = ?, updated_at = ? WHERE task_id = ?`,
		StateQueued, string(feedbackJSON), now, taskID)
	if err != nil {
		return err
	}

	if err := insertTaskEvent(tx, taskID, "requeued", supervisorID, map[string]any{
		"previous_state": state,
		"reason":         reason,
	}, now); err != nil {
		return err
	}

	return tx.Commit()
}

// AddErrorCall appends an error call record to the task's error history.
func (s *Store) AddErrorCall(ctx context.Context, taskID string, record ErrorCallRecord) error {
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return ErrUnknownTask
	}

	history := task.ErrorCallHistory
	if history == nil {
		history = []ErrorCallRecord{}
	}
	history = append(history, record)

	historyJSON, err := json.Marshal(history)
	if err != nil {
		return err
	}

	now := float64(s.clock().UnixNano()) / 1e9
	_, err = s.db.ExecContext(ctx,
		`UPDATE tasks SET error_call_history = ?, updated_at = ? WHERE task_id = ?`,
		string(historyJSON), now, taskID)
	return err
}

// ValidateResult stores the result validation outcome.
func (s *Store) ValidateResult(ctx context.Context, taskID string, validation ResultValidation) error {
	validation.ValidatedAt = float64(s.clock().UnixNano()) / 1e9
	validationJSON, err := json.Marshal(validation)
	if err != nil {
		return err
	}

	now := float64(s.clock().UnixNano()) / 1e9
	_, err = s.db.ExecContext(ctx,
		`UPDATE tasks SET result_validation = ?, updated_at = ? WHERE task_id = ?`,
		string(validationJSON), now, taskID)
	return err
}

// ValidateContract stores the contract validation outcome.
func (s *Store) ValidateContract(ctx context.Context, taskID string, validation ContractValidation) error {
	validation.ValidatedAt = float64(s.clock().UnixNano()) / 1e9
	validationJSON, err := json.Marshal(validation)
	if err != nil {
		return err
	}

	now := float64(s.clock().UnixNano()) / 1e9
	_, err = s.db.ExecContext(ctx,
		`UPDATE tasks SET contract_validation = ?, updated_at = ? WHERE task_id = ?`,
		string(validationJSON), now, taskID)
	return err
}

// Compile-time guarantee that Store satisfies the full DELTA-03 interface
// once the lifecycle slice lands.
var _ ControlPlane = (*Store)(nil)
