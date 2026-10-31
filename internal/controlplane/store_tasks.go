package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"os"

	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Sentinel errors surfaced by query validation.

const taskColumns = `task_id, parent_task_id, idempotency_key, schema_version, state, priority,
	request_json, request_hash, result_json, result_hash, receipt_hash,
	attempts, max_attempts, lease_owner, lease_token, lease_expires_at,
	cancel_requested, created_at, updated_at, completed_at, last_error,
	orchestrator_id, worktree_id, worker_name, iter,
	error_call_history, result_validation, supervisor_feedback,
	allowed_paths, allowed_tools, max_output_size, output_schema, contract_validation,
	checkpoint_data, session_id`

// scanTask decodes one tasks row into a Task pointer, translating JSON text
// columns and integer booleans exactly like _decode_task in the baseline.
func scanTask(scanner interface{ Scan(...any) error }) (*Task, error) {
	var t Task
	var requestJSON string
	var resultJSON sql.NullString
	var cancelRequested int
	var errorCallHistoryJSON sql.NullString
	var resultValidationJSON sql.NullString
	var supervisorFeedbackJSON sql.NullString
	var allowedPathsJSON sql.NullString
	var allowedToolsJSON sql.NullString
	var maxOutputSize sql.NullInt64
	var outputSchemaJSON sql.NullString
	var contractValidationJSON sql.NullString
	var checkpointDataJSON sql.NullString
	var sessionIDJSON sql.NullString
	err := scanner.Scan(
		&t.TaskID, &t.ParentTaskID, &t.IdempotencyKey, &t.SchemaVersion, &t.State, &t.Priority,
		&requestJSON, &t.RequestHash, &resultJSON, &t.ResultHash, &t.ReceiptHash,
		&t.Attempts, &t.MaxAttempts, &t.LeaseOwner, &t.LeaseToken, &t.LeaseExpiresAt,
		&cancelRequested, &t.CreatedAt, &t.UpdatedAt, &t.CompletedAt, &t.LastError,
		&t.OrchestratorID, &t.WorktreeID, &t.WorkerName, &t.Iter,
		&errorCallHistoryJSON, &resultValidationJSON, &supervisorFeedbackJSON,
		&allowedPathsJSON, &allowedToolsJSON, &maxOutputSize, &outputSchemaJSON, &contractValidationJSON,
		&checkpointDataJSON,
		&sessionIDJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan task row: %w", err)
	}
	t.Request = json.RawMessage(requestJSON)
	if resultJSON.Valid && resultJSON.String != "" {
		t.Result = json.RawMessage(resultJSON.String)
	}
	if errorCallHistoryJSON.Valid && errorCallHistoryJSON.String != "" {
		_ = json.Unmarshal([]byte(errorCallHistoryJSON.String), &t.ErrorCallHistory)
	}
	if resultValidationJSON.Valid && resultValidationJSON.String != "" {
		_ = json.Unmarshal([]byte(resultValidationJSON.String), &t.ResultValidation)
	}
	if supervisorFeedbackJSON.Valid && supervisorFeedbackJSON.String != "" {
		_ = json.Unmarshal([]byte(supervisorFeedbackJSON.String), &t.SupervisorFeedback)
	}
	if allowedPathsJSON.Valid && allowedPathsJSON.String != "" {
		// Contract fields are stored in separate columns, not in request
		_ = allowedPathsJSON
	}
	if allowedToolsJSON.Valid && allowedToolsJSON.String != "" {
		_ = allowedToolsJSON
	}
	_ = maxOutputSize
	_ = outputSchemaJSON
	if contractValidationJSON.Valid && contractValidationJSON.String != "" {
		_ = json.Unmarshal([]byte(contractValidationJSON.String), &t.ContractValidation)
	}
	if checkpointDataJSON.Valid && checkpointDataJSON.String != "" {
		_ = json.Unmarshal([]byte(checkpointDataJSON.String), &t.CheckpointData)
	}
	if sessionIDJSON.Valid && sessionIDJSON.String != "" {
		t.SessionID = &sessionIDJSON.String
	}
	t.CancelRequested = cancelRequested != 0
	return &t, nil
}

