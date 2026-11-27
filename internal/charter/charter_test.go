package charter

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/signing"
	_ "modernc.org/sqlite"
)

func TestCharterLifecycleAndCanonicalDigest(t *testing.T) {
	tmp := t.TempDir()
	store, err := Open(tmp)
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	defer store.Close()

	c, err := store.Issue(IssueRequest{
		IssuerStratum:  StratumS1,
		RepoID:         "tamld/g8s",
		ScopeRoot:      tmp,
		AllowedPaths:   []string{"cmd/g8s/**", "internal/charter/**", "internal/charter/**"},
		OutcomeMandate: "Ship DELTA-33 Orthogonal Stratum Federation",
		BudgetTokens:   150000,
		TTL:            time.Hour,
	})
	if err != nil {
		t.Fatalf("Issue charter: %v", err)
	}
	if c.Epoch != 1 {
		t.Fatalf("expected epoch 1, got %d", c.Epoch)
	}
	if c.State != StateActive {
		t.Fatalf("expected ACTIVE state, got %s", c.State)
	}
	if len(c.AllowedPaths) != 2 {
		t.Fatalf("expected deduplicated AllowedPaths len 2, got %v", c.AllowedPaths)
	}
	if c.CharterDigest == "" || c.Fence == "" {
		t.Fatalf("expected non-empty digest and fence: %+v", c)
	}

	verified, err := store.Verify(c.CharterID, "tamld/g8s", c.Epoch, c.Fence)
	if err != nil {
		t.Fatalf("Verify error: %v", err)
	}
	if verified.CharterID != c.CharterID {
		t.Fatalf("expected verified charter ID %s, got %s", c.CharterID, verified.CharterID)
	}

	// Tamper check: canonical digest must change if OutcomeMandate changes.
	tampered := *c
	tampered.OutcomeMandate = "Mutated objective"
	if ComputeDigest(&tampered) == c.CharterDigest {
		t.Fatalf("expected digest mismatch when mandate is mutated")
	}

	// Complete lifecycle
	completed, err := store.Complete(c.CharterID, "sha256:receipt-round-1")
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	if completed.State != StateCompleted || completed.SealedReceiptHash != "sha256:receipt-round-1" {
		t.Fatalf("expected COMPLETED state with sealed receipt hash, got %+v", completed)
	}
}

func TestStratumAdjacencyAndMutationFirewall(t *testing.T) {
	// Valid downward adjacencies
	validPairs := [][2]Stratum{
		{StratumS0, StratumS1},
		{StratumS0, StratumS2},
		{StratumS1, StratumS2},
		{StratumS2, StratumS3Mutator},
		{StratumS2, StratumS3Adjudicator},
	}
	for _, pair := range validPairs {
		if err := ValidateAdjacency(pair[0], pair[1]); err != nil {
			t.Fatalf("expected %s -> %s to be valid, got: %v", pair[0], pair[1], err)
		}
	}

	// Invalid bypass or upward edges (e.g. S1 -> S3-M direct bypass, S3-A -> S3-M, O-Plane -> S2)
	invalidPairs := [][2]Stratum{
		{StratumS1, StratumS3Mutator},
		{StratumS1, StratumS3Adjudicator},
		{StratumS3Mutator, StratumS2},
		{StratumS3Adjudicator, StratumS3Mutator},
		{StratumOPlane, StratumS2},
	}
	for _, pair := range invalidPairs {
		if err := ValidateAdjacency(pair[0], pair[1]); err == nil {
			t.Fatalf("expected %s -> %s to be rejected by adjacency matrix", pair[0], pair[1])
		}
	}

	// Mutation firewall: S1, S3-A, and O-Plane cannot mutate files directly.
	for _, s := range []Stratum{StratumS1, StratumS3Adjudicator, StratumOPlane} {
		if err := VerifyMutationAllowed(s, "internal/charter/charter.go"); err == nil {
			t.Fatalf("expected stratum %s to be blocked from mutating repo files", s)
		}
	}

	// S2 and S3-M are allowed at the stratum layer (ring/receipt checks still apply downstream).
	for _, s := range []Stratum{StratumS2, StratumS3Mutator} {
		if err := VerifyMutationAllowed(s, "internal/charter/charter.go"); err != nil {
			t.Fatalf("expected stratum %s to pass stratum mutation check, got: %v", s, err)
		}
	}

	// Anti-spoofing clamp on Issue(): even if G8S_TIER is unset, a non-empty G8S_RECEIPT_ID
	// clamps CurrentStratum() to S3-M and blocks charter issuance.
	tmp := t.TempDir()
	store, err := Open(tmp)
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	defer store.Close()
	t.Setenv("G8S_TIER", "")
	t.Setenv("G8S_RECEIPT_ID", "rc-spoof-attempt")
	if _, err := store.Issue(IssueRequest{
		IssuerStratum:  StratumS1,
		RepoID:         "tamld/g8s",
		OutcomeMandate: "Spoof attempt",
	}); err == nil {
		t.Fatalf("expected Issue() to reject process with non-empty G8S_RECEIPT_ID")
	}
}

