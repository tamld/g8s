package harness

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/lockfile"
	"github.com/tamld/g8s/internal/sandbox"
	"github.com/tamld/g8s/internal/signing"
)

// EvolutionTier classifies an evolution change into Tier A (bounded autonomous) or Tier B (S0 Human TCB gated).
type EvolutionTier string

const (
	// TierABoundedAuto permits autonomous self-improvement strictly outside the TCB under TRUST-G3/G6 bounds.
	TierABoundedAuto EvolutionTier = "tier_a_bounded_auto"
	// TierBS0HumanTCB enforces mandatory deterministic checks plus distinct S0 Human Principal approval for any TCB change.
	TierBS0HumanTCB EvolutionTier = "tier_b_s0_human_tcb"
)

const (
	CodeTrustRootWorkerTCBDenied             = "E_TRUST_ROOT_WORKER_TCB_DENIED"
	CodeTrustRootS0ApprovalRequired          = "E_TRUST_ROOT_S0_APPROVAL_REQUIRED"
	CodeTrustRootSelfAttestationBlocked      = "E_TRUST_ROOT_SELF_ATTESTATION_BLOCKED"
	CodeTrustRootDeterministicChecksRequired = "E_TRUST_ROOT_DETERMINISTIC_CHECKS_REQUIRED"
	CodeTrustRootS0KeyMissing                = "E_TRUST_ROOT_S0_KEY_MISSING"
	CodeTrustRootS0KeyReuseForbidden         = "E_TRUST_ROOT_S0_KEY_REUSE_FORBIDDEN"
	CodeTrustRootS0SignatureInvalid          = "E_TRUST_ROOT_S0_SIGNATURE_INVALID"
	CodeTrustRootS0Expired                   = "E_TRUST_ROOT_S0_EXPIRED"
	CodeTrustRootS0ReplayDetected            = "E_TRUST_ROOT_S0_REPLAY_DETECTED"
	CodeTrustRootS0NonceStoreRequired        = "E_TRUST_ROOT_S0_NONCE_STORE_REQUIRED"
	CodeTrustRootDigestMismatch              = "E_TRUST_ROOT_DIGEST_MISMATCH"
	CodeTrustRootTargetPathsMismatch         = "E_TRUST_ROOT_TARGET_PATHS_MISMATCH"
)

const (
	// MaxS0ApprovalTTL caps how long a single signed S0 TCB approval may remain valid.
	MaxS0ApprovalTTL = 24 * time.Hour
	// MaxS0ClockSkew bounds acceptable clock skew when verifying IssuedAt against current time.
	MaxS0ClockSkew = 5 * time.Minute
)

// ErrS0NonceReplayed is returned when an S0 approval attestation nonce is presented more than once.
var ErrS0NonceReplayed = errors.New("trust-root: S0 approval nonce already consumed")

// S0NonceStore tracks single-use S0 approval nonces to block attestation replay attacks.
type S0NonceStore interface {
	// CheckAndConsume verifies nonce has not been used. When consume is true,
	// it atomically persists nonce until expiresAt; when consume is false (dry-run),
	// it checks availability without burning the nonce.
	CheckAndConsume(nonce string, expiresAt time.Time, now time.Time, consume bool) error
}

// MemoryS0NonceStore is an in-memory mutex-guarded S0NonceStore for tests and in-process callers.
type MemoryS0NonceStore struct {
	mu   sync.Mutex
	used map[string]time.Time
}

// NewMemoryS0NonceStore constructs an empty in-memory S0NonceStore.
func NewMemoryS0NonceStore() *MemoryS0NonceStore {
	return &MemoryS0NonceStore{
		used: make(map[string]time.Time),
	}
}