// GetTask returns nil (not an error) when no task carries taskID.
func (s *Store) GetTask(ctx context.Context, taskID string) (*Task, error) {
	row := s.db.QueryRow(
		"SELECT "+taskColumns+" FROM tasks WHERE task_id = ?", taskID,
	)
	t, err := scanTask(row)
	if err != nil || t == nil {
		return t, err
	}
	s.populateTaskReceiptExport(ctx, t)
	return t, nil
}

// GetTaskInSession returns a task only if it belongs to the given session.
func (s *Store) GetTaskInSession(ctx context.Context, taskID, sessionID string) (*Task, error) {
	row := s.db.QueryRow(
		"SELECT "+taskColumns+" FROM tasks WHERE task_id = ? AND session_id = ?", taskID, sessionID,
	)
	t, err := scanTask(row)
	if err != nil || t == nil {
		return t, err
	}
	s.populateTaskReceiptExport(ctx, t)
	return t, nil
}

func (s *Store) populateTaskReceiptExport(ctx context.Context, t *Task) {
	if t == nil {
		return
	}
	var statusJSON string
	err := s.db.QueryRowContext(ctx, "SELECT status_json FROM task_receipt_exports WHERE task_id = ?", t.TaskID).Scan(&statusJSON)
	if err == nil && statusJSON != "" {
		_ = json.Unmarshal([]byte(statusJSON), &t.ReceiptExport)
	}
}