func TestCharterFenceRejectsStaleEpoch(t *testing.T) {
	tmp := t.TempDir()
	store, err := Open(tmp)
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	defer store.Close()

	c1, err := store.Issue(IssueRequest{
		IssuerStratum:  StratumS1,
		RepoID:         "tamld/g8s",
		ScopeRoot:      tmp,
		AllowedPaths:   []string{"internal/**"},
		OutcomeMandate: "Epoch 1 charter",
		BudgetTokens:   50000,
		TTL:            time.Hour,
	})
	if err != nil {
		t.Fatalf("Issue c1: %v", err)
	}
	oldEpoch := c1.Epoch
	oldFence := c1.Fence

	// Revoke and reclaim c1 (increments epoch and rotates fence token)
	revoked, err := store.RevokeAndReclaim(c1.CharterID, "Reclaimed by S1 coordinator")
	if err != nil {
		t.Fatalf("RevokeAndReclaim: %v", err)
	}
	if revoked.Epoch <= oldEpoch || revoked.Fence == oldFence || revoked.State != StateRevoked {
		t.Fatalf("expected incremented epoch, rotated fence, and REVOKED state; got %+v", revoked)
	}

	// Verification with old epoch/fence must fail
	if _, err := store.Verify(c1.CharterID, "tamld/g8s", oldEpoch, oldFence); err == nil {
		t.Fatalf("expected revoked charter c1 to fail verification, got nil")
	}

	// Issue a new active charter c2 and verify stale epoch rejection
	c2, err := store.Issue(IssueRequest{
		IssuerStratum:  StratumS1,
		RepoID:         "tamld/g8s",
		ScopeRoot:      tmp,
		AllowedPaths:   []string{"internal/**"},
		OutcomeMandate: "Epoch 2 replacement charter",
		BudgetTokens:   80000,
		TTL:            time.Hour,
	})
	if err != nil {
		t.Fatalf("Issue c2: %v", err)
	}
	if _, err := store.Verify(c2.CharterID, "tamld/g8s", c2.Epoch+1, c2.Fence); err == nil {
		t.Fatalf("expected mismatched epoch to fail verification")
	}
	if _, err := store.Verify(c2.CharterID, "tamld/g8s", c2.Epoch, "bad-fence-token"); err == nil {
		t.Fatalf("expected mismatched fence token to fail verification")
	}
	if _, err := store.Verify(c2.CharterID, "tamld/g8s", c2.Epoch, c2.Fence); err != nil {
		t.Fatalf("expected valid c2 verification, got: %v", err)
	}

	// Portfolio projection check
	snap, err := store.ProjectPortfolio(tmp)
	if err != nil {
		t.Fatalf("ProjectPortfolio: %v", err)
	}
	if snap.TotalCount != 2 || snap.ActiveCount != 1 || snap.RevokedCount != 1 {
		t.Fatalf("unexpected portfolio counts: %+v", snap)
	}

	// Ensure persistence on disk
	if _, err := os.Stat(filepath.Join(tmp, "charters.db")); err != nil {
		t.Fatalf("expected charters.db on disk: %v", err)
	}
}

