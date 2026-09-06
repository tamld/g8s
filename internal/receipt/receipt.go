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
	"strings"
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
// Version 3: additive Concern B columns for provenance-and-replay context
//
//	(canonical_envelope, rule_graph_snapshot, provenance_lineage).
const SchemaVersion = 3

// Sentinel errors returned wrapped by typed errors below.
var (
	// ErrEmptyPaths is returned when AllowedPaths is missing entirely.
	ErrEmptyPaths = errors.New("allowed_paths must not be empty")
	// ErrTTLBounds is returned when the requested TTL is outside [1s, 3600s].
	ErrTTLBounds = errors.New("ttl_seconds must be between 1 and 3600")
	// ErrEnvelopeRequired is returned when a Brain-tier caller omits
	// CanonicalEnvelope on IssueReceipt.
	ErrEnvelopeRequired = errors.New("canonical envelope required for Brain-tier issuance")
	// ErrEnvelopeSchemaURI is returned when CanonicalEnvelope.SchemaURI does
	// not satisfy the required g8s:// prefix.
	ErrEnvelopeSchemaURI = errors.New("canonical envelope schema_uri must start with g8s://")
	// ErrEnvelopeFieldOrder is returned when CanonicalEnvelope.FieldOrder is empty.
	ErrEnvelopeFieldOrder = errors.New("canonical envelope field_order must not be empty")
	// ErrRuleGraphRequired is returned when a Brain-tier caller omits
	// RuleGraphSnapshot on IssueReceipt.
	ErrRuleGraphRequired = errors.New("rule graph snapshot required for Brain-tier issuance")
)

// envelopeSchemaPrefix is the required prefix for CanonicalEnvelope.SchemaURI.
const envelopeSchemaPrefix = "g8s://"

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

// CanonicalEnvelope pins the wire format used by the receipt issuer so that
// downstream parsers can decode receipts regardless of tool version drift.
type CanonicalEnvelope struct {
	SchemaURI      string   `json:"schema_uri"`
	FieldOrder     []string `json:"field_order"`
	RequiredFields []string `json:"required_fields"`
}

// RuleGraphSnapshot pins the rule registry state at issue time so that
// receipts remain replayable after the registry evolves.
type RuleGraphSnapshot struct {
	RulesetVersion string `json:"ruleset_version"`
	PipelineDigest string `json:"pipeline_digest"`
	ADRRef         string `json:"adr_ref,omitempty"`
}

// ProvenanceLineage records actor chain, trace ID, tool version, and source
// commit at issue time for forensic audit. Auto-populated by IssueReceipt.
type ProvenanceLineage struct {
	IssuedBy     string   `json:"issued_by"`
	ToolVersion  string   `json:"tool_version"`
	TraceID      string   `json:"trace_id"`
	ActorChain   []string `json:"actor_chain"`
	SourceCommit string   `json:"source_commit"`
}

// WriteReceipt is the durable proof-of-authorization for one bounded write.
type WriteReceipt struct {
	ReceiptID         string             `json:"receipt_id"`
	Issuer            string             `json:"issuer"`
	AllowedPaths      []string           `json:"allowed_paths"`
	ExpiresAt         time.Time          `json:"expires_at"`
	Consumed          bool               `json:"consumed"`
	ConsumerTaskID    *string            `json:"consumer_task_id,omitempty"`
	CreatedAt         time.Time          `json:"created_at"`
	SupervisorMeta    *SupervisorMeta    `json:"supervisor_meta,omitempty"`
	CanonicalEnvelope *CanonicalEnvelope `json:"canonical_envelope,omitempty"`
	RuleGraphSnapshot *RuleGraphSnapshot `json:"rule_graph_snapshot,omitempty"`
	ProvenanceLineage *ProvenanceLineage `json:"provenance_lineage,omitempty"`
}

// IssueOption configures optional receipt issuance attributes.
type IssueOption func(*issueOptions)

type issueOptions struct {
	supervisorMeta    *SupervisorMeta
	canonicalEnvelope *CanonicalEnvelope
	ruleGraph         *RuleGraphSnapshot
	requireConcernB   bool
	toolVersion       string
	sourceCommit      string
	traceID           string
}

// requireConcernBDefault is the strictness default for IssueReceipt: callers
// MUST provide CanonicalEnvelope and RuleGraphSnapshot. Use
// WithConcernBDisabled to opt out (legacy callers, internal tests).
const requireConcernBDefault = true