// CheckAndConsume verifies nonce uniqueness and optionally records nonce expiration.
func (s *MemoryS0NonceStore) CheckAndConsume(nonce string, expiresAt time.Time, now time.Time, consume bool) error {
	nonce = strings.TrimSpace(nonce)
	if !IsValidS0Nonce(nonce) {
		return fmt.Errorf("invalid S0 nonce %q", nonce)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used == nil {
		s.used = make(map[string]time.Time)
	}
	for k, exp := range s.used {
		if now.After(exp.Add(MaxS0ClockSkew)) {
			delete(s.used, k)
		}
	}
	if _, exists := s.used[nonce]; exists {
		return ErrS0NonceReplayed
	}
	if consume {
		s.used[nonce] = expiresAt.UTC()
	}
	return nil
}

// FileS0NonceStore persists consumed S0 approval nonces atomically in a JSON file under stateDir.
type FileS0NonceStore struct {
	mu   sync.Mutex
	path string
}

// NewFileS0NonceStore constructs a file-backed S0NonceStore at path.
func NewFileS0NonceStore(path string) *FileS0NonceStore {
	return &FileS0NonceStore{path: path}
}

// CheckAndConsume loads persisted nonces, rejects duplicates fail-closed, and atomically records new consumption.
func (s *FileS0NonceStore) CheckAndConsume(nonce string, expiresAt time.Time, now time.Time, consume bool) error {
	nonce = strings.TrimSpace(nonce)
	if !IsValidS0Nonce(nonce) {
		return fmt.Errorf("invalid S0 nonce %q", nonce)
	}
	if strings.TrimSpace(s.path) == "" {
		return errors.New("nonce store path is empty")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// #972: the in-process mutex cannot serialize two coordinator processes
	// sharing the same state dir. Hold a cross-process file lock across the
	// read-check-commit window so concurrent S0 approval consumers serialize;
	// without it, both processes can observe the nonce as unused and both
	// consume the same approval (replay). Fail-closed on lock timeout: a trust
	// decision must never proceed on an unserialized registry read.
	nonceLock, lockErr := acquireNonceLock(s.path)
	if lockErr != nil {
		return fmt.Errorf("nonce store lock: %w", lockErr)
	}
	defer func() {
		if relErr := nonceLock.Release(); relErr != nil {
			fmt.Fprintf(os.Stderr, "warning: release nonce registry lock %s: %v\n", s.path, relErr)
		}
	}()

	entries := make(map[string]string)
	// #nosec G304 -- s.path points to operator stateDir nonce registry file.
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read nonce store %q: %w", s.path, err)
		}
	} else if len(strings.TrimSpace(string(raw))) > 0 {
		if jErr := json.Unmarshal(raw, &entries); jErr != nil {
			return fmt.Errorf("parse nonce store %q: %w", s.path, jErr)
		}
	}

	for k, expStr := range entries {
		exp, pErr := time.Parse(time.RFC3339Nano, expStr)
		if pErr == nil && now.After(exp.Add(MaxS0ClockSkew)) {
			delete(entries, k)
		}
	}

	if _, exists := entries[nonce]; exists {
		return ErrS0NonceReplayed
	}
	if !consume {
		return nil
	}

	entries[nonce] = expiresAt.UTC().Format(time.RFC3339Nano)
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create nonce store directory %q: %w", dir, err)
	}
	encoded, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("encode nonce store: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, ".s0-nonces-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp nonce file: %w", err)
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(encoded); err != nil {
		if cErr := tmpFile.Close(); cErr != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("write nonce store: %w (close: %v)", err, cErr)
		}
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write nonce store: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp nonce file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("commit nonce store %q: %w", s.path, err)
	}
	return nil
}

