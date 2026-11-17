package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/featuremap"
)

func loadRegistryFeatures(t *testing.T) *featuremap.FeatureMap {
	t.Helper()
	root := findRepoRoot(t)
	fm, err := featuremap.Load(filepath.Join(root, featuremap.DefaultFeatureMapPath))
	if err != nil {
		t.Fatalf("load feature map: %v", err)
	}
	return fm
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root")
		}
		dir = parent
	}
}

func TestFeatureVerify_Valid(t *testing.T) {
	binPath := buildG8sBinary(t)
	root := findRepoRoot(t)

	cmd := exec.Command(binPath, "feature", "verify", "--file", ".g8s/feature-map.yml", "--repo", ".")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("feature verify failed: %v\nOutput: %s", err, string(out))
	}
	if !strings.Contains(string(out), "is valid") {
		t.Errorf("expected 'is valid' in output, got: %s", string(out))
	}
}

func TestFeatureVerify_JSON(t *testing.T) {
	binPath := buildG8sBinary(t)
	root := findRepoRoot(t)

	cmd := exec.Command(binPath, "feature", "verify", "--file", ".g8s/feature-map.yml", "--repo", ".", "--json")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("feature verify --json failed: %v\nOutput: %s", err, string(out))
	}

	var report featuremap.ValidationReport
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("failed to parse JSON report: %v\nOutput: %s", err, string(out))
	}
	if !report.Valid {
		t.Errorf("expected report.Valid == true, got false. Errors: %v", report.Errors)
	}
	registry := loadRegistryFeatures(t)
	if report.FeatureCount != len(registry.Features) {
		t.Errorf("expected %d features from registry, got %d", len(registry.Features), report.FeatureCount)
	}
	if len(registry.Features) == 0 {
		t.Fatal("registry must contain at least one feature")
	}
}

func TestFeatureVerify_Invalid(t *testing.T) {
	binPath := buildG8sBinary(t)
	root := findRepoRoot(t)
	tmpDir := t.TempDir()
	badFile := filepath.Join(tmpDir, "bad-feature-map.yml")
	badContent := `schema_version: "1.0"
features:
  - id: "BAD-01"
    title: "Bad"
    persona_story: "Story"
    srs_reqs: ["FR-1"]
    code_paths: ["nonexistent/**"]
    invariants: ["Inv"]
    visible_tests: ["nonexistent/test.go"]
    verifier_class: "test"
    blast_radius:
      depends_on: ["MISSING-DEP"]
      direct_consumers: []
`
	if err := os.WriteFile(badFile, []byte(badContent), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binPath, "feature", "verify", "--file", badFile, "--repo", tmpDir, "--json")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected feature verify to fail on invalid map, but succeeded:\nOutput: %s", string(out))
	}

	var report featuremap.ValidationReport
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("failed to parse JSON report: %v\nOutput: %s", err, string(out))
	}
	if report.Valid {
		t.Errorf("expected report.Valid == false, got true")
	}
	if len(report.Errors) == 0 {
		t.Errorf("expected error messages in report")
	}
}

func TestFeatureList_TextAndJSON(t *testing.T) {
	binPath := buildG8sBinary(t)
	root := findRepoRoot(t)

	// Text format
	cmd := exec.Command(binPath, "feature", "list", "--file", ".g8s/feature-map.yml")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("feature list failed: %v\nOutput: %s", err, string(out))
	}
	if !strings.Contains(string(out), "FEAT-CONTROLPLANE-01") {
		t.Errorf("expected FEAT-CONTROLPLANE-01 in list output, got:\n%s", string(out))
	}

	// JSON format
	cmdJSON := exec.Command(binPath, "feature", "list", "--file", ".g8s/feature-map.yml", "--json")
	cmdJSON.Dir = root
	outJSON, err := cmdJSON.CombinedOutput()
	if err != nil {
		t.Fatalf("feature list --json failed: %v\nOutput: %s", err, string(outJSON))
	}

	var features []featuremap.FeatureNode
	if err := json.Unmarshal(outJSON, &features); err != nil {
		t.Fatalf("failed to parse list JSON: %v\nOutput: %s", err, string(outJSON))
	}
	registry := loadRegistryFeatures(t)
	if len(features) != len(registry.Features) {
		t.Errorf("expected %d features from registry, got %d", len(registry.Features), len(features))
	}
}