// WithSupervisorMeta attaches supervisor-tier provenance metadata to the issued receipt.
func WithSupervisorMeta(meta *SupervisorMeta) IssueOption {
	return func(o *issueOptions) {
		o.supervisorMeta = meta
	}
}

// WithCanonicalEnvelope attaches the canonical wire-format envelope to the
// issued receipt. Validates SchemaURI prefix and FieldOrder non-emptiness;
// returns an error from IssueReceipt on validation failure.
func WithCanonicalEnvelope(env *CanonicalEnvelope) IssueOption {
	return func(o *issueOptions) {
		o.canonicalEnvelope = env
	}
}

// WithRuleGraph attaches the rule-registry snapshot to the issued receipt.
func WithRuleGraph(rs *RuleGraphSnapshot) IssueOption {
	return func(o *issueOptions) {
		o.ruleGraph = rs
	}
}

// WithCurrentRuleGraph is a convenience wrapper that returns a WithRuleGraph
// option pre-populated with the provided snapshot. Designed for callers that
// already have a snapshot from an internal ruleset registry.
func WithCurrentRuleGraph(rs *RuleGraphSnapshot) IssueOption {
	return WithRuleGraph(rs)
}

// WithConcernBDisabled opts out of the Concern B (provenance-and-replay)
// enforcement. By default, IssueReceipt rejects calls missing
// CanonicalEnvelope or RuleGraphSnapshot. Use this only for legacy callers,
// internal tests, or pre-migration migration scaffolding. Production
// callers (CLI, MCP server) MUST NOT use this option.
func WithConcernBDisabled() IssueOption {
	return func(o *issueOptions) {
		o.requireConcernB = false
	}
}

// WithProvenanceContext provides runtime-derived provenance fields. Empty
// values are accepted (system can fill later) but supplying them keeps
// provenance self-contained for tests and non-default builds.
func WithProvenanceContext(toolVersion, sourceCommit, traceID string) IssueOption {
	return func(o *issueOptions) {
		o.toolVersion = toolVersion
		o.sourceCommit = sourceCommit
		o.traceID = traceID
	}
}

