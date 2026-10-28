package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tamld/g8s/internal/featuremap"
	"github.com/tamld/g8s/internal/memory"
	"github.com/tamld/g8s/internal/vault"
)

func TestCoordinatorIntakeAndMasteryScorecard(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	v, err := vault.NewVault(filepath.Join(tmpDir, "vault.db"), nil)
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	defer v.Close()

	memAdapter, err := memory.NewLocalSQLiteMemoryAdapter(memory.AdapterOptions{
		DBPath: filepath.Join(tmpDir, "memory.db"),
		Vault:  v,
	})
	if err != nil {
		t.Fatalf("NewLocalSQLiteMemoryAdapter: %v", err)
	}
	defer memAdapter.Close()

	// Seed 2 live doctrines for FEAT-VAULT-MEMORY-01 and 3 corroborated first-pass memories
	for _, rec := range []vault.DistillationRecord{
		{
			ID:     "DOC-MEM-01",
			Title:  "RRF Hybrid Recall Invariant",
			Status: "DOCTRINE",
			Causality: vault.CausalityAnchor{
				Problem:  "Scale distortion between BM25 and cosine similarity",
				TradeOff: "Use Reciprocal Rank Fusion k=60",
			},
			SpatialCoordinates: vault.SpatialAnchor{
				Package: "internal/memory",
				File:    "internal/memory/lifecycle.go",
				Symbol:  "RecallHybridRRF",
			},
			ForensicVerification: vault.ForensicAnchor{
				TestFile: "internal/memory/contextual_memory_test.go",
				TestCase: "TestRecallHybridRRF",
			},
		},
		{
			ID:     "DOC-MEM-02",
			Title:  "Self-Contained Vault Promotion",
			Status: "DOCTRINE",
			Causality: vault.CausalityAnchor{
				Problem:  "External wiki coupling breaks standalone binary portability",
				TradeOff: "Promote directly into internal/vault SQLite FTS5",
			},
			SpatialCoordinates: vault.SpatialAnchor{
				Package: "internal/memory",
				File:    "internal/memory/lifecycle.go",
				Symbol:  "PromoteToVault",
			},
			ForensicVerification: vault.ForensicAnchor{
				TestFile: "internal/memory/contextual_memory_test.go",
				TestCase: "TestSelfContainedPromoteToVaultAndCitationRotDetection",
			},
		},
	} {
		if _, err := v.Store(ctx, rec); err != nil {
			t.Fatalf("v.Store: %v", err)
		}
	}

	entry := &memory.MemoryEntry{
		ID:        "mem-vault-1",
		Kind:      memory.KindDecision,
		Scope:     memory.ScopeGlobal,
		SessionID: "sess-0",
		Payload:   "Filter preconditions before RRF scoring in internal/memory/lifecycle.go",
		Preconditions: map[string]string{
			"feature": "FEAT-VAULT-MEMORY-01",
		},
	}
	if err := memAdapter.ProposeEntry(ctx, entry); err != nil {
		t.Fatalf("ProposeEntry: %v", err)
	}
	for _, s := range []string{"s1", "s2", "s3"} {
		if _, err := memAdapter.RecordOutcome(ctx, []string{entry.ID}, s, true); err != nil {
			t.Fatalf("RecordOutcome: %v", err)
		}
	}

	// Find repo root containing .g8s/feature-map.yml
	repoRoot := "../.."
	if _, err := os.Stat(filepath.Join(repoRoot, ".g8s/feature-map.yml")); err != nil {
		t.Skipf("skipping test: .g8s/feature-map.yml not found at %s", repoRoot)
	}
	fm, err := featuremap.Load(filepath.Join(repoRoot, ".g8s/feature-map.yml"))
	if err != nil {
		t.Fatalf("featuremap.Load: %v", err)
	}

	// 1. Compute Domain Mastery Scorecard (#690)
	scorecard, err := ComputeDomainMastery(ctx, fm, memAdapter, v, repoRoot)
	if err != nil {
		t.Fatalf("ComputeDomainMastery: %v", err)
	}
	if len(scorecard.Domains) != len(fm.Features) {
		t.Fatalf("expected %d domains in scorecard, got %d", len(fm.Features), len(scorecard.Domains))
	}

	var vaultDomain *DomainMasteryStatus
	for i := range scorecard.Domains {
		if scorecard.Domains[i].FeatureID == "FEAT-VAULT-MEMORY-01" {
			vaultDomain = &scorecard.Domains[i]
			break
		}
	}
	if vaultDomain == nil {
		t.Fatalf("expected FEAT-VAULT-MEMORY-01 in mastery scorecard")
	}
	if vaultDomain.MasteryTier != MasteryTierMaster && vaultDomain.MasteryTier != MasteryTierExpert {
		t.Fatalf("expected FEAT-VAULT-MEMORY-01 to reach EXPERT or MASTER tier, got %s (%+v)", vaultDomain.MasteryTier, *vaultDomain)
	}

	// 2. Run Coordinator Cognitive Intake (#688)
	brief, err := RunCoordinatorIntake(ctx, CoordinatorIntakeRequest{
		Goal:          "Add Experiential Autopsy Loop to internal/memory and wire into verify",
		TargetFiles:   []string{"internal/memory/lifecycle.go"},
		ActiveContext: map[string]string{"feature": "FEAT-VAULT-MEMORY-01"},
		RepoRoot:      repoRoot,
		FeatureMap:    fm,
		Memory:        memAdapter,
		Vault:         v,
	})
	if err != nil {
		t.Fatalf("RunCoordinatorIntake: %v", err)
	}
	if len(brief.Orientation.MatchedFeatures) == 0 || brief.Orientation.MatchedFeatures[0] != "FEAT-VAULT-MEMORY-01" {
		t.Fatalf("expected FEAT-VAULT-MEMORY-01 in matched features, got %v", brief.Orientation.MatchedFeatures)
	}
	if brief.Orientation.GateLane != "S" {
		t.Fatalf("expected GateLane=S for trust-boundary path internal/memory/lifecycle.go, got %s", brief.Orientation.GateLane)
	}
	if !brief.ResearchGate.Required {
		t.Fatalf("expected ResearchGate.Required=true for Lane S change")
	}
	if len(brief.DialecticalQuestions.RootCoreProbes) < 3 {
		t.Fatalf("expected at least 3 Root-Core dialectical probes, got %d", len(brief.DialecticalQuestions.RootCoreProbes))
	}
	if len(brief.RoutingPlan.SkillChain) == 0 || len(brief.RoutingPlan.NativeTools) == 0 {
		t.Fatalf("expected populated SkillChain and NativeTools in RoutingPlan: %+v", brief.RoutingPlan)
	}
}