func TestFeatureInspect_TextAndJSON(t *testing.T) {
	binPath := buildG8sBinary(t)
	root := findRepoRoot(t)

	// Text format
	cmd := exec.Command(binPath, "feature", "inspect", "FEAT-CONTROLPLANE-01", "--file", ".g8s/feature-map.yml")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("feature inspect failed: %v\nOutput: %s", err, string(out))
	}
	if !strings.Contains(string(out), "Feature: FEAT-CONTROLPLANE-01") {
		t.Errorf("expected 'Feature: FEAT-CONTROLPLANE-01' in output, got:\n%s", string(out))
	}

	// JSON format
	cmdJSON := exec.Command(binPath, "feature", "inspect", "FEAT-CONTROLPLANE-01", "--file", ".g8s/feature-map.yml", "--json")
	cmdJSON.Dir = root
	outJSON, err := cmdJSON.CombinedOutput()
	if err != nil {
		t.Fatalf("feature inspect --json failed: %v\nOutput: %s", err, string(outJSON))
	}

	var inspectOut FeatureInspectOutput
	if err := json.Unmarshal(outJSON, &inspectOut); err != nil {
		t.Fatalf("failed to parse inspect JSON: %v\nOutput: %s", err, string(outJSON))
	}
	if inspectOut.Feature == nil || inspectOut.Feature.ID != "FEAT-CONTROLPLANE-01" {
		t.Errorf("expected FEAT-CONTROLPLANE-01 feature, got: %+v", inspectOut.Feature)
	}
	if len(inspectOut.DerivedAllowedPaths) == 0 {
		t.Errorf("expected non-empty DerivedAllowedPaths")
	}
	if len(inspectOut.TransitiveConsumers) == 0 {
		t.Errorf("expected non-empty TransitiveConsumers")
	}

	// Non-existent feature
	cmdMissing := exec.Command(binPath, "feature", "inspect", "NON-EXISTENT", "--file", ".g8s/feature-map.yml", "--json")
	cmdMissing.Dir = root
	outMissing, err := cmdMissing.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit for missing feature, got 0:\n%s", string(outMissing))
	}
}

func TestFeatureAudit_TestReportFlag(t *testing.T) {
	binPath := buildG8sBinary(t)
	tmpDir := t.TempDir()
	pkgDir := filepath.Join(tmpDir, "internal", "demo")
	cmdDir := filepath.Join(tmpDir, "cmd", "g8s")
	if err := os.MkdirAll(pkgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cmdDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module example.com/demo\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "demo.go"), []byte("package demo\nfunc Add(a, b int) int { return a + b }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "demo_test.go"), []byte("package demo\nimport \"testing\"\nfunc TestDemoPass(t *testing.T) {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fmPath := filepath.Join(tmpDir, "feature-map.yml")
	fmYAML := `schema_version: "2.0"
features:
  - id: "FEAT-DEMO-01"
    title: "Demo"
    persona_story: "story"
    srs_reqs: ["FR-1"]
    code_paths: ["internal/demo/**"]
    owned_paths: ["internal/demo/**"]
    invariants: ["inv"]
    visible_tests: ["internal/demo/demo_test.go"]
    verifier_class: "test"
    blast_radius:
      depends_on: []
      direct_consumers: []
`
	if err := os.WriteFile(fmPath, []byte(fmYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	passReportPath := filepath.Join(tmpDir, "pass.log")
	passLog := `=== RUN   TestDemoPass
--- PASS: TestDemoPass (0.00s)
PASS
ok  	example.com/demo/internal/demo	0.005s
`
	if err := os.WriteFile(passReportPath, []byte(passLog), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binPath, "feature", "audit", "--file", fmPath, "--repo", tmpDir, "--test-report", passReportPath, "--json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("feature audit --test-report failed: %v\n%s", err, string(out))
	}
	var env struct {
		Data featuremap.AuditReport `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, string(out))
	}
	if !env.Data.Valid || env.Data.RuntimeProvenCount != 1 || env.Data.StaticOnlyCount != 0 {
		t.Fatalf("unexpected audit report: %+v", env.Data)
	}

	failReportPath := filepath.Join(tmpDir, "fail.log")
	failLog := `=== RUN   TestDemoPass
--- FAIL: TestDemoPass (0.00s)
FAIL
FAIL	example.com/demo/internal/demo	0.005s
`
	if err := os.WriteFile(failReportPath, []byte(failLog), 0o600); err != nil {
		t.Fatal(err)
	}
	failCmd := exec.Command(binPath, "feature", "audit", "--file", fmPath, "--repo", tmpDir, "--test-report", failReportPath, "--json")
	failOut, failErr := failCmd.CombinedOutput()
	if failErr == nil {
		t.Fatalf("expected non-zero exit for failing test report, got 0:\n%s", string(failOut))
	}
	var failEnv struct {
		Data featuremap.AuditReport `json:"data"`
	}
	if err := json.Unmarshal(failOut, &failEnv); err != nil {
		t.Fatalf("decode fail envelope: %v\n%s", err, string(failOut))
	}
	if failEnv.Data.Valid || failEnv.Data.BrokenRuntimeCount != 1 {
		t.Fatalf("expected BROKEN_RUNTIME report, got %+v", failEnv.Data)
	}
}
