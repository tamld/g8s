package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// Sentinel errors surfaced by query validation.
var (
	ErrUnknownState = errors.New("unknown task state")
	ErrLimitBounds  = errors.New("limit must be between 1 and 200")
)

// Store is the concrete SQLite-backed implementation of ControlPlane. It is
// safe for concurrent use: database/sql serializes access through its pool
// and all mutations run inside BEGIN IMMEDIATE transactions.
type Store struct {
	db    *sql.DB
	clock func() time.Time
}

// NewControlPlane opens (creating if needed) the control-plane database at
// dbPath and initializes or migrates its schema under an exclusive lock.
// A nil clock falls back to time.Now; tests inject deterministic clocks per
// the constitution's injectable-clock axiom.
func NewControlPlane(dbPath string, clock func() time.Time) (*Store, error) {
	if clock == nil {
		clock = time.Now
	}
	dsn := fmt.Sprintf(
		"file:%s?_txlock=immediate&_pragma=busy_timeout(30000)&_pragma=foreign_keys(ON)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)",
		url.PathEscape(dbPath),
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open control-plane database: %w", err)
	}
	s := &Store{db: db, clock: clock}
	if err := s.initialize(); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(dbPath, 0o600); err != nil {
		db.Close()
		return nil, fmt.Errorf("restrict control-plane database permissions: %w", err)
	}
	return s, nil
}

// Close releases the underlying connection pool.
func (s *Store) Close() error {
	return s.db.Close()
}

// initialize runs the schema gate inside one EXCLUSIVE transaction on a single
// pinned connection, mirroring _initialize in the Python baseline: accept
// user_version 0 (fresh), 1, 2, or current; migrate legacy layouts by adding
// parent_task_id when absent; reject anything else.
func (s *Store) initialize() error {
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		return fmt.Errorf("pin initialization connection: %w", err)
	}
	defer conn.Close()

	// Fast-path: if database already matches current schema version, skip exclusive lock
	var version int
	if err := conn.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&version); err == nil && version == SchemaVersion {
		return nil
	}

	if _, err := conn.ExecContext(context.Background(), "BEGIN EXCLUSIVE"); err != nil {
		return fmt.Errorf("begin exclusive schema transaction: %w", err)
	}

	if err := checkSchemaVersion(conn); err != nil {
		rollbackInit(conn)
		return err
	}
	if err := applyBaseSchema(conn); err != nil {
		rollbackInit(conn)
		return err
	}
	if err := migrateTasksTable(conn); err != nil {
		rollbackInit(conn)
		return err
	}
	if err := applySupervisorSchema(conn); err != nil {
		rollbackInit(conn)
		return err
	}
	if err := migrateSupervisorSchema(conn); err != nil {
		rollbackInit(conn)
		return err
	}

	if _, err := conn.ExecContext(context.Background(),
		fmt.Sprintf("PRAGMA user_version = %d", SchemaVersion)); err != nil {
		rollbackInit(conn)
		return fmt.Errorf("record schema version: %w", err)
	}

	if _, err := conn.ExecContext(context.Background(), "COMMIT"); err != nil {
		return fmt.Errorf("commit schema transaction: %w", err)
	}
	return nil
}

func checkSchemaVersion(conn *sql.Conn) error {
	version := 0
	if err := conn.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version < 0 || (version > 3 && version != SchemaVersion) {
		return fmt.Errorf("unsupported control-plane schema version %d; expected %d", version, SchemaVersion)
	}
	return nil
}