// acquireNonceLock takes a cross-process lock on the nonce registry sibling
// file (#972), retrying briefly so concurrent deliveries serialize instead of
// failing. The lock is advisory-only for other tooling but every in-repo
// consumer of the registry goes through this path.
func acquireNonceLock(noncePath string) (*lockfile.Lock, error) {
	lockPath := noncePath + ".lock"
	var lastErr error
	for i := 0; i < 25; i++ {
		lock, err := lockfile.TryLock(lockPath)
		if err == nil {
			return lock, nil
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	return nil, fmt.Errorf("timed out acquiring nonce registry lock %q: %w", lockPath, lastErr)
}

// S0ApprovalAttestation carries signed non-LLM human principal sign-off bound to target paths, digests, nonce, and TTL.
type S0ApprovalAttestation struct {
	ApprovedBy                string   `json:"approved_by"`
	PrincipalStratum          string   `json:"principal_stratum"`
	Nonce                     string   `json:"nonce"`
	IssuedAt                  string   `json:"issued_at"`
	ExpiresAt                 string   `json:"expires_at"`
	TargetPaths               []string `json:"target_paths"`
	DeterministicChecksPassed bool     `json:"deterministic_checks_passed"`
	ChecksDigest              string   `json:"checks_digest"`
	ArtifactDigest            string   `json:"artifact_digest"`
	Signature                 string   `json:"signature"`
}

// TrustRootRequest describes a proposed mutation or evolution target evaluated against ADR-0042 TRUST-G2.
type TrustRootRequest struct {
	ActorStratum              string                 `json:"actor_stratum"` // "F0" | "S2" | "S1" | "F1" | "F2" | "S3-M" | "S3-A" | "S0"
	ActorID                   string                 `json:"actor_id"`
	TargetPaths               []string               `json:"target_paths"`
	RequestedTier             EvolutionTier          `json:"requested_tier,omitempty"`
	DeterministicChecksPassed bool                   `json:"deterministic_checks_passed"`
	ObservedChecksDigest      string                 `json:"observed_checks_digest,omitempty"`
	ObservedArtifactDigest    string                 `json:"observed_artifact_digest,omitempty"`
	S0PublicKey               ed25519.PublicKey      `json:"-"`
	F0PublicKey               ed25519.PublicKey      `json:"-"`
	Now                       time.Time              `json:"-"`
	NonceStore                S0NonceStore           `json:"-"`
	DryRun                    bool                   `json:"dry_run,omitempty"`
	S0Approval                *S0ApprovalAttestation `json:"s0_approval,omitempty"`
}

// TrustRootDecision records the deterministic enforcement verdict outside LLM inference.
type TrustRootDecision struct {
	Allowed         bool                   `json:"allowed"`
	EffectiveTier   EvolutionTier          `json:"effective_tier"`
	TouchesTCB      bool                   `json:"touches_tcb"`
	MatchedTCBPaths []string               `json:"matched_tcb_paths,omitempty"`
	Code            string                 `json:"code,omitempty"`
	Reason          string                 `json:"reason"`
	EpistemicClass  sandbox.EpistemicClass `json:"epistemic_class"`
}

var staticTCBExactOrSuffix = []string{
	".g8s/trust-boundaries.yml",
	".g8s/verifier-classes.yml",
	".g8s/lane-bundles.yml",
	"f0-signing.key",
	"f0-signing.pub",
	"s0-human.key",
	"s0-human.pub",
	"internal/worker/diff_firewall.go",
}

// trustRootBoundaryFloor mirrors .g8s/trust-boundaries.yml as an offline floor so
// internal/harness avoids an import cycle with internal/lane test helpers while staying SSoT-aligned.
var trustRootBoundaryFloor = []string{
	"internal/receipt/*",
	"internal/worker/proc_*",
	"internal/worker/diff_firewall*",
	"internal/server/*",
	"internal/harness/*",
	"internal/memory/*",
	"internal/dispatch/*",
	"internal/sandbox/*",
	"internal/signing/*",
	"internal/charter/*",
	"internal/fsops/*",
	"internal/verifier/*",
	"internal/premise/*",
	"internal/provider/conformance/*",
	"cmd/g8s/deliver.go",
	"scripts/install.sh",
}

func loadTCBBoundaryPatterns() []string {
	wd, err := os.Getwd()
	if err != nil {
		return trustRootBoundaryFloor
	}
	dir := filepath.Clean(wd)
	for {
		cand := filepath.Join(dir, ".g8s", "trust-boundaries.yml")
		// #nosec G304 -- cand walks ancestors of current working directory for fixed .g8s/trust-boundaries.yml path.
		raw, rErr := os.ReadFile(cand)
		if rErr == nil {
			var parsed []string
			inSection := false
			for _, line := range strings.Split(string(raw), "\n") {
				s := strings.TrimSpace(line)
				if s == "" || strings.HasPrefix(s, "#") {
					continue
				}
				if s == "trust_boundaries:" {
					inSection = true
					continue
				}
				if inSection && strings.HasPrefix(s, "- ") {
					val := strings.TrimSpace(strings.TrimPrefix(s, "- "))
					val = strings.Trim(val, `"'`)
					if val != "" {
						parsed = append(parsed, val)
					}
				} else if inSection && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
					inSection = false
				}
			}
			if len(parsed) > 0 {
				return parsed
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return trustRootBoundaryFloor
}

func normalizeTCBPath(raw string) string {
	cleaned := filepath.ToSlash(filepath.Clean(strings.TrimSpace(raw)))
	cleaned = strings.TrimPrefix(cleaned, "./")
	cleaned = strings.TrimLeft(cleaned, "/")
	return cleaned
}

// CanonicalizeTCBPaths normalizes, deduplicates, and lexicographically sorts path slices.
func CanonicalizeTCBPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, raw := range paths {
		norm := normalizeTCBPath(raw)
		if norm == "" || norm == "." {
			continue
		}
		if _, exists := seen[norm]; exists {
			continue
		}
		seen[norm] = struct{}{}
		out = append(out, norm)
	}
	sort.Strings(out)
	return out
}

func matchTCBPattern(cleanPath, pattern string) bool {
	pat := normalizeTCBPath(pattern)
	if pat == "" || cleanPath == "" {
		return false
	}
	if strings.HasSuffix(pat, "/*") {
		prefix := strings.TrimSuffix(pat, "/*")
		if cleanPath == prefix || strings.HasPrefix(cleanPath, prefix+"/") {
			return true
		}
	}
	if strings.HasSuffix(pat, "*") {
		prefix := strings.TrimSuffix(pat, "*")
		if strings.HasPrefix(cleanPath, prefix) {
			return true
		}
	}
	if matched, err := filepath.Match(pat, cleanPath); err == nil && matched {
		return true
	}
	return cleanPath == pat
}

// IsTCBPath reports whether rawPath touches g8s's Trusted Computing Base (ADR-0042 §4 Tier B + .g8s/trust-boundaries.yml).
func IsTCBPath(rawPath string) bool {
	norm := normalizeTCBPath(rawPath)
	if norm == "" || norm == "." {
		return false
	}
	lower := strings.ToLower(norm)
	for _, pat := range loadTCBBoundaryPatterns() {
		if matchTCBPattern(lower, strings.ToLower(pat)) {
			return true
		}
	}
	for _, pat := range trustRootBoundaryFloor {
		if matchTCBPattern(lower, strings.ToLower(pat)) {
			return true
		}
	}
	base := filepath.Base(lower)
	for _, fixed := range staticTCBExactOrSuffix {
		fLower := strings.ToLower(fixed)
		if lower == fLower || strings.HasSuffix(lower, "/"+fLower) ||
			base == "f0-signing.key" || base == "s0-human.key" || base == "s0-human.pub" {
			return true
		}
	}
	return false
}

// IsValidSHA256Digest reports whether s is a well-formed "sha256:<64-hex>" digest.
func IsValidSHA256Digest(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if !strings.HasPrefix(s, "sha256:") {
		return false
	}
	hexPart := strings.TrimPrefix(s, "sha256:")
	if len(hexPart) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(hexPart)
	return err == nil
}

// IsValidS0Nonce validates length [16,128] and safe ASCII identifier characters [A-Za-z0-9._-].
func IsValidS0Nonce(nonce string) bool {
	nonce = strings.TrimSpace(nonce)
	if len(nonce) < 16 || len(nonce) > 128 {
		return false
	}
	for i := 0; i < len(nonce); i++ {
		b := nonce[i]
		isAlnum := (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
		if !isAlnum && b != '.' && b != '_' && b != '-' {
			return false
		}
	}
	return true
}

// ComputeChecksDigest computes a canonical SHA-256 digest of deterministic check evidence bytes.
// Empty evidence returns "" so empty checks cannot accidentally match an attestation.
func ComputeChecksDigest(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ComputeDeliverableArtifactDigest deterministically hashes all staged additions/modifications and deletions.
func ComputeDeliverableArtifactDigest(worktreeDir string, applyPaths, deletePaths []string) (string, error) {
	if strings.TrimSpace(worktreeDir) == "" {
		return "", errors.New("trust-root: deliverable worktree dir is empty")
	}
	normApply := CanonicalizeTCBPaths(applyPaths)
	normDelete := CanonicalizeTCBPaths(deletePaths)

	h := sha256.New()
	_, _ = h.Write([]byte("g8s.deliverable_artifact.v1\x00"))

	for _, rel := range normApply {
		if rel == ".." || strings.HasPrefix(rel, "../") {
			return "", fmt.Errorf("trust-root: unsafe deliverable path %q", rel)
		}
		fullPath := filepath.Join(worktreeDir, filepath.FromSlash(rel))
		// #nosec G304 -- rel has already been normalized and checked against traversal above.
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return "", fmt.Errorf("trust-root: read deliverable artifact %q: %w", rel, err)
		}
		_, _ = fmt.Fprintf(h, "A:%s\x00%d:", rel, len(data))
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
	}
	for _, rel := range normDelete {
		if rel == ".." || strings.HasPrefix(rel, "../") {
			return "", fmt.Errorf("trust-root: unsafe deleted path %q", rel)
		}
		_, _ = fmt.Fprintf(h, "D:%s\x00", rel)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func s0ApprovalCanonicalFields(att *S0ApprovalAttestation) []string {
	return []string{
		strings.TrimSpace(att.ApprovedBy),
		strings.ToUpper(strings.TrimSpace(att.PrincipalStratum)),
		strings.TrimSpace(att.Nonce),
		strings.TrimSpace(att.IssuedAt),
		strings.TrimSpace(att.ExpiresAt),
		strings.Join(CanonicalizeTCBPaths(att.TargetPaths), "\n"),
		"deterministic_checks_passed=" + strconv.FormatBool(att.DeterministicChecksPassed),
		strings.ToLower(strings.TrimSpace(att.ChecksDigest)),
		strings.ToLower(strings.TrimSpace(att.ArtifactDigest)),
	}
}

// SignS0ApprovalAttestation signs a populated S0ApprovalAttestation using the Human S0 private key.
func SignS0ApprovalAttestation(att *S0ApprovalAttestation, s0Priv ed25519.PrivateKey) error {
	if att == nil {
		return errors.New("trust-root: nil S0 approval attestation")
	}
	if len(s0Priv) != ed25519.PrivateKeySize {
		return signing.ErrInvalidSignature
	}
	if !strings.EqualFold(strings.TrimSpace(att.PrincipalStratum), "S0") {
		return errors.New("trust-root: PrincipalStratum must be S0")
	}
	if strings.TrimSpace(att.ApprovedBy) == "" {
		return errors.New("trust-root: ApprovedBy is required")
	}
	if !IsValidS0Nonce(att.Nonce) {
		return errors.New("trust-root: valid Nonce is required")
	}
	if !IsValidSHA256Digest(att.ChecksDigest) || !IsValidSHA256Digest(att.ArtifactDigest) {
		return errors.New("trust-root: valid sha256 ChecksDigest and ArtifactDigest are required")
	}
	iss, iErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(att.IssuedAt))
	exp, eErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(att.ExpiresAt))
	if iErr != nil || eErr != nil || !exp.After(iss) || exp.Sub(iss) > MaxS0ApprovalTTL {
		return errors.New("trust-root: valid IssuedAt/ExpiresAt window within 24h TTL is required")
	}
	if len(CanonicalizeTCBPaths(att.TargetPaths)) == 0 {
		return errors.New("trust-root: non-empty TargetPaths is required")
	}
	att.Signature = signing.SignS0Approval(s0Priv, s0ApprovalCanonicalFields(att)...)
	return nil
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func isWorkerOrSubagentStratum(stratum string) bool {
	switch strings.ToUpper(strings.TrimSpace(stratum)) {
	case "", "F1", "F2", "S3", "S3-M", "S3-A", "WORKER", "SUBAGENT":
		return true
	default:
		return false
	}
}

func isSelfOrAgentAttestor(approvedBy, actorID string) bool {
	ab := strings.ToLower(strings.TrimSpace(approvedBy))
	aid := strings.ToLower(strings.TrimSpace(actorID))
	if ab == "" {
		return false
	}
	if aid != "" && ab == aid {
		return true
	}
	tokens := strings.FieldsFunc(ab, func(r rune) bool {
		return r == '-' || r == '_' || r == ':' || r == '.' || r == '/' || r == ' '
	})
	for _, tok := range tokens {
		switch tok {
		case "f0", "f1", "f2", "s1", "s2", "s3", "llm", "agent", "worker", "bot", "autonomous", "coordinator":
			return true
		}
	}
	return false
}

func tierBDeny(matched []string, code, reason string) TrustRootDecision {
	return TrustRootDecision{
		Allowed:         false,
		EffectiveTier:   TierBS0HumanTCB,
		TouchesTCB:      true,
		MatchedTCBPaths: matched,
		Code:            code,
		Reason:          reason,
		EpistemicClass:  sandbox.EpistemicMeasuredMetric,
	}
}

// EvaluateTrustRootMutation enforces ADR-0042 TRUST-G2: TCB changes ALWAYS resolve to
// Tier B (cryptographically signed S0 Human Principal + deterministic checks + non-replayable digest binding),
// blocking worker mutations, F0 LLM self-attestation, unsigned/expired/replayed approvals, and key reuse.
func EvaluateTrustRootMutation(req TrustRootRequest) TrustRootDecision {
	var matched []string
	for _, p := range req.TargetPaths {
		if IsTCBPath(p) {
			matched = append(matched, normalizeTCBPath(p))
		}
	}

	touchesTCB := len(matched) > 0
	if !touchesTCB {
		if !req.DeterministicChecksPassed {
			return TrustRootDecision{
				Allowed:        false,
				EffectiveTier:  TierABoundedAuto,
				TouchesTCB:     false,
				Code:           CodeTrustRootDeterministicChecksRequired,
				Reason:         "Tier A bounded evolution still requires passing deterministic checks",
				EpistemicClass: sandbox.EpistemicMeasuredMetric,
			}
		}
		return TrustRootDecision{
			Allowed:        true,
			EffectiveTier:  TierABoundedAuto,
			TouchesTCB:     false,
			Reason:         "non-TCB target permitted under Tier A bounded autonomous evolution",
			EpistemicClass: sandbox.EpistemicMeasuredMetric,
		}
	}

	// 1-2. Worker block & live deterministic check prerequisite.
	if isWorkerOrSubagentStratum(req.ActorStratum) {
		return tierBDeny(matched, CodeTrustRootWorkerTCBDenied,
			fmt.Sprintf("stratum %q is forbidden from modifying TCB paths (%v)", req.ActorStratum, matched))
	}
	if !req.DeterministicChecksPassed {
		return tierBDeny(matched, CodeTrustRootDeterministicChecksRequired,
			"Tier B TCB mutation requires deterministic checks to pass before S0 approval")
	}

	// 3-4. S0 approval presence & anti-self-attestation guard.
	att := req.S0Approval
	if att == nil {
		return tierBDeny(matched, CodeTrustRootS0ApprovalRequired,
			fmt.Sprintf("Tier B TCB paths (%v) require explicit S0 Human Principal approval", matched))
	}
	if isSelfOrAgentAttestor(att.ApprovedBy, req.ActorID) {
		return tierBDeny(matched, CodeTrustRootSelfAttestationBlocked,
			fmt.Sprintf("blocked non-human/self-attestation in S0 approval (%q)", att.ApprovedBy))
	}

	// 5-6. Dedicated S0 key custody (fail-closed if missing or colliding with F0 key).
	if len(req.S0PublicKey) != ed25519.PublicKeySize {
		return tierBDeny(matched, CodeTrustRootS0KeyMissing,
			"pinned S0 Human Ed25519 public key is missing or malformed")
	}
	if err := signing.VerifyDisjointS0Key(req.S0PublicKey, req.F0PublicKey); err != nil {
		return tierBDeny(matched, CodeTrustRootS0KeyReuseForbidden,
			fmt.Sprintf("S0 key custody violation: %v", err))
	}

	// 7-8. Required structural fields & attestation checks flag.
	if !strings.EqualFold(strings.TrimSpace(att.PrincipalStratum), "S0") ||
		strings.TrimSpace(att.ApprovedBy) == "" ||
		!IsValidS0Nonce(att.Nonce) ||
		!IsValidSHA256Digest(att.ChecksDigest) ||
		!IsValidSHA256Digest(att.ArtifactDigest) {
		return tierBDeny(matched, CodeTrustRootS0ApprovalRequired,
			"S0 approval must declare PrincipalStratum=S0, ApprovedBy, valid Nonce (>=16 chars), and sha256 ChecksDigest/ArtifactDigest")
	}
	if !att.DeterministicChecksPassed {
		return tierBDeny(matched, CodeTrustRootDeterministicChecksRequired,
			"S0 approval attestation recorded deterministic_checks_passed=false")
	}

	// 9. IssuedAt / ExpiresAt TTL & expiry bounds.
	now := req.Now.UTC()
	if req.Now.IsZero() {
		now = time.Now().UTC()
	}
	issuedAt, issErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(att.IssuedAt))
	expiresAt, expErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(att.ExpiresAt))
	if issErr != nil || expErr != nil {
		return tierBDeny(matched, CodeTrustRootS0Expired,
			"S0 approval must carry valid RFC3339 issued_at and expires_at timestamps")
	}
	if !expiresAt.After(issuedAt) || expiresAt.Sub(issuedAt) > MaxS0ApprovalTTL {
		return tierBDeny(matched, CodeTrustRootS0Expired,
			fmt.Sprintf("S0 approval TTL invalid (must be >0 and <= %s)", MaxS0ApprovalTTL))
	}
	if now.After(expiresAt) {
		return tierBDeny(matched, CodeTrustRootS0Expired,
			fmt.Sprintf("S0 approval expired at %s", expiresAt.UTC().Format(time.RFC3339)))
	}
	if issuedAt.After(now.Add(MaxS0ClockSkew)) {
		return tierBDeny(matched, CodeTrustRootS0Expired,
			fmt.Sprintf("S0 approval issued_at %s exceeds clock skew window", issuedAt.UTC().Format(time.RFC3339)))
	}

	// 10. Exact target path set binding (prevents smuggling unapproved files).
	reqPaths := CanonicalizeTCBPaths(req.TargetPaths)
	attPaths := CanonicalizeTCBPaths(att.TargetPaths)
	if len(reqPaths) == 0 || !stringSlicesEqual(reqPaths, attPaths) {
		return tierBDeny(matched, CodeTrustRootTargetPathsMismatch,
			fmt.Sprintf("target paths %v do not match S0-signed target_paths %v", reqPaths, attPaths))
	}

	// 11. Mechanical digest binding against observed artifact and check outputs.
	if !IsValidSHA256Digest(req.ObservedArtifactDigest) || !strings.EqualFold(strings.TrimSpace(req.ObservedArtifactDigest), strings.TrimSpace(att.ArtifactDigest)) {
		return tierBDeny(matched, CodeTrustRootDigestMismatch,
			fmt.Sprintf("observed artifact digest %q does not match S0-signed digest %q", req.ObservedArtifactDigest, att.ArtifactDigest))
	}
	if !IsValidSHA256Digest(req.ObservedChecksDigest) || !strings.EqualFold(strings.TrimSpace(req.ObservedChecksDigest), strings.TrimSpace(att.ChecksDigest)) {
		return tierBDeny(matched, CodeTrustRootDigestMismatch,
			fmt.Sprintf("observed checks digest %q does not match S0-signed digest %q", req.ObservedChecksDigest, att.ChecksDigest))
	}

	// 12. Mandatory Ed25519 signature verification against pinned S0 human key.
	if strings.TrimSpace(att.Signature) == "" {
		return tierBDeny(matched, CodeTrustRootS0SignatureInvalid,
			"S0 approval is missing mandatory Ed25519 signature")
	}
	if err := signing.VerifyS0Approval(req.S0PublicKey, att.Signature, s0ApprovalCanonicalFields(att)...); err != nil {
		return tierBDeny(matched, CodeTrustRootS0SignatureInvalid,
			fmt.Sprintf("S0 Ed25519 signature verification failed: %v", err))
	}

	// 13. Single-use nonce anti-replay (checked/consumed ONLY after signature and digests pass).
	// #972: the nonce store is REQUIRED for real (non-dry-run) application —
	// replay protection is part of the Tier-B gate and cannot be omitted by a
	// future caller. Dry-run planning decisions may proceed without a store
	// because no authority is consumed; the reason records the skipped check.
	if req.NonceStore == nil {
		if req.DryRun {
			return TrustRootDecision{
				Allowed:         true,
				EffectiveTier:   TierBS0HumanTCB,
				TouchesTCB:      true,
				MatchedTCBPaths: matched,
				Reason:          "DRY-RUN planning decision: nonce store not configured — replay check SKIPPED; real Tier-B application requires a nonce store",
				EpistemicClass:  sandbox.EpistemicMeasuredMetric,
			}
		}
		return tierBDeny(matched, CodeTrustRootS0NonceStoreRequired,
			"S0 nonce store is required for real Tier-B application — replay protection cannot be omitted")
	}
	if err := req.NonceStore.CheckAndConsume(att.Nonce, expiresAt, now, !req.DryRun); err != nil {
		return tierBDeny(matched, CodeTrustRootS0ReplayDetected,
			fmt.Sprintf("S0 nonce check failed: %v", err))
	}

	return TrustRootDecision{
		Allowed:         true,
		EffectiveTier:   TierBS0HumanTCB,
		TouchesTCB:      true,
		MatchedTCBPaths: matched,
		Reason:          fmt.Sprintf("Tier B TCB change authorized by signed S0 principal %q (nonce=%s checks=%s)", att.ApprovedBy, att.Nonce, att.ChecksDigest),
		EpistemicClass:  sandbox.EpistemicMeasuredMetric,
	}
}
