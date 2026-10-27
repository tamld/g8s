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
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tamld/g8s/internal/pathutil"

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
	Status               string            `json:"status"`               // PROPOSED, ACCEPTED, APPLIED, DEPRECATED
	RepoID               string            `json:"repo_id,omitempty"`    // e.g. "g8s", "adopter-utility-01", "aegis" (#661)
	ScopeTier            string            `json:"scope_tier,omitempty"` // "repo" | "domain" | "global" (#661)
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
	Milestone     *string `json:"milestone,omitempty"`
	Status        *string `json:"status,omitempty"`
	Package       *string `json:"package,omitempty"`
	RepoID        *string `json:"repo_id,omitempty"`
	ScopeTier     *string `json:"scope_tier,omitempty"`
	IncludeShared bool    `json:"include_shared,omitempty"`
	Limit         int     `json:"limit,omitempty"`
	Offset        int     `json:"offset,omitempty"`
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
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, fmt.Errorf("create vault directory: %w", err)
	}

	dsn := pathutil.SQLiteURI(dbPath, "_txlock=immediate&_pragma=busy_timeout(30000)&_pragma=foreign_keys(ON)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open vault db: %w", err)
	}
	db.SetMaxOpenConns(1)

	v := &Vault{
		db:    db,
		clock: clock,
	}
	if err := v.initSchema(); err != nil {
		db.Close()
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
		repo_id TEXT NOT NULL DEFAULT '',
		scope_tier TEXT NOT NULL DEFAULT 'global',
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
	`
	if _, err := v.db.Exec(schema); err != nil {
		return err
	}
	// Additive migration for pre-v0.20.1 databases (#661)
	_, _ = v.db.Exec(`ALTER TABLE vault_records ADD COLUMN repo_id TEXT NOT NULL DEFAULT ''`)
	_, _ = v.db.Exec(`ALTER TABLE vault_records ADD COLUMN scope_tier TEXT NOT NULL DEFAULT 'global'`)
	_, _ = v.db.Exec(`CREATE INDEX IF NOT EXISTS idx_vault_repo_scope ON vault_records(repo_id, scope_tier)`)

	ftsSchema := `
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
	_, err := v.db.Exec(ftsSchema)
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
	rec.RepoID = strings.TrimSpace(rec.RepoID)
	rec.ScopeTier = strings.ToLower(strings.TrimSpace(rec.ScopeTier))
	if rec.ScopeTier == "" {
		if rec.RepoID != "" {
			rec.ScopeTier = "repo"
		} else {
			rec.ScopeTier = "global"
		}
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
		id, title, milestone, status, repo_id, scope_tier, package_name, file_path, symbol,
		problem, trade_off, root_cause, test_file, test_case, raw_json, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		title=excluded.title,
		milestone=excluded.milestone,
		status=excluded.status,
		repo_id=excluded.repo_id,
		scope_tier=excluded.scope_tier,
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
		rec.RepoID,
		rec.ScopeTier,
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
	if filter.RepoID != nil && strings.TrimSpace(*filter.RepoID) != "" {
		repoID := strings.TrimSpace(*filter.RepoID)
		if filter.IncludeShared {
			conditions = append(conditions, "(repo_id = ? OR repo_id = '' OR scope_tier IN ('domain', 'global'))")
			args = append(args, repoID)
		} else {
			conditions = append(conditions, "repo_id = ?")
			args = append(args, repoID)
		}
	}
	if filter.ScopeTier != nil && strings.TrimSpace(*filter.ScopeTier) != "" {
		conditions = append(conditions, "scope_tier = ?")
		args = append(args, strings.ToLower(strings.TrimSpace(*filter.ScopeTier)))
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

		start := 0
		var prev rune

		i := 0
		for i < len(word) {
			r, size := utf8.DecodeRuneInString(word[i:])

			if i > 0 && unicode.IsUpper(r) {
				// Peek next rune to handle consecutive uppercase correctly (e.g. XMLParser -> XML, Parser)
				var next rune
				if i+size < len(word) {
					next, _ = utf8.DecodeRuneInString(word[i+size:])
				}

				if unicode.IsLower(prev) || (i+size < len(word) && unicode.IsLower(next) && unicode.IsUpper(prev)) {
					if start < i {
						tokens = append(tokens, strings.ToLower(word[start:i]))
						start = i
					}
				}
			}
			prev = r
			i += size
		}

		if start < len(word) {
			tokens = append(tokens, strings.ToLower(word[start:]))
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
	seen := make(map[string]bool)

	for _, term := range terms {
		// Extract sub-tokens from camelCase/snake_case
		subTokens := TokenizeCodeSymbols(term)
		for _, tok := range subTokens {
			if !seen[tok] && len(tok) > 0 {
				seen[tok] = true
				sanitized = append(sanitized, tok+"*")
			}
		}

		cleaned := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
				return r
			}
			return ' '
		}, term)
		for _, sub := range strings.Fields(cleaned) {
			sLower := strings.ToLower(sub)
			if !seen[sLower] && len(sLower) > 0 {
				seen[sLower] = true
				sanitized = append(sanitized, sLower+"*")
			}
		}
	}
	return strings.Join(sanitized, " OR ")
}

