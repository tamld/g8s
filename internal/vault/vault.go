// Package vault implements a Zero-CGO, decoupled Knowledge Vault for g8s
// utilizing SQLite FTS5 with BM25 ranking to index and retrieve Tri-Anchor
// contextual distillation records.
package vault

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound      = errors.New("vault record not found")
	ErrInvalidRecord = errors.New("invalid distillation record")
)

// DistillationRecord defines the Tri-Anchor contextual distillation schema
// according to docs/CONTEXTUAL_DISTILLATION_SPEC.md.
type DistillationRecord struct {
	ID                   string            `json:"id"`
	Title                string            `json:"title"`
	Milestone            string            `json:"milestone"`
	Status               string            `json:"status"` // PROPOSED, ACCEPTED, APPLIED, DEPRECATED
	CreatedAt            time.Time         `json:"created_at"`
	UpdatedAt            time.Time         `json:"updated_at"`
	Causality            CausalityAnchor   `json:"causality"`
	SpatialCoordinates   SpatialAnchor     `json:"spatial_coordinates"`
	ForensicVerification ForensicAnchor    `json:"forensic_verification"`
	Metadata             map[string]string `json:"metadata,omitempty"`
}

type CausalityAnchor struct {
	Problem   string `json:"problem"`
	TradeOff  string `json:"trade_off"`
	RootCause string `json:"root_cause,omitempty"`
}

type SpatialAnchor struct {
	Package         string   `json:"package"`
	File            string   `json:"file"`
	Symbol          string   `json:"symbol,omitempty"`
	DeniedFragments []string `json:"denied_fragments,omitempty"`
}

type ForensicAnchor struct {
	TestFile     string `json:"test_file"`
	TestCase     string `json:"test_case"`
	ExitCriteria string `json:"exit_criteria,omitempty"`
	ReceiptHash  string `json:"receipt_hash,omitempty"`
}

// SearchResult represents a match from FTS5 query ranking.
type SearchResult struct {
	Record  DistillationRecord `json:"record"`
	Score   float64            `json:"score"`
	Snippet string             `json:"snippet,omitempty"`
}

// VaultFilter defines parameters for listing records.
type VaultFilter struct {
	Milestone *string `json:"milestone,omitempty"`
	Status    *string `json:"status,omitempty"`
	Package   *string `json:"package,omitempty"`
	Limit     int     `json:"limit,omitempty"`
	Offset    int     `json:"offset,omitempty"`
}

var schemaInitMu sync.Mutex

// Vault manages the persistence and full-text search indexing of knowledge records.
type Vault struct {
	db    *sql.DB
	clock func() time.Time
	mu    sync.RWMutex
}

// NewVault opens or initializes the Pure-Go SQLite FTS5 Knowledge Vault.
func NewVault(dbPath string, clock func() time.Time) (*Vault, error) {
	if clock == nil {
		clock = time.Now
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return nil, fmt.Errorf("create vault directory: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)&_txlock=immediate", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open vault db: %w", err)
	}

	v := &Vault{
		db:    db,
		clock: clock,
	}
	if err := v.initSchema(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init vault schema: %w", err)
	}
	return v, nil
}

// SetClock allows injecting a mock or deterministic clock function safely.
func (v *Vault) SetClock(clock func() time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if clock == nil {
		clock = time.Now
	}
	v.clock = clock
}

func (v *Vault) getClock() time.Time {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.clock()
}

// Close closes the underlying SQLite database safely.
func (v *Vault) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.db != nil {
		return v.db.Close()
	}
	return nil
}