func applyBaseSchema(conn *sql.Conn) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS tasks (
			task_id TEXT PRIMARY KEY,
			parent_task_id TEXT REFERENCES tasks(task_id),
			idempotency_key TEXT NOT NULL UNIQUE,
			schema_version TEXT NOT NULL,
			state TEXT NOT NULL,
			priority INTEGER NOT NULL,
			request_json TEXT NOT NULL,
			request_hash TEXT NOT NULL,
			result_json TEXT,
			result_hash TEXT,
			receipt_hash TEXT,
			attempts INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL,
			lease_owner TEXT,
			lease_token TEXT,
			lease_expires_at REAL,
			cancel_requested INTEGER NOT NULL DEFAULT 0,
			created_at REAL NOT NULL,
			updated_at REAL NOT NULL,
			completed_at REAL,
			last_error TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_claim
			ON tasks(state, priority DESC, created_at ASC)`,
		`CREATE TABLE IF NOT EXISTS task_events (
			event_id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL REFERENCES tasks(task_id) ON DELETE CASCADE,
			timestamp REAL NOT NULL,
			event_type TEXT NOT NULL,
			actor TEXT NOT NULL,
			details_json TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_task_events_task
			ON task_events(task_id, event_id)`,
		`CREATE TABLE IF NOT EXISTS control_plane_maintenance (
			singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
			owner TEXT NOT NULL,
			expires_at REAL NOT NULL,
			updated_at REAL NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := conn.ExecContext(context.Background(), stmt); err != nil {
			return fmt.Errorf("apply control-plane schema: %w", err)
		}
	}
	return nil
}

func migrateTasksTable(conn *sql.Conn) error {
	var hasParent int
	parentRows, err := conn.QueryContext(context.Background(), "PRAGMA table_info(tasks)")
	if err != nil {
		return fmt.Errorf("inspect tasks columns: %w", err)
	}
	defer parentRows.Close()
	for parentRows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := parentRows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return fmt.Errorf("scan tasks columns: %w", err)
		}
		if name == "parent_task_id" {
			hasParent = 1
		}
	}
	if err := parentRows.Err(); err != nil {
		return fmt.Errorf("iterate tasks columns: %w", err)
	}
	if hasParent == 0 {
		if _, err := conn.ExecContext(context.Background(),
			"ALTER TABLE tasks ADD COLUMN parent_task_id TEXT REFERENCES tasks(task_id)"); err != nil {
			return fmt.Errorf("migrate parent_task_id column: %w", err)
		}
	}
	return nil
}

// applySupervisorSchema creates the three supervisor persistence tables
// required by internal/supervisor (Concern A). CREATE TABLE IF NOT EXISTS is
// idempotent; the table_info check in migrateSupervisorSchema covers any
// partial upgrade from a pre-v4 database where the table existed but lacked
// a column.
func applySupervisorSchema(conn *sql.Conn) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS supervisor_tasks (
			id              TEXT PRIMARY KEY,
			state           TEXT NOT NULL,
			envelope_json   TEXT NOT NULL,
			approach_idx    INTEGER NOT NULL DEFAULT 0,
			attempt_idx     INTEGER NOT NULL DEFAULT 0,
			parent_task_id  TEXT,
			created_at      REAL NOT NULL,
			updated_at      REAL NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS supervisor_decisions (
			id              TEXT PRIMARY KEY,
			task_id         TEXT NOT NULL REFERENCES supervisor_tasks(id),
			kind            TEXT NOT NULL,
			payload_json    TEXT NOT NULL,
			created_at      REAL NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_supervisor_decisions_task_id
			ON supervisor_decisions(task_id)`,
		`CREATE TABLE IF NOT EXISTS supervisor_metrics (
			supervisor_task_id    TEXT PRIMARY KEY REFERENCES supervisor_tasks(id),
			envelope_score        REAL NOT NULL,
			first_attempt_success INTEGER NOT NULL,
			attempts_to_success   INTEGER NOT NULL,
			approaches_to_success INTEGER NOT NULL,
			rca_confidence_avg    REAL NOT NULL,
			cycle_duration_seconds REAL NOT NULL,
			escalation_count      INTEGER NOT NULL,
			false_escalation_rate  REAL NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := conn.ExecContext(context.Background(), stmt); err != nil {
			return fmt.Errorf("apply supervisor schema: %w", err)
		}
	}
	return nil
}

// migrateSupervisorSchema is the defensive upgrade for pre-v4 databases. It
// inspects each supervisor table's column set via PRAGMA table_info and
// adds any missing column with ALTER TABLE ADD COLUMN. CREATE TABLE IF NOT
// EXISTS already creates the latest shape on fresh databases, so this path
// only fires when the table predates the current schema generation.
func migrateSupervisorSchema(conn *sql.Conn) error {
	expected := map[string]map[string]struct{}{
		"supervisor_tasks": {
			"id": {}, "state": {}, "envelope_json": {},
			"approach_idx": {}, "attempt_idx": {},
			"parent_task_id": {}, "created_at": {}, "updated_at": {},
		},
		"supervisor_decisions": {
			"id": {}, "task_id": {}, "kind": {},
			"payload_json": {}, "created_at": {},
		},
		"supervisor_metrics": {
			"supervisor_task_id": {}, "envelope_score": {},
			"first_attempt_success": {}, "attempts_to_success": {},
			"approaches_to_success": {}, "rca_confidence_avg": {},
			"cycle_duration_seconds": {}, "escalation_count": {},
			"false_escalation_rate": {},
		},
	}
	for table, cols := range expected {
		present := map[string]struct{}{}
		rows, err := conn.QueryContext(context.Background(), "PRAGMA table_info("+table+")")
		if err != nil {
			return fmt.Errorf("inspect %s columns: %w", table, err)
		}
		for rows.Next() {
			var cid int
			var name, colType string
			var notNull int
			var dflt sql.NullString
			var pk int
			if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
				rows.Close()
				return fmt.Errorf("scan %s columns: %w", table, err)
			}
			present[name] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate %s columns: %w", table, err)
		}
		rows.Close()
		for col := range cols {
			if _, ok := present[col]; ok {
				continue
			}
			if _, err := conn.ExecContext(context.Background(),
				"ALTER TABLE "+table+" ADD COLUMN "+col+" TEXT"); err != nil {
				return fmt.Errorf("migrate %s.%s: %w", table, col, err)
			}
		}
	}
	return nil
}