// DefaultMaxActiveRulesPerPkg is the default carrying capacity K_max for active
// spatially-anchored JIT rules per package (Hypothesis H1, issue #594).
const DefaultMaxActiveRulesPerPkg = 3

// ResolveMaxActiveRules returns the configured carrying capacity K_max for JIT
// spatial recall. Operators or shadow-mode experiments may override K_max via
// G8S_JIT_MAX_RULES (including "0" to test the zero-rule baseline).
func ResolveMaxActiveRules() int {
	if raw := strings.TrimSpace(os.Getenv("G8S_JIT_MAX_RULES")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			return n
		}
	}
	return DefaultMaxActiveRulesPerPkg
}

// MatchSpatial retrieves active (APPLIED or ACCEPTED) distillation records
// whose SpatialCoordinates match filePath or its package directory, capped at
// maxRules (carrying capacity K_max for Hypothesis H1). Exact file matches rank
// ahead of package-level matches, ordered by UpdatedAt descending.
func (v *Vault) MatchSpatial(ctx context.Context, filePath string, maxRules int) ([]DistillationRecord, error) {
	if maxRules < 0 {
		maxRules = ResolveMaxActiveRules()
	}
	if maxRules == 0 {
		return nil, nil
	}
	all, err := v.matchSpatialInternal(ctx, filePath, true)
	if err != nil {
		return nil, err
	}
	if len(all) > maxRules {
		return all[:maxRules], nil
	}
	return all, nil
}

// MatchSpatialAllStates retrieves all distillation records (including COLD /
// DORMANT / DEPRECATED records) whose SpatialCoordinates match filePath or its
// package directory. Used by the Net-Delta Diff Guard (#595) to detect dormant
// rule regressions and test Hypothesis H2 (Sine-Wave Dormant Re-Awakening).
func (v *Vault) MatchSpatialAllStates(ctx context.Context, filePath string) ([]DistillationRecord, error) {
	return v.matchSpatialInternal(ctx, filePath, false)
}