func (v *Vault) initSchema() error {
	schemaInitMu.Lock()
	defer schemaInitMu.Unlock()
	schema := `
	CREATE TABLE IF NOT EXISTS vault_records (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		milestone TEXT NOT NULL,
		status TEXT NOT NULL,
		package_name TEXT NOT NULL,
		file_path TEXT NOT NULL,
		symbol TEXT,
		problem TEXT NOT NULL,
		trade_off TEXT NOT NULL,
		root_cause TEXT,
		test_file TEXT,
		test_case TEXT,
		raw_json TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_vault_milestone ON vault_records(milestone);
	CREATE INDEX IF NOT EXISTS idx_vault_status ON vault_records(status);
	CREATE INDEX IF NOT EXISTS idx_vault_package ON vault_records(package_name);

	CREATE VIRTUAL TABLE IF NOT EXISTS vault_fts USING fts5(
		id UNINDEXED,
		title,
		problem,
		trade_off,
		root_cause,
		package_name,
		file_path,
		symbol,
		test_case,
		content=vault_records,
		content_rowid=rowid,
		tokenize='unicode61 remove_diacritics 2'
	);

	CREATE TRIGGER IF NOT EXISTS trg_vault_ai AFTER INSERT ON vault_records BEGIN
		INSERT INTO vault_fts(rowid, id, title, problem, trade_off, root_cause, package_name, file_path, symbol, test_case)
		VALUES (new.rowid, new.id, new.title, new.problem, new.trade_off, new.root_cause, new.package_name, new.file_path, new.symbol, new.test_case);
	END;

	CREATE TRIGGER IF NOT EXISTS trg_vault_ad AFTER DELETE ON vault_records BEGIN
		INSERT INTO vault_fts(vault_fts, rowid, id, title, problem, trade_off, root_cause, package_name, file_path, symbol, test_case)
		VALUES ('delete', old.rowid, old.id, old.title, old.problem, old.trade_off, old.root_cause, old.package_name, old.file_path, old.symbol, old.test_case);
	END;

	CREATE TRIGGER IF NOT EXISTS trg_vault_au AFTER UPDATE ON vault_records BEGIN
		INSERT INTO vault_fts(vault_fts, rowid, id, title, problem, trade_off, root_cause, package_name, file_path, symbol, test_case)
		VALUES ('delete', old.rowid, old.id, old.title, old.problem, old.trade_off, old.root_cause, old.package_name, old.file_path, old.symbol, old.test_case);
		INSERT INTO vault_fts(rowid, id, title, problem, trade_off, root_cause, package_name, file_path, symbol, test_case)
		VALUES (new.rowid, new.id, new.title, new.problem, new.trade_off, new.root_cause, new.package_name, new.file_path, new.symbol, new.test_case);
	END;
	`
	_, err := v.db.Exec(schema)
	return err
}

// Store saves or replaces a Tri-Anchor distillation record in the vault.
func (v *Vault) Store(ctx context.Context, rec DistillationRecord) (*DistillationRecord, error) {
	if strings.TrimSpace(rec.ID) == "" {
		return nil, fmt.Errorf("%w: missing id", ErrInvalidRecord)
	}
	if strings.TrimSpace(rec.Title) == "" {
		return nil, fmt.Errorf("%w: missing title", ErrInvalidRecord)
	}
	if strings.TrimSpace(rec.Causality.Problem) == "" {
		return nil, fmt.Errorf("%w: missing causality problem", ErrInvalidRecord)
	}
	if strings.TrimSpace(rec.Causality.TradeOff) == "" {
		return nil, fmt.Errorf("%w: missing causality trade_off", ErrInvalidRecord)
	}

	if rec.Status == "" {
		rec.Status = "APPLIED"
	}
	if rec.Milestone == "" {
		rec.Milestone = "v0.3.0"
	}

	now := v.getClock().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	rec.UpdatedAt = now

	raw, err := json.Marshal(rec)
	if err != nil {
		return nil, fmt.Errorf("marshal record: %w", err)
	}

	query := `
	INSERT INTO vault_records (
		id, title, milestone, status, package_name, file_path, symbol,
		problem, trade_off, root_cause, test_file, test_case, raw_json, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		title=excluded.title,
		milestone=excluded.milestone,
		status=excluded.status,
		package_name=excluded.package_name,
		file_path=excluded.file_path,
		symbol=excluded.symbol,
		problem=excluded.problem,
		trade_off=excluded.trade_off,
		root_cause=excluded.root_cause,
		test_file=excluded.test_file,
		test_case=excluded.test_case,
		raw_json=excluded.raw_json,
		updated_at=excluded.updated_at;
	`

	_, err = v.db.ExecContext(ctx, query,
		rec.ID,
		rec.Title,
		rec.Milestone,
		rec.Status,
		rec.SpatialCoordinates.Package,
		rec.SpatialCoordinates.File,
		rec.SpatialCoordinates.Symbol,
		rec.Causality.Problem,
		rec.Causality.TradeOff,
		rec.Causality.RootCause,
		rec.ForensicVerification.TestFile,
		rec.ForensicVerification.TestCase,
		string(raw),
		rec.CreatedAt.UnixNano(),
		rec.UpdatedAt.UnixNano(),
	)
	if err != nil {
		return nil, fmt.Errorf("store vault record: %w", err)
	}

	return &rec, nil
}