func TestCharterRejectsForgedSQLiteRow(t *testing.T) {
	tmp := t.TempDir()
	store, err := Open(tmp)
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.Issue(IssueRequest{
		IssuerStratum:  StratumS1,
		RepoID:         "tamld/g8s",
		OutcomeMandate: "signed mandate",
		TTL:            time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(tmp, "charters.db")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE charters SET sig_tag = 'ed25519:v1:deadbeef' WHERE charter_id = ?`, c.CharterID); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	store2, err := Open(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()
	if _, err := store2.Get(c.CharterID); !errors.Is(err, ErrCharterTampered) {
		t.Fatalf("expected ErrCharterTampered, got %v", err)
	}
}

func TestCharterStratumTokenVerification(t *testing.T) {
	tmp := t.TempDir()
	store, err := Open(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	priv, pub, err := signing.EnsureOrLoadF0Signer(tmp)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	tok := signing.SignStratumToken(priv, string(StratumS2), now.Add(time.Hour).Unix())
	t.Setenv("G8S_STRATUM_TOKEN", tok)
	_, err = store.Issue(IssueRequest{
		IssuerStratum:  StratumS1,
		RepoID:         "tamld/g8s",
		OutcomeMandate: "token ok",
		TTL:            time.Hour,
	})
	if err != nil {
		t.Fatalf("valid token: %v", err)
	}
	bad := signing.SignStratumToken(priv, string(StratumS3Mutator), now.Add(time.Hour).Unix())
	t.Setenv("G8S_STRATUM_TOKEN", bad)
	if _, err := store.Issue(IssueRequest{IssuerStratum: StratumS1, RepoID: "tamld/g8s", OutcomeMandate: "wrong", TTL: time.Hour}); err == nil {
		t.Fatal("wrong stratum token must fail")
	}
	expired := signing.SignStratumToken(priv, string(StratumS2), now.Add(-time.Hour).Unix())
	t.Setenv("G8S_STRATUM_TOKEN", expired)
	if _, err := store.Issue(IssueRequest{IssuerStratum: StratumS1, RepoID: "tamld/g8s", OutcomeMandate: "expired", TTL: time.Hour}); err == nil {
		t.Fatal("expired token must fail")
	}
	t.Setenv("G8S_STRATUM_TOKEN", "")
	t.Setenv("G8S_STRATUM_REQUIRE_TOKEN", "1")
	if _, err := store.Issue(IssueRequest{IssuerStratum: StratumS1, RepoID: "tamld/g8s", OutcomeMandate: "missing", TTL: time.Hour}); err == nil {
		t.Fatal("missing required token must fail")
	}
	_ = pub
}

func TestCharterStateDirPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix perm test")
	}
	tmp := t.TempDir()
	if _, err := Open(tmp); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(tmp, "charters.db"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("charters.db mode %o", fi.Mode().Perm())
	}
	dirFi, err := os.Stat(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if dirFi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("state dir mode %o", dirFi.Mode().Perm())
	}
}

func TestParseAndCurrentStratumVariants(t *testing.T) {
	cases := []struct {
		raw  string
		want Stratum
	}{
		{"", StratumS2},
		{"S0", StratumS0},
		{"PRINCIPAL", StratumS0},
		{"S1", StratumS1},
		{"FEDERATION_COORDINATOR", StratumS1},
		{"S2", StratumS2},
		{"DOMAIN_GOVERNOR", StratumS2},
		{"S3", StratumS3Mutator},
		{"S3-M", StratumS3Mutator},
		{"S3-A", StratumS3Adjudicator},
		{"BLIND_ADJUDICATOR", StratumS3Adjudicator},
		{"O-PLANE", StratumOPlane},
		{"TELEMETRY_PROJECTOR", StratumOPlane},
	}
	for _, tc := range cases {
		got, err := ParseStratum(tc.raw)
		if err != nil || got != tc.want {
			t.Fatalf("ParseStratum(%q) = (%q, %v), want (%q, nil)", tc.raw, got, err, tc.want)
		}
	}
	if _, err := ParseStratum("BOGUS"); !errors.Is(err, ErrInvalidCharterRequest) {
		t.Fatalf("expected ErrInvalidCharterRequest for BOGUS, got %v", err)
	}

	t.Setenv("G8S_RECEIPT_ID", "")
	t.Setenv("G8S_TIER", "F1")
	t.Setenv("G8S_STRATUM", "S3-A")
	if got := CurrentStratum(); got != StratumS3Adjudicator {
		t.Fatalf("F1 + S3-A = %s, want %s", got, StratumS3Adjudicator)
	}

	t.Setenv("G8S_TIER", "F2")
	t.Setenv("G8S_STRATUM", "S0")
	if got := CurrentStratum(); got != StratumS3Mutator {
		t.Fatalf("F2 + S0 = %s, want %s", got, StratumS3Mutator)
	}

	t.Setenv("G8S_TIER", "")
	t.Setenv("G8S_STRATUM", "BOGUS")
	if got := CurrentStratum(); got != StratumS2 {
		t.Fatalf("BOGUS stratum fallback = %s, want %s", got, StratumS2)
	}

	t.Setenv("G8S_STRATUM", "S1")
	if got := CurrentStratum(); got != StratumS1 {
		t.Fatalf("S1 stratum = %s, want %s", got, StratumS1)
	}
}

func TestCharterLegacySchemaMigrationBackfill(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "charters.db")
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`CREATE TABLE charters (
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
	);`)
	if err != nil {
		t.Fatal(err)
	}

	c := Charter{
		CharterID:      "ch-legacy01",
		IssuerStratum:  StratumS1,
		TargetStratum:  StratumS2,
		RepoID:         "tamld/g8s",
		ScopeRoot:      tmp,
		AllowedPaths:   []string{"**"},
		OutcomeMandate: "legacy row",
		BudgetTokens:   100000,
		Epoch:          1,
		State:          StateActive,
		IssuedAt:       1700000000,
		ExpiresAt:      1999999999,
		UpdatedAt:      1700000000,
	}
	c.Fence = ComputeFence(c.CharterID, c.RepoID, c.Epoch)
	c.CharterDigest = ComputeDigest(&c)
	_, err = raw.Exec(
		`INSERT INTO charters (
			charter_id, issuer_stratum, target_stratum, repo_id, scope_root,
			allowed_paths_json, outcome_mandate, budget_tokens, epoch, fence,
			charter_digest, state, issued_at, expires_at, updated_at,
			revoke_reason, sealed_receipt_hash
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '')`,
		c.CharterID, string(c.IssuerStratum), string(c.TargetStratum), c.RepoID, c.ScopeRoot,
		`["**"]`, c.OutcomeMandate, c.BudgetTokens, c.Epoch, c.Fence,
		c.CharterDigest, string(c.State), c.IssuedAt, c.ExpiresAt, c.UpdatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(tmp)
	if err != nil {
		t.Fatalf("Open migrated store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	}()

	got, err := store.Get("ch-legacy01")
	if err != nil {
		t.Fatalf("Get migrated row: %v", err)
	}
	if got.Signature == "" {
		t.Fatal("expected backfilled signature to be non-empty")
	}

	filtered, err := store.List("tamld/g8s")
	if err != nil || len(filtered) != 1 {
		t.Fatalf("List(tamld/g8s) = (%d, %v), want len 1", len(filtered), err)
	}
}

func TestCharterStoreValidationAndEdgeCases(t *testing.T) {
	if _, err := NewStore("", nil); !errors.Is(err, ErrInvalidCharterRequest) {
		t.Fatalf("NewStore(\"\") = %v, want ErrInvalidCharterRequest", err)
	}

	t.Run("in_memory", func(t *testing.T) {
		t.Chdir(t.TempDir())
		memStore, err := NewStore(":memory:", nil)
		if err != nil {
			t.Fatalf("NewStore(:memory:): %v", err)
		}
		mc, err := memStore.Issue(IssueRequest{
			RepoID:         "tamld/g8s",
			OutcomeMandate: "memory charter",
		})
		if err != nil {
			t.Fatalf("Issue in memory: %v", err)
		}
		if _, err := memStore.Get(mc.CharterID); err != nil {
			t.Fatalf("Get in memory: %v", err)
		}
		if err := memStore.Close(); err != nil {
			t.Fatalf("memStore.Close: %v", err)
		}
	})
	var nilStore *Store
	if err := nilStore.Close(); err != nil {
		t.Fatalf("nilStore.Close: %v", err)
	}

	t.Setenv("G8S_STATE_DIR", t.TempDir())
	defStore, err := Open("")
	if err != nil {
		t.Fatalf("Open(\"\"): %v", err)
	}
	if err := defStore.Close(); err != nil {
		t.Fatalf("defStore.Close: %v", err)
	}

	now := time.Unix(1_700_000_000, 0).UTC()
	clockDir := t.TempDir()
	store, err := NewStore(filepath.Join(clockDir, "clock.db"), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewStore clock: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Fatalf("clock store Close: %v", err)
		}
	}()

	if _, err := store.Issue(IssueRequest{
		IssuerStratum:  StratumS2,
		RepoID:         "tamld/g8s",
		OutcomeMandate: "bad issuer",
	}); !errors.Is(err, ErrStratumAdjacencyViolation) {
		t.Fatalf("expected ErrStratumAdjacencyViolation, got %v", err)
	}
	if _, err := store.Issue(IssueRequest{
		OutcomeMandate: "missing repo",
	}); !errors.Is(err, ErrInvalidCharterRequest) {
		t.Fatalf("expected ErrInvalidCharterRequest for empty repo, got %v", err)
	}
	if _, err := store.Issue(IssueRequest{
		RepoID: "tamld/g8s",
	}); !errors.Is(err, ErrInvalidCharterRequest) {
		t.Fatalf("expected ErrInvalidCharterRequest for empty mandate, got %v", err)
	}
	if _, err := store.Issue(IssueRequest{
		RepoID:         "tamld/g8s",
		OutcomeMandate: "ttl too long",
		TTL:            25 * time.Hour,
	}); !errors.Is(err, ErrInvalidCharterRequest) {
		t.Fatalf("expected ErrInvalidCharterRequest for 25h TTL, got %v", err)
	}

	defCharter, err := store.Issue(IssueRequest{
		RepoID:         "tamld/g8s",
		OutcomeMandate: "defaults check",
	})
	if err != nil {
		t.Fatalf("Issue defaults: %v", err)
	}
	if !reflect.DeepEqual(defCharter.AllowedPaths, []string{"**"}) || defCharter.BudgetTokens != 100000 {
		t.Fatalf("unexpected defaults: %+v", defCharter)
	}

	if _, err := store.Get("missing-id"); !errors.Is(err, ErrCharterNotFound) {
		t.Fatalf("expected ErrCharterNotFound, got %v", err)
	}

	shortC, err := store.Issue(IssueRequest{
		RepoID:         "tamld/g8s",
		OutcomeMandate: "expiry check",
		TTL:            10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Issue shortC: %v", err)
	}
	if _, err := store.Verify(shortC.CharterID, "other/repo", 1, shortC.Fence); !errors.Is(err, ErrCharterRepoMismatch) {
		t.Fatalf("expected ErrCharterRepoMismatch, got %v", err)
	}
	now = now.Add(20 * time.Minute)
	if _, err := store.Verify(shortC.CharterID, "tamld/g8s", 1, shortC.Fence); !errors.Is(err, ErrCharterExpired) {
		t.Fatalf("expected ErrCharterExpired, got %v", err)
	}

	if _, err := store.Complete(defCharter.CharterID, "sha256:r1"); err != nil {
		t.Fatalf("Complete first time: %v", err)
	}
	if _, err := store.Complete(defCharter.CharterID, "sha256:r2"); !errors.Is(err, ErrCharterInactive) {
		t.Fatalf("expected ErrCharterInactive on second Complete, got %v", err)
	}
}

// TestIssuingStratumFromEnv — issue #977 PR-2 (V8/B-12): the charter issuer
// for authority-minting surfaces is a SERVER process fact. IssuingStratumFromEnv
// resolves the declared G8S_STRATUM (S0/S1 pass) and fails closed with
// ErrStratumAdjacencyViolation for every non-issuing process stratum: the S2
// default of an undeclared env, the S3 clamp of worker tiers, and the S3 clamp
// of receipt-bound processes.
func TestIssuingStratumFromEnv(t *testing.T) {
	cases := []struct {
		name              string
		stratum, tier     string
		receiptID         string
		want              Stratum
		wantAdjacencyFail bool
	}{
		{name: "s0_principal_may_issue", stratum: "S0", want: StratumS0},
		{name: "s1_federation_may_issue", stratum: "S1", want: StratumS1},
		{name: "undeclared_defaults_s2_refused", stratum: "", want: StratumS2, wantAdjacencyFail: true},
		{name: "s2_domain_governor_refused", stratum: "S2", want: StratumS2, wantAdjacencyFail: true},
		{name: "opaque_stratum_defaults_s2_refused", stratum: "BOGUS", want: StratumS2, wantAdjacencyFail: true},
		{name: "f1_tier_clamped_refused", stratum: "S1", tier: "F1", want: StratumS3Mutator, wantAdjacencyFail: true},
		{name: "f2_tier_clamped_refused", stratum: "S0", tier: "F2", want: StratumS3Mutator, wantAdjacencyFail: true},
		{name: "receipt_bound_clamped_refused", stratum: "S1", receiptID: "rc-bound", want: StratumS3Mutator, wantAdjacencyFail: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("G8S_STRATUM", tc.stratum)
			t.Setenv("G8S_TIER", tc.tier)
			t.Setenv("G8S_RECEIPT_ID", tc.receiptID)
			got, err := IssuingStratumFromEnv()
			if got != tc.want {
				t.Fatalf("IssuingStratumFromEnv() = %s, want %s", got, tc.want)
			}
			if tc.wantAdjacencyFail && !errors.Is(err, ErrStratumAdjacencyViolation) {
				t.Fatalf("expected ErrStratumAdjacencyViolation, got %v", err)
			}
			if !tc.wantAdjacencyFail && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
