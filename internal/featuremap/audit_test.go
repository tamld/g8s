package featuremap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAuditFeaturesASTAndLive(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "internal", "demo")
	cmdDir := filepath.Join(dir, "cmd", "g8s")
	if err := os.MkdirAll(pkgDir, 0o700); err != nil {
		t.Fatalf("MkdirAll pkgDir: %v", err)
	}
	if err := os.MkdirAll(cmdDir, 0o700); err != nil {
		t.Fatalf("MkdirAll cmdDir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/demo\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "demo.go"), []byte("package demo\nfunc Add(a, b int) int { return a + b }\n"), 0o600); err != nil {
		t.Fatalf("write demo.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "demo_test.go"), []byte("package demo\nimport \"testing\"\nfunc TestAddWorks(t *testing.T) {\n\tif Add(2, 3) != 5 { t.Fatal(\"bad\") }\n}\n"), 0o600); err != nil {
		t.Fatalf("write demo_test.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte("package main\nfunc main() {\n\t_ = \"demo-cli\"\n}\n"), 0o600); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	fm := &FeatureMap{
		SchemaVersion: SchemaV2,
		Features: []FeatureNode{
			{
				ID:        "FEAT-OK-01",
				Title:     "Healthy Demo Feature",
				CodePaths: []string{"internal/demo/**"},
				Interfaces: &Interfaces{
					CLI: []string{"demo-cli"},
				},
				VisibleTests: []string{
					"internal/demo/demo_test.go",
					"go test ./internal/demo -run TestAddWorks",
				},
			},
			{
				ID:        "FEAT-DRIFT-01",
				Title:     "Drifted Feature With Missing Test Symbol",
				CodePaths: []string{"internal/demo/**"},
				VisibleTests: []string{
					"go test ./internal/demo -run TestNonExistentFunction",
				},
			},
		},
	}

	// 1. Static AST audit
	report, err := Audit(fm, dir, AuditOptions{Live: false})
	if err != nil {
		t.Fatalf("Audit static error: %v", err)
	}
	if report.TotalFeatures != 2 {
		t.Fatalf("expected 2 features in report, got %d", report.TotalFeatures)
	}
	if report.Results[0].Status != AuditStatusStaticOnly {
		t.Fatalf("expected FEAT-OK-01 status STATIC_ONLY, got %s (%v)", report.Results[0].Status, report.Results[0].Findings)
	}
	if report.Results[1].Status != AuditStatusDriftedContract {
		t.Fatalf("expected FEAT-DRIFT-01 status DRIFTED_CONTRACT due to missing AST test symbol, got %s", report.Results[1].Status)
	}

	// 2. Live execution audit on FEAT-OK-01
	liveReport, err := Audit(fm, dir, AuditOptions{FeatureID: "FEAT-OK-01", Live: true})
	if err != nil {
		t.Fatalf("Audit live error: %v", err)
	}
	if len(liveReport.Results) != 1 || liveReport.Results[0].Status != AuditStatusVerifiedLive {
		t.Fatalf("expected FEAT-OK-01 status VERIFIED_LIVE, got %+v", liveReport.Results)
	}
}
