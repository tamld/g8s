// Package memory — provides a unified memory facade across working, episodic, semantic, and capability storage tiers.
package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/receipt"
	"github.com/tamld/g8s/internal/vault"
	"modernc.org/sqlite"
)

// AdapterOptions configures the LocalSQLiteMemoryAdapter.
type AdapterOptions struct {
	DBPath         string // Path to memory.db or ":memory:"
	ControlPlane   *controlplane.Store
	Vault          *vault.Vault
	ReceiptManager receipt.ReceiptManager
	Clock          func() time.Time
}

// LocalSQLiteMemoryAdapter implements MemoryAdapter as a Pure-Go facade over
// working memory, controlplane (episodic), vault (semantic), and receipt (capability).
type LocalSQLiteMemoryAdapter struct {
	db             *sql.DB
	controlPlane   *controlplane.Store
	vault          *vault.Vault
	receiptManager receipt.ReceiptManager
	clock          func() time.Time
	gateSensor     GateSensor
	sinkFn         GateEventSink
	mu             sync.RWMutex

	// proposeSeam is a test-only scheduling seam (C-12, #986): tests set it
	// to interleave a concurrent revocation while a ProposeEntry is in
	// flight. Production leaves it nil — the call is a no-op.
	proposeSeam func()
}

// NewLocalSQLiteMemoryAdapter creates and initializes a LocalSQLiteMemoryAdapter.
func NewLocalSQLiteMemoryAdapter(opts AdapterOptions) (*LocalSQLiteMemoryAdapter, error) {
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}

	dbPath := opts.DBPath
	if dbPath == "" {
		dbPath = ":memory:"
	}

	if err := pathutil.PrepareSQLiteFile(dbPath); err != nil {
		return nil, fmt.Errorf("memory: prepare sqlite %s: %w", dbPath, err)
	}

	// #380: use pathutil.SQLiteURI for cross-platform path escaping (was raw
	// fmt.Sprintf — Windows paths failed, same bug as #336 finding 3).
	dsn := pathutil.SQLiteURI(dbPath, "_txlock=immediate&_pragma=busy_timeout(30000)&_pragma=foreign_keys(ON)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("memory: open sqlite: %w", err)
	}

	// #380: WAL mode supports concurrent readers — raise from 1 to 4.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)

	adapter := &LocalSQLiteMemoryAdapter{
		db:             db,
		controlPlane:   opts.ControlPlane,
		vault:          opts.Vault,
		receiptManager: opts.ReceiptManager,
		clock:          clock,
	}

	if err := adapter.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("memory: init schema: %w", err)
	}
	if err := pathutil.HardenSQLiteFiles(dbPath); err != nil {
		db.Close()
		return nil, fmt.Errorf("memory: harden sqlite %s: %w", dbPath, err)
	}

	if adapter.sinkFn == nil {
		adapter.sinkFn = func(event, detail string) {}
	}

	return adapter, nil
}

