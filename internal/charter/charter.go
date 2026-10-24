// Package charter implements the Orthogonal Stratum Federation (S0..S3 + O-Plane)
// and Epoch-Fenced Charter Engine (DELTA-33 / ADR-0035). It separates physical
// OS/worktree containment rings (F0/F1/F2) from organizational authority strata
// (S0 Principal, S1 FederationCoordinator, S2 DomainGovernor, S3-M BoundedMutator,
// S3-A BlindAdjudicator, and O-Plane TelemetryProjector) using Pure-Go SQLite WAL.
package charter

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // Pure-Go SQLite driver (Zero-CGO constitution).

	"github.com/tamld/g8s/internal/pathutil"
)

// Ring identifies the physical containment sandbox tier (ADR-0032).
type Ring string

const (
	RingF0 Ring = "F0"
	RingF1 Ring = "F1"
	RingF2 Ring = "F2"
)

// Stratum identifies the hierarchical control-plane authority tier (ADR-0035).
type Stratum string

const (
	StratumS0            Stratum = "S0"      // Principal (Human Operator / Root of Trust)
	StratumS1            Stratum = "S1"      // FederationCoordinator (Multi-Repo Portfolio Orchestrator)
	StratumS2            Stratum = "S2"      // DomainGovernor (Single-Repository Sovereign Supervisor)
	StratumS3Mutator     Stratum = "S3-M"    // BoundedMutator (Isolated Worktree Coder, F1/F2)
	StratumS3Adjudicator Stratum = "S3-A"    // BlindAdjudicator (Disjoint-Family Blind Verifier, F1)
	StratumOPlane        Stratum = "O-Plane" // TelemetryProjector (Deterministic Read-Only Projection)
)

// CharterState represents the lifecycle state of a domain charter.
type CharterState string

const (
	StateIssued    CharterState = "ISSUED"
	StateActive    CharterState = "ACTIVE"
	StateSuspended CharterState = "SUSPENDED"
	StateCompleted CharterState = "COMPLETED"
	StateRevoked   CharterState = "REVOKED"
)

// Sentinel errors for mechanical stratum and charter enforcement.
var (
	ErrStratumAdjacencyViolation = errors.New("E_STRATUM_ADJACENCY_VIOLATION: caller stratum cannot dispatch target stratum directly")
	ErrStratumMutationForbidden  = errors.New("E_S1_DIRECT_MUTATION_FORBIDDEN: stratum is prohibited from mutating repository files directly")
	ErrCharterNotFound           = errors.New("E_CHARTER_NOT_FOUND: charter does not exist")
	ErrCharterExpired            = errors.New("E_CHARTER_EXPIRED: charter TTL has expired")
	ErrCharterInactive           = errors.New("E_CHARTER_INACTIVE: charter is revoked, suspended, or completed")
	ErrCharterFenceStale         = errors.New("E_CHARTER_FENCE_STALE: charter epoch or fence token has been superseded")
	ErrCharterRepoMismatch       = errors.New("E_CHARTER_REPO_MISMATCH: charter repo_id does not match target repository")
	ErrCharterDigestMismatch     = errors.New("E_CHARTER_DIGEST_MISMATCH: stored charter digest failed integrity check")
	ErrInvalidCharterRequest     = errors.New("E_INVALID_CHARTER_REQUEST: invalid charter parameters")
)

// ParseStratum normalizes and validates a stratum identifier. When raw is empty,
// it defaults to StratumS2 (DomainGovernor standalone single-repo mode).
func ParseStratum(raw string) (Stratum, error) {
	trimmed := strings.TrimSpace(strings.ToUpper(raw))
	switch trimmed {
	case "":
		return StratumS2, nil
	case "S0", "PRINCIPAL":
		return StratumS0, nil
	case "S1", "FEDERATION", "FEDERATION_COORDINATOR":
		return StratumS1, nil
	case "S2", "DOMAIN", "DOMAIN_GOVERNOR":
		return StratumS2, nil
	case "S3", "S3-M", "MUTATOR", "BOUNDED_MUTATOR":
		return StratumS3Mutator, nil
	case "S3-A", "ADJUDICATOR", "BLIND_ADJUDICATOR":
		return StratumS3Adjudicator, nil
	case "O-PLANE", "OPLANE", "TELEMETRY_PROJECTOR":
		return StratumOPlane, nil
	default:
		return "", fmt.Errorf("%w: unknown stratum %q", ErrInvalidCharterRequest, raw)
	}
}

