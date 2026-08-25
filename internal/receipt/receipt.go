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

// WriteReceipt is the durable proof-of-authorization for one bounded write.
type WriteReceipt struct {
	ReceiptID      string    `json:"receipt_id"`
	Issuer         string    `json:"issuer"`
	AllowedPaths   []string  `json:"allowed_paths"`
	ExpiresAt      time.Time `json:"expires_at"`
	Consumed       bool      `json:"consumed"`
	ConsumerTaskID *string   `json:"consumer_task_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// ReceiptManager is the durable API surface of the receipt engine.
type ReceiptManager interface {
	IssueReceipt(issuer string, allowedPaths []string, ttl time.Duration) (*WriteReceipt, error)
	ValidateAndConsume(receiptID string, consumerTaskID string) (*WriteReceipt, error)
	RevokeReceipt(receiptID string) (bool, error)
	ListActiveReceipts() ([]*WriteReceipt, error)
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
	created_at         REAL NOT NULL
);`

// NewReceiptManager opens (or creates) the receipt database at dbPath and
// applies the WAL-mode schema. A nil clock defaults to time.Now; supply a
// custom clock for deterministic expiry testing. The database file is
// restricted to owner-only permissions (0600).
func NewReceiptManager(dbPath string, clock func() time.Time) (*Manager, error) {
	if clock == nil {
		clock = time.Now
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", url.PathEscape(dbPath))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open receipt database %q: %w", dbPath, err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize receipt schema in %q: %w", dbPath, err)
	}
	if err := os.Chmod(dbPath, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("restrict receipt database permissions %q: %w", dbPath, err)
	}
	return &Manager{db: db, clock: clock}, nil
}

// Close releases the underlying database handle.
func (m *Manager) Close() error {
	return m.db.Close()
}

// IssueReceipt mints a single-use receipt scoped to allowedPaths with the
// given TTL. The caller identity is recorded verbatim; the engine performs
// no internal authentication (trust boundary is filesystem permissions and
// upstream harness gating).
func (m *Manager) IssueReceipt(issuer string, allowedPaths []string, ttl time.Duration) (*WriteReceipt, error) {
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
	if _, err := m.db.Exec(
		`INSERT INTO write_receipts
			(receipt_id, issuer, allowed_paths_json, expires_at, consumed, consumer_task_id, created_at)
		 VALUES (?, ?, ?, ?, 0, NULL, ?)`,
		r.ReceiptID, r.Issuer, string(pathsJSON), timeToUnix(r.ExpiresAt), timeToUnix(r.CreatedAt),
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
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRow(
		`SELECT issuer, allowed_paths_json, expires_at, consumed, consumer_task_id, created_at
		 FROM write_receipts WHERE receipt_id = ?`, receiptID,
	)
	var (
		issuer     string
		pathsJSON  string
		expiresAt  float64
		consumed   int
		storedTask sql.NullString
		createdAt  float64
	)
	switch scanErr := row.Scan(&issuer, &pathsJSON, &expiresAt, &consumed, &storedTask, &createdAt); {
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
	return &WriteReceipt{
		ReceiptID:      receiptID,
		Issuer:         issuer,
		AllowedPaths:   paths,
		ExpiresAt:      expiry,
		Consumed:       true,
		ConsumerTaskID: &taskID,
		CreatedAt:      unixToTime(createdAt),
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
		`SELECT receipt_id, issuer, allowed_paths_json, expires_at, created_at
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
			r         WriteReceipt
			pathsJSON string
			expiresAt float64
			createdAt float64
		)
		if err := rows.Scan(&r.ReceiptID, &r.Issuer, &pathsJSON, &expiresAt, &createdAt); err != nil {
			return nil, fmt.Errorf("scan active receipt: %w", err)
		}
		var paths []string
		if err := json.Unmarshal([]byte(pathsJSON), &paths); err != nil {
			return nil, fmt.Errorf("deserialize allowed_paths of %q: %w", r.ReceiptID, err)
		}
		r.AllowedPaths = paths
		r.ExpiresAt = unixToTime(expiresAt)
		r.CreatedAt = unixToTime(createdAt)
		out = append(out, &r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active receipts: %w", err)
	}
	return out, nil
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