func (v *Vault) matchSpatialInternal(ctx context.Context, filePath string, activeOnly bool) ([]DistillationRecord, error) {
	cleanFile := filepath.ToSlash(filepath.Clean(strings.TrimSpace(filePath)))
	cleanFile = strings.TrimPrefix(cleanFile, "./")
	if cleanFile == "" || cleanFile == "." {
		return nil, nil
	}
	pkgDir := filepath.ToSlash(filepath.Dir(cleanFile))
	pkgBase := filepath.Base(pkgDir)

	statusFilter := ""
	if activeOnly {
		statusFilter = "AND status IN ('APPLIED', 'ACCEPTED', 'DOCTRINE') "
	}

	query := fmt.Sprintf(`
		SELECT raw_json
		FROM vault_records
		WHERE 1=1 %s
		ORDER BY updated_at DESC
		LIMIT 200
	`, statusFilter)

	rows, err := v.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("match spatial records: %w", err)
	}
	defer rows.Close()

	var exactFileMatches []DistillationRecord
	var pkgMatches []DistillationRecord
	activeRepoID := strings.TrimSpace(os.Getenv("G8S_REPO_ID"))
	if activeRepoID == "" {
		if sr := strings.TrimSpace(os.Getenv("G8S_SCOPE_ROOT")); sr != "" {
			activeRepoID = InferRepoID(sr)
		}
	}

	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		var rec DistillationRecord
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			continue
		}
		recRepo := strings.TrimSpace(rec.RepoID)
		recTier := strings.ToLower(strings.TrimSpace(rec.ScopeTier))
		if activeRepoID != "" && recRepo != "" && recRepo != activeRepoID && recTier != "domain" && recTier != "global" {
			continue
		}
		recFile := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(rec.SpatialCoordinates.File)), "./")
		recPkg := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(rec.SpatialCoordinates.Package)), "./")

		if recFile != "" && recFile != "." && (recFile == cleanFile || strings.HasSuffix(cleanFile, "/"+recFile)) {
			exactFileMatches = append(exactFileMatches, rec)
			continue
		}
		if recPkg != "" && recPkg != "." && (recPkg == pkgDir || recPkg == pkgBase || strings.HasSuffix(pkgDir, "/"+recPkg)) {
			pkgMatches = append(pkgMatches, rec)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]DistillationRecord, 0, len(exactFileMatches)+len(pkgMatches))
	out = append(out, exactFileMatches...)
	out = append(out, pkgMatches...)
	return out, nil
}

// InferRepoID derives a deterministic repository identifier for scopeRoot.
// Priority:
//  1. G8S_REPO_ID environment variable (if set and scopeRoot is empty).
//  2. go.mod module path in scopeRoot (e.g. "github.com/tamld/g8s" -> "tamld/g8s").
//  3. package.json "name" field in scopeRoot.
//  4. Base directory name of scopeRoot.
func InferRepoID(scopeRoot string) string {
	scopeRoot = strings.TrimSpace(scopeRoot)
	if scopeRoot == "" {
		if envID := strings.TrimSpace(os.Getenv("G8S_REPO_ID")); envID != "" {
			return envID
		}
		return ""
	}
	goModPath := filepath.Join(scopeRoot, "go.mod")
	if b, err := os.ReadFile(goModPath); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "module ") {
				mod := strings.TrimSpace(strings.TrimPrefix(line, "module "))
				mod = strings.Trim(mod, `"`)
				parts := strings.Split(mod, "/")
				if len(parts) >= 2 && strings.Contains(parts[0], ".") {
					return strings.Join(parts[1:], "/")
				}
				if mod != "" {
					return mod
				}
			}
		}
	}
	pkgJSONPath := filepath.Join(scopeRoot, "package.json")
	if b, err := os.ReadFile(pkgJSONPath); err == nil {
		var pkg struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(b, &pkg) == nil && strings.TrimSpace(pkg.Name) != "" {
			return strings.TrimSpace(pkg.Name)
		}
	}
	if abs, err := filepath.Abs(scopeRoot); err == nil {
		base := filepath.Base(abs)
		if base != "." && base != "/" {
			return base
		}
	}
	return ""
}