// CurrentStratum resolves the active process stratum from G8S_STRATUM and G8S_TIER.
// Any process running inside F1/F2 is clamped to S3 regardless of G8S_STRATUM to
// defeat child-process environment spoofing (Antithesis A4).
func CurrentStratum() Stratum {
	tier := strings.ToUpper(strings.TrimSpace(os.Getenv("G8S_TIER")))
	if tier == "F1" || tier == "F2" || strings.TrimSpace(os.Getenv("G8S_RECEIPT_ID")) != "" {
		if strings.EqualFold(strings.TrimSpace(os.Getenv("G8S_STRATUM")), string(StratumS3Adjudicator)) {
			return StratumS3Adjudicator
		}
		return StratumS3Mutator
	}
	s, err := ParseStratum(os.Getenv("G8S_STRATUM"))
	if err != nil {
		return StratumS2
	}
	return s
}

// ValidateAdjacency enforces the Strict Stratum Adjacency Law (S_k -> S_{k+1}):
//   - S0 (Principal) -> S1 (FederationCoordinator) or S2 (DomainGovernor standalone)
//   - S1 (FederationCoordinator) -> S2 (DomainGovernor) or O-Plane ONLY (never S3!)
//   - S2 (DomainGovernor) -> S3-M (BoundedMutator), S3-A (BlindAdjudicator), or O-Plane
//   - S3-M / S3-A / O-Plane -> cannot dispatch any Stratum
func ValidateAdjacency(caller, target Stratum) error {
	switch caller {
	case StratumS0:
		if target == StratumS1 || target == StratumS2 || target == StratumOPlane {
			return nil
		}
	case StratumS1:
		if target == StratumS2 || target == StratumOPlane {
			return nil
		}
	case StratumS2:
		if target == StratumS3Mutator || target == StratumS3Adjudicator || target == StratumOPlane {
			return nil
		}
	}
	return fmt.Errorf("%w: caller %s cannot dispatch target %s", ErrStratumAdjacencyViolation, caller, target)
}

// VerifyMutationAllowed enforces the Cross-Stratum Mutation Firewall:
// S1 (FederationCoordinator), S3-A (BlindAdjudicator), and O-Plane (TelemetryProjector)
// are kernel-blocked from mutating repository files directly.
func VerifyMutationAllowed(stratum Stratum, targetPath string) error {
	switch stratum {
	case StratumS1, StratumS3Adjudicator, StratumOPlane:
		return fmt.Errorf("%w: stratum %s cannot mutate %q; delegate via S2 DomainGovernor Charter", ErrStratumMutationForbidden, stratum, targetPath)
	default:
		return nil
	}
}

// Charter represents an immutable, epoch-fenced authority delegation from S0/S1 to S2.
type Charter struct {
	CharterID         string       `json:"charter_id"`
	IssuerStratum     Stratum      `json:"issuer_stratum"`
	TargetStratum     Stratum      `json:"target_stratum"`
	RepoID            string       `json:"repo_id"`
	ScopeRoot         string       `json:"scope_root"`
	AllowedPaths      []string     `json:"allowed_paths"`
	OutcomeMandate    string       `json:"outcome_mandate"`
	BudgetTokens      int          `json:"budget_tokens"`
	Epoch             int          `json:"epoch"`
	Fence             string       `json:"fence"`
	CharterDigest     string       `json:"charter_digest"`
	State             CharterState `json:"state"`
	IssuedAt          int64        `json:"issued_at"`
	ExpiresAt         int64        `json:"expires_at"`
	UpdatedAt         int64        `json:"updated_at"`
	RevokeReason      string       `json:"revoke_reason,omitempty"`
	SealedReceiptHash string       `json:"sealed_receipt_hash,omitempty"`
}

