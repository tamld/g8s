package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/receipt"
	"github.com/tamld/g8s/internal/state"
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
	db     *sql.DB
	clock  func() time.Time
	dbPath string

	// leaseExecOverride allows tests to inject transient failures into extendLease (#465).
	leaseExecOverride func(ctx context.Context, query string, args ...any) (sql.Result, error)

	// receiptsMgr is the canonical receipt ledger (receipts.db, a sibling of
	// g8s.db) — lazily opened on first delegated-write consume (#346). It is
	// a separate file owned by the receipt schema, so there is no
	// user_version conflict with control-plane migrations.
	receiptsOnce sync.Once
	receiptsMgr  *receipt.Manager
	receiptsErr  error

	// signalMu protects signalFile and lazy creation of the tasks signal file (#481).
	signalMu   sync.Mutex
	signalPath string
	signalFile *os.File
}

// receiptsManager opens the receipt ledger lazily.
func (s *Store) receiptsManager() (*receipt.Manager, error) {
	s.receiptsOnce.Do(func() {
		receiptsPath := filepath.Join(filepath.Dir(s.dbPath), "receipts.db")
		s.receiptsMgr, s.receiptsErr = receipt.NewReceiptManager(receiptsPath, s.clock)
	})
	return s.receiptsMgr, s.receiptsErr
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
		sqlitePathEscape(dbPath),
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open control-plane database: %w", err)
	}
	signalPath := filepath.Join(filepath.Dir(dbPath), "signals", "tasks.jsonl")
	s := &Store{db: db, clock: clock, dbPath: dbPath, signalPath: signalPath}
	// #380: cap the connection pool — SQLite WAL allows concurrent readers
	// but a single writer; unlimited connections cause fd exhaustion and
	// "database is locked" under concurrent workers.
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
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
	// Close the lazily-opened receipt ledger too (#346): an open receipts.db
	// handle blocks TempDir cleanup on Windows. A ledger close error is
	// surfaced after the control-plane DB closes so both handles are
	// released either way.
	var ledgerErr error
	if s.receiptsMgr != nil {
		ledgerErr = s.receiptsMgr.Close()
		s.receiptsMgr = nil
	}
	s.signalMu.Lock()
	if s.signalFile != nil {
		if err := s.signalFile.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "[warn] controlplane: close signals file: %v\n", err)
		}
		s.signalFile = nil
	}
	s.signalMu.Unlock()
	dbErr := s.db.Close()
	if ledgerErr != nil {
		return fmt.Errorf("close receipt ledger: %w (db close: %v)", ledgerErr, dbErr)
	}
	return dbErr
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
		_ = applyAccessAuditSchema(conn)
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
	if err := migrateSessionsSchema(conn); err != nil {
		rollbackInit(conn)
		return err
	}
	if err := migrateReceiptLake(conn); err != nil {
		rollbackInit(conn)
		return err
	}
	if err := applyBriefsSchema(conn); err != nil {
		rollbackInit(conn)
		return err
	}
	if err := migrateBriefsSchema(conn); err != nil {
		rollbackInit(conn)
		return err
	}
	if err := applyEventLogSchema(conn); err != nil {
		rollbackInit(conn)
		return err
	}
	if err := migrateEventLogSchema(conn); err != nil {
		rollbackInit(conn)
		return err
	}
	if err := applyAccessAuditSchema(conn); err != nil {
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
	if version < 0 || (version > SchemaVersion && version != SchemaVersion) {
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
			last_error TEXT,
			orchestrator_id TEXT,
			worktree_id TEXT,
			worker_name TEXT,
			iter INTEGER DEFAULT 0
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
		`CREATE TABLE IF NOT EXISTS session_quotas (
			session_id TEXT PRIMARY KEY,
			max_concurrent_tasks INTEGER NOT NULL DEFAULT 10,
			max_tasks_per_hour INTEGER NOT NULL DEFAULT 100,
			created_at REAL NOT NULL,
			updated_at REAL NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id            TEXT PRIMARY KEY,
			started_at    REAL NOT NULL,
			heartbeat_at  REAL NOT NULL,
			status        TEXT NOT NULL DEFAULT 'active',
			mode          TEXT NOT NULL DEFAULT 'in_place',
			worktree_path TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_active
			ON sessions(id) WHERE status = 'active'`,
		`CREATE TABLE IF NOT EXISTS task_receipt_exports (
			task_id TEXT PRIMARY KEY REFERENCES tasks(task_id) ON DELETE CASCADE,
			status_json TEXT NOT NULL,
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
	var hasParent, hasErrorHistory, hasValidation, hasFeedback int
	var hasAllowedPaths, hasAllowedTools, hasMaxOutputSize, hasOutputSchema, hasContractValidation, hasCheckpointData, hasSessionID int
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
		switch name {
		case "parent_task_id":
			hasParent = 1
		case "error_call_history":
			hasErrorHistory = 1
		case "result_validation":
			hasValidation = 1
		case "supervisor_feedback":
			hasFeedback = 1
		case "allowed_paths":
			hasAllowedPaths = 1
		case "allowed_tools":
			hasAllowedTools = 1
		case "max_output_size":
			hasMaxOutputSize = 1
		case "output_schema":
			hasOutputSchema = 1
		case "contract_validation":
			hasContractValidation = 1
		case "checkpoint_data":
			hasCheckpointData = 1
		case "session_id":
			hasSessionID = 1
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
	if hasErrorHistory == 0 {
		if _, err := conn.ExecContext(context.Background(),
			"ALTER TABLE tasks ADD COLUMN error_call_history TEXT"); err != nil {
			return fmt.Errorf("migrate error_call_history column: %w", err)
		}
	}
	if hasValidation == 0 {
		if _, err := conn.ExecContext(context.Background(),
			"ALTER TABLE tasks ADD COLUMN result_validation TEXT"); err != nil {
			return fmt.Errorf("migrate result_validation column: %w", err)
		}
	}
	if hasFeedback == 0 {
		if _, err := conn.ExecContext(context.Background(),
			"ALTER TABLE tasks ADD COLUMN supervisor_feedback TEXT"); err != nil {
			return fmt.Errorf("migrate supervisor_feedback column: %w", err)
		}
	}
	if hasAllowedPaths == 0 {
		if _, err := conn.ExecContext(context.Background(),
			"ALTER TABLE tasks ADD COLUMN allowed_paths TEXT"); err != nil {
			return fmt.Errorf("migrate allowed_paths column: %w", err)
		}
	}
	if hasAllowedTools == 0 {
		if _, err := conn.ExecContext(context.Background(),
			"ALTER TABLE tasks ADD COLUMN allowed_tools TEXT"); err != nil {
			return fmt.Errorf("migrate allowed_tools column: %w", err)
		}
	}
	if hasMaxOutputSize == 0 {
		if _, err := conn.ExecContext(context.Background(),
			"ALTER TABLE tasks ADD COLUMN max_output_size INTEGER"); err != nil {
			return fmt.Errorf("migrate max_output_size column: %w", err)
		}
	}
	if hasOutputSchema == 0 {
		if _, err := conn.ExecContext(context.Background(),
			"ALTER TABLE tasks ADD COLUMN output_schema TEXT"); err != nil {
			return fmt.Errorf("migrate output_schema column: %w", err)
		}
	}
	if hasContractValidation == 0 {
		if _, err := conn.ExecContext(context.Background(),
			"ALTER TABLE tasks ADD COLUMN contract_validation TEXT"); err != nil {
			return fmt.Errorf("migrate contract_validation column: %w", err)
		}
	}
	if hasCheckpointData == 0 {
		if _, err := conn.ExecContext(context.Background(),
			"ALTER TABLE tasks ADD COLUMN checkpoint_data TEXT"); err != nil {
			return fmt.Errorf("migrate checkpoint_data column: %w", err)
		}
	}
	if hasSessionID == 0 {
		if _, err := conn.ExecContext(context.Background(),
			"ALTER TABLE tasks ADD COLUMN session_id TEXT"); err != nil {
			return fmt.Errorf("migrate session_id column: %w", err)
		}
	}
	// #381: create session index AFTER the column exists (migration-safe).
	if _, err := conn.ExecContext(context.Background(),
		"CREATE INDEX IF NOT EXISTS idx_tasks_session ON tasks(session_id, state)"); err != nil {
		return fmt.Errorf("create session index: %w", err)
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
			"session_id": {},
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

// migrateSessionsSchema upgrades pre-existing databases to the sessions
// registry (#393). Fresh databases already get the latest shape from
// applyBaseSchema and the lifecycle write_receipts DDL; this path covers
// databases created before SchemaVersion 11. All steps are idempotent.
func migrateSessionsSchema(conn *sql.Conn) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id            TEXT PRIMARY KEY,
			started_at    REAL NOT NULL,
			heartbeat_at  REAL NOT NULL,
			status        TEXT NOT NULL DEFAULT 'active',
			mode          TEXT NOT NULL DEFAULT 'in_place',
			worktree_path TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_active
			ON sessions(id) WHERE status = 'active'`,
	}
	for _, stmt := range stmts {
		if _, err := conn.ExecContext(context.Background(), stmt); err != nil {
			return fmt.Errorf("ensure sessions schema: %w", err)
		}
	}
	// The worker-side write_receipts mirror may predate the session_id
	// provenance column, or may not exist yet (it is ensured lazily by
	// lifecycle.go, fresh shapes include the column).
	var tableCount int
	if err := conn.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'write_receipts'").Scan(&tableCount); err != nil {
		return fmt.Errorf("inspect write_receipts presence: %w", err)
	}
	if tableCount == 0 {
		return nil
	}
	var hasSessionID int
	rows, err := conn.QueryContext(context.Background(), "PRAGMA table_info(write_receipts)")
	if err != nil {
		return fmt.Errorf("inspect write_receipts columns: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return fmt.Errorf("scan write_receipts columns: %w", err)
		}
		if name == "session_id" {
			hasSessionID = 1
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate write_receipts columns: %w", err)
	}
	rows.Close()
	if hasSessionID == 0 {
		if _, err := conn.ExecContext(context.Background(),
			"ALTER TABLE write_receipts ADD COLUMN session_id TEXT"); err != nil {
			return fmt.Errorf("migrate write_receipts.session_id: %w", err)
		}
	}
	return nil
}

