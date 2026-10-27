package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tamld/g8s/internal/vault"
)

func TestContextualMemoryPreconditionFilterAndSupersession(t *testing.T) {
	adapter, _ := setupTestAdapter(t)
	adapter.SetGateSensor(func(naked string) (string, error) { return "clean", nil })
	ctx := context.Background()

	e1 := &MemoryEntry{
		ID:            "mem-quota-low-01",
		Kind:          KindDecision,
		Scope:         ScopeSession,
		SessionID:     "sess-01",
		PromotedBy:    "f0",
		Payload:       "Route coding tasks to cu/composer-2.5 when frontier quota is below 45%",
		Preconditions: map[string]string{"quota_mode": "saving", "provider": "9router"},
	}
	if err := adapter.ProposeEntry(ctx, e1); err != nil {
		t.Fatalf("ProposeEntry e1 failed: %v", err)
	}

	// Query with mismatched context -> must NOT recall e1
	matchedWrong, err := adapter.RecallContextualEntries(ctx, map[string]string{"quota_mode": "abundant", "provider": "9router"}, 0)
	if err != nil {
		t.Fatalf("RecallContextualEntries wrong context failed: %v", err)
	}
	if len(matchedWrong) != 0 {
		t.Fatalf("expected 0 entries for mismatched context, got %d", len(matchedWrong))
	}

	// Query with matching context -> must recall e1
	matchedRight, err := adapter.RecallContextualEntries(ctx, map[string]string{"quota_mode": "saving", "provider": "9router", "os": "darwin"}, 0)
	if err != nil {
		t.Fatalf("RecallContextualEntries right context failed: %v", err)
	}
	if len(matchedRight) != 1 || matchedRight[0].ID != "mem-quota-low-01" {
		t.Fatalf("expected mem-quota-low-01, got %+v", matchedRight)
	}

	// Supersede e1 with e2
	e2 := &MemoryEntry{
		ID:            "mem-quota-low-02",
		Kind:          KindDecision,
		Scope:         ScopeSession,
		SessionID:     "sess-01",
		PromotedBy:    "f0",
		Payload:       "Use Sandwich Architecture with batch verification on cu/composer-2.5",
		Preconditions: map[string]string{"quota_mode": "saving"},
	}
	if err := adapter.SupersedeEntry(ctx, "mem-quota-low-01", e2); err != nil {
		t.Fatalf("SupersedeEntry failed: %v", err)
	}

	// Recall must now return ONLY e2, not superseded e1
	afterSupersede, err := adapter.RecallContextualEntries(ctx, map[string]string{"quota_mode": "saving", "provider": "9router"}, 0)
	if err != nil {
		t.Fatalf("RecallContextualEntries after supersede failed: %v", err)
	}
	if len(afterSupersede) != 1 || afterSupersede[0].ID != "mem-quota-low-02" {
		t.Fatalf("expected only mem-quota-low-02 after supersession, got %+v", afterSupersede)
	}
}

func TestSelfContainedPromoteToVaultAndCitationRotDetection(t *testing.T) {
	adapter, _ := setupTestAdapter(t)
	adapter.SetGateSensor(func(naked string) (string, error) { return "clean", nil })
	ctx := context.Background()

	repoDir := t.TempDir()
	srcFile := filepath.Join(repoDir, "pipe_win.go")
	testFile := filepath.Join(repoDir, "pipe_win_test.go")
	_ = os.WriteFile(srcFile, []byte("package main\nfunc captureStdoutFn() {}\n"), 0o644)
	_ = os.WriteFile(testFile, []byte("package main\nfunc TestCLISurfaceContractParity() {}\n"), 0o644)

	e := &MemoryEntry{
		ID:         "mem-win-pipe-01",
		Kind:       KindFact,
		Scope:      ScopeGlobal,
		SessionID:  "sess-win",
		PromotedBy: "f0",
		Payload:    "Drain os.Pipe reader in a concurrent goroutine to avoid 4KB Windows pipe deadlock",
	}
	if err := adapter.ProposeEntry(ctx, e); err != nil {
		t.Fatalf("ProposeEntry: %v", err)
	}

	rec := vault.DistillationRecord{
		ID:        "KNOW-WIN-PIPE-01",
		Title:     "Windows 4KB Pipe Buffer Drain",
		Milestone: "v0.21.0",
		Status:    "DOCTRINE",
		Causality: vault.CausalityAnchor{
			Problem:  "os.Pipe deadlocks on Windows when writing >4KB synchronously",
			TradeOff: "Spawn concurrent io.Copy reader goroutine before closing writer",
		},
		SpatialCoordinates: vault.SpatialAnchor{
			Package: "main",
			File:    "pipe_win.go",
			Symbol:  "captureStdoutFn",
		},
		ForensicVerification: vault.ForensicAnchor{
			TestFile: "pipe_win_test.go",
			TestCase: "TestCLISurfaceContractParity",
		},
	}

	stored, err := adapter.PromoteToVault(ctx, "mem-win-pipe-01", rec)
	if err != nil || stored.ID != "KNOW-WIN-PIPE-01" {
		t.Fatalf("PromoteToVault failed: stored=%+v err=%v", stored, err)
	}

	updatedEntry, err := adapter.GetEntry(ctx, "mem-win-pipe-01")
	if err != nil || updatedEntry.Lifecycle != LifecycleDoctrine {
		t.Fatalf("expected LifecycleDoctrine after promotion, got %+v err=%v", updatedEntry, err)
	}

	// 1. Anchors valid -> 0 stale
	stale, err := adapter.vault.VerifyAnchors(ctx, repoDir, true)
	if err != nil || len(stale) != 0 {
		t.Fatalf("expected 0 stale anchors when files exist, got %v err=%v", stale, err)
	}

	// 2. Portable JSONL export & import (self-contained in .g8s/ without external wiki)
	jsonlPath := filepath.Join(repoDir, ".g8s", "knowledge-vault.jsonl")
	nExp, err := adapter.vault.ExportPortableJSONL(ctx, jsonlPath, "")
	if err != nil || nExp != 1 {
		t.Fatalf("ExportPortableJSONL: n=%d err=%v", nExp, err)
	}

	// 3. Simulate Citation Rot (symbol removed from pipe_win.go)
	_ = os.WriteFile(srcFile, []byte("package main\nfunc renamedHelper() {}\n"), 0o644)
	staleAfter, err := adapter.vault.VerifyAnchors(ctx, repoDir, true)
	if err != nil || len(staleAfter) != 1 || staleAfter[0] != "KNOW-WIN-PIPE-01" {
		t.Fatalf("expected KNOW-WIN-PIPE-01 flagged stale after symbol removal, got %v err=%v", staleAfter, err)
	}
	rotRec, err := adapter.vault.Get(ctx, "KNOW-WIN-PIPE-01")
	if err != nil || rotRec.Status != "STALE_ANCHOR" {
		t.Fatalf("expected status STALE_ANCHOR, got %+v err=%v", rotRec, err)
	}
}