// IssueRequest holds the parameters for minting a new S2 DomainGovernor Charter.
type IssueRequest struct {
	CharterID      string
	IssuerStratum  Stratum
	RepoID         string
	ScopeRoot      string
	AllowedPaths   []string
	OutcomeMandate string
	BudgetTokens   int
	TTL            time.Duration
}

// PortfolioSnapshot is the O-Plane read-only projection of active, completed,
// and revoked charters across repositories.
type PortfolioSnapshot struct {
	ProjectedAt    string     `json:"projected_at"`
	Stratum        Stratum    `json:"projector_stratum"`
	TotalCount     int        `json:"total_charters"`
	ActiveCount    int        `json:"active_charters"`
	CompletedCount int        `json:"completed_charters"`
	RevokedCount   int        `json:"revoked_charters"`
	WorkspaceDir   string     `json:"workspace_dir,omitempty"`
	Charters       []*Charter `json:"charters"`
}

// ComputeFence calculates the deterministic 32-char hex fence token for (charterID, repoID, epoch).
func ComputeFence(charterID, repoID string, epoch int) string {
	payload := fmt.Sprintf("g8s.fence.v1|%s|%s|%d", strings.TrimSpace(charterID), strings.TrimSpace(repoID), epoch)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:16])
}

// ComputeDigest calculates the canonical SHA-256 digest of the immutable charter fields.
func ComputeDigest(c *Charter) string {
	paths := make([]string, len(c.AllowedPaths))
	copy(paths, c.AllowedPaths)
	sort.Strings(paths)
	canonical := fmt.Sprintf(
		"g8s.charter.v1|id=%s|issuer=%s|target=%s|repo=%s|root=%s|paths=%s|mandate=%s|budget=%d|issued=%d|expires=%d",
		c.CharterID,
		c.IssuerStratum,
		c.TargetStratum,
		c.RepoID,
		c.ScopeRoot,
		strings.Join(paths, ","),
		c.OutcomeMandate,
		c.BudgetTokens,
		c.IssuedAt,
		c.ExpiresAt,
	)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// Store persists charters in a Pure-Go SQLite WAL database.
type Store struct {
	db    *sql.DB
	clock func() time.Time
}

// Open opens or initializes the SQLite WAL charter store inside stateDir/charters.db.
// When stateDir is empty, it defaults to pathutil.DefaultStateDir().
func Open(stateDir string) (*Store, error) {
	if strings.TrimSpace(stateDir) == "" {
		stateDir = pathutil.DefaultStateDir()
	}
	return NewStore(filepath.Join(stateDir, "charters.db"), nil)
}

// NewStore opens or initializes the SQLite WAL charter store at dbPath.
func NewStore(dbPath string, clock func() time.Time) (*Store, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, fmt.Errorf("%w: empty db path", ErrInvalidCharterRequest)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, fmt.Errorf("create charter dir: %w", err)
	}
	if clock == nil {
		clock = time.Now
	}
	dsn := pathutil.SQLiteURI(dbPath, "_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open charter sqlite: %w", err)
	}
	s := &Store{db: db, clock: clock}
	if err := s.initSchema(); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return s, nil
}