func (a *LocalSQLiteMemoryAdapter) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS working_contexts (
		task_id TEXT PRIMARY KEY,
		prompt TEXT NOT NULL,
		role TEXT NOT NULL,
		allowed_paths TEXT NOT NULL,
		status TEXT NOT NULL,
		redacted_digest TEXT,
		created_at TIMESTAMP NOT NULL,
		updated_at TIMESTAMP NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_working_contexts_status ON working_contexts(status);

	CREATE TABLE IF NOT EXISTS episodic_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL,
		event_type TEXT NOT NULL,
		payload TEXT,
		created_at TIMESTAMP NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_episodic_events_task ON episodic_events(task_id);

	CREATE TABLE IF NOT EXISTS vector_embeddings (
		record_id TEXT PRIMARY KEY,
		vector_blob BLOB NOT NULL,
		dimensions INTEGER NOT NULL,
		metadata TEXT,
		created_at TIMESTAMP NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_vector_dimensions ON vector_embeddings(dimensions);

	CREATE TABLE IF NOT EXISTS memory_entries (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		lifecycle TEXT NOT NULL,
		trust TEXT NOT NULL,
		salience INTEGER NOT NULL DEFAULT 0,
		last_used_at REAL NOT NULL DEFAULT 0,
		scope TEXT NOT NULL,
		session_id TEXT NOT NULL DEFAULT '',
		task_id TEXT NOT NULL DEFAULT '',
		promoted_by TEXT NOT NULL DEFAULT '',
		payload TEXT NOT NULL,
		payload_hash TEXT NOT NULL,
		preconditions TEXT NOT NULL DEFAULT '{}',
		superseded_by TEXT NOT NULL DEFAULT '',
		valid_until REAL NOT NULL DEFAULT 0,
		applied_count INTEGER NOT NULL DEFAULT 0,
		first_pass_success_count INTEGER NOT NULL DEFAULT 0,
		failure_after_recall_count INTEGER NOT NULL DEFAULT 0,
		corroborating_sessions TEXT NOT NULL DEFAULT '[]',
		review_flag TEXT NOT NULL DEFAULT '',
		outcome_path TEXT NOT NULL DEFAULT '',
		provenance_json TEXT NOT NULL DEFAULT '{}',
		confidence REAL NOT NULL DEFAULT 0,
		confidence_class TEXT NOT NULL DEFAULT '',
		applicability_scope TEXT NOT NULL DEFAULT '',
		promotion_status TEXT NOT NULL DEFAULT '',
		revocable INTEGER NOT NULL DEFAULT 1,
		created_at REAL NOT NULL,
		updated_at REAL NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_memory_entries_session ON memory_entries(session_id);
	CREATE INDEX IF NOT EXISTS idx_memory_entries_lifecycle ON memory_entries(lifecycle);

	CREATE TABLE IF NOT EXISTS memory_tombstones (
		payload_hash TEXT PRIMARY KEY,
		reason TEXT NOT NULL DEFAULT '',
		created_at REAL NOT NULL
	);
	`
	if _, err := a.db.Exec(schema); err != nil {
		return err
	}
	for _, ddl := range []string{
		`ALTER TABLE memory_entries ADD COLUMN preconditions TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE memory_entries ADD COLUMN superseded_by TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE memory_entries ADD COLUMN valid_until REAL NOT NULL DEFAULT 0`,
		`ALTER TABLE memory_entries ADD COLUMN applied_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE memory_entries ADD COLUMN first_pass_success_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE memory_entries ADD COLUMN failure_after_recall_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE memory_entries ADD COLUMN corroborating_sessions TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE memory_entries ADD COLUMN review_flag TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE memory_entries ADD COLUMN outcome_path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE memory_entries ADD COLUMN provenance_json TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE memory_entries ADD COLUMN confidence REAL NOT NULL DEFAULT 0`,
		`ALTER TABLE memory_entries ADD COLUMN confidence_class TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE memory_entries ADD COLUMN applicability_scope TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE memory_entries ADD COLUMN promotion_status TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE memory_entries ADD COLUMN revocable INTEGER NOT NULL DEFAULT 1`,
	} {
		if err := addColumnToleratingDuplicate(a.db, ddl); err != nil {
			return err
		}
	}
	// The unique (payload_hash, session_id) index is created last, after the
	// legacy rows have been pre-deduped (C-11): creating it earlier bricks
	// databases that accumulated duplicate active rows before the index
	// existed.
	if err := a.applyUniquePayloadIndex(); err != nil {
		return err
	}
	return nil
}

// applyUniquePayloadIndex pre-dedups memory_entries and creates the
// (payload_hash, session_id) unique index inside one immediate transaction
// (C-11, #986): legacy databases that already contain duplicate non-scratch
// rows would otherwise fail index creation permanently and become unreadable.
// The newest row per (payload_hash, session_id) is kept; older duplicates are
// demoted to 'scratch' and flagged for review — the partial index covers every
// lifecycle except scratch/working (including 'archived'), so demotion is the
// only state change that removes a row from the index without deleting it.
// Scratch/working rows themselves are exempt and untouched.
func (a *LocalSQLiteMemoryAdapter) applyUniquePayloadIndex() error {
	tx, err := a.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("memory: begin unique-index migration: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		UPDATE memory_entries
		SET lifecycle = 'scratch',
		    review_flag = 'duplicate_demoted_on_migration'
		WHERE lifecycle NOT IN ('scratch', 'working')
		  AND id NOT IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (
					PARTITION BY payload_hash, session_id
					ORDER BY updated_at DESC, created_at DESC, id DESC
				) AS rn
				FROM memory_entries
				WHERE lifecycle NOT IN ('scratch', 'working')
			)
			WHERE rn = 1
		)`); err != nil {
		return fmt.Errorf("memory: pre-dedup for unique payload index: %w", err)
	}

	if _, err := tx.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_entries_payload ON memory_entries(payload_hash, session_id)
			WHERE lifecycle NOT IN ('scratch', 'working')`); err != nil {
		return fmt.Errorf("memory: create unique payload index: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("memory: commit unique-index migration: %w", err)
	}
	return nil
}

// addColumnToleratingDuplicate applies one additive-column migration,
// tolerating ONLY the duplicate-column error that re-running the migration on
// an already-migrated database produces (mirrors the controlplane pattern in
// lifecycle.go). Every other failure — locked database, corrupted schema,
// readonly file — propagates instead of being silently skipped (C-7, #986).
func addColumnToleratingDuplicate(db *sql.DB, ddl string) error {
	if _, err := db.Exec(ddl); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return nil
		}
		return fmt.Errorf("memory: migration %q: %w", ddl, err)
	}
	return nil
}

// Vault returns the underlying semantic Knowledge Vault configured on the adapter.
func (a *LocalSQLiteMemoryAdapter) Vault() *vault.Vault {
	return a.vault
}

// Close closes the underlying SQLite database.
func (a *LocalSQLiteMemoryAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

// Package-level retry configuration for SQLITE_BUSY handling (#440).
var (
	busyRetryAttempts = 3
	busyBackoff       = func(attempt int) time.Duration {
		return time.Duration(50+rand.IntN(151)) * time.Millisecond
	}
)

// isBusyErr reports whether err is a SQLITE_BUSY-class error.
func isBusyErr(err error) bool {
	if err == nil {
		return false
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
	return err
}

// -----------------------------------------------------------------------------
// Working Memory
// -----------------------------------------------------------------------------

// StoreWorkingContext persists the ephemeral execution context for a task.
func (a *LocalSQLiteMemoryAdapter) StoreWorkingContext(ctx context.Context, taskID string, wCtx *WorkingContext) error {
	if taskID == "" || wCtx == nil {
		return ErrInvalidInput
	}

	pathsJSON, err := json.Marshal(wCtx.AllowedPaths)
	if err != nil {
		return fmt.Errorf("memory: marshal allowed_paths: %w", err)
	}

	now := a.clock()
	createdAt := wCtx.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}

	status := wCtx.Status
	if status == "" {
		status = StatusActive
	}

	query := `
	INSERT INTO working_contexts (task_id, prompt, role, allowed_paths, status, redacted_digest, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(task_id) DO UPDATE SET
		prompt = excluded.prompt,
		role = excluded.role,
		allowed_paths = excluded.allowed_paths,
		status = excluded.status,
		redacted_digest = excluded.redacted_digest,
		updated_at = excluded.updated_at
	`

	err = withBusyRetry(func() error {
		_, execErr := a.db.ExecContext(ctx, query,
			taskID,
			wCtx.Prompt,
			wCtx.Role,
			string(pathsJSON),
			string(status),
			wCtx.RedactedDigest,
			createdAt,
			now,
		)
		return execErr
	})
	if err != nil {
		return fmt.Errorf("memory: store working context: %w", err)
	}

	return nil
}

// LoadWorkingContext retrieves the active or compacted working context for a task.
func (a *LocalSQLiteMemoryAdapter) LoadWorkingContext(ctx context.Context, taskID string) (*WorkingContext, error) {
	if taskID == "" {
		return nil, ErrInvalidInput
	}

	query := `
	SELECT task_id, prompt, role, allowed_paths, status, redacted_digest, created_at, updated_at
	FROM working_contexts
	WHERE task_id = ?
	`

	var (
		tID            string
		prompt         string
		role           string
		pathsJSON      string
		statusStr      string
		redactedDigest sql.NullString
		createdAt      time.Time
		updatedAt      time.Time
	)

	err := withBusyRetry(func() error {
		row := a.db.QueryRowContext(ctx, query, taskID)
		return row.Scan(&tID, &prompt, &role, &pathsJSON, &statusStr, &redactedDigest, &createdAt, &updatedAt)
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("memory: load working context: %w", err)
	}

	var allowedPaths []string
	if pathsJSON != "" {
		_ = json.Unmarshal([]byte(pathsJSON), &allowedPaths)
	}

	digest := ""
	if redactedDigest.Valid {
		digest = redactedDigest.String
	}

	return &WorkingContext{
		TaskID:         tID,
		Prompt:         prompt,
		Role:           role,
		AllowedPaths:   allowedPaths,
		Status:         WorkingContextStatus(statusStr),
		RedactedDigest: digest,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}, nil
}

// PurgeWorkingContext redacts the prompt text with its SHA-256 digest and sets status to PURGED.
func (a *LocalSQLiteMemoryAdapter) PurgeWorkingContext(ctx context.Context, taskID string) error {
	if taskID == "" {
		return ErrInvalidInput
	}

	existing, err := a.LoadWorkingContext(ctx, taskID)
	if err != nil {
		return err
	}

	digest := existing.RedactedDigest
	if digest == "" && existing.Prompt != "" {
		sum := sha256.Sum256([]byte(existing.Prompt))
		digest = fmt.Sprintf("sha256:%x", sum)
	}

	now := a.clock()
	query := `
	UPDATE working_contexts
	SET prompt = '', status = ?, redacted_digest = ?, updated_at = ?
	WHERE task_id = ?
	`

	_, err = a.db.ExecContext(ctx, query, string(StatusPurged), digest, now, taskID)
	if err != nil {
		return fmt.Errorf("memory: purge working context: %w", err)
	}

	return nil
}

// -----------------------------------------------------------------------------
// Episodic Memory
// -----------------------------------------------------------------------------

// RecordEpisode appends an execution event to the episodic log.
func (a *LocalSQLiteMemoryAdapter) RecordEpisode(ctx context.Context, taskID string, event *EpisodicEvent) error {
	if taskID == "" || event == nil {
		return ErrInvalidInput
	}

	payloadJSON := ""
	if event.Payload != nil {
		b, err := json.Marshal(event.Payload)
		if err == nil {
			payloadJSON = string(b)
		}
	}

	now := event.CreatedAt
	if now.IsZero() {
		now = a.clock()
	}

	query := `
	INSERT INTO episodic_events (task_id, event_type, payload, created_at)
	VALUES (?, ?, ?, ?)
	`

	_, err := a.db.ExecContext(ctx, query, taskID, event.EventType, payloadJSON, now)
	if err != nil {
		return fmt.Errorf("memory: record episode: %w", err)
	}

	return nil
}

// GetLineageTree constructs a causal task DAG showing ancestry and descendant child tasks.
func (a *LocalSQLiteMemoryAdapter) GetLineageTree(ctx context.Context, taskID string) (*LineageDAG, error) {
	if taskID == "" {
		return nil, ErrInvalidInput
	}

	if a.controlPlane != nil {
		lineage, err := a.controlPlane.GetTaskLineage(ctx, taskID)
		if err != nil {
			return nil, fmt.Errorf("memory: query controlplane lineage: %w", err)
		}

		children, err := a.controlPlane.ListChildTasks(ctx, taskID)
		if err != nil {
			return nil, fmt.Errorf("memory: query controlplane children: %w", err)
		}

		nodes := make([]*LineageNode, 0, len(lineage)+len(children))
		for _, t := range lineage {
			var exitCode *int
			switch t.State {
			case controlplane.StateSucceeded, "WORKER_COMPLETED":
				code := 0
				exitCode = &code
			case controlplane.StateFailed, "SUPERVISOR_REJECTED", "REJECTED":
				code := 1
				exitCode = &code
			}
			parentID := ""
			if t.ParentTaskID != nil {
				parentID = *t.ParentTaskID
			}
			workerName := ""
			if t.WorkerName != nil {
				workerName = *t.WorkerName
			}
			createdTime := time.Unix(int64(t.CreatedAt), 0)

			nodes = append(nodes, &LineageNode{
				TaskID:       t.TaskID,
				ParentTaskID: parentID,
				Role:         workerName,
				Status:       t.State,
				ExitCode:     exitCode,
				CreatedAt:    createdTime,
			})
		}

		// Attach children to the target task node
		var targetNode *LineageNode
		for _, n := range nodes {
			if n.TaskID == taskID {
				targetNode = n
				break
			}
		}

		for _, ch := range children {
			var exitCode *int
			switch ch.State {
			case controlplane.StateSucceeded, "WORKER_COMPLETED":
				code := 0
				exitCode = &code
			case controlplane.StateFailed, "SUPERVISOR_REJECTED", "REJECTED":
				code := 1
				exitCode = &code
			}
			parentID := ""
			if ch.ParentTaskID != nil {
				parentID = *ch.ParentTaskID
			}
			workerName := ""
			if ch.WorkerName != nil {
				workerName = *ch.WorkerName
			}
			createdTime := time.Unix(int64(ch.CreatedAt), 0)

			childNode := &LineageNode{
				TaskID:       ch.TaskID,
				ParentTaskID: parentID,
				Role:         workerName,
				Status:       ch.State,
				ExitCode:     exitCode,
				CreatedAt:    createdTime,
			}
			if targetNode != nil {
				targetNode.Children = append(targetNode.Children, childNode)
			}
			nodes = append(nodes, childNode)
		}

		rootID := taskID
		if len(lineage) > 0 {
			rootID = lineage[0].TaskID
		}

		return &LineageDAG{
			RootTaskID: rootID,
			TotalNodes: len(nodes),
			Nodes:      nodes,
		}, nil
	}

	// Fallback when controlplane is nil: query working context
	wCtx, err := a.LoadWorkingContext(ctx, taskID)
	if err != nil {
		if err == ErrNotFound {
			return &LineageDAG{RootTaskID: taskID, TotalNodes: 0, Nodes: nil}, nil
		}
		return nil, err
	}

	node := &LineageNode{
		TaskID:    wCtx.TaskID,
		Role:      wCtx.Role,
		Status:    string(wCtx.Status),
		CreatedAt: wCtx.CreatedAt,
	}

	return &LineageDAG{
		RootTaskID: taskID,
		TotalNodes: 1,
		Nodes:      []*LineageNode{node},
	}, nil
}

// -----------------------------------------------------------------------------
// Semantic Memory
// -----------------------------------------------------------------------------

// StoreKnowledge indexes a tri-anchor distillation record in the Knowledge Vault.
func (a *LocalSQLiteMemoryAdapter) StoreKnowledge(ctx context.Context, record *vault.DistillationRecord) error {
	if record == nil {
		return ErrInvalidInput
	}
	if a.vault == nil {
		return fmt.Errorf("memory: semantic vault not configured")
	}

	_, err := a.vault.Store(ctx, *record)
	return err
}

// SearchKnowledge executes a BM25 ranked full-text query over knowledge distillation records.
func (a *LocalSQLiteMemoryAdapter) SearchKnowledge(ctx context.Context, query string, limit int) ([]vault.SearchResult, error) {
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	if a.vault == nil {
		return nil, fmt.Errorf("memory: semantic vault not configured")
	}

	return a.vault.Query(ctx, query, limit)
}

// -----------------------------------------------------------------------------
// Capability Memory
// -----------------------------------------------------------------------------

// ValidateCapability validates and consumes a single-use write receipt.
func (a *LocalSQLiteMemoryAdapter) ValidateCapability(ctx context.Context, receiptID string, consumerTaskID string) (*receipt.WriteReceipt, error) {
	if receiptID == "" {
		return nil, ErrInvalidInput
	}
	if a.receiptManager == nil {
		return nil, fmt.Errorf("memory: capability receipt manager not configured")
	}

	return a.receiptManager.ValidateAndConsume(receiptID, consumerTaskID)
}

// -----------------------------------------------------------------------------
// Vector Similarity Engine
// -----------------------------------------------------------------------------

// StoreVector indexes an embedding vector alongside record metadata.
func (a *LocalSQLiteMemoryAdapter) StoreVector(ctx context.Context, recordID string, vector []float32, metadata map[string]any) error {
	if recordID == "" || len(vector) == 0 {
		return ErrInvalidInput
	}

	blob := EncodeVectorBlob(vector)
	metaJSON := ""
	if metadata != nil {
		b, err := json.Marshal(metadata)
		if err == nil {
			metaJSON = string(b)
		}
	}

	query := `
	INSERT INTO vector_embeddings (record_id, vector_blob, dimensions, metadata, created_at)
	VALUES (?, ?, ?, ?, ?)
	ON CONFLICT(record_id) DO UPDATE SET
		vector_blob = excluded.vector_blob,
		dimensions = excluded.dimensions,
		metadata = excluded.metadata
	`

	now := a.clock()
	_, err := a.db.ExecContext(ctx, query, recordID, blob, len(vector), metaJSON, now)
	if err != nil {
		return fmt.Errorf("memory: store vector: %w", err)
	}

	return nil
}

// SearchVector ranks stored vectors by pure-Go cosine similarity against queryVector.
func (a *LocalSQLiteMemoryAdapter) SearchVector(ctx context.Context, queryVector []float32, limit int, minScore float64) ([]VectorMatch, error) {
	if len(queryVector) == 0 {
		return nil, ErrEmptyVector
	}
	if limit <= 0 {
		limit = 10
	}

	query := `SELECT record_id, vector_blob, dimensions, metadata FROM vector_embeddings WHERE dimensions = ?`
	rows, err := a.db.QueryContext(ctx, query, len(queryVector))
	if err != nil {
		return nil, fmt.Errorf("memory: query vectors: %w", err)
	}
	defer rows.Close()

	type rawCandidate struct {
		recordID   string
		score      float64
		dimensions int
		metaJSON   string
	}

	var candidates []rawCandidate
	for rows.Next() {
		var (
			recordID string
			blob     []byte
			dims     int
			metaJSON sql.NullString
		)

		if err := rows.Scan(&recordID, &blob, &dims, &metaJSON); err != nil {
			return nil, fmt.Errorf("memory: scan vector row: %w", err)
		}

		storedVec, err := DecodeVectorBlob(blob)
		if err != nil {
			continue
		}

		score, err := CosineSimilarity(queryVector, storedVec)
		if err != nil {
			continue
		}

		if score >= minScore {
			rawMeta := ""
			if metaJSON.Valid {
				rawMeta = metaJSON.String
			}
			candidates = append(candidates, rawCandidate{
				recordID:   recordID,
				score:      score,
				dimensions: dims,
				metaJSON:   rawMeta,
			})
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory: iterate vector rows: %w", err)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	matches := make([]VectorMatch, 0, len(candidates))
	for _, c := range candidates {
		var meta map[string]any
		if c.metaJSON != "" {
			_ = json.Unmarshal([]byte(c.metaJSON), &meta)
		}
		matches = append(matches, VectorMatch{
			RecordID:   c.recordID,
			Score:      c.score,
			Dimensions: c.dimensions,
			Metadata:   meta,
		})
	}

	return matches, nil
}