// RecordReceiptExport persists the structured receipt export status for a task
// without modifying its execution state, result, result_hash, receipt_hash, or completed_at.
func (s *Store) RecordReceiptExport(ctx context.Context, taskID string, status ReceiptExportStatus) error {
	status.UpdatedAt = float64(s.clock().UnixNano()) / 1e9
	data, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("marshal receipt export status: %w", err)
	}

	var exists int
	err = s.db.QueryRowContext(ctx, "SELECT 1 FROM tasks WHERE task_id = ?", taskID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s", ErrUnknownTask, taskID)
	}
	if err != nil {
		return fmt.Errorf("verify task %s: %w", taskID, err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO task_receipt_exports (task_id, status_json, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(task_id) DO UPDATE SET
			status_json = excluded.status_json,
			updated_at = excluded.updated_at`,
		taskID, string(data), status.UpdatedAt)
	if err != nil {
		return fmt.Errorf("record receipt export for task %s: %w", taskID, err)
	}
	return nil
}

// ListTasks returns up to filter.Limit newest tasks, optionally narrowed to
// one state. limit must be between 1 and 200; unknown states are rejected.
func (s *Store) ListTasks(ctx context.Context, filter TaskFilter) ([]*Task, error) {
	limit := filter.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return nil, ErrLimitBounds
	}
	stateSet := false
	if filter.State != nil {
		if !IsValidState(*filter.State) {
			return nil, fmt.Errorf("%w: %s", ErrUnknownState, *filter.State)
		}
		stateSet = true
	}
	sessionSet := filter.SessionID != nil && *filter.SessionID != ""

	query := "SELECT " + taskColumns + " FROM tasks"
	args := []any{}
	whereClauses := []string{}
	if stateSet {
		whereClauses = append(whereClauses, "state = ?")
		args = append(args, *filter.State)
	}
	if sessionSet {
		whereClauses = append(whereClauses, "session_id = ?")
		args = append(args, *filter.SessionID)
	}
	if len(whereClauses) > 0 {
		query += " WHERE " + strings.Join(whereClauses, " AND ")
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	out := []*Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tasks: %w", err)
	}
	for _, t := range out {
		s.populateTaskReceiptExport(ctx, t)
	}
	return out, nil
}

// ListChildTasks returns all direct subtasks where parent_task_id = parentTaskID,
// ordered chronologically by created_at.
func (s *Store) ListChildTasks(ctx context.Context, parentTaskID string) ([]*Task, error) {
	if strings.TrimSpace(parentTaskID) == "" {
		return nil, errors.New("parent_task_id is required")
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+taskColumns+" FROM tasks WHERE parent_task_id = ? ORDER BY created_at ASC",
		parentTaskID,
	)
	if err != nil {
		return nil, fmt.Errorf("list child tasks for %s: %w", parentTaskID, err)
	}
	defer rows.Close()

	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate child tasks: %w", err)
	}
	return out, nil
}

// ListChildTasksInSession returns all direct subtasks where parent_task_id = parentTaskID
// and session_id matches, ordered chronologically by created_at.
func (s *Store) ListChildTasksInSession(ctx context.Context, parentTaskID, sessionID string) ([]*Task, error) {
	if strings.TrimSpace(parentTaskID) == "" {
		return nil, errors.New("parent_task_id is required")
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("session_id is required")
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+taskColumns+" FROM tasks WHERE parent_task_id = ? AND session_id = ? ORDER BY created_at ASC",
		parentTaskID, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list child tasks for %s in session %s: %w", parentTaskID, sessionID, err)
	}
	defer rows.Close()

	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate child tasks: %w", err)
	}
	return out, nil
}

const taskColumnsPrefixed = `t.task_id, t.parent_task_id, t.idempotency_key, t.schema_version, t.state, t.priority,
	t.request_json, t.request_hash, t.result_json, t.result_hash, t.receipt_hash,
	t.attempts, t.max_attempts, t.lease_owner, t.lease_token, t.lease_expires_at,
	t.cancel_requested, t.created_at, t.updated_at, t.completed_at, t.last_error,
	t.orchestrator_id, t.worktree_id, t.worker_name, t.iter,
	t.error_call_history, t.result_validation, t.supervisor_feedback,
	t.allowed_paths, t.allowed_tools, t.max_output_size, t.output_schema, t.contract_validation,
	t.checkpoint_data, t.session_id`

// GetTaskLineage returns the full ancestry chain of a task up to the root parent,
// starting with the root task and ending with the requested task.
// It executes via an atomic SQLite Recursive Common Table Expression (CTE) for O(1) query traversal.
func (s *Store) GetTaskLineage(ctx context.Context, taskID string) ([]*Task, error) {
	if taskID == "" {
		return nil, nil
	}

	query := `
WITH RECURSIVE lineage_tree(task_id, parent_task_id, depth) AS (
    SELECT task_id, parent_task_id, 0
    FROM tasks
    WHERE task_id = ?
    UNION ALL
    SELECT t.task_id, t.parent_task_id, lt.depth + 1
    FROM tasks t
    JOIN lineage_tree lt ON t.task_id = lt.parent_task_id
    WHERE lt.depth < 1000
)
SELECT ` + taskColumnsPrefixed + `
FROM tasks t
JOIN lineage_tree lt ON t.task_id = lt.task_id
ORDER BY lt.depth DESC;`

	rows, err := s.db.QueryContext(ctx, query, taskID)
	if err != nil {
		return nil, fmt.Errorf("query task lineage: %w", err)
	}
	defer rows.Close()

	var lineage []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan lineage task: %w", err)
		}
		if t != nil {
			lineage = append(lineage, t)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate task lineage: %w", err)
	}
	return lineage, nil
}

// GetTaskLineageInSession returns the full ancestry chain of a task within a session.
func (s *Store) GetTaskLineageInSession(ctx context.Context, taskID, sessionID string) ([]*Task, error) {
	if taskID == "" || sessionID == "" {
		return nil, nil
	}

	query := `
WITH RECURSIVE lineage_tree(task_id, parent_task_id, depth) AS (
    SELECT task_id, parent_task_id, 0
    FROM tasks
    WHERE task_id = ? AND session_id = ?
    UNION ALL
    SELECT t.task_id, t.parent_task_id, lt.depth + 1
    FROM tasks t
    JOIN lineage_tree lt ON t.task_id = lt.parent_task_id
    WHERE lt.depth < 1000 AND t.session_id = ?
)
SELECT ` + taskColumnsPrefixed + `
FROM tasks t
JOIN lineage_tree lt ON t.task_id = lt.task_id
ORDER BY lt.depth DESC;`

	rows, err := s.db.QueryContext(ctx, query, taskID, sessionID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query task lineage in session: %w", err)
	}
	defer rows.Close()

	var lineage []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan lineage task: %w", err)
		}
		if t != nil {
			lineage = append(lineage, t)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate task lineage: %w", err)
	}
	return lineage, nil
}

// ActiveTaskCount reports how many tasks currently hold a LEASED or RUNNING
// state. It deliberately ignores any page size used by ListTasks, matching
// active_task_count in the Python baseline.
func (s *Store) ActiveTaskCount(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM tasks WHERE state IN ('LEASED', 'RUNNING')",
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count active tasks: %w", err)
	}
	return count, nil
}

// ActiveTaskCountInSession reports how many tasks in a session currently hold
// a LEASED or RUNNING state.
func (s *Store) ActiveTaskCountInSession(ctx context.Context, sessionID string) (int, error) {
	if strings.TrimSpace(sessionID) == "" {
		return 0, errors.New("session_id is required")
	}
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM tasks WHERE state IN ('LEASED', 'RUNNING') AND session_id = ?",
		sessionID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count active tasks in session: %w", err)
	}
	return count, nil
}

// ErrLeaseLost reports that a lease token no longer matches the stored lease
// (stale worker, expired lease, or unknown task).
var ErrLeaseLost = errors.New("lease lost")

// ErrBusy indicates that a database operation timed out due to transient locking or SQLite busy state (#465).
var ErrBusy = errors.New("database busy")

// ErrNonCanonicalDerivedKey is returned when a derived idempotency key does not match the canonical shape.
var ErrNonCanonicalDerivedKey = errors.New("non-canonical derived idempotency key")

// canonicalizeIdempotencyKey trims whitespace and canonicalizes derived retry keys.
// If the key is a derived retry key (contains #r or #R suffix), it enforces the strict pattern:
// ^<task-id>#r[1-9][0-9]*$
// Trailing spaces are trimmed, uppercase #RN suffix is lowercased to #rN, and non-canonical shapes
// (leading zeros like #r01, non-digits, #r0, missing attempt number) are refused.
func canonicalizeIdempotencyKey(key string) (string, error) {
	trimmed := strings.TrimSpace(key)
	hashIdx := strings.LastIndex(trimmed, "#")
	if hashIdx == -1 {
		return trimmed, nil
	}
	prefix := trimmed[:hashIdx]
	suffix := trimmed[hashIdx:] // e.g. "#r1", "#R1", "#r01"
	if len(suffix) < 2 {
		return trimmed, nil
	}
	if suffix[1] != 'r' && suffix[1] != 'R' {
		return trimmed, nil
	}
	// It has #[rR]... suffix
	if prefix == "" {
		return "", fmt.Errorf("%w: missing task-id prefix in %q", ErrNonCanonicalDerivedKey, key)
	}
	digits := suffix[2:]
	if digits == "" {
		return "", fmt.Errorf("%w: missing attempt number in %q", ErrNonCanonicalDerivedKey, key)
	}
	// Must match strict pattern [1-9][0-9]*
	if digits[0] < '1' || digits[0] > '9' {
		return "", fmt.Errorf("%w: attempt number must start with 1-9 in %q", ErrNonCanonicalDerivedKey, key)
	}
	for i := 1; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return "", fmt.Errorf("%w: attempt number must contain only digits in %q", ErrNonCanonicalDerivedKey, key)
		}
	}
	return prefix + "#r" + digits, nil
}

func prepareSubmitRequest(req SubmitTaskRequest) (SubmitTaskRequest, string, string, error) {
	if req.IdempotencyKey == "" || strings.TrimSpace(req.IdempotencyKey) == "" {
		return req, "", "", errors.New("idempotency_key is required")
	}
	canonicalKey, err := canonicalizeIdempotencyKey(req.IdempotencyKey)
	if err != nil {
		return req, "", "", err
	}
	req.IdempotencyKey = canonicalKey
	if len(req.IdempotencyKey) > 200 {
		return req, "", "", errors.New("idempotency_key must be at most 200 characters")
	}
	if req.ParentTaskID != nil && *req.ParentTaskID == "" {
		return req, "", "", errors.New("parent_task_id must be a non-empty string when provided")
	}
	if req.MaxAttempts == 0 {
		req.MaxAttempts = 1
	}
	if req.Priority < -100 || req.Priority > 100 {
		return req, "", "", errors.New("priority must be an integer between -100 and 100")
	}
	if req.MaxAttempts < 1 || req.MaxAttempts > 10 {
		return req, "", "", errors.New("max_attempts must be an integer between 1 and 10")
	}
	if err := ValidateSubmitRequest(req); err != nil {
		return req, "", "", err
	}

	// Merge contract fields into payload
	payload := req.Payload
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}
	var payloadMap map[string]any
	if err := json.Unmarshal(payload, &payloadMap); err != nil {
		return req, "", "", fmt.Errorf("unmarshal payload: %w", err)
	}

	// Add contract fields to payload
	if len(req.AllowedPaths) > 0 {
		payloadMap["allowed_paths"] = req.AllowedPaths
	}
	if len(req.AllowedTools) > 0 {
		payloadMap["allowed_tools"] = req.AllowedTools
	}
	if req.MaxOutputSize > 0 {
		payloadMap["max_output_size"] = req.MaxOutputSize
	}
	if len(req.OutputSchema) > 0 {
		payloadMap["output_schema"] = json.RawMessage(req.OutputSchema)
	}

	mergedPayload, err := json.Marshal(payloadMap)
	if err != nil {
		return req, "", "", fmt.Errorf("marshal merged payload: %w", err)
	}
	payload = mergedPayload

	requestJSON, err := canonicalJSON(payload)
	if err != nil {
		return req, "", "", fmt.Errorf("canonicalize request: %w", err)
	}
	requestHash, err := contentHash(requestJSON)
	if err != nil {
		return req, "", "", fmt.Errorf("hash request: %w", err)
	}
	return req, requestJSON, requestHash, nil
}

func checkParentTask(ctx context.Context, tx *sql.Tx, parentTaskID *string) error {
	if parentTaskID == nil {
		return nil
	}
	var one int
	err := tx.QueryRowContext(ctx,
		"SELECT 1 FROM tasks WHERE task_id = ?", *parentTaskID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("unknown parent task: %s", *parentTaskID)
	}
	if err != nil {
		return fmt.Errorf("check parent task: %w", err)
	}
	return nil
}

func checkExistingTask(ctx context.Context, tx *sql.Tx, req SubmitTaskRequest, requestHash string) (*Task, error) {
	existing := tx.QueryRowContext(ctx,
		"SELECT "+taskColumns+" FROM tasks WHERE idempotency_key = ?", req.IdempotencyKey)
	task, scanErr := scanTask(existing)
	if scanErr != nil {
		return nil, fmt.Errorf("check idempotency key: %w", scanErr)
	}
	if task != nil {
		if task.RequestHash != requestHash ||
			parentKeyOf(task) != parentKeyOfReq(req) {
			return nil, errors.New("idempotency_key already exists with a different request")
		}
		task.Deduplicated = true
		return task, nil
	}
	return nil, nil
}

func insertNewTask(ctx context.Context, tx *sql.Tx, req SubmitTaskRequest, requestJSON string, requestHash string, now float64) (*Task, error) {
	taskID := uuid.NewString()

	// Extract contract fields from request JSON for column storage
	var requestMap map[string]any
	_ = json.Unmarshal([]byte(requestJSON), &requestMap)
	allowedPathsJSON, _ := json.Marshal(requestMap["allowed_paths"])
	allowedToolsJSON, _ := json.Marshal(requestMap["allowed_tools"])
	maxOutputSize := int64(0)
	if v, ok := requestMap["max_output_size"].(float64); ok {
		maxOutputSize = int64(v)
	}
	outputSchemaJSON, _ := json.Marshal(requestMap["output_schema"])

	sessionID := ""
	if req.SessionID != nil {
		sessionID = *req.SessionID
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO tasks(
			task_id, parent_task_id, idempotency_key, schema_version, state, priority,
			request_json, request_hash, max_attempts, created_at, updated_at,
			orchestrator_id, worktree_id, worker_name, iter,
			allowed_paths, allowed_tools, max_output_size, output_schema, contract_validation,
			session_id
		) VALUES (?, ?, ?, ?, 'QUEUED', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		taskID, req.ParentTaskID, req.IdempotencyKey, TaskSchemaVersion,
		req.Priority, requestJSON, requestHash, req.MaxAttempts, now, now,
		req.OrchestratorID, req.WorktreeID, req.WorkerName, req.Iter,
		string(allowedPathsJSON), string(allowedToolsJSON), maxOutputSize, string(outputSchemaJSON), "{}",
		sessionID)
	if err != nil {
		return nil, fmt.Errorf("insert task: %w", err)
	}
	details := map[string]any{
		"request_hash": requestHash,
		"priority":     req.Priority,
	}
	if req.ParentTaskID != nil {
		details["parent_task_id"] = *req.ParentTaskID
	}
	if req.OrchestratorID != nil {
		details["orchestrator_id"] = *req.OrchestratorID
	}
	if req.WorktreeID != nil {
		details["worktree_id"] = *req.WorktreeID
	}
	if req.WorkerName != nil {
		details["worker_name"] = *req.WorkerName
	}
	if req.Iter != 0 {
		details["iter"] = req.Iter
	}
	if err := insertTaskEvent(tx, taskID, "task_submitted", "orchestrator", details, now); err != nil {
		return nil, err
	}
	inserted, scanErr := scanTask(tx.QueryRowContext(ctx,
		"SELECT "+taskColumns+" FROM tasks WHERE task_id = ?", taskID))
	if scanErr != nil {
		return nil, fmt.Errorf("read submitted task: %w", scanErr)
	}
	if inserted == nil {
		return nil, errors.New("submitted task missing after insert")
	}
	return inserted, nil
}

// ErrSubmitRateLimited is returned when an actor exceeds the hourly submission rate limit.
type ErrSubmitRateLimited struct {
	Actor  string
	Limit  int
	Window time.Duration
}

func (e ErrSubmitRateLimited) Error() string {
	return fmt.Sprintf("submit rate limit exceeded: actor=%s limit=%d window=%s", e.Actor, e.Limit, e.Window)
}

func (e ErrSubmitRateLimited) Is(target error) bool {
	if _, ok := target.(*ErrSubmitRateLimited); ok {
		return true
	}
	if _, ok := target.(ErrSubmitRateLimited); ok {
		return true
	}
	return false
}

var (
	submitRateLimitMu      sync.RWMutex
	submitRateLimitPerHour = 0
)

// SetSubmitRateLimitPerHour sets the default hourly submission rate limit per actor (0 = unlimited).
func SetSubmitRateLimitPerHour(limit int) {
	submitRateLimitMu.Lock()
	defer submitRateLimitMu.Unlock()
	submitRateLimitPerHour = limit
}

// GetSubmitRateLimitPerHour returns the default hourly submission rate limit per actor.
func GetSubmitRateLimitPerHour() int {
	submitRateLimitMu.RLock()
	defer submitRateLimitMu.RUnlock()
	return submitRateLimitPerHour
}

// SetSubmitRateLimitPerHour sets the hourly submission rate limit per actor for this Store (0 = unlimited).
func (s *Store) SetSubmitRateLimitPerHour(limit int) {
	SetSubmitRateLimitPerHour(limit)
}

func extractActorFromReq(req SubmitTaskRequest, requestJSON string) string {
	if len(requestJSON) > 0 {
		var m map[string]any
		if err := json.Unmarshal([]byte(requestJSON), &m); err == nil {
			if a, ok := m["actor"].(string); ok && strings.TrimSpace(a) != "" {
				return strings.TrimSpace(a)
			}
		}
	}
	if req.OrchestratorID != nil && strings.TrimSpace(*req.OrchestratorID) != "" {
		return strings.TrimSpace(*req.OrchestratorID)
	}
	if req.WorkerName != nil && strings.TrimSpace(*req.WorkerName) != "" {
		return strings.TrimSpace(*req.WorkerName)
	}
	if req.SessionID != nil && strings.TrimSpace(*req.SessionID) != "" {
		return strings.TrimSpace(*req.SessionID)
	}
	return "operator"
}

// SubmitTask validates and inserts a QUEUED task, deduplicating on
// idempotency key plus request hash plus parent lineage.
func (s *Store) SubmitTask(ctx context.Context, req SubmitTaskRequest) (*Task, error) {
	req, requestJSON, requestHash, err := prepareSubmitRequest(req)
	if err != nil {
		return nil, err
	}
	now := float64(s.clock().UnixNano()) / 1e9

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin submit: %w", err)
	}
	defer tx.Rollback()

	if err := checkParentTask(ctx, tx, req.ParentTaskID); err != nil {
		return nil, err
	}

	task, err := checkExistingTask(ctx, tx, req, requestHash)
	if err != nil {
		return nil, err
	}
	if task != nil {
		return task, tx.Commit()
	}

	// Rate limit check per actor inside submit transaction (#485)
	limit := GetSubmitRateLimitPerHour()
	if limit > 0 {
		actor := extractActorFromReq(req, requestJSON)
		windowStart := now - 3600.0
		var count int
		err := tx.QueryRowContext(ctx, `
			SELECT COUNT(1) FROM tasks
			WHERE coalesce(json_extract(request_json, '$.actor'), orchestrator_id, worker_name, session_id, 'operator') = ?
			  AND created_at >= ?`, actor, windowStart).Scan(&count)
		if err != nil {
			return nil, fmt.Errorf("check submit rate limit: %w", err)
		}
		if count >= limit {
			fmt.Fprintf(os.Stderr, "submit rate limit exceeded: actor=%s limit=%d window=%s\n", actor, limit, time.Hour)
			return nil, &ErrSubmitRateLimited{
				Actor:  actor,
				Limit:  limit,
				Window: time.Hour,
			}
		}
	}

	inserted, err := insertNewTask(ctx, tx, req, requestJSON, requestHash, now)
	if err != nil {
		return nil, err
	}
	return inserted, tx.Commit()
}

func parentKeyOf(t *Task) string {
	if t.ParentTaskID == nil {
		return ""
	}
	return *t.ParentTaskID
}

func parentKeyOfReq(req SubmitTaskRequest) string {
	if req.ParentTaskID == nil {
		return ""
	}
	return *req.ParentTaskID
}

// ClaimTask leases the highest-priority QUEUED task to workerID under a
// BEGIN IMMEDIATE transaction so concurrent claimers elect a single winner.