// ExtractToolchainGrounding inspects scopeRoot for live toolchain manifests
// (go.mod, package.json) and returns a compact single-line grounding header to
// prevent LLM training cut-off hallucinations (#661). Returns "" if no manifest
// exists in scopeRoot.
func ExtractToolchainGrounding(scopeRoot string) string {
	scopeRoot = strings.TrimSpace(scopeRoot)
	if scopeRoot == "" {
		return ""
	}
	var facts []string
	repoID := InferRepoID(scopeRoot)

	goModPath := filepath.Join(scopeRoot, "go.mod")
	if b, err := os.ReadFile(goModPath); err == nil {
		var modName, goVer string
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "module ") && modName == "" {
				modName = strings.TrimSpace(strings.TrimPrefix(line, "module "))
			} else if strings.HasPrefix(line, "go ") && goVer == "" {
				goVer = strings.TrimSpace(strings.TrimPrefix(line, "go "))
			}
		}
		if goVer != "" {
			facts = append(facts, "go="+goVer)
		}
		if modName != "" {
			facts = append(facts, "module="+modName)
		}
	}

	pkgJSONPath := filepath.Join(scopeRoot, "package.json")
	if b, err := os.ReadFile(pkgJSONPath); err == nil {
		var pkg struct {
			Name            string            `json:"name"`
			Version         string            `json:"version"`
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
		}
		if json.Unmarshal(b, &pkg) == nil {
			if pkg.Name != "" {
				nv := pkg.Name
				if pkg.Version != "" {
					nv += "@" + pkg.Version
				}
				facts = append(facts, "pkg="+nv)
			}
			for _, key := range []string{"electron", "react", "next", "typescript", "vite"} {
				if v, ok := pkg.Dependencies[key]; ok && v != "" {
					facts = append(facts, key+"="+v)
				} else if v, ok := pkg.DevDependencies[key]; ok && v != "" {
					facts = append(facts, key+"="+v)
				}
			}
		}
	}

	if len(facts) == 0 {
		return ""
	}
	if repoID != "" {
		return fmt.Sprintf("[grounding] repo=%s %s", repoID, strings.Join(facts, " "))
	}
	return fmt.Sprintf("[grounding] %s", strings.Join(facts, " "))
}

// BuildPreExecutionGrounding assembles a pre-Turn-1 grounding block combining
// live toolchain facts from scopeRoot and up to maxNotes active Vault whispers
// scoped to the repository (plus shared domain/global rules, #661).
func BuildPreExecutionGrounding(ctx context.Context, v *Vault, scopeRoot string, maxNotes int) string {
	var lines []string
	if tc := ExtractToolchainGrounding(scopeRoot); tc != "" {
		lines = append(lines, tc)
	}
	if v != nil {
		if maxNotes <= 0 {
			maxNotes = ResolveMaxActiveRules()
		}
		if maxNotes > 0 {
			repoID := InferRepoID(scopeRoot)
			applied := "APPLIED"
			filter := VaultFilter{
				Status:        &applied,
				Limit:         maxNotes,
				IncludeShared: true,
			}
			if repoID != "" {
				filter.RepoID = &repoID
			}
			if recs, err := v.List(ctx, filter); err == nil && len(recs) > 0 {
				lines = append(lines, FormatJITWhisper(recs)...)
			}
		}
	}
	return strings.Join(lines, "\n")
}

// FormatJITWhisper renders each matched DistillationRecord into a single
// non-blocking 1-line hint (<= 200 chars) suitable for attaching to ReadResult
// or EditResult metadata without interrupting agent flow (#594).
func FormatJITWhisper(records []DistillationRecord) []string {
	if len(records) == 0 {
		return nil
	}
	whispers := make([]string, 0, len(records))
	for _, r := range records {
		line := fmt.Sprintf("[jit:%s] %s — %s", r.ID, strings.TrimSpace(r.Title), strings.TrimSpace(r.Causality.TradeOff))
		if len(r.SpatialCoordinates.DeniedFragments) > 0 {
			line += fmt.Sprintf(" (avoid: %s)", strings.Join(r.SpatialCoordinates.DeniedFragments, ", "))
		}
		line = strings.Join(strings.Fields(line), " ")
		if len(line) > 200 {
			cut := line[:197]
			for !utf8.ValidString(cut) && len(cut) > 0 {
				cut = cut[:len(cut)-1]
			}
			line = cut + "..."
		}
		whispers = append(whispers, line)
	}
	return whispers
}

