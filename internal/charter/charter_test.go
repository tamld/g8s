package charter

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
