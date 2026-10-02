package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/receipt"
	"github.com/tamld/g8s/internal/state"
	"modernc.org/sqlite"
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
	return nil
}

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
func (s *Store) GetTask(_ context.Context, taskID string) (*Task, error) {
	row := s.db.QueryRow(
		"SELECT "+taskColumns+" FROM tasks WHERE task_id = ?", taskID,
	)
	return scanTask(row)
}

// GetTaskInSession returns a task only if it belongs to the given session.
func (s *Store) GetTaskInSession(_ context.Context, taskID, sessionID string) (*Task, error) {
	row := s.db.QueryRow(
		"SELECT "+taskColumns+" FROM tasks WHERE task_id = ? AND session_id = ?", taskID, sessionID,
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
func (s *Store) ClaimTask(ctx context.Context, workerID string, leaseDurationSeconds int) (*Task, error) {
	return s.claimTaskInternal(ctx, workerID, leaseDurationSeconds, "")
}

// ClaimTaskProvider leases the highest-priority QUEUED task matching provider to workerID.
// Empty provider = no filter (delegates to existing ClaimTask logic).
func (s *Store) ClaimTaskProvider(ctx context.Context, workerID string, leaseDurationSeconds int, provider string) (*Task, error) {
	if provider == "" {
		return s.ClaimTask(ctx, workerID, leaseDurationSeconds)
	}
	return s.claimTaskInternal(ctx, workerID, leaseDurationSeconds, provider)
}

func (s *Store) claimTaskInternal(ctx context.Context, workerID string, leaseDurationSeconds int, provider string) (*Task, error) {
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

	var reconcileSignals []pendingSignal
	if _, reconcileSignals, err = reconcileExpiredTx(ctx, tx, now); err != nil {
		return nil, err
	}
	commitAndSignal := func() error {
		if err := tx.Commit(); err != nil {
			return err
		}
		for _, sig := range reconcileSignals {
			s.appendTaskSignal(sig.taskID, sig.from, sig.to, s.clock())
		}
		return nil
	}
	var expiresAt float64
	err = tx.QueryRowContext(ctx,
		"SELECT expires_at FROM control_plane_maintenance WHERE singleton = 1").Scan(&expiresAt)
	switch {
	case err == nil && expiresAt > now:
		return nil, commitAndSignal()
	case err == nil:
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM control_plane_maintenance WHERE singleton = 1"); err != nil {
			return nil, fmt.Errorf("clear stale maintenance: %w", err)
		}
	case !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("read maintenance: %w", err)
	}

	var row *sql.Row
	if provider != "" {
		row = tx.QueryRowContext(ctx, `
		SELECT `+taskColumns+` FROM tasks
		WHERE state = 'QUEUED' AND cancel_requested = 0 AND attempts < max_attempts
		  AND json_extract(request_json, '$.provider') = ?
		  AND (json_extract(request_json, '$.not_before') IS NULL OR json_extract(request_json, '$.not_before') <= ?)
		ORDER BY priority DESC, created_at ASC
		LIMIT 1`, provider, now)
	} else {
		row = tx.QueryRowContext(ctx, `
		SELECT `+taskColumns+` FROM tasks
		WHERE state = 'QUEUED' AND cancel_requested = 0 AND attempts < max_attempts
		  AND (json_extract(request_json, '$.not_before') IS NULL OR json_extract(request_json, '$.not_before') <= ?)
		ORDER BY priority DESC, created_at ASC
		LIMIT 1`, now)
	}
	candidate, scanErr := scanTask(row)
	if scanErr != nil {
		return nil, fmt.Errorf("select claim candidate: %w", scanErr)
	}
	if candidate == nil {
		return nil, commitAndSignal()
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
		return nil, commitAndSignal()
	}
	if err := insertTaskEvent(tx, candidate.TaskID, "task_claimed", workerID, map[string]any{
		"lease_token":   leaseToken,
		"lease_seconds": leaseSeconds,
	}, now); err != nil {
		return nil, err
	}
	if err := commitAndSignal(); err != nil {
		return nil, err
	}
	return s.GetTask(ctx, candidate.TaskID)
}

// ClaimTaskInSession leases the highest-priority QUEUED task in a session to workerID.
func (s *Store) ClaimTaskInSession(ctx context.Context, workerID, sessionID string, leaseDurationSeconds int) (*Task, error) {
	if strings.TrimSpace(workerID) == "" {
		return nil, errors.New("worker_id is required")
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("session_id is required")
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

	var reconcileSignals []pendingSignal
	if _, reconcileSignals, err = reconcileExpiredTx(ctx, tx, now); err != nil {
		return nil, err
	}
	commitAndSignal := func() error {
		if err := tx.Commit(); err != nil {
			return err
		}
		for _, sig := range reconcileSignals {
			s.appendTaskSignal(sig.taskID, sig.from, sig.to, s.clock())
		}
		return nil
	}
	var expiresAt float64
	err = tx.QueryRowContext(ctx,
		"SELECT expires_at FROM control_plane_maintenance WHERE singleton = 1").Scan(&expiresAt)
	switch {
	case err == nil && expiresAt > now:
		return nil, commitAndSignal()
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
		  AND session_id = ?
		  AND (json_extract(request_json, '$.not_before') IS NULL OR json_extract(request_json, '$.not_before') <= ?)
		ORDER BY priority DESC, created_at ASC
		LIMIT 1`, sessionID, now)
	candidate, scanErr := scanTask(row)
	if scanErr != nil {
		return nil, fmt.Errorf("select claim candidate: %w", scanErr)
	}
	if candidate == nil {
		return nil, commitAndSignal()
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
		return nil, commitAndSignal()
	}
	if err := insertTaskEvent(tx, candidate.TaskID, "task_claimed", workerID, map[string]any{
		"lease_token":   leaseToken,
		"lease_seconds": leaseSeconds,
	}, now); err != nil {
		return nil, err
	}
	if err := commitAndSignal(); err != nil {
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
		return fmt.Errorf("%w: task %s (instance %s)", ErrLeaseLost, taskID, pathutil.InstanceID())
	}
	return s.extendLease(ctx, taskID, workerID, leaseToken, extensionSeconds)
}

// Package-level retry configuration for SQLITE_BUSY handling in controlplane (#465).
var (
	busyRetryAttempts = 3
	busyBackoff       = func(attempt int) time.Duration {
		return time.Duration(50+rand.IntN(151)) * time.Millisecond
	}
)

// isBusyErr reports whether err is a SQLITE_BUSY-class error.
// Controlplane must not import internal/memory (#465 P1), so this helper is
// replicated locally following the #440 idiom.
func isBusyErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrBusy) {
		return true
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		code := se.Code()
		if code == 5 || (code&0xff) == 5 || code == 6 || (code&0xff) == 6 {
			return true
		}
	}
	msg := err.Error()
	if strings.Contains(msg, "database is locked") || strings.Contains(msg, "database table is locked") {
		return true
	}
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "database is locked") ||
		strings.Contains(lower, "database table is locked") ||
		strings.Contains(lower, "sqlite_busy")
}

// withBusyRetry runs op, retrying on SQLITE_BUSY-class errors up to busyRetryAttempts.
// Controlplane must not import internal/memory (#465 P1), so this helper is
// replicated locally with jittered backoff following the #440 idiom.
func withBusyRetry(op func() error) error {
	attempts := busyRetryAttempts
	if attempts < 1 {
		attempts = 1
	}
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		err = op()
		if err == nil {
			return nil
		}
		if !isBusyErr(err) {
			return err
		}
		if attempt < attempts-1 {
			if busyBackoff != nil {
				d := busyBackoff(attempt)
				if d > 0 {
					time.Sleep(d)
				}
			}
		}
	}
	if isBusyErr(err) && !errors.Is(err, ErrBusy) {
		return fmt.Errorf("%w: %w", ErrBusy, err)
	}
	return err
}

func (s *Store) extendLease(ctx context.Context, taskID, workerID, leaseToken string, extensionSeconds int) error {
	now := float64(s.clock().UnixNano()) / 1e9
	query := `
		UPDATE tasks SET lease_expires_at = ?, updated_at = ?
		WHERE task_id = ? AND state IN ('LEASED', 'RUNNING')
		  AND lease_owner = ? AND cancel_requested = 0
		  AND (lease_expires_at IS NULL OR lease_expires_at > ?)`
	args := []any{now + float64(extensionSeconds), now, taskID, workerID, now}
	if leaseToken != "" {
		query += ` AND lease_token = ?`
		args = append(args, leaseToken)
	}

	var affected int64
	err := withBusyRetry(func() error {
		var res sql.Result
		var execErr error
		if s.leaseExecOverride != nil {
			res, execErr = s.leaseExecOverride(ctx, query, args...)
		} else {
			res, execErr = s.db.ExecContext(ctx, query, args...)
		}
		if execErr != nil {
			return execErr
		}
		aff, affErr := res.RowsAffected()
		if affErr != nil {
			return affErr
		}
		affected = aff
		return nil
	})
	if err != nil {
		return fmt.Errorf("heartbeat (instance %s): %w", pathutil.InstanceID(), err)
	}
	if affected != 1 {
		return fmt.Errorf("%w: task %s (instance %s)", ErrLeaseLost, taskID, pathutil.InstanceID())
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

// pendingSignal captures a committed terminal transition waiting to be appended to tasks.jsonl.
type pendingSignal struct {
	taskID string
	from   string
	to     string
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
	count, signals, err := reconcileExpiredTx(ctx, tx, now)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	for _, sig := range signals {
		s.appendTaskSignal(sig.taskID, sig.from, sig.to, s.clock())
	}
	return count, nil
}

// reconcileExpiredTx applies lease-expiry transitions inside an open
// transaction: CANCELLED for cancel-requested tasks, FAILED at retry-budget
// exhaustion, otherwise requeue.
func reconcileExpiredTx(ctx context.Context, tx *sql.Tx, now float64) (int, []pendingSignal, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT `+taskColumns+` FROM tasks
		WHERE state IN ('LEASED', 'RUNNING')
		  AND lease_expires_at IS NOT NULL
		  AND lease_expires_at <= ?`, now)
	if err != nil {
		return 0, nil, fmt.Errorf("select expired leases: %w", err)
	}
	var expired []*Task
	for rows.Next() {
		t, scanErr := scanTask(rows)
		if scanErr != nil {
			rows.Close()
			return 0, nil, fmt.Errorf("scan expired lease: %w", scanErr)
		}
		expired = append(expired, t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, nil, fmt.Errorf("iterate expired leases: %w", err)
	}
	rows.Close()

	var signals []pendingSignal
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
			signals = append(signals, pendingSignal{
				taskID: t.TaskID,
				from:   t.State,
				to:     nextState,
			})
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE tasks
			SET state = ?, lease_owner = NULL, lease_token = NULL,
			    lease_expires_at = NULL, updated_at = ?,
			    completed_at = CASE WHEN ? IN ('FAILED', 'CANCELLED') THEN ? ELSE NULL END,
			    last_error = ?
			WHERE task_id = ? AND lease_token = ?`,
			nextState, now, nextState, completedAt, lastError, t.TaskID, deref(t.LeaseToken)); err != nil {
			return reconciled, signals, fmt.Errorf("reconcile task %s: %w", t.TaskID, err)
		}
		owner := ""
		if t.LeaseOwner != nil {
			owner = *t.LeaseOwner
		}
		if err := insertTaskEvent(tx, t.TaskID, "lease_expired", "reconciler",
			map[string]any{"next_state": nextState, "previous_owner": owner}, now); err != nil {
			return reconciled, signals, err
		}

		if nextState == StateCancelled || nextState == StateFailed {
			requestJSON := string(t.Request)
			if err := redactPayload(&requestJSON); err != nil {
				return reconciled, signals, fmt.Errorf("redact task %s: %w", t.TaskID, err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE tasks SET request_json = ? WHERE task_id = ?`,
				requestJSON, t.TaskID); err != nil {
				return reconciled, signals, fmt.Errorf("store redacted request %s: %w", t.TaskID, err)
			}
			fresh := *t
			fresh.State = nextState
			fresh.CompletedAt = &now
			_, receiptHash, err := buildReceiptTx(tx, &fresh)
			if err != nil {
				return reconciled, signals, fmt.Errorf("receipt for task %s: %w", t.TaskID, err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE tasks SET receipt_hash = ? WHERE task_id = ?`,
				receiptHash, t.TaskID); err != nil {
				return reconciled, signals, fmt.Errorf("seal receipt %s: %w", t.TaskID, err)
			}
		}
		reconciled++
	}
	return reconciled, signals, nil
}

// taskSignalEvent models one record in <db-dir>/signals/tasks.jsonl (#481).
type taskSignalEvent struct {
	TS     string `json:"ts"`
	TaskID string `json:"task_id"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// isTerminalSignalState returns true for the five terminal states monitored by
// the signal file: FAILED, WORKER_COMPLETED, SUCCEEDED, CANCELLED, NEEDS_INFO (#481).
func isTerminalSignalState(to string) bool {
	switch to {
	case StateFailed, StateWorkerCompleted, StateSucceeded, StateCancelled, StateNeedsInfo:
		return true
	default:
		return false
	}
}

// appendTaskSignal appends a single terminal-transition signal JSON line to
// <db-dir>/signals/tasks.jsonl.
//
// Crash window design note:
// The signal line is written AFTER the transition's DB transaction COMMITS
// (a file append cannot join a SQL tx). The crash window (committed transition
// without signal line) is acceptable BY DESIGN: the DB is the truth and the
// supervisor's fallback deadline-check re-derives from state_event_log.
func (s *Store) appendTaskSignal(taskID, from, to string, ts time.Time) {
	if !isTerminalSignalState(to) {
		return
	}
	if ts.IsZero() {
		ts = s.clock()
	}

	sig := taskSignalEvent{
		TS:     ts.UTC().Format(time.RFC3339),
		TaskID: taskID,
		From:   from,
		To:     to,
	}
	data, err := json.Marshal(sig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[warn] controlplane: marshal task signal: %v\n", err)
		return
	}
	line := append(data, '\n')

	s.signalMu.Lock()
	defer s.signalMu.Unlock()

	if s.signalPath == "" && s.dbPath != "" {
		s.signalPath = filepath.Join(filepath.Dir(s.dbPath), "signals", "tasks.jsonl")
	}
	if s.signalPath == "" {
		return
	}

	if s.signalFile == nil {
		dir := filepath.Dir(s.signalPath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "[warn] controlplane: mkdir signals dir %s: %v\n", dir, err)
			return
		}
		f, err := os.OpenFile(s.signalPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[warn] controlplane: open signals file %s: %v\n", s.signalPath, err)
			return
		}
		s.signalFile = f
	}

	if _, err := s.signalFile.Write(line); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] controlplane: write signals file: %v\n", err)
		if closeErr := s.signalFile.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "[warn] controlplane: close signals file: %v\n", closeErr)
		}
		s.signalFile = nil
		return
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// SessionQuota represents the quota configuration for a session.
type SessionQuota struct {
	SessionID          string  `json:"session_id"`
	MaxConcurrentTasks int     `json:"max_concurrent_tasks"`
	MaxTasksPerHour    int     `json:"max_tasks_per_hour"`
	CreatedAt          float64 `json:"created_at"`
	UpdatedAt          float64 `json:"updated_at"`
}

// GetSessionQuota returns the quota configuration for a session.
// Returns default quotas if no explicit configuration exists.
func (s *Store) GetSessionQuota(ctx context.Context, sessionID string) (*SessionQuota, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("session_id is required")
	}
	now := float64(s.clock().UnixNano()) / 1e9
	var sq SessionQuota
	err := s.db.QueryRowContext(ctx,
		`SELECT session_id, max_concurrent_tasks, max_tasks_per_hour, created_at, updated_at
		 FROM session_quotas WHERE session_id = ?`, sessionID,
	).Scan(&sq.SessionID, &sq.MaxConcurrentTasks, &sq.MaxTasksPerHour, &sq.CreatedAt, &sq.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		// Return defaults
		return &SessionQuota{
			SessionID:          sessionID,
			MaxConcurrentTasks: 10,
			MaxTasksPerHour:    100,
			CreatedAt:          now,
			UpdatedAt:          now,
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session quota: %w", err)
	}
	return &sq, nil
}

// SetSessionQuota sets or updates the quota configuration for a session.
func (s *Store) SetSessionQuota(ctx context.Context, quota SessionQuota) error {
	if strings.TrimSpace(quota.SessionID) == "" {
		return errors.New("session_id is required")
	}
	if quota.MaxConcurrentTasks <= 0 {
		quota.MaxConcurrentTasks = 10
	}
	if quota.MaxTasksPerHour <= 0 {
		quota.MaxTasksPerHour = 100
	}
	now := float64(s.clock().UnixNano()) / 1e9
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO session_quotas (session_id, max_concurrent_tasks, max_tasks_per_hour, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			max_concurrent_tasks = excluded.max_concurrent_tasks,
			max_tasks_per_hour = excluded.max_tasks_per_hour,
			updated_at = excluded.updated_at`,
		quota.SessionID, quota.MaxConcurrentTasks, quota.MaxTasksPerHour, now, now)
	if err != nil {
		return fmt.Errorf("set session quota: %w", err)
	}
	return nil
}

// CheckSessionQuota checks if a session can accept a new task.
// Returns true if within quota, false if quota exceeded.
func (s *Store) CheckSessionQuota(ctx context.Context, sessionID string) (bool, error) {
	if strings.TrimSpace(sessionID) == "" {
		return true, nil // No session = no quota enforcement
	}
	quota, err := s.GetSessionQuota(ctx, sessionID)
	if err != nil {
		return false, err
	}
	// Check concurrent tasks
	active, err := s.ActiveTaskCountInSession(ctx, sessionID)
	if err != nil {
		return false, err
	}
	if active >= quota.MaxConcurrentTasks {
		return false, nil
	}
	if quota.MaxTasksPerHour > 0 {
		now := float64(s.clock().UnixNano()) / 1e9
		windowStart := now - 3600.0
		var count int
		err := s.db.QueryRowContext(ctx, `
			SELECT COUNT(1) FROM tasks
			WHERE session_id = ? AND created_at >= ?`, sessionID, windowStart).Scan(&count)
		if err != nil {
			return false, fmt.Errorf("check session hourly quota: %w", err)
		}
		if count >= quota.MaxTasksPerHour {
			return false, nil
		}
	}
	return true, nil
}

// sqlitePathEscapeFor escapes a database file path for a SQLite file: URI on
// the given GOOS (#336). On Windows url.PathEscape percent-encodes drive
// colons (%3A) and separators (%5C) — the modernc driver does not decode
// them back, producing an unusable URI. Windows keeps the path literal and
// escapes only the URI-significant characters, preserving the v0.9.2
// connection-string injection guarantee (?, # and % cannot smuggle query
// parameters). Non-Windows keeps the proven url.PathEscape behavior.
func sqlitePathEscapeFor(goos, path string) string {
	if goos != "windows" {
		return url.PathEscape(path)
	}
	p := strings.ReplaceAll(path, "%", "%25")
	p = strings.ReplaceAll(p, "?", "%3F")
	p = strings.ReplaceAll(p, "#", "%23")
	return p
}

// sqlitePathEscape escapes the database path for the running platform.
func sqlitePathEscape(path string) string {
	return sqlitePathEscapeFor(runtime.GOOS, path)
}
