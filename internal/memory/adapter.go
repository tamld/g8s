package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/receipt"
	"github.com/tamld/g8s/internal/vault"
	_ "modernc.org/sqlite"
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
	mu             sync.RWMutex
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

	// Enforce POSIX 0600 file permissions for SQLite databases created on disk
	if dbPath != ":memory:" {
		f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, fmt.Errorf("memory: enforce 0600 permissions on %s: %w", dbPath, err)
		}
		if err := f.Close(); err != nil {
			return nil, fmt.Errorf("memory: close file %s: %w", dbPath, err)
		}
		if err := os.Chmod(dbPath, 0o600); err != nil {
			return nil, fmt.Errorf("memory: chmod 0600 on %s: %w", dbPath, err)
		}
	}

	// #380: use pathutil.SQLiteURI for cross-platform path escaping (was raw
	// fmt.Sprintf — Windows paths failed, same bug as #336 finding 3).
	dsn := pathutil.SQLiteURI(dbPath, "_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
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
	`
	_, err := a.db.Exec(schema)
	return err
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

	_, err = a.db.ExecContext(ctx, query,
		taskID,
		wCtx.Prompt,
		wCtx.Role,
		string(pathsJSON),
		string(status),
		wCtx.RedactedDigest,
		createdAt,
		now,
	)
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

	row := a.db.QueryRowContext(ctx, query, taskID)

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

	err := row.Scan(&tID, &prompt, &role, &pathsJSON, &statusStr, &redactedDigest, &createdAt, &updatedAt)
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