// migrateReceiptLake is the idempotent migration for DELTA-17 (receipt lake
// wiring). It inspects tasks columns via PRAGMA table_info and adds any missing
// column with ALTER TABLE ADD COLUMN. It also creates idx_tasks_orchestrator_iter.
func migrateReceiptLake(conn *sql.Conn) error {
	expected := map[string]string{
		"orchestrator_id": "TEXT",
		"worktree_id":     "TEXT",
		"worker_name":     "TEXT",
		"iter":            "INTEGER DEFAULT 0",
	}
	present := map[string]struct{}{}
	rows, err := conn.QueryContext(context.Background(), "PRAGMA table_info(tasks)")
	if err != nil {
		return fmt.Errorf("inspect tasks columns for receipt lake: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return fmt.Errorf("scan tasks columns: %w", err)
		}
		present[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate tasks columns: %w", err)
	}

	cols := []string{"orchestrator_id", "worktree_id", "worker_name", "iter"}
	for _, col := range cols {
		if _, ok := present[col]; ok {
			continue
		}
		colDef := expected[col]
		if _, err := conn.ExecContext(context.Background(),
			fmt.Sprintf("ALTER TABLE tasks ADD COLUMN %s %s", col, colDef)); err != nil {
			return fmt.Errorf("migrate tasks.%s: %w", col, err)
		}
	}

	if _, err := conn.ExecContext(context.Background(),
		"CREATE INDEX IF NOT EXISTS idx_tasks_orchestrator_iter ON tasks(orchestrator_id, iter)"); err != nil {
		return fmt.Errorf("create idx_tasks_orchestrator_iter: %w", err)
	}

	return nil
}

// applyBriefsSchema creates the briefs persistence table with idempotent CREATE TABLE IF NOT EXISTS.
func applyBriefsSchema(conn *sql.Conn) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS briefs (
			id         TEXT PRIMARY KEY,
			title      TEXT NOT NULL,
			payload_md TEXT NOT NULL,
			dod_md     TEXT NOT NULL,
			issued_by  TEXT NOT NULL,
			issued_at  REAL NOT NULL,
			expires_at REAL NOT NULL,
			status     TEXT NOT NULL DEFAULT 'active'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_briefs_status ON briefs(status)`,
	}
	for _, stmt := range stmts {
		if _, err := conn.ExecContext(context.Background(), stmt); err != nil {
			return fmt.Errorf("apply briefs schema: %w", err)
		}
	}
	return nil
}

// migrateBriefsSchema is the defensive upgrade for pre-v6 databases.
func migrateBriefsSchema(conn *sql.Conn) error {
	expected := map[string]string{
		"id":         "TEXT",
		"title":      "TEXT",
		"payload_md": "TEXT",
		"dod_md":     "TEXT",
		"issued_by":  "TEXT",
		"issued_at":  "REAL",
		"expires_at": "REAL",
		"status":     "TEXT DEFAULT 'active'",
	}
	present := map[string]struct{}{}
	rows, err := conn.QueryContext(context.Background(), "PRAGMA table_info(briefs)")
	if err != nil {
		return fmt.Errorf("inspect briefs columns: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return fmt.Errorf("scan briefs columns: %w", err)
		}
		present[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate briefs columns: %w", err)
	}
	for col, colDef := range expected {
		if _, ok := present[col]; ok {
			continue
		}
		if _, err := conn.ExecContext(context.Background(),
			fmt.Sprintf("ALTER TABLE briefs ADD COLUMN %s %s", col, colDef)); err != nil {
			return fmt.Errorf("migrate briefs.%s: %w", col, err)
		}
	}
	return nil
}

// applyEventLogSchema creates the event_log persistence table with idempotent CREATE TABLE IF NOT EXISTS.
func applyEventLogSchema(conn *sql.Conn) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS event_log (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			subject_id TEXT NOT NULL,
			subject    TEXT NOT NULL DEFAULT '',
			from_state TEXT NOT NULL,
			to_state   TEXT NOT NULL,
			event      TEXT NOT NULL,
			actor      TEXT NOT NULL,
			reason     TEXT NOT NULL,
			ts         REAL NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_event_log_subject ON event_log(subject_id, id ASC)`,
	}
	for _, stmt := range stmts {
		if _, err := conn.ExecContext(context.Background(), stmt); err != nil {
			return fmt.Errorf("apply event log schema: %w", err)
		}
	}
	return nil
}

