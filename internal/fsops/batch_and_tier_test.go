package fsops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/contextpack"
	"github.com/tamld/g8s/internal/vault"
)

func TestReadWithASTTierElision(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "calc.go")
	src := `package calc

// ComputeTotal sums positive values.
func ComputeTotal(items []int) int {
	total := 0
	for _, v := range items {
		if v > 0 {
			total += v
		}
	}
	return total
}
`
	if err := os.WriteFile(goFile, []byte(src), 0o644); err != nil {
		t.Fatalf("write calc.go: %v", err)
	}

	ctx := context.Background()
	skelRes, err := Read(ctx, ReadOptions{
		Path:      goFile,
		ScopeRoot: dir,
		Tier:      "skel",
	})
	if err != nil {
		t.Fatalf("Read tier=skel failed: %v", err)
	}
	if skelRes.Tier != "SKEL" || !strings.Contains(skelRes.Content, "[SKEL: ComputeTotal") {
		t.Fatalf("expected SKEL elision in content, got tier=%q content=%q", skelRes.Tier, skelRes.Content)
	}

	sigsRes, err := Read(ctx, ReadOptions{
		Path:      goFile,
		ScopeRoot: dir,
		Tier:      "sigs",
	})
	if err != nil {
		t.Fatalf("Read tier=sigs failed: %v", err)
	}
	if sigsRes.Tier != "SIGS" || !strings.Contains(sigsRes.Content, "func ComputeTotal(items []int) int") {
		t.Fatalf("expected SIGS signature output, got tier=%q content=%q", sigsRes.Tier, sigsRes.Content)
	}
}

func TestApplyBatchEditsAtomicRollbackOnFailure(t *testing.T) {
	dir := t.TempDir()
	fileA := filepath.Join(dir, "a.go")
	fileB := filepath.Join(dir, "b.go")
	origA := "package a\nconst Version = \"1.0\"\n"
	origB := "package b\nconst Version = \"1.0\"\n"
	_ = os.WriteFile(fileA, []byte(origA), 0o644)
	_ = os.WriteFile(fileB, []byte(origB), 0o644)

	ctx := context.Background()

	// Batch where edit #1 is valid but edit #2 fails IfSHA256 -> fileA must NOT be modified
	_, err := ApplyBatchEdits(ctx, []EditOptions{
		{
			Path:      fileA,
			ScopeRoot: dir,
			OldString: `"1.0"`,
			NewString: `"2.0"`,
		},
		{
			Path:      fileB,
			ScopeRoot: dir,
			OldString: `"1.0"`,
			NewString: `"2.0"`,
			IfSHA256:  "0000000000000000000000000000000000000000000000000000000000000000",
		},
	})
	if err == nil {
		t.Fatalf("expected ApplyBatchEdits to fail on SHA256 conflict in second edit")
	}

	gotA, _ := os.ReadFile(fileA)
	if string(gotA) != origA {
		t.Fatalf("atomic batch violated: fileA was modified despite edit #2 failing: %q", string(gotA))
	}

	// Valid batch -> both files updated atomically
	results, err := ApplyBatchEdits(ctx, []EditOptions{
		{
			Path:      fileA,
			ScopeRoot: dir,
			OldString: `"1.0"`,
			NewString: `"2.0"`,
		},
		{
			Path:      fileB,
			ScopeRoot: dir,
			OldString: `"1.0"`,
			NewString: `"2.0"`,
		},
	})
	if err != nil || len(results) != 2 {
		t.Fatalf("expected valid ApplyBatchEdits to succeed with 2 results, got %v err=%v", results, err)
	}
	gotA2, _ := os.ReadFile(fileA)
	gotB2, _ := os.ReadFile(fileB)
	if !strings.Contains(string(gotA2), `"2.0"`) || !strings.Contains(string(gotB2), `"2.0"`) {
		t.Fatalf("expected both files updated to 2.0, got A=%q B=%q", string(gotA2), string(gotB2))
	}
}

func TestContextPackIncludesVerifiedVaultWhispers(t *testing.T) {
	dir := t.TempDir()
	workerFile := filepath.Join(dir, "worker.go")
	workerTest := filepath.Join(dir, "worker_test.go")
	_ = os.WriteFile(workerFile, []byte("package worker\nfunc RunLoop() {}\n"), 0o644)
	_ = os.WriteFile(workerTest, []byte("package worker\nfunc TestRunLoop() {}\n"), 0o644)

	vdb := filepath.Join(dir, "vault.db")
	v, err := vault.NewVault(vdb, time.Now)
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	defer v.Close()

	ctx := context.Background()
	_, _ = v.Store(ctx, vault.DistillationRecord{
		ID:        "KNOW-VALID-01",
		Title:     "RunLoop Lease Renewal",
		Milestone: "v0.21.0",
		Status:    "DOCTRINE",
		Causality: vault.CausalityAnchor{
			Problem:  "Worker loses lease on long tasks",
			TradeOff: "Renew CAS lease every heartbeat interval in RunLoop",
		},
		SpatialCoordinates: vault.SpatialAnchor{
			Package: "worker",
			File:    "worker.go",
			Symbol:  "RunLoop",
		},
		ForensicVerification: vault.ForensicAnchor{
			TestFile: "worker_test.go",
			TestCase: "TestRunLoop",
		},
	})
	_, _ = v.Store(ctx, vault.DistillationRecord{
		ID:        "KNOW-STALE-01",
		Title:     "Obsolete LegacyFunc Rule",
		Milestone: "v0.19.0",
		Status:    "DOCTRINE",
		Causality: vault.CausalityAnchor{
			Problem:  "LegacyFunc race",
			TradeOff: "Hold global lock in LegacyFunc",
		},
		SpatialCoordinates: vault.SpatialAnchor{
			Package: "worker",
			File:    "worker.go",
			Symbol:  "DeletedLegacyFunc",
		},
		ForensicVerification: vault.ForensicAnchor{
			TestFile: "worker_test.go",
			TestCase: "TestRunLoop",
		},
	})

	packRes, err := contextpack.Pack(contextpack.PackRequest{
		RootDir:      dir,
		VaultDBPath:  vdb,
		BudgetTokens: 4000,
	})
	if err != nil {
		t.Fatalf("contextpack.Pack: %v", err)
	}
	if len(packRes.KnowledgeWhispers) != 1 || !strings.Contains(packRes.KnowledgeWhispers[0], "KNOW-VALID-01") {
		t.Fatalf("expected only KNOW-VALID-01 in KnowledgeWhispers, got %+v", packRes.KnowledgeWhispers)
	}
}
