// Package receipt implements zero-trust write receipts for delegated
// workspace mutations.
//
// A Brain-tier orchestrator issues single-use, TTL-bounded, path-scoped
// receipts that Worker-tier tasks must present before performing any write.
// Receipt state lives in a Zero-CGO SQLite database (modernc.org/sqlite) in
// WAL mode, supporting coordinated access from independent processes.
//
// All time-dependent behavior accepts an injectable clock function so tests
// can drive expiry deterministically without sleeping.
package receipt

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	_ "modernc.org/sqlite" // Pure-Go SQLite driver; Zero-CGO constitution axiom.
)

// Receipt bounds enforced by IssueReceipt.
const (
	minTTL = time.Second
	maxTTL = 3600 * time.Second
)

// SchemaVersion defines the current SQLite database schema generation for write receipts.
// Version 1: original write receipts schema.
// Version 2: additive supervisor metadata columns (approach_idx, attempt_idx, rca_confidence, adr_path).
const SchemaVersion = 2

// Sentinel errors returned wrapped by typed errors below.
var (
	// ErrEmptyPaths is returned when AllowedPaths is missing entirely.
	ErrEmptyPaths = errors.New("allowed_paths must not be empty")
	// ErrTTLBounds is returned when the requested TTL is outside [1s, 3600s].
	ErrTTLBounds = errors.New("ttl_seconds must be between 1 and 3600")
)

// NotFoundError reports that a receipt ID has no matching row.
type NotFoundError struct{ ReceiptID string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("write receipt not found: %s", e.ReceiptID)
}

// AlreadyConsumedError reports that a receipt was validated once and can
// never validate again (single-use guarantee).
type AlreadyConsumedError struct{ ReceiptID string }

func (e *AlreadyConsumedError) Error() string {
	return fmt.Sprintf("write receipt already consumed: %s", e.ReceiptID)
}

// ExpiredError reports that a receipt's TTL elapsed before consumption.
type ExpiredError struct {
	ReceiptID string
	Elapsed   time.Duration
}

func (e *ExpiredError) Error() string {
	return fmt.Sprintf("write receipt expired: %s (expired %.0fs ago)", e.ReceiptID, e.Elapsed.Seconds())
}

// SupervisorMeta holds supervisor-tier provenance and decision linkage
// for a write receipt.
type SupervisorMeta struct {
	ApproachIdx   int     `json:"approach_idx"`
	AttemptIdx    int     `json:"attempt_idx"`
	RCAConfidence float64 `json:"rca_confidence"`
	ADRPath       string  `json:"adr_path"`
}