// Get retrieves a distillation record by ID.
func (v *Vault) Get(ctx context.Context, id string) (*DistillationRecord, error) {
	row := v.db.QueryRowContext(ctx, "SELECT raw_json FROM vault_records WHERE id = ?", id)
	var raw string
	if err := row.Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get vault record: %w", err)
	}

	var rec DistillationRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, fmt.Errorf("unmarshal vault record: %w", err)
	}
	return &rec, nil
}

// Query performs a BM25 ranked full-text search over indexed knowledge records.
func (v *Vault) Query(ctx context.Context, q string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	q = sanitizeFTSQuery(q)
	if q == "" {
		return nil, nil
	}

	query := `
	SELECT vr.raw_json, rank, snippet(vault_fts, 1, '<b>', '</b>', '...', 10)
	FROM vault_records vr
	JOIN vault_fts ON vault_fts.rowid = vr.rowid
	WHERE vault_fts MATCH ?
	ORDER BY rank
	LIMIT ?;
	`

	rows, err := v.db.QueryContext(ctx, query, q, limit)
	if err != nil {
		return nil, fmt.Errorf("fts5 query: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var raw string
		var score float64
		var snippet string
		if err := rows.Scan(&raw, &score, &snippet); err != nil {
			return nil, fmt.Errorf("scan fts result: %w", err)
		}
		var rec DistillationRecord
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			continue
		}
		results = append(results, SearchResult{
			Record:  rec,
			Score:   score,
			Snippet: snippet,
		})
	}
	return results, rows.Err()
}

// List queries records by optional filters and pagination.
func (v *Vault) List(ctx context.Context, filter VaultFilter) ([]DistillationRecord, error) {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.Limit > 200 {
		filter.Limit = 200
	}

	var conditions []string
	var args []any

	if filter.Milestone != nil {
		conditions = append(conditions, "milestone = ?")
		args = append(args, *filter.Milestone)
	}
	if filter.Status != nil {
		conditions = append(conditions, "status = ?")
		args = append(args, *filter.Status)
	}
	if filter.Package != nil {
		conditions = append(conditions, "package_name = ?")
		args = append(args, *filter.Package)
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	query := fmt.Sprintf("SELECT raw_json FROM vault_records %s ORDER BY created_at DESC LIMIT ? OFFSET ?", whereClause)
	args = append(args, filter.Limit, filter.Offset)

	rows, err := v.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list vault records: %w", err)
	}
	defer rows.Close()

	var records []DistillationRecord
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan record: %w", err)
		}
		var rec DistillationRecord
		if err := json.Unmarshal([]byte(raw), &rec); err == nil {
			records = append(records, rec)
		}
	}
	return records, rows.Err()
}

// Delete removes a record by ID.
func (v *Vault) Delete(ctx context.Context, id string) error {
	res, err := v.db.ExecContext(ctx, "DELETE FROM vault_records WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete vault record: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// TokenizeCodeSymbols splits camelCase, PascalCase, snake_case, and kebab-case
// identifiers into their constituent lowercase words.
// Example: "CalculateBlastRadius" -> ["calculate", "blast", "radius"]
// Example: "write_receipt_id" -> ["write", "receipt", "id"]
func TokenizeCodeSymbols(s string) []string {
	var tokens []string
	words := strings.FieldsFunc(s, func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == '(' || r == ')' || unicode.IsSpace(r)
	})
	for _, word := range words {
		if len(word) == 0 {
			continue
		}
		var current strings.Builder
		runes := []rune(word)
		for i := 0; i < len(runes); i++ {
			r := runes[i]
			if i > 0 && unicode.IsUpper(r) {
				prev := runes[i-1]
				if unicode.IsLower(prev) || (i+1 < len(runes) && unicode.IsLower(runes[i+1]) && unicode.IsUpper(prev)) {
					if current.Len() > 0 {
						tokens = append(tokens, strings.ToLower(current.String()))
						current.Reset()
					}
				}
			}
			current.WriteRune(r)
		}
		if current.Len() > 0 {
			tokens = append(tokens, strings.ToLower(current.String()))
		}
	}
	return tokens
}

// sanitizeFTSQuery cleans user search strings and decomposes code symbol identifiers for FTS5 matching.
func sanitizeFTSQuery(q string) string {
	terms := strings.Fields(q)
	if len(terms) == 0 {
		return ""
	}
	var sanitized []string
	for _, term := range terms {
		cleaned := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
				return r
			}
			return ' '
		}, term)
		for _, sub := range strings.Fields(cleaned) {
			if len(sub) > 0 {
				sanitized = append(sanitized, sub+"*")
			}
		}
	}
	return strings.Join(sanitized, " ")
}
