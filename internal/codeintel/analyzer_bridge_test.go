package codeintel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzerAdapterAndRouterBlastRadius(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "service.go")
	callerFile := filepath.Join(dir, "caller.go")

	srcCode := `package sample

// ComputeScore calculates the score.
func ComputeScore(x int) int {
	return x * 2
}
`
	callerCode := `package sample

func Run() int {
	return ComputeScore(21)
}
`
	if err := os.WriteFile(srcFile, []byte(srcCode), 0o644); err != nil {
		t.Fatalf("write service.go: %v", err)
	}
	if err := os.WriteFile(callerFile, []byte(callerCode), 0o644); err != nil {
		t.Fatalf("write caller.go: %v", err)
	}

	bridge, err := NewAnalyzerAdapter(dir)
	if err != nil {
		t.Fatalf("NewAnalyzerAdapter: %v", err)
	}
	if bridge.Name() != "blast-radius-analyzer" {
		t.Fatalf("unexpected adapter name: %s", bridge.Name())
	}

	ctx := context.Background()
	astAdapter, err := NewASTAdapter(dir)
	if err != nil {
		t.Fatalf("NewASTAdapter: %v", err)
	}
	router := NewMultiTierRouter(astAdapter, bridge)

	locs, err := bridge.References(ctx, srcFile, "ComputeScore")
	if err != nil || len(locs) == 0 {
		t.Fatalf("bridge.References: len=%d err=%v", len(locs), err)
	}

	tree, err := bridge.CallHierarchy(ctx, srcFile, "ComputeScore")
	if err != nil || tree == nil || len(tree.Callers) == 0 {
		t.Fatalf("bridge.CallHierarchy: tree=%+v err=%v", tree, err)
	}

	diags, err := bridge.Diagnostics(ctx, srcFile)
	if err != nil {
		t.Fatalf("bridge.Diagnostics: %v", err)
	}
	_ = diags

	report, err := router.BlastRadius(ctx, dir, srcFile, "ComputeScore")
	if err != nil {
		t.Fatalf("router.BlastRadius: %v", err)
	}
	if report.TargetSymbol != "ComputeScore" || len(report.AffectedFiles) < 2 {
		t.Fatalf("unexpected blast radius report: %+v", report)
	}
}