// migrateEventLogSchema is the defensive upgrade for pre-v7 databases.
func migrateEventLogSchema(conn *sql.Conn) error {
	return applyEventLogSchema(conn)
}

// LogStateEvent appends an event record to the event_log table.
func (s *Store) LogStateEvent(ctx context.Context, subjectID string, subject state.Subject, from, to state.State, event state.Event, actor, reason string) error {
	return state.Log(ctx, s.db, subjectID, subject, from, to, event, actor, reason, s.clock())
}

// ReplayStateEvents returns all logged events for subjectID in ascending chronological order.
func (s *Store) ReplayStateEvents(ctx context.Context, subjectID string) ([]state.EventRecord, error) {
	return state.Replay(ctx, s.db, subjectID)
}

// ShowStateEvents returns the last N events for subjectID in chronological order.
func (s *Store) ShowStateEvents(ctx context.Context, subjectID string, limit int) ([]state.EventRecord, error) {
	return state.Show(ctx, s.db, subjectID, limit)
}

// Clock returns the store's current time from its configured clock.
func (s *Store) Clock() time.Time {
	return s.clock()
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
	if err := insertAccessAuditTx(tx, ts, actor, mapTaskEventToAuditAction(eventType), taskID, encoded); err != nil {
		return fmt.Errorf("insert access audit event: %w", err)
	}
	return nil
}