func rollbackInit(conn *sql.Conn) {
	_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
}

// insertTaskEvent is the package-level event writer shared by helpers that
// operate on a bare *sql.Tx without a Store receiver.
func insertTaskEvent(tx *sql.Tx, taskID string, eventType string, actor string, details any, ts float64) error {
	payload := details
	if payload == nil {
		payload = map[string]any{}
	}
	encoded, err := canonicalJSON(payload)
	if err != nil {
		return fmt.Errorf("encode event details: %w", err)
	}
	if _, err := tx.ExecContext(context.Background(),
		"INSERT INTO task_events(task_id, timestamp, event_type, actor, details_json) VALUES (?, ?, ?, ?, ?)",
		taskID, ts, eventType, actor, encoded); err != nil {
		return fmt.Errorf("insert task event: %w", err)
	}
	return nil
}

const taskColumns = `task_id, parent_task_id, idempotency_key, schema_version, state, priority,
	request_json, request_hash, result_json, result_hash, receipt_hash,
	attempts, max_attempts, lease_owner, lease_token, lease_expires_at,
	cancel_requested, created_at, updated_at, completed_at, last_error`

// scanTask decodes one tasks row into a Task pointer, translating JSON text
// columns and integer booleans exactly like _decode_task in the baseline.
func scanTask(scanner interface{ Scan(...any) error }) (*Task, error) {
	var t Task
	var requestJSON string
	var resultJSON sql.NullString
	var cancelRequested int
	err := scanner.Scan(
		&t.TaskID, &t.ParentTaskID, &t.IdempotencyKey, &t.SchemaVersion, &t.State, &t.Priority,
		&requestJSON, &t.RequestHash, &resultJSON, &t.ResultHash, &t.ReceiptHash,
		&t.Attempts, &t.MaxAttempts, &t.LeaseOwner, &t.LeaseToken, &t.LeaseExpiresAt,
		&cancelRequested, &t.CreatedAt, &t.UpdatedAt, &t.CompletedAt, &t.LastError,
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
	t.CancelRequested = cancelRequested != 0
	return &t, nil
}

// GetTask returns nil (not an error) when no task carries taskID.
func (s *Store) GetTask(_ context.Context, taskID string) (*Task, error) {
	row := s.db.QueryRow(
		"SELECT "+taskColumns+" FROM tasks WHERE task_id = ?", taskID,
	)
	return scanTask(row)
}

// ListTasks returns up to filter.Limit newest tasks, optionally narrowed to
// one state. limit must be between 1 and 200; unknown states are rejected.
func (s *Store) ListTasks(_ context.Context, filter TaskFilter) ([]*Task, error) {
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
	query := "SELECT " + taskColumns + " FROM tasks"
	args := []any{}
	if stateSet {
		query += " WHERE state = ?"
		args = append(args, *filter.State)
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

const taskColumnsPrefixed = `t.task_id, t.parent_task_id, t.idempotency_key, t.schema_version, t.state, t.priority,
	t.request_json, t.request_hash, t.result_json, t.result_hash, t.receipt_hash,
	t.attempts, t.max_attempts, t.lease_owner, t.lease_token, t.lease_expires_at,
	t.cancel_requested, t.created_at, t.updated_at, t.completed_at, t.last_error`

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

// ErrLeaseLost reports that a lease token no longer matches the stored lease
// (stale worker, expired lease, or unknown task).
var ErrLeaseLost = errors.New("lease lost")

func prepareSubmitRequest(req SubmitTaskRequest) (SubmitTaskRequest, string, string, error) {
	if req.IdempotencyKey == "" || strings.TrimSpace(req.IdempotencyKey) == "" {
		return req, "", "", errors.New("idempotency_key is required")
	}
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
	payload := req.Payload
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}
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
	_, err := tx.ExecContext(ctx, `
		INSERT INTO tasks(
			task_id, parent_task_id, idempotency_key, schema_version, state, priority,
			request_json, request_hash, max_attempts, created_at, updated_at
		) VALUES (?, ?, ?, ?, 'QUEUED', ?, ?, ?, ?, ?, ?)`,
		taskID, req.ParentTaskID, req.IdempotencyKey, TaskSchemaVersion,
		req.Priority, requestJSON, requestHash, req.MaxAttempts, now, now)
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
func (s *Store) ClaimTask(ctx context.Context, workerID string, leaseDurationSeconds int) (*Task, error) {
	if strings.TrimSpace(workerID) == "" {
		return nil, errors.New("worker_id is required")
	}
	if leaseDurationSeconds <= 0 {
		return nil, errors.New("lease_seconds must be positive")
	}
	now := float64(s.clock().UnixNano()) / 1e9
	leaseSeconds := float64(leaseDurationSeconds)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin claim: %w", err)
	}
	defer tx.Rollback()

	if _, err := reconcileExpiredTx(ctx, tx, now); err != nil {
		return nil, err
	}
	var expiresAt float64
	err = tx.QueryRowContext(ctx,
		"SELECT expires_at FROM control_plane_maintenance WHERE singleton = 1").Scan(&expiresAt)
	switch {
	case err == nil && expiresAt > now:
		return nil, tx.Commit()
	case err == nil:
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM control_plane_maintenance WHERE singleton = 1"); err != nil {
			return nil, fmt.Errorf("clear stale maintenance: %w", err)
		}
	case !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("read maintenance: %w", err)
	}

	row := tx.QueryRowContext(ctx, `
		SELECT `+taskColumns+` FROM tasks
		WHERE state = 'QUEUED' AND cancel_requested = 0 AND attempts < max_attempts
		ORDER BY priority DESC, created_at ASC
		LIMIT 1`)
	candidate, scanErr := scanTask(row)
	if scanErr != nil {
		return nil, fmt.Errorf("select claim candidate: %w", scanErr)
	}
	if candidate == nil {
		return nil, tx.Commit()
	}

	leaseToken := uuid.NewString()
	res, err := tx.ExecContext(ctx, `
		UPDATE tasks
		SET state = 'LEASED', lease_owner = ?, lease_token = ?,
		    lease_expires_at = ?, attempts = attempts + 1, updated_at = ?
		WHERE task_id = ? AND state = 'QUEUED' AND cancel_requested = 0`,
		workerID, leaseToken, now+leaseSeconds, now, candidate.TaskID)
	if err != nil {
		return nil, fmt.Errorf("claim task: %w", err)
	}
	if affected, _ := res.RowsAffected(); affected != 1 {
		return nil, tx.Commit()
	}
	if err := insertTaskEvent(tx, candidate.TaskID, "task_claimed", workerID, map[string]any{
		"lease_token":   leaseToken,
		"lease_seconds": leaseSeconds,
	}, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetTask(ctx, candidate.TaskID)
}

// StartTask promotes a LEASED task to RUNNING when the caller presents the
// matching worker and lease token; it reports false on any mismatch.
func (s *Store) StartTask(taskID, workerID, leaseToken string) bool {
	now := float64(s.clock().UnixNano()) / 1e9
	tx, err := s.db.Begin()
	if err != nil {
		return false
	}
	defer tx.Rollback()
	res, err := tx.Exec(`
		UPDATE tasks SET state = 'RUNNING', updated_at = ?
		WHERE task_id = ? AND state = 'LEASED'
		  AND lease_owner = ? AND lease_token = ? AND cancel_requested = 0`,
		now, taskID, workerID, leaseToken)
	if err != nil {
		return false
	}
	if affected, _ := res.RowsAffected(); affected != 1 {
		return false
	}
	if err := insertTaskEvent(tx, taskID, "task_started", workerID, map[string]any{}, now); err != nil {
		return false
	}
	return tx.Commit() == nil
}

// RenewHeartbeat extends the lease of a LEASED or RUNNING task held by
// workerID. It implements the spec interface exactly (owner-scoped, no
// token argument); callers holding a lease token should prefer Heartbeat.
// A mismatched owner yields ErrLeaseLost.
func (s *Store) RenewHeartbeat(ctx context.Context, taskID, workerID string, extensionSeconds int) error {
	return s.extendLease(ctx, taskID, workerID, "", extensionSeconds)
}

// Heartbeat extends the lease only when the caller presents the exact
// lease token issued at claim time; any other token (including empty)
// yields ErrLeaseLost.
func (s *Store) Heartbeat(ctx context.Context, taskID, workerID, leaseToken string, extensionSeconds int) error {
	if leaseToken == "" {
		return fmt.Errorf("%w: task %s", ErrLeaseLost, taskID)
	}
	return s.extendLease(ctx, taskID, workerID, leaseToken, extensionSeconds)
}

func (s *Store) extendLease(ctx context.Context, taskID, workerID, leaseToken string, extensionSeconds int) error {
	now := float64(s.clock().UnixNano()) / 1e9
	query := `
		UPDATE tasks SET lease_expires_at = ?, updated_at = ?
		WHERE task_id = ? AND state IN ('LEASED', 'RUNNING')
		  AND lease_owner = ? AND cancel_requested = 0`
	args := []any{now + float64(extensionSeconds), now, taskID, workerID}
	if leaseToken != "" {
		query += ` AND lease_token = ?`
		args = append(args, leaseToken)
	}
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	if affected, _ := res.RowsAffected(); affected != 1 {
		return fmt.Errorf("%w: task %s", ErrLeaseLost, taskID)
	}
	return nil
}

// ExecutionSignal tells a RUNNING worker whether to continue ("active"),
// stop ("cancel_requested"), or treat its lease as gone ("lease_lost").
func (s *Store) ExecutionSignal(taskID, workerID, leaseToken string) string {
	var (
		state           string
		cancelRequested int
		leaseOwner      sql.NullString
		storedToken     sql.NullString
	)
	err := s.db.QueryRow(`
		SELECT state, cancel_requested, lease_owner, lease_token
		FROM tasks WHERE task_id = ?`, taskID,
	).Scan(&state, &cancelRequested, &leaseOwner, &storedToken)
	if err != nil || state != StateRunning ||
		leaseOwner.String != workerID || storedToken.String != leaseToken {
		return "lease_lost"
	}
	if cancelRequested != 0 {
		return "cancel_requested"
	}
	return "active"
}

// ReconcileExpired requeues expired leases (or finalizes them once their
// retry budget is exhausted) and returns how many tasks were reconciled.
func (s *Store) ReconcileExpired(ctx context.Context) (int, error) {
	now := float64(s.clock().UnixNano()) / 1e9
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin reconcile: %w", err)
	}
	defer tx.Rollback()
	count, err := reconcileExpiredTx(ctx, tx, now)
	if err != nil {
		return 0, err
	}
	return count, tx.Commit()
}

// reconcileExpiredTx applies lease-expiry transitions inside an open
// transaction: CANCELLED for cancel-requested tasks, FAILED at retry-budget
// exhaustion, otherwise requeue.
func reconcileExpiredTx(ctx context.Context, tx *sql.Tx, now float64) (int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT `+taskColumns+` FROM tasks
		WHERE state IN ('LEASED', 'RUNNING')
		  AND lease_expires_at IS NOT NULL
		  AND lease_expires_at <= ?`, now)
	if err != nil {
		return 0, fmt.Errorf("select expired leases: %w", err)
	}
	var expired []*Task
	for rows.Next() {
		t, scanErr := scanTask(rows)
		if scanErr != nil {
			rows.Close()
			return 0, fmt.Errorf("scan expired lease: %w", scanErr)
		}
		expired = append(expired, t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate expired leases: %w", err)
	}
	rows.Close()

	reconciled := 0
	for _, t := range expired {
		nextState := StateQueued
		lastError := "lease expired; task requeued"
		switch {
		case t.CancelRequested:
			nextState = StateCancelled
			lastError = "cancelled after lease expiry"
		case t.Attempts >= t.MaxAttempts:
			nextState = StateFailed
			lastError = "lease expired and retry budget exhausted"
		}
		completedAt := any(nil)
		if nextState == StateFailed || nextState == StateCancelled {
			completedAt = now
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE tasks
			SET state = ?, lease_owner = NULL, lease_token = NULL,
			    lease_expires_at = NULL, updated_at = ?,
			    completed_at = CASE WHEN ? IN ('FAILED', 'CANCELLED') THEN ? ELSE NULL END,
			    last_error = ?
			WHERE task_id = ? AND lease_token = ?`,
			nextState, now, nextState, completedAt, lastError, t.TaskID, deref(t.LeaseToken)); err != nil {
			return reconciled, fmt.Errorf("reconcile task %s: %w", t.TaskID, err)
		}
		owner := ""
		if t.LeaseOwner != nil {
			owner = *t.LeaseOwner
		}
		if err := insertTaskEvent(tx, t.TaskID, "lease_expired", "reconciler",
			map[string]any{"next_state": nextState, "previous_owner": owner}, now); err != nil {
			return reconciled, err
		}

		if nextState == StateCancelled || nextState == StateFailed {
			requestJSON := string(t.Request)
			if err := redactPayload(&requestJSON); err != nil {
				return reconciled, fmt.Errorf("redact task %s: %w", t.TaskID, err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE tasks SET request_json = ? WHERE task_id = ?`,
				requestJSON, t.TaskID); err != nil {
				return reconciled, fmt.Errorf("store redacted request %s: %w", t.TaskID, err)
			}
			fresh := *t
			fresh.State = nextState
			fresh.CompletedAt = &now
			_, receiptHash, err := buildReceiptTx(tx, &fresh)
			if err != nil {
				return reconciled, fmt.Errorf("receipt for task %s: %w", t.TaskID, err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE tasks SET receipt_hash = ? WHERE task_id = ?`,
				receiptHash, t.TaskID); err != nil {
				return reconciled, fmt.Errorf("seal receipt %s: %w", t.TaskID, err)
			}
		}
		reconciled++
	}
	return reconciled, nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