// WriteReceipt is the durable proof-of-authorization for one bounded write.
type WriteReceipt struct {
	ReceiptID      string          `json:"receipt_id"`
	Issuer         string          `json:"issuer"`
	AllowedPaths   []string        `json:"allowed_paths"`
	ExpiresAt      time.Time       `json:"expires_at"`
	Consumed       bool            `json:"consumed"`
	ConsumerTaskID *string         `json:"consumer_task_id,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	SupervisorMeta *SupervisorMeta `json:"supervisor_meta,omitempty"`
}

// IssueOption configures optional receipt issuance attributes.
type IssueOption func(*issueOptions)

type issueOptions struct {
	supervisorMeta *SupervisorMeta
}

// WithSupervisorMeta attaches supervisor-tier provenance metadata to the issued receipt.
func WithSupervisorMeta(meta *SupervisorMeta) IssueOption {
	return func(o *issueOptions) {
		o.supervisorMeta = meta
	}
}

// ReceiptManager is the durable API surface of the receipt engine.
type ReceiptManager interface {
	IssueReceipt(issuer string, allowedPaths []string, ttl time.Duration, opts ...IssueOption) (*WriteReceipt, error)
	ValidateAndConsume(receiptID string, consumerTaskID string) (*WriteReceipt, error)
	RevokeReceipt(receiptID string) (bool, error)
	ListActiveReceipts() ([]*WriteReceipt, error)
	VerifyReceipt(receiptID string) (*WriteReceipt, error)
}

// Manager is the SQLite-backed ReceiptManager implementation.
type Manager struct {
	db    *sql.DB
	mu    sync.Mutex
	clock func() time.Time
}

const schema = `
CREATE TABLE IF NOT EXISTS write_receipts (
	receipt_id         TEXT PRIMARY KEY,
	issuer             TEXT NOT NULL,
	allowed_paths_json TEXT NOT NULL,
	expires_at         REAL NOT NULL,
	consumed           INTEGER NOT NULL DEFAULT 0,
	consumer_task_id   TEXT,
	created_at         REAL NOT NULL,
	approach_idx       INTEGER,
	attempt_idx        INTEGER,
	rca_confidence     REAL,
	adr_path           TEXT
);`

// NewReceiptManager opens (or creates) the receipt database at dbPath and
// applies the WAL-mode schema and migrations. A nil clock defaults to time.Now;
// supply a custom clock for deterministic expiry testing. The database file is
// restricted to owner-only permissions (0600).
func NewReceiptManager(dbPath string, clock func() time.Time) (*Manager, error) {
	if clock == nil {
		clock = time.Now
	}
	dsn := fmt.Sprintf("file:%s?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", url.PathEscape(dbPath))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open receipt database %q: %w", dbPath, err)
	}
	m := &Manager{db: db, clock: clock}
	if err := m.initialize(); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize receipt schema in %q: %w", dbPath, err)
	}
	if err := os.Chmod(dbPath, 0o600); err != nil {
		db.Close()
		return nil, fmt.Errorf("restrict receipt database permissions %q: %w", dbPath, err)
	}
	return m, nil
}

// Close releases the underlying database handle.
func (m *Manager) Close() error {
	return m.db.Close()
}

// initialize runs the schema gate inside one EXCLUSIVE transaction on a single
// pinned connection: accept user_version 0 (fresh), 1 (legacy), or current;
// migrate schema by adding missing columns; reject unsupported versions.
func (m *Manager) initialize() error {
	conn, err := m.db.Conn(context.Background())
	if err != nil {
		return fmt.Errorf("pin initialization connection: %w", err)
	}
	defer conn.Close()

	// Fast-path: if database already matches current schema version, skip exclusive lock.
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
	if _, err := conn.ExecContext(context.Background(), schema); err != nil {
		rollbackInit(conn)
		return fmt.Errorf("apply base schema: %w", err)
	}
	if err := migrateSupervisorSchema(conn); err != nil {
		rollbackInit(conn)
		return err
	}
	if _, err := conn.ExecContext(context.Background(), fmt.Sprintf("PRAGMA user_version = %d", SchemaVersion)); err != nil {
		rollbackInit(conn)
		return fmt.Errorf("record schema version: %w", err)
	}
	if _, err := conn.ExecContext(context.Background(), "COMMIT"); err != nil {
		return fmt.Errorf("commit schema transaction: %w", err)
	}
	return nil
}

func checkSchemaVersion(conn *sql.Conn) error {
	var version int
	if err := conn.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version < 0 || (version > 1 && version != SchemaVersion) {
		return fmt.Errorf("unsupported receipt schema version %d; expected %d", version, SchemaVersion)
	}
	return nil
}

func rollbackInit(conn *sql.Conn) {
	_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
}

// migrateSupervisorSchema is the idempotent migration for supervisor metadata columns.
// It inspects write_receipts columns via PRAGMA table_info and adds any missing
// column with ALTER TABLE ADD COLUMN.
func migrateSupervisorSchema(conn *sql.Conn) error {
	expected := map[string]string{
		"approach_idx":   "INTEGER",
		"attempt_idx":    "INTEGER",
		"rca_confidence": "REAL",
		"adr_path":       "TEXT",
	}
	present := map[string]struct{}{}
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
			return fmt.Errorf("scan write_receipts column info: %w", err)
		}
		present[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate write_receipts column info: %w", err)
	}

	for col, colType := range expected {
		if _, ok := present[col]; ok {
			continue
		}
		if _, err := conn.ExecContext(context.Background(),
			fmt.Sprintf("ALTER TABLE write_receipts ADD COLUMN %s %s", col, colType)); err != nil {
			return fmt.Errorf("migrate write_receipts.%s: %w", col, err)
		}
	}
	return nil
}

// IssueReceipt mints a single-use receipt scoped to allowedPaths with the
// given TTL. Optional supervisor metadata can be attached via WithSupervisorMeta.
// The caller identity is recorded verbatim; the engine performs no internal
// authentication (trust boundary is filesystem permissions and upstream harness gating).
func (m *Manager) IssueReceipt(issuer string, allowedPaths []string, ttl time.Duration, opts ...IssueOption) (*WriteReceipt, error) {
	if len(allowedPaths) == 0 {
		return nil, ErrEmptyPaths
	}
	if ttl < minTTL || ttl > maxTTL {
		return nil, ErrTTLBounds
	}
	pathsJSON, err := json.Marshal(allowedPaths)
	if err != nil {
		return nil, fmt.Errorf("serialize allowed_paths: %w", err)
	}

	var options issueOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.clock()
	r := &WriteReceipt{
		ReceiptID:    uuid.NewString(),
		Issuer:       issuer,
		AllowedPaths: append([]string(nil), allowedPaths...),
		ExpiresAt:    now.Add(ttl),
		CreatedAt:    now,
	}

	var (
		approachIdx   sql.NullInt64
		attemptIdx    sql.NullInt64
		rcaConfidence sql.NullFloat64
		adrPath       sql.NullString
	)
	if meta := options.supervisorMeta; meta != nil {
		approachIdx = sql.NullInt64{Int64: int64(meta.ApproachIdx), Valid: true}
		attemptIdx = sql.NullInt64{Int64: int64(meta.AttemptIdx), Valid: true}
		rcaConfidence = sql.NullFloat64{Float64: meta.RCAConfidence, Valid: true}
		adrPath = sql.NullString{String: meta.ADRPath, Valid: true}
		r.SupervisorMeta = &SupervisorMeta{
			ApproachIdx:   meta.ApproachIdx,
			AttemptIdx:    meta.AttemptIdx,
			RCAConfidence: meta.RCAConfidence,
			ADRPath:       meta.ADRPath,
		}
	}

	if _, err := m.db.Exec(
		`INSERT INTO write_receipts
			(receipt_id, issuer, allowed_paths_json, expires_at, consumed, consumer_task_id, created_at,
			 approach_idx, attempt_idx, rca_confidence, adr_path)
		 VALUES (?, ?, ?, ?, 0, NULL, ?, ?, ?, ?, ?)`,
		r.ReceiptID, r.Issuer, string(pathsJSON), timeToUnix(r.ExpiresAt), timeToUnix(r.CreatedAt),
		approachIdx, attemptIdx, rcaConfidence, adrPath,
	); err != nil {
		return nil, fmt.Errorf("insert receipt %q: %w", r.ReceiptID, err)
	}
	return r, nil
}

// ValidateAndConsume atomically validates receiptID and marks it consumed.
// Exactly one caller ever succeeds for a given receipt; every subsequent
// call fails with AlreadyConsumedError even across independent processes.
func (m *Manager) ValidateAndConsume(receiptID string, consumerTaskID string) (*WriteReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tx, err := m.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin consume transaction: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRow(
		`SELECT issuer, allowed_paths_json, expires_at, consumed, consumer_task_id, created_at,
		        approach_idx, attempt_idx, rca_confidence, adr_path
		 FROM write_receipts WHERE receipt_id = ?`, receiptID,
	)
	var (
		issuer        string
		pathsJSON     string
		expiresAt     float64
		consumed      int
		storedTask    sql.NullString
		createdAt     float64
		approachIdx   sql.NullInt64
		attemptIdx    sql.NullInt64
		rcaConfidence sql.NullFloat64
		adrPath       sql.NullString
	)
	switch scanErr := row.Scan(&issuer, &pathsJSON, &expiresAt, &consumed, &storedTask, &createdAt,
		&approachIdx, &attemptIdx, &rcaConfidence, &adrPath); {
	case errors.Is(scanErr, sql.ErrNoRows):
		return nil, &NotFoundError{ReceiptID: receiptID}
	case scanErr != nil:
		return nil, fmt.Errorf("load receipt %q: %w", receiptID, scanErr)
	}
	if consumed == 1 {
		return nil, &AlreadyConsumedError{ReceiptID: receiptID}
	}

	now := m.clock()
	expiry := unixToTime(expiresAt)
	if now.After(expiry) {
		return nil, &ExpiredError{ReceiptID: receiptID, Elapsed: now.Sub(expiry)}
	}

	res, err := tx.Exec(
		`UPDATE write_receipts SET consumed = 1, consumer_task_id = ?
		 WHERE receipt_id = ? AND consumed = 0`,
		consumerTaskID, receiptID,
	)
	if err != nil {
		return nil, fmt.Errorf("consume receipt %q: %w", receiptID, err)
	}
	if affected, err := res.RowsAffected(); err == nil && affected != 1 {
		return nil, &AlreadyConsumedError{ReceiptID: receiptID}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit consume of %q: %w", receiptID, err)
	}

	var paths []string
	if err := json.Unmarshal([]byte(pathsJSON), &paths); err != nil {
		return nil, fmt.Errorf("deserialize allowed_paths of %q: %w", receiptID, err)
	}
	taskID := consumerTaskID

	var meta *SupervisorMeta
	if approachIdx.Valid || attemptIdx.Valid || rcaConfidence.Valid || adrPath.Valid {
		meta = &SupervisorMeta{
			ApproachIdx:   int(approachIdx.Int64),
			AttemptIdx:    int(attemptIdx.Int64),
			RCAConfidence: rcaConfidence.Float64,
			ADRPath:       adrPath.String,
		}
	}

	return &WriteReceipt{
		ReceiptID:      receiptID,
		Issuer:         issuer,
		AllowedPaths:   paths,
		ExpiresAt:      expiry,
		Consumed:       true,
		ConsumerTaskID: &taskID,
		CreatedAt:      unixToTime(createdAt),
		SupervisorMeta: meta,
	}, nil
}

// RevokeReceipt deletes an unconsumed receipt, returning true when a live
// receipt was revoked. Missing or already-consumed receipts report false.
func (m *Manager) RevokeReceipt(receiptID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	res, err := m.db.Exec(`DELETE FROM write_receipts WHERE receipt_id = ? AND consumed = 0`, receiptID)
	if err != nil {
		return false, fmt.Errorf("revoke receipt %q: %w", receiptID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count revoked rows for %q: %w", receiptID, err)
	}
	return affected == 1, nil
}

// ListActiveReceipts returns all unconsumed, unexpired receipts ordered by
// creation time. Listing never mutates receipt state.
func (m *Manager) ListActiveReceipts() ([]*WriteReceipt, error) {
	rows, err := m.db.Query(
		`SELECT receipt_id, issuer, allowed_paths_json, expires_at, created_at,
		        approach_idx, attempt_idx, rca_confidence, adr_path
		 FROM write_receipts
		 WHERE consumed = 0 AND expires_at > ?
		 ORDER BY created_at ASC, receipt_id ASC`,
		timeToUnix(m.clock()),
	)
	if err != nil {
		return nil, fmt.Errorf("list active receipts: %w", err)
	}
	defer rows.Close()

	var out []*WriteReceipt
	for rows.Next() {
		var (
			r             WriteReceipt
			pathsJSON     string
			expiresAt     float64
			createdAt     float64
			approachIdx   sql.NullInt64
			attemptIdx    sql.NullInt64
			rcaConfidence sql.NullFloat64
			adrPath       sql.NullString
		)
		if err := rows.Scan(&r.ReceiptID, &r.Issuer, &pathsJSON, &expiresAt, &createdAt,
			&approachIdx, &attemptIdx, &rcaConfidence, &adrPath); err != nil {
			return nil, fmt.Errorf("scan active receipt: %w", err)
		}
		var paths []string
		if err := json.Unmarshal([]byte(pathsJSON), &paths); err != nil {
			return nil, fmt.Errorf("deserialize allowed_paths of %q: %w", r.ReceiptID, err)
		}
		r.AllowedPaths = paths
		r.ExpiresAt = unixToTime(expiresAt)
		r.CreatedAt = unixToTime(createdAt)
		if approachIdx.Valid || attemptIdx.Valid || rcaConfidence.Valid || adrPath.Valid {
			r.SupervisorMeta = &SupervisorMeta{
				ApproachIdx:   int(approachIdx.Int64),
				AttemptIdx:    int(attemptIdx.Int64),
				RCAConfidence: rcaConfidence.Float64,
				ADRPath:       adrPath.String,
			}
		}
		out = append(out, &r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active receipts: %w", err)
	}
	return out, nil
}

// VerifyReceipt retrieves and verifies the validity of a write receipt
// without consuming it. NULL supervisor metadata columns are tolerated
// and treated as absent (SupervisorMeta is nil).
func (m *Manager) VerifyReceipt(receiptID string) (*WriteReceipt, error) {
	if receiptID == "" {
		return nil, &NotFoundError{ReceiptID: receiptID}
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	row := m.db.QueryRow(
		`SELECT issuer, allowed_paths_json, expires_at, consumed, consumer_task_id, created_at,
		        approach_idx, attempt_idx, rca_confidence, adr_path
		 FROM write_receipts WHERE receipt_id = ?`, receiptID,
	)
	var (
		issuer        string
		pathsJSON     string
		expiresAt     float64
		consumed      int
		storedTask    sql.NullString
		createdAt     float64
		approachIdx   sql.NullInt64
		attemptIdx    sql.NullInt64
		rcaConfidence sql.NullFloat64
		adrPath       sql.NullString
	)
	switch scanErr := row.Scan(&issuer, &pathsJSON, &expiresAt, &consumed, &storedTask, &createdAt,
		&approachIdx, &attemptIdx, &rcaConfidence, &adrPath); {
	case errors.Is(scanErr, sql.ErrNoRows):
		return nil, &NotFoundError{ReceiptID: receiptID}
	case scanErr != nil:
		return nil, fmt.Errorf("load receipt %q: %w", receiptID, scanErr)
	}

	now := m.clock()
	expiry := unixToTime(expiresAt)
	if now.After(expiry) {
		return nil, &ExpiredError{ReceiptID: receiptID, Elapsed: now.Sub(expiry)}
	}

	var paths []string
	if err := json.Unmarshal([]byte(pathsJSON), &paths); err != nil {
		return nil, fmt.Errorf("deserialize allowed_paths of %q: %w", receiptID, err)
	}

	var taskIDPtr *string
	if storedTask.Valid {
		taskIDPtr = &storedTask.String
	}

	var meta *SupervisorMeta
	if approachIdx.Valid || attemptIdx.Valid || rcaConfidence.Valid || adrPath.Valid {
		meta = &SupervisorMeta{
			ApproachIdx:   int(approachIdx.Int64),
			AttemptIdx:    int(attemptIdx.Int64),
			RCAConfidence: rcaConfidence.Float64,
			ADRPath:       adrPath.String,
		}
	}

	return &WriteReceipt{
		ReceiptID:      receiptID,
		Issuer:         issuer,
		AllowedPaths:   paths,
		ExpiresAt:      expiry,
		Consumed:       consumed == 1,
		ConsumerTaskID: taskIDPtr,
		CreatedAt:      unixToTime(createdAt),
		SupervisorMeta: meta,
	}, nil
}

// timeToUnix encodes a timestamp as fractional Unix seconds, preserving
// sub-second precision for REAL column storage.
func timeToUnix(t time.Time) float64 {
	return float64(t.Unix()) + float64(t.Nanosecond())/1e9
}

// unixToTime decodes fractional Unix seconds back into a UTC timestamp.
func unixToTime(u float64) time.Time {
	secs := int64(u)
	nanos := int64((u - float64(secs)) * 1e9)
	return time.Unix(secs, nanos).UTC()
}