// VerifyAnchors checks every non-deprecated Vault record against the live workspace
// at rootDir to detect Citation Rot (missing SpatialCoordinates.File/Symbol or
// missing ForensicVerification.TestFile/TestCase). When markStale is true, any
// record whose anchored file or symbol no longer exists is updated to status
// "STALE_ANCHOR" so stale rules are never injected into worker context.
func (v *Vault) VerifyAnchors(ctx context.Context, rootDir string, markStale bool) ([]string, error) {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return nil, fmt.Errorf("vault: rootDir is required for VerifyAnchors")
	}

	rows, err := v.db.QueryContext(ctx, `
		SELECT raw_json FROM vault_records
		WHERE status NOT IN ('DEPRECATED', 'STALE_ANCHOR')
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("vault: query records for anchor verification: %w", err)
	}
	defer rows.Close()

	var candidates []DistillationRecord
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("vault: scan record: %w", err)
		}
		var rec DistillationRecord
		if err := json.Unmarshal([]byte(raw), &rec); err == nil {
			candidates = append(candidates, rec)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var staleIDs []string
	for _, rec := range candidates {
		hasSpatialFile := strings.TrimSpace(rec.SpatialCoordinates.File) != ""
		hasTestFile := strings.TrimSpace(rec.ForensicVerification.TestFile) != ""
		if !hasSpatialFile && !hasTestFile {
			continue
		}

		isStale := false
		if hasSpatialFile {
			fullPath := filepath.Join(rootDir, filepath.FromSlash(strings.TrimSpace(rec.SpatialCoordinates.File)))
			b, err := os.ReadFile(fullPath)
			if err != nil {
				isStale = true
			} else if sym := strings.TrimSpace(rec.SpatialCoordinates.Symbol); sym != "" {
				if !strings.Contains(string(b), sym) {
					isStale = true
				}
			}
		}

		if !isStale && hasTestFile {
			fullTestPath := filepath.Join(rootDir, filepath.FromSlash(strings.TrimSpace(rec.ForensicVerification.TestFile)))
			b, err := os.ReadFile(fullTestPath)
			if err != nil {
				isStale = true
			} else if tc := strings.TrimSpace(rec.ForensicVerification.TestCase); tc != "" {
				if !strings.Contains(string(b), tc) {
					isStale = true
				}
			}
		}

		if isStale {
			staleIDs = append(staleIDs, rec.ID)
			if markStale {
				rec.Status = "STALE_ANCHOR"
				if _, err := v.Store(ctx, rec); err != nil {
					return staleIDs, fmt.Errorf("vault: mark record %s stale: %w", rec.ID, err)
				}
			}
		}
	}

	return staleIDs, nil
}

// ExportPortableJSONL exports active/doctrine Vault distillation records to a
// self-contained JSONL file (e.g. .g8s/knowledge-vault.jsonl) so any repository
// can version and share verified engineering knowledge without external tools.
func (v *Vault) ExportPortableJSONL(ctx context.Context, filePath string, repoID string) (int, error) {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return 0, fmt.Errorf("vault: export filePath is required")
	}

	query := `
		SELECT raw_json FROM vault_records
		WHERE status NOT IN ('DEPRECATED', 'STALE_ANCHOR')
	`
	var args []any
	if strings.TrimSpace(repoID) != "" {
		query += ` AND (repo_id = ? OR repo_id = '' OR scope_tier IN ('domain', 'global'))`
		args = append(args, strings.TrimSpace(repoID))
	}
	query += ` ORDER BY id ASC`

	rows, err := v.db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("vault: query export records: %w", err)
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return 0, err
		}
		var rec DistillationRecord
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			continue
		}
		b, err := json.Marshal(rec)
		if err != nil {
			return 0, err
		}
		lines = append(lines, string(b))
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		return 0, fmt.Errorf("vault: create export dir: %w", err)
	}
	content := ""
	if len(lines) > 0 {
		content = strings.Join(lines, "\n") + "\n"
	}
	if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
		return 0, fmt.Errorf("vault: write export file: %w", err)
	}
	return len(lines), nil
}

// ImportPortableJSONL imports Tri-Anchor distillation records from a portable
// JSONL file (e.g. .g8s/knowledge-vault.jsonl) into the local SQLite Vault.
func (v *Vault) ImportPortableJSONL(ctx context.Context, filePath string) (int, error) {
	b, err := os.ReadFile(strings.TrimSpace(filePath))
	if err != nil {
		return 0, fmt.Errorf("vault: read import file: %w", err)
	}
	count := 0
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var rec DistillationRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return count, fmt.Errorf("vault: unmarshal JSONL line %d: %w", count+1, err)
		}
		if _, err := v.Store(ctx, rec); err != nil {
			return count, fmt.Errorf("vault: store imported record %s: %w", rec.ID, err)
		}
		count++
	}
	return count, nil
}