func (s *Store) initSchema() error {
	stmts := []string{
		`PRAGMA journal_mode=WAL;`,
		`CREATE TABLE IF NOT EXISTS charters (
			charter_id TEXT PRIMARY KEY,
			issuer_stratum TEXT NOT NULL,
			target_stratum TEXT NOT NULL,
			repo_id TEXT NOT NULL,
			scope_root TEXT NOT NULL,
			allowed_paths_json TEXT NOT NULL,
			outcome_mandate TEXT NOT NULL,
			budget_tokens INTEGER NOT NULL,
			epoch INTEGER NOT NULL,
			fence TEXT NOT NULL,
			charter_digest TEXT NOT NULL,
			state TEXT NOT NULL,
			issued_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			revoke_reason TEXT NOT NULL DEFAULT '',
			sealed_receipt_hash TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE INDEX IF NOT EXISTS idx_charters_repo_state ON charters(repo_id, state);`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("init charter schema: %w", err)
		}
	}
	return nil
}

// Close closes the underlying SQLite connection.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Issue mints a new S2 DomainGovernor Charter after validating S0/S1 -> S2 adjacency.
func (s *Store) Issue(req IssueRequest) (*Charter, error) {
	if cur := CurrentStratum(); cur == StratumS3Mutator || cur == StratumS3Adjudicator || cur == StratumOPlane {
		return nil, fmt.Errorf("%w: process in stratum %s (F1/F2 or receipt-bound) cannot issue charters", ErrStratumAdjacencyViolation, cur)
	}
	issuer := req.IssuerStratum
	if issuer == "" {
		issuer = StratumS1
	}
	if issuer != StratumS0 && issuer != StratumS1 {
		return nil, fmt.Errorf("%w: only S0 or S1 may issue a domain charter (got %s)", ErrStratumAdjacencyViolation, issuer)
	}
	if err := ValidateAdjacency(issuer, StratumS2); err != nil {
		return nil, err
	}
	repoID := strings.TrimSpace(req.RepoID)
	if repoID == "" {
		return nil, fmt.Errorf("%w: repo_id is required", ErrInvalidCharterRequest)
	}
	mandate := strings.TrimSpace(req.OutcomeMandate)
	if mandate == "" {
		return nil, fmt.Errorf("%w: outcome_mandate is required", ErrInvalidCharterRequest)
	}
	scopeRoot := strings.TrimSpace(req.ScopeRoot)
	if scopeRoot == "" {
		scopeRoot = "."
	}
	absRoot, err := filepath.Abs(scopeRoot)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid scope_root: %v", ErrInvalidCharterRequest, err)
	}
	ttl := req.TTL
	if ttl <= 0 {
		ttl = 2 * time.Hour
	}
	if ttl > 24*time.Hour {
		return nil, fmt.Errorf("%w: charter TTL cannot exceed 24h", ErrInvalidCharterRequest)
	}
	now := s.clock().UTC()
	charterID := strings.TrimSpace(req.CharterID)
	if charterID == "" {
		seed := fmt.Sprintf("%s|%s|%d", repoID, mandate, now.UnixNano())
		sum := sha256.Sum256([]byte(seed))
		charterID = "ch-" + hex.EncodeToString(sum[:8])
	}
	paths := deduplicatePaths(req.AllowedPaths)
	if len(paths) == 0 {
		paths = []string{"**"}
	}
	budget := req.BudgetTokens
	if budget <= 0 {
		budget = 100000
	}
	epoch := 1
	fence := ComputeFence(charterID, repoID, epoch)
	c := &Charter{
		CharterID:      charterID,
		IssuerStratum:  issuer,
		TargetStratum:  StratumS2,
		RepoID:         repoID,
		ScopeRoot:      absRoot,
		AllowedPaths:   paths,
		OutcomeMandate: mandate,
		BudgetTokens:   budget,
		Epoch:          epoch,
		Fence:          fence,
		State:          StateActive,
		IssuedAt:       now.Unix(),
		ExpiresAt:      now.Add(ttl).Unix(),
		UpdatedAt:      now.Unix(),
	}
	c.CharterDigest = ComputeDigest(c)

	pathsJSON, err := json.Marshal(c.AllowedPaths)
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec(
		`INSERT INTO charters (
			charter_id, issuer_stratum, target_stratum, repo_id, scope_root,
			allowed_paths_json, outcome_mandate, budget_tokens, epoch, fence,
			charter_digest, state, issued_at, expires_at, updated_at,
			revoke_reason, sealed_receipt_hash
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '')`,
		c.CharterID, string(c.IssuerStratum), string(c.TargetStratum), c.RepoID, c.ScopeRoot,
		string(pathsJSON), c.OutcomeMandate, c.BudgetTokens, c.Epoch, c.Fence,
		c.CharterDigest, string(c.State), c.IssuedAt, c.ExpiresAt, c.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert charter: %w", err)
	}
	return c, nil
}

