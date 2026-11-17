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

func setupTestReportFixture(t *testing.T) (string, *FeatureMap) {
	t.Helper()
	dir := t.TempDir()
	pkgA := filepath.Join(dir, "internal", "alpha")
	pkgB := filepath.Join(dir, "internal", "beta")
	cmdDir := filepath.Join(dir, "cmd", "g8s")
	for _, d := range []string{pkgA, pkgB, cmdDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("MkdirAll %s: %v", d, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/demo\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgA, "alpha.go"), []byte("package alpha\nfunc A() int { return 1 }\n"), 0o600); err != nil {
		t.Fatalf("write alpha.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgA, "alpha_test.go"), []byte("package alpha\nimport \"testing\"\nfunc TestAlpha(t *testing.T) {}\nfunc TestMain(m *testing.M) {}\n"), 0o600); err != nil {
		t.Fatalf("write alpha_test.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgB, "beta.go"), []byte("package beta\nfunc B() int { return 2 }\n"), 0o600); err != nil {
		t.Fatalf("write beta.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgB, "beta_test.go"), []byte("package beta\nimport \"testing\"\nfunc TestBeta(t *testing.T) {}\n"), 0o600); err != nil {
		t.Fatalf("write beta_test.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	fm := &FeatureMap{
		SchemaVersion: SchemaV2,
		Features: []FeatureNode{
			{
				ID:           "FEAT-A",
				Title:        "Alpha Feature",
				CodePaths:    []string{"internal/alpha/**"},
				VisibleTests: []string{"internal/alpha/alpha_test.go", "go test ./internal/alpha -run TestAlpha"},
			},
			{
				ID:           "FEAT-B",
				Title:        "Beta Feature",
				CodePaths:    []string{"internal/beta/**"},
				VisibleTests: []string{"internal/beta/beta_test.go"},
			},
		},
	}
	return dir, fm
}

func TestAuditFeatures_WithTestReport_JSONAndVerbose(t *testing.T) {
	dir, fm := setupTestReportFixture(t)

	jsonReport := filepath.Join(dir, "go-test.jsonl")
	jsonBody := `{"Action":"run","Package":"example.com/demo/internal/alpha","Test":"TestAlpha"}
{"Action":"pass","Package":"example.com/demo/internal/alpha","Test":"TestAlpha"}
{"Action":"pass","Package":"example.com/demo/internal/alpha"}
{"Action":"run","Package":"example.com/demo/internal/beta","Test":"TestBeta"}
{"Action":"pass","Package":"example.com/demo/internal/beta","Test":"TestBeta"}
{"Action":"pass","Package":"example.com/demo/internal/beta"}
`
	if err := os.WriteFile(jsonReport, []byte(jsonBody), 0o600); err != nil {
		t.Fatalf("write json report: %v", err)
	}

	rep, err := Audit(fm, dir, AuditOptions{TestReportPath: jsonReport})
	if err != nil {
		t.Fatalf("Audit json report failed: %v", err)
	}
	if !rep.Valid || rep.RuntimeProvenCount != 2 || rep.StaticOnlyCount != 0 || rep.BrokenRuntimeCount != 0 {
		t.Fatalf("unexpected JSON audit summary: %+v", rep)
	}
	for _, r := range rep.Results {
		if r.Status != AuditStatusRuntimeProven || r.ProvenTests == 0 {
			t.Fatalf("expected RUNTIME_PROVEN with proven tests > 0, got %+v", r)
		}
	}

	verboseReport := filepath.Join(dir, "go-test.log")
	verboseBody := `=== RUN   TestAlpha
--- PASS: TestAlpha (0.01s)
PASS
coverage: 100.0% of statements
ok  	example.com/demo/internal/alpha	0.012s	coverage: 100.0% of statements
=== RUN   TestBeta
--- PASS: TestBeta (0.01s)
PASS
coverage: 100.0% of statements
ok  	example.com/demo/internal/beta	0.010s	coverage: 100.0% of statements
`
	if err := os.WriteFile(verboseReport, []byte(verboseBody), 0o600); err != nil {
		t.Fatalf("write verbose report: %v", err)
	}

	vRep, err := Audit(fm, dir, AuditOptions{TestReportPath: verboseReport})
	if err != nil {
		t.Fatalf("Audit verbose report failed: %v", err)
	}
	if !vRep.Valid || vRep.RuntimeProvenCount != 2 || vRep.StaticOnlyCount != 0 {
		t.Fatalf("unexpected verbose audit summary: %+v", vRep)
	}
}

func TestAuditFeatures_WithTestReport_MissingOrFailedTestsBreakRuntime(t *testing.T) {
	dir, fm := setupTestReportFixture(t)

	// Missing package beta
	partialReport := filepath.Join(dir, "partial.log")
	partialBody := `=== RUN   TestAlpha
--- PASS: TestAlpha (0.01s)
ok  	example.com/demo/internal/alpha	0.012s
`
	if err := os.WriteFile(partialReport, []byte(partialBody), 0o600); err != nil {
		t.Fatalf("write partial report: %v", err)
	}
	rep, err := Audit(fm, dir, AuditOptions{TestReportPath: partialReport})
	if err != nil {
		t.Fatalf("Audit partial report returned unexpected error: %v", err)
	}
	if rep.Valid || rep.RuntimeProvenCount != 1 || rep.BrokenRuntimeCount != 1 {
		t.Fatalf("expected valid=false runtime_proven=1 broken=1, got %+v", rep)
	}
	if rep.Results[1].Status != AuditStatusBrokenRuntime {
		t.Fatalf("expected FEAT-B BROKEN_RUNTIME, got %+v", rep.Results[1])
	}

	// Failed test in beta
	failReport := filepath.Join(dir, "fail.jsonl")
	failBody := `{"Action":"pass","Package":"example.com/demo/internal/alpha","Test":"TestAlpha"}
{"Action":"pass","Package":"example.com/demo/internal/alpha"}
{"Action":"fail","Package":"example.com/demo/internal/beta","Test":"TestBeta"}
{"Action":"fail","Package":"example.com/demo/internal/beta"}
`
	if err := os.WriteFile(failReport, []byte(failBody), 0o600); err != nil {
		t.Fatalf("write fail report: %v", err)
	}
	failRep, err := Audit(fm, dir, AuditOptions{TestReportPath: failReport})
	if err != nil {
		t.Fatalf("Audit fail report returned unexpected error: %v", err)
	}
	if failRep.Valid || failRep.BrokenRuntimeCount != 1 {
		t.Fatalf("expected failed test to break runtime, got %+v", failRep)
	}

	// Empty report fails closed
	emptyReport := filepath.Join(dir, "empty.log")
	if err := os.WriteFile(emptyReport, []byte("no recognized events here\n"), 0o600); err != nil {
		t.Fatalf("write empty report: %v", err)
	}
	if _, err := Audit(fm, dir, AuditOptions{TestReportPath: emptyReport}); err == nil {
		t.Fatalf("expected error on report without test events")
	}

	// Unreadable/missing report file fails closed
	if _, err := Audit(fm, dir, AuditOptions{TestReportPath: filepath.Join(dir, "missing-report.log")}); err == nil {
		t.Fatalf("expected error on nonexistent test report file")
	}

	// Verbose subtest failure + failing command-based visible_test -run in JSON report
	verboseFail := filepath.Join(dir, "verbose-fail.log")
	verboseFailBody := `=== RUN   TestAlpha
--- PASS: TestAlpha (0.01s)
PASS
ok  	example.com/demo/internal/alpha	0.012s
=== RUN   TestBeta
    --- FAIL: TestBeta/subcase (0.00s)
--- FAIL: TestBeta (0.01s)
FAIL
FAIL	example.com/demo/internal/beta	0.005s
`
	if err := os.WriteFile(verboseFail, []byte(verboseFailBody), 0o600); err != nil {
		t.Fatalf("write verboseFail: %v", err)
	}
	vFailRep, err := Audit(fm, dir, AuditOptions{TestReportPath: verboseFail})
	if err != nil {
		t.Fatalf("Audit verboseFail unexpected error: %v", err)
	}
	if vFailRep.Valid || vFailRep.BrokenRuntimeCount != 1 {
		t.Fatalf("expected BROKEN_RUNTIME for verbose fail report, got %+v", vFailRep)
	}

	// Root-module package normalization & failing command -run check
	if got := normalizeReportPackage("example.com/demo", "example.com/demo"); got != "." {
		t.Fatalf("normalizeReportPackage root = %q, want \".\"", got)
	}
	if got := normalizeReportPackage("example.com/demo/cmd/g8s", "example.com/demo"); got != "./cmd/g8s" {
		t.Fatalf("normalizeReportPackage sub = %q, want \"./cmd/g8s\"", got)
	}
	jsonFailAlphaRun := filepath.Join(dir, "json-fail-cmd.jsonl")
	jsonFailCmdBody := `{"Action":"fail","Package":"example.com/demo/internal/alpha","Test":"TestAlpha"}
{"Action":"pass","Package":"example.com/demo/internal/alpha"}
{"Action":"pass","Package":"example.com/demo/internal/beta","Test":"TestBeta"}
{"Action":"pass","Package":"example.com/demo/internal/beta"}
`
	if err := os.WriteFile(jsonFailAlphaRun, []byte(jsonFailCmdBody), 0o600); err != nil {
		t.Fatalf("write jsonFailAlphaRun: %v", err)
	}
	cmdFailRep, err := Audit(fm, dir, AuditOptions{FeatureID: "FEAT-A", TestReportPath: jsonFailAlphaRun})
	if err != nil {
		t.Fatalf("Audit cmdFailRep unexpected error: %v", err)
	}
	if cmdFailRep.Valid || cmdFailRep.BrokenRuntimeCount != 1 {
		t.Fatalf("expected FEAT-A BROKEN_RUNTIME on failed command test, got %+v", cmdFailRep)
	}
}