// ReceiptManager is the durable API surface of the receipt engine.
type ReceiptManager interface {
	IssueReceipt(issuer string, allowedPaths []string, ttl time.Duration, opts ...IssueOption) (*WriteReceipt, error)
	ValidateAndConsume(receiptID string, consumerTaskID string) (*WriteReceipt, error)
	RevokeReceipt(receiptID string) (bool, error)
	ListActiveReceipts() ([]*WriteReceipt, error)
	VerifyReceipt(receiptID string) (*WriteReceipt, error)
	PurgeExpired(maxAge time.Duration, maxRows int) (int64, error)
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
	adr_path           TEXT,
	envelope_schema_uri           TEXT,
	envelope_field_order_json     TEXT,
	envelope_required_fields_json TEXT,
	ruleset_version               TEXT,
	pipeline_digest               TEXT,
	adr_ref                       TEXT,
	issued_by                     TEXT,
	tool_version                  TEXT,
	trace_id                      TEXT,
	actor_chain_json              TEXT,
	source_commit                 TEXT
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
	if err := migrateConcernBSchema(conn); err != nil {
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
	if version < 0 || (version > 2 && version != SchemaVersion) {
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

// migrateConcernBSchema is the idempotent migration for Concern B
// (provenance-and-replay context) columns. All columns are nullable so
// legacy v0.8.0 receipts remain readable.
func migrateConcernBSchema(conn *sql.Conn) error {
	expected := map[string]string{
		"envelope_schema_uri":           "TEXT",
		"envelope_field_order_json":     "TEXT",
		"envelope_required_fields_json": "TEXT",
		"ruleset_version":               "TEXT",
		"pipeline_digest":               "TEXT",
		"adr_ref":                       "TEXT",
		"issued_by":                     "TEXT",
		"tool_version":                  "TEXT",
		"trace_id":                      "TEXT",
		"actor_chain_json":              "TEXT",
		"source_commit":                 "TEXT",
	}
	present := map[string]struct{}{}
	rows, err := conn.QueryContext(context.Background(), "PRAGMA table_info(write_receipts)")
	if err != nil {
		return fmt.Errorf("inspect write_receipts columns for Concern B: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return fmt.Errorf("scan write_receipts column info for Concern B: %w", err)
		}
		present[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate write_receipts column info for Concern B: %w", err)
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
	options.requireConcernB = requireConcernBDefault
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	if options.requireConcernB {
		if options.canonicalEnvelope == nil {
			return nil, ErrEnvelopeRequired
		}
		if err := validateCanonicalEnvelope(options.canonicalEnvelope); err != nil {
			return nil, err
		}
		if options.ruleGraph == nil {
			return nil, ErrRuleGraphRequired
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.clock()
	id := uuid.NewString()
	r := &WriteReceipt{
		ReceiptID:    id,
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

	var (
		envelopeSchemaURI    sql.NullString
		envelopeFieldOrder   sql.NullString
		envelopeRequiredFlds sql.NullString
		rulesetVersion       sql.NullString
		pipelineDigest       sql.NullString
		adrRefVal            sql.NullString
		issuedBy             sql.NullString
		toolVersion          sql.NullString
		traceID              sql.NullString
		actorChainJSON       sql.NullString
		sourceCommit         sql.NullString
	)
	if env := options.canonicalEnvelope; env != nil {
		envelopeSchemaURI = sql.NullString{String: env.SchemaURI, Valid: true}
		if fo, ferr := json.Marshal(env.FieldOrder); ferr == nil {
			envelopeFieldOrder = sql.NullString{String: string(fo), Valid: true}
		}
		if rf, ferr := json.Marshal(env.RequiredFields); ferr == nil {
			envelopeRequiredFlds = sql.NullString{String: string(rf), Valid: true}
		}
		r.CanonicalEnvelope = &CanonicalEnvelope{
			SchemaURI:      env.SchemaURI,
			FieldOrder:     append([]string(nil), env.FieldOrder...),
			RequiredFields: append([]string(nil), env.RequiredFields...),
		}
	}
	if rs := options.ruleGraph; rs != nil {
		rulesetVersion = sql.NullString{String: rs.RulesetVersion, Valid: true}
		pipelineDigest = sql.NullString{String: rs.PipelineDigest, Valid: true}
		adrRefVal = sql.NullString{String: rs.ADRRef, Valid: rs.ADRRef != ""}
		r.RuleGraphSnapshot = &RuleGraphSnapshot{
			RulesetVersion: rs.RulesetVersion,
			PipelineDigest: rs.PipelineDigest,
			ADRRef:         rs.ADRRef,
		}
	}

	provenance := &ProvenanceLineage{
		IssuedBy:     issuer,
		ToolVersion:  options.toolVersion,
		TraceID:      options.traceID,
		SourceCommit: options.sourceCommit,
		ActorChain:   []string{issuer},
	}
	issuedBy = sql.NullString{String: issuer, Valid: true}
	if options.toolVersion != "" {
		toolVersion = sql.NullString{String: options.toolVersion, Valid: true}
	}
	if options.traceID != "" {
		traceID = sql.NullString{String: options.traceID, Valid: true}
	}
	if options.sourceCommit != "" {
		sourceCommit = sql.NullString{String: options.sourceCommit, Valid: true}
	}
	if ac, aerr := json.Marshal(provenance.ActorChain); aerr == nil {
		actorChainJSON = sql.NullString{String: string(ac), Valid: true}
	}
	r.ProvenanceLineage = provenance

	if _, err := m.db.Exec(
		`INSERT INTO write_receipts
			(receipt_id, issuer, allowed_paths_json, expires_at, consumed, consumer_task_id, created_at,
			 approach_idx, attempt_idx, rca_confidence, adr_path,
			 envelope_schema_uri, envelope_field_order_json, envelope_required_fields_json,
			 ruleset_version, pipeline_digest, adr_ref,
			 issued_by, tool_version, trace_id, actor_chain_json, source_commit)
		 VALUES (?, ?, ?, ?, 0, NULL, ?, ?, ?, ?, ?,
		 ?, ?, ?,
		 ?, ?, ?,
		 ?, ?, ?, ?, ?)`,
		r.ReceiptID, r.Issuer, string(pathsJSON), timeToUnix(r.ExpiresAt), timeToUnix(r.CreatedAt),
		approachIdx, attemptIdx, rcaConfidence, adrPath,
		envelopeSchemaURI, envelopeFieldOrder, envelopeRequiredFlds,
		rulesetVersion, pipelineDigest, adrRefVal,
		issuedBy, toolVersion, traceID, actorChainJSON, sourceCommit,
	); err != nil {
		return nil, fmt.Errorf("insert receipt %q: %w", r.ReceiptID, err)
	}
	return r, nil
}

// validateCanonicalEnvelope enforces mandatory invariants when a Brain-tier
// caller attaches the envelope. SchemaURI must start with g8s://, FieldOrder
// must be non-empty.
func validateCanonicalEnvelope(env *CanonicalEnvelope) error {
	if env == nil {
		return ErrEnvelopeRequired
	}
	if !strings.HasPrefix(env.SchemaURI, envelopeSchemaPrefix) {
		return ErrEnvelopeSchemaURI
	}
	if len(env.FieldOrder) == 0 {
		return ErrEnvelopeFieldOrder
	}
	return nil
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
		`SELECT `+receiptSelectColumns+`
		 FROM write_receipts WHERE receipt_id = ?`, receiptID,
	)
	var (
		issuer                 string
		pathsJSON              string
		expiresAt              float64
		consumed               int
		storedTask             sql.NullString
		createdAt              float64
		approachIdx            sql.NullInt64
		attemptIdx             sql.NullInt64
		rcaConfidence          sql.NullFloat64
		adrPath                sql.NullString
		envelopeSchemaURI      sql.NullString
		envelopeFieldOrderJSON sql.NullString
		envelopeRequiredJSON   sql.NullString
		rulesetVersion         sql.NullString
		pipelineDigest         sql.NullString
		adrRefVal              sql.NullString
		issuedBy               sql.NullString
		toolVersion            sql.NullString
		traceID                sql.NullString
		actorChainJSON         sql.NullString
		sourceCommit           sql.NullString
	)
	scanErr := row.Scan(&issuer, &pathsJSON, &expiresAt, &consumed, &storedTask, &createdAt,
		&approachIdx, &attemptIdx, &rcaConfidence, &adrPath,
		&envelopeSchemaURI, &envelopeFieldOrderJSON, &envelopeRequiredJSON,
		&rulesetVersion, &pipelineDigest, &adrRefVal,
		&issuedBy, &toolVersion, &traceID, &actorChainJSON, &sourceCommit,
	)
	switch {
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

	// Decode JSON + envelope fields BEFORE consuming so corrupted payloads
	// return an error without burning the receipt (B3 adversarial blocker).
	r, err := scanIntoReceipt(row, receiptID, pathsJSON, expiresAt, consumed, storedTask, createdAt,
		approachIdx, attemptIdx, rcaConfidence, adrPath,
		envelopeSchemaURI, envelopeFieldOrderJSON, envelopeRequiredJSON,
		rulesetVersion, pipelineDigest, adrRefVal,
		issuedBy, toolVersion, traceID, actorChainJSON, sourceCommit,
	)
	if err != nil {
		return nil, err
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

	r.Issuer = issuer
	r.Consumed = true
	taskID := consumerTaskID
	r.ConsumerTaskID = &taskID
	if r.ProvenanceLineage != nil && consumerTaskID != "" {
		r.ProvenanceLineage.ActorChain = append(r.ProvenanceLineage.ActorChain, consumerTaskID)
	}
	return r, nil
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
		`SELECT receipt_id, `+receiptSelectColumns+`
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
			receiptID              string
			issuer                 string
			pathsJSON              string
			expiresAt              float64
			consumed               int
			storedTask             sql.NullString
			createdAt              float64
			approachIdx            sql.NullInt64
			attemptIdx             sql.NullInt64
			rcaConfidence          sql.NullFloat64
			adrPath                sql.NullString
			envelopeSchemaURI      sql.NullString
			envelopeFieldOrderJSON sql.NullString
			envelopeRequiredJSON   sql.NullString
			rulesetVersion         sql.NullString
			pipelineDigest         sql.NullString
			adrRefVal              sql.NullString
			issuedBy               sql.NullString
			toolVersion            sql.NullString
			traceID                sql.NullString
			actorChainJSON         sql.NullString
			sourceCommit           sql.NullString
		)
		if err := rows.Scan(&receiptID, &issuer, &pathsJSON, &expiresAt, &consumed, &storedTask, &createdAt,
			&approachIdx, &attemptIdx, &rcaConfidence, &adrPath,
			&envelopeSchemaURI, &envelopeFieldOrderJSON, &envelopeRequiredJSON,
			&rulesetVersion, &pipelineDigest, &adrRefVal,
			&issuedBy, &toolVersion, &traceID, &actorChainJSON, &sourceCommit,
		); err != nil {
			return nil, fmt.Errorf("scan active receipt: %w", err)
		}
		r, err := scanIntoReceipt(rows, receiptID, pathsJSON, expiresAt, consumed, storedTask, createdAt,
			approachIdx, attemptIdx, rcaConfidence, adrPath,
			envelopeSchemaURI, envelopeFieldOrderJSON, envelopeRequiredJSON,
			rulesetVersion, pipelineDigest, adrRefVal,
			issuedBy, toolVersion, traceID, actorChainJSON, sourceCommit,
		)
		if err != nil {
			return nil, err
		}
		r.Issuer = issuer
		out = append(out, r)
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
		`SELECT `+receiptSelectColumns+`
		 FROM write_receipts WHERE receipt_id = ?`, receiptID,
	)
	var (
		issuer                 string
		pathsJSON              string
		expiresAt              float64
		consumed               int
		storedTask             sql.NullString
		createdAt              float64
		approachIdx            sql.NullInt64
		attemptIdx             sql.NullInt64
		rcaConfidence          sql.NullFloat64
		adrPath                sql.NullString
		envelopeSchemaURI      sql.NullString
		envelopeFieldOrderJSON sql.NullString
		envelopeRequiredJSON   sql.NullString
		rulesetVersion         sql.NullString
		pipelineDigest         sql.NullString
		adrRefVal              sql.NullString
		issuedBy               sql.NullString
		toolVersion            sql.NullString
		traceID                sql.NullString
		actorChainJSON         sql.NullString
		sourceCommit           sql.NullString
	)
	scanErr := row.Scan(&issuer, &pathsJSON, &expiresAt, &consumed, &storedTask, &createdAt,
		&approachIdx, &attemptIdx, &rcaConfidence, &adrPath,
		&envelopeSchemaURI, &envelopeFieldOrderJSON, &envelopeRequiredJSON,
		&rulesetVersion, &pipelineDigest, &adrRefVal,
		&issuedBy, &toolVersion, &traceID, &actorChainJSON, &sourceCommit,
	)
	switch {
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

	r, err := scanIntoReceipt(row, receiptID, pathsJSON, expiresAt, consumed, storedTask, createdAt,
		approachIdx, attemptIdx, rcaConfidence, adrPath,
		envelopeSchemaURI, envelopeFieldOrderJSON, envelopeRequiredJSON,
		rulesetVersion, pipelineDigest, adrRefVal,
		issuedBy, toolVersion, traceID, actorChainJSON, sourceCommit,
	)
	if err != nil {
		return nil, err
	}
	r.Issuer = issuer
	return r, nil
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

// receiptSelectColumns is the canonical column list for every read path that
// materializes a WriteReceipt from a row. Keep INSERT in IssueReceipt in sync.
const receiptSelectColumns = `issuer, allowed_paths_json, expires_at, consumed, consumer_task_id, created_at,
		approach_idx, attempt_idx, rca_confidence, adr_path,
		envelope_schema_uri, envelope_field_order_json, envelope_required_fields_json,
		ruleset_version, pipeline_digest, adr_ref,
		issued_by, tool_version, trace_id, actor_chain_json, source_commit`

// scanIntoReceipt scans one row into a WriteReceipt and the provided aux
// pointers (consumed, createdAt, pathsJSON). Returns sql.ErrNoRows pass-through
// for callers to detect NotFoundError.
func scanIntoReceipt(
	row interface {
		Scan(dest ...any) error
	},
	receiptID string,
	pathsJSON string,
	expiresAt float64,
	consumed int,
	storedTask sql.NullString,
	createdAt float64,
	approachIdx sql.NullInt64,
	attemptIdx sql.NullInt64,
	rcaConfidence sql.NullFloat64,
	adrPath sql.NullString,
	envelopeSchemaURI sql.NullString,
	envelopeFieldOrderJSON sql.NullString,
	envelopeRequiredFieldsJSON sql.NullString,
	rulesetVersion sql.NullString,
	pipelineDigest sql.NullString,
	adrRefVal sql.NullString,
	issuedBy sql.NullString,
	toolVersion sql.NullString,
	traceID sql.NullString,
	actorChainJSON sql.NullString,
	sourceCommit sql.NullString,
) (*WriteReceipt, error) {
	var paths []string
	if err := json.Unmarshal([]byte(pathsJSON), &paths); err != nil {
		return nil, fmt.Errorf("deserialize allowed_paths of %q: %w", receiptID, err)
	}

	r := &WriteReceipt{
		ReceiptID:    receiptID,
		AllowedPaths: paths,
		ExpiresAt:    unixToTime(expiresAt),
		Consumed:     consumed == 1,
		CreatedAt:    unixToTime(createdAt),
	}

	var issuer string
	if issuedBy.Valid {
		issuer = issuedBy.String
	}

	if approachIdx.Valid || attemptIdx.Valid || rcaConfidence.Valid || adrPath.Valid {
		r.SupervisorMeta = &SupervisorMeta{
			ApproachIdx:   int(approachIdx.Int64),
			AttemptIdx:    int(attemptIdx.Int64),
			RCAConfidence: rcaConfidence.Float64,
			ADRPath:       adrPath.String,
		}
	}

	hasNonEmpty := func(s sql.NullString) bool { return s.Valid && s.String != "" }
	if hasNonEmpty(envelopeSchemaURI) || hasNonEmpty(envelopeFieldOrderJSON) || hasNonEmpty(envelopeRequiredFieldsJSON) {
		env := &CanonicalEnvelope{SchemaURI: envelopeSchemaURI.String}
		if hasNonEmpty(envelopeFieldOrderJSON) {
			_ = json.Unmarshal([]byte(envelopeFieldOrderJSON.String), &env.FieldOrder)
		}
		if hasNonEmpty(envelopeRequiredFieldsJSON) {
			_ = json.Unmarshal([]byte(envelopeRequiredFieldsJSON.String), &env.RequiredFields)
		}
		r.CanonicalEnvelope = env
	}
	if hasNonEmpty(rulesetVersion) || hasNonEmpty(pipelineDigest) || hasNonEmpty(adrRefVal) {
		r.RuleGraphSnapshot = &RuleGraphSnapshot{
			RulesetVersion: rulesetVersion.String,
			PipelineDigest: pipelineDigest.String,
			ADRRef:         adrRefVal.String,
		}
	}
	if hasNonEmpty(issuedBy) || hasNonEmpty(toolVersion) || hasNonEmpty(traceID) || hasNonEmpty(actorChainJSON) || hasNonEmpty(sourceCommit) {
		prov := &ProvenanceLineage{
			IssuedBy:     issuedBy.String,
			ToolVersion:  toolVersion.String,
			TraceID:      traceID.String,
			SourceCommit: sourceCommit.String,
		}
		if hasNonEmpty(actorChainJSON) {
			_ = json.Unmarshal([]byte(actorChainJSON.String), &prov.ActorChain)
		}
		r.ProvenanceLineage = prov
	}

	if storedTask.Valid {
		tid := storedTask.String
		r.ConsumerTaskID = &tid
	}
	if issuer != "" && r.Issuer == "" {
		r.Issuer = issuer
	}
	return r, nil
}

// PurgeExpired deletes receipts whose (a) TTL has elapsed AND (b) age exceeds
// maxAge, OR which are consumed, bounded by maxRows per call to avoid long DB
// locks under load. Returns the count of rows deleted.
func (m *Manager) PurgeExpired(maxAge time.Duration, maxRows int) (int64, error) {
	if maxRows <= 0 {
		return 0, fmt.Errorf("maxRows must be > 0, got %d", maxRows)
	}
	if maxAge < 0 {
		return 0, fmt.Errorf("maxAge must be >= 0, got %s", maxAge)
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.clock()
	cutoff := timeToUnix(now.Add(-maxAge))
	nowUnix := timeToUnix(now)

	res, err := m.db.Exec(
		`DELETE FROM write_receipts
		 WHERE receipt_id IN (
			 SELECT receipt_id FROM write_receipts
			 WHERE (consumed = 1 OR expires_at < ?)
			   AND created_at < ?
			 LIMIT ?
		 )`,
		nowUnix, cutoff, maxRows,
	)
	if err != nil {
		return 0, fmt.Errorf("purge expired receipts: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count purged rows: %w", err)
	}
	return affected, nil
}