// Get retrieves a charter by ID and verifies its canonical digest.
func (s *Store) Get(charterID string) (*Charter, error) {
	row := s.db.QueryRow(
		`SELECT charter_id, issuer_stratum, target_stratum, repo_id, scope_root,
		        allowed_paths_json, outcome_mandate, budget_tokens, epoch, fence,
		        charter_digest, state, issued_at, expires_at, updated_at,
		        revoke_reason, sealed_receipt_hash
		   FROM charters WHERE charter_id = ?`,
		strings.TrimSpace(charterID),
	)
	return scanCharter(row)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCharter(sc rowScanner) (*Charter, error) {
	var c Charter
	var issuer, target, state, pathsJSON string
	err := sc.Scan(
		&c.CharterID, &issuer, &target, &c.RepoID, &c.ScopeRoot,
		&pathsJSON, &c.OutcomeMandate, &c.BudgetTokens, &c.Epoch, &c.Fence,
		&c.CharterDigest, &state, &c.IssuedAt, &c.ExpiresAt, &c.UpdatedAt,
		&c.RevokeReason, &c.SealedReceiptHash,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCharterNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan charter: %w", err)
	}
	c.IssuerStratum = Stratum(issuer)
	c.TargetStratum = Stratum(target)
	c.State = CharterState(state)
	if err := json.Unmarshal([]byte(pathsJSON), &c.AllowedPaths); err != nil {
		return nil, fmt.Errorf("unmarshal allowed_paths: %w", err)
	}
	if ComputeDigest(&c) != c.CharterDigest {
		return nil, ErrCharterDigestMismatch
	}
	return &c, nil
}

// Verify checks that charterID is ACTIVE, unexpired, matches repoID (when non-empty),
// and matches the expected epoch and fence token (when provided).
func (s *Store) Verify(charterID, repoID string, expectedEpoch int, expectedFence string) (*Charter, error) {
	c, err := s.Get(charterID)
	if err != nil {
		return nil, err
	}
	now := s.clock().UTC().Unix()
	if now > c.ExpiresAt {
		return nil, fmt.Errorf("%w: charter %s expired at %d", ErrCharterExpired, c.CharterID, c.ExpiresAt)
	}
	if c.State != StateActive && c.State != StateIssued {
		return nil, fmt.Errorf("%w: charter %s is in state %s", ErrCharterInactive, c.CharterID, c.State)
	}
	if strings.TrimSpace(repoID) != "" && !strings.EqualFold(strings.TrimSpace(repoID), c.RepoID) {
		return nil, fmt.Errorf("%w: charter bound to %q, caller requested %q", ErrCharterRepoMismatch, c.RepoID, repoID)
	}
	if expectedEpoch > 0 && expectedEpoch != c.Epoch {
		return nil, fmt.Errorf("%w: caller epoch %d != active charter epoch %d", ErrCharterFenceStale, expectedEpoch, c.Epoch)
	}
	if strings.TrimSpace(expectedFence) != "" && strings.TrimSpace(expectedFence) != c.Fence {
		return nil, fmt.Errorf("%w: caller fence token does not match active charter fence", ErrCharterFenceStale)
	}
	return c, nil
}

// RevokeAndReclaim atomically increments the charter's Epoch, rotates its Fence
// token, and transitions the charter to REVOKED so any in-flight S2/S3 operations
// presenting the old epoch/fence are immediately rejected.
func (s *Store) RevokeAndReclaim(charterID, reason string) (*Charter, error) {
	c, err := s.Get(charterID)
	if err != nil {
		return nil, err
	}
	newEpoch := c.Epoch + 1
	newFence := ComputeFence(c.CharterID, c.RepoID, newEpoch)
	now := s.clock().UTC().Unix()
	if strings.TrimSpace(reason) == "" {
		reason = "revoked and reclaimed by S1 FederationCoordinator"
	}
	_, err = s.db.Exec(
		`UPDATE charters
		    SET epoch = ?, fence = ?, state = ?, revoke_reason = ?, updated_at = ?
		  WHERE charter_id = ?`,
		newEpoch, newFence, string(StateRevoked), reason, now, c.CharterID,
	)
	if err != nil {
		return nil, fmt.Errorf("revoke charter: %w", err)
	}
	return s.Get(charterID)
}

// Complete seals an active charter with the final RoundReceipt SHA-256 hash.
func (s *Store) Complete(charterID, sealedReceiptHash string) (*Charter, error) {
	c, err := s.Get(charterID)
	if err != nil {
		return nil, err
	}
	if c.State != StateActive && c.State != StateIssued {
		return nil, fmt.Errorf("%w: cannot complete charter in state %s", ErrCharterInactive, c.State)
	}
	now := s.clock().UTC().Unix()
	_, err = s.db.Exec(
		`UPDATE charters
		    SET state = ?, sealed_receipt_hash = ?, updated_at = ?
		  WHERE charter_id = ?`,
		string(StateCompleted), strings.TrimSpace(sealedReceiptHash), now, c.CharterID,
	)
	if err != nil {
		return nil, fmt.Errorf("complete charter: %w", err)
	}
	return s.Get(charterID)
}

// List returns all stored charters, optionally filtered by repoID.
func (s *Store) List(repoIDFilter string) ([]*Charter, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if strings.TrimSpace(repoIDFilter) != "" {
		rows, err = s.db.Query(
			`SELECT charter_id, issuer_stratum, target_stratum, repo_id, scope_root,
			        allowed_paths_json, outcome_mandate, budget_tokens, epoch, fence,
			        charter_digest, state, issued_at, expires_at, updated_at,
			        revoke_reason, sealed_receipt_hash
			   FROM charters WHERE repo_id = ? ORDER BY issued_at DESC`,
			strings.TrimSpace(repoIDFilter),
		)
	} else {
		rows, err = s.db.Query(
			`SELECT charter_id, issuer_stratum, target_stratum, repo_id, scope_root,
			        allowed_paths_json, outcome_mandate, budget_tokens, epoch, fence,
			        charter_digest, state, issued_at, expires_at, updated_at,
			        revoke_reason, sealed_receipt_hash
			   FROM charters ORDER BY issued_at DESC`,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list charters: %w", err)
	}
	defer rows.Close()

	var out []*Charter
	for rows.Next() {
		c, err := scanCharter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ProjectPortfolio executes the O-Plane deterministic read-only projection over
// the charter registry and optional multi-repository workspace directory.
func (s *Store) ProjectPortfolio(workspaceDir string) (*PortfolioSnapshot, error) {
	list, err := s.List("")
	if err != nil {
		return nil, err
	}
	snap := &PortfolioSnapshot{
		ProjectedAt:  s.clock().UTC().Format(time.RFC3339),
		Stratum:      StratumOPlane,
		TotalCount:   len(list),
		WorkspaceDir: strings.TrimSpace(workspaceDir),
		Charters:     list,
	}
	for _, c := range list {
		switch c.State {
		case StateActive, StateIssued:
			snap.ActiveCount++
		case StateCompleted:
			snap.CompletedCount++
		case StateRevoked:
			snap.RevokedCount++
		}
	}
	return snap, nil
}

func deduplicatePaths(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; !ok {
			seen[trimmed] = struct{}{}
			out = append(out, trimmed)
		}
	}
	sort.Strings(out)
	return out
}
