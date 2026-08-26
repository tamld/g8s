package analyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func createSampleProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// pkgA/foo.go
	pkgADir := filepath.Join(dir, "pkgA")
	if err := os.MkdirAll(pkgADir, 0755); err != nil {
		t.Fatalf("mkdir pkgA: %v", err)
	}
	fooCode := `package pkgA

func CalculateTotal(a, b int) int {
	return a + b
}
`
	if err := os.WriteFile(filepath.Join(pkgADir, "foo.go"), []byte(fooCode), 0644); err != nil {
		t.Fatalf("write foo.go: %v", err)
	}

	// pkgB/bar.go (calls CalculateTotal)
	pkgBDir := filepath.Join(dir, "pkgB")
	if err := os.MkdirAll(pkgBDir, 0755); err != nil {
		t.Fatalf("mkdir pkgB: %v", err)
	}
	barCode := `package pkgB

import "pkgA"

func ProcessOrder() int {
	return pkgA.CalculateTotal(10, 20)
}
`
	if err := os.WriteFile(filepath.Join(pkgBDir, "bar.go"), []byte(barCode), 0644); err != nil {
		t.Fatalf("write bar.go: %v", err)
	}

	return dir
}

func TestAnalyzeSymbolImpact(t *testing.T) {
	root := createSampleProject(t)
	an, err := NewAnalyzer(root)
	if err != nil {
		t.Fatalf("NewAnalyzer failed: %v", err)
	}

	targetFile := filepath.Join(root, "pkgA", "foo.go")
	report, err := an.AnalyzeSymbolImpact(targetFile, "CalculateTotal")
	if err != nil {
		t.Fatalf("AnalyzeSymbolImpact failed: %v", err)
	}

	if report.TargetSymbol != "CalculateTotal" {
		t.Fatalf("expected TargetSymbol CalculateTotal, got %s", report.TargetSymbol)
	}
	if len(report.AffectedFiles) < 2 {
		t.Fatalf("expected at least 2 affected files (pkgA/foo.go and pkgB/bar.go), got %v", report.AffectedFiles)
	}
	if len(report.DirectCallers) == 0 {
		t.Fatalf("expected direct callers to be recorded, got empty")
	}
}

func TestAnalyzeFileImpact(t *testing.T) {
	root := createSampleProject(t)
	an, err := NewAnalyzer(root)
	if err != nil {
		t.Fatalf("NewAnalyzer failed: %v", err)
	}

	targetFile := filepath.Join(root, "pkgA", "foo.go")
	report, err := an.AnalyzeFileImpact(targetFile)
	if err != nil {
		t.Fatalf("AnalyzeFileImpact failed: %v", err)
	}

	if len(report.AffectedFiles) < 2 {
		t.Fatalf("expected affected files to include caller, got %v", report.AffectedFiles)
	}
	if report.BlastRadiusScore <= 0 {
		t.Fatalf("expected positive blast radius score, got %f", report.BlastRadiusScore)
	}
}

func TestAnalyzeNonExistentFile(t *testing.T) {
	root := t.TempDir()
	an, err := NewAnalyzer(root)
	if err != nil {
		t.Fatalf("NewAnalyzer: %v", err)
	}

	_, err = an.AnalyzeFileImpact(filepath.Join(root, "nonexistent.go"))
	if err == nil {
		t.Fatalf("expected error for nonexistent file, got nil")
	}
}

func TestCorePackageCriticalWeight(t *testing.T) {
	risk := categorizeRisk("internal/harness/harness.go", 3, 12.0)
	if risk != "CRITICAL" {
		t.Fatalf("expected CRITICAL risk for internal/harness, got %s", risk)
	}

	lowRisk := categorizeRisk("pkg/util/math.go", 1, 1.5)
	if lowRisk != "LOW" {
		t.Fatalf("expected LOW risk, got %s", lowRisk)
	}
}
