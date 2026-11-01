package vault

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func reachPromoteSample(receiptHash, summary string) DistillationRecord {
	rec := sampleRecord("unused", "Reach artifact")
	rec.Causality.Problem = summary
	rec.ForensicVerification.ReceiptHash = receiptHash
	return rec
}

func TestPromoteReachArtifact_DefaultStatusProposed(t *testing.T) {
	v := newTestVault(t, nil)
	ctx := context.Background()
	rec := reachPromoteSample("sha256:deadbeef", "external read summary")
	stored, err := PromoteReachArtifact(ctx, v, rec)
	if err != nil {
		t.Fatalf("PromoteReachArtifact: %v", err)
	}
	if stored.Status != "PROPOSED" {
		t.Fatalf("status = %q, want PROPOSED", stored.Status)
	}
	got, err := v.Get(ctx, stored.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "PROPOSED" {
		t.Fatalf("persisted status = %q, want PROPOSED", got.Status)
	}
}

func TestPromoteReachArtifact_RejectsEmptyReceiptHash(t *testing.T) {
	v := newTestVault(t, nil)
	rec := reachPromoteSample("", "summary without receipt")
	_, err := PromoteReachArtifact(context.Background(), v, rec)
	if !errors.Is(err, ErrEmptyReachReceiptHash) {
		t.Fatalf("err = %v, want ErrEmptyReachReceiptHash", err)
	}
}

func TestPromoteReachArtifact_ScrubsCredentialPatterns(t *testing.T) {
	v := newTestVault(t, nil)
	ctx := context.Background()
	ghToken := "ghp_" + strings.Repeat("1", 36)
	skToken := "sk-" + strings.Repeat("a", 24)
	awsKey := "AKIA" + "1234567890ABCDEF"
	pemHeader := "-----BEGIN " + "RSA PRIVATE KEY-----"
	summary := "token " + ghToken + " " + skToken + " " + awsKey + " " + pemHeader + " tail"
	rec := reachPromoteSample("sha256:abc", summary)
	stored, err := PromoteReachArtifact(ctx, v, rec)
	if err != nil {
		t.Fatalf("PromoteReachArtifact: %v", err)
	}
	if strings.Contains(stored.Causality.Problem, "ghp_") ||
		strings.Contains(stored.Causality.Problem, "sk-") ||
		strings.Contains(stored.Causality.Problem, "AKIA") ||
		strings.Contains(stored.Causality.Problem, "BEGIN RSA PRIVATE KEY") {
		t.Fatalf("credentials not scrubbed: %q", stored.Causality.Problem)
	}
	if !strings.Contains(stored.Causality.Problem, reachRedacted) {
		t.Fatalf("expected %q markers in summary, got %q", reachRedacted, stored.Causality.Problem)
	}
}

func TestPromoteReachArtifact_IdempotentOnSameBodySHA256(t *testing.T) {
	v := newTestVault(t, nil)
	ctx := context.Background()
	rec := reachPromoteSample("sha256:receipt1", "stable reach body")
	first, err := PromoteReachArtifact(ctx, v, rec)
	if err != nil {
		t.Fatalf("first promote: %v", err)
	}
	rec.Title = "Updated title only"
	second, err := PromoteReachArtifact(ctx, v, rec)
	if err != nil {
		t.Fatalf("second promote: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("ids differ: %q vs %q", first.ID, second.ID)
	}
	if !strings.HasPrefix(second.ID, "reach:") {
		t.Fatalf("id = %q, want reach: prefix", second.ID)
	}
}
