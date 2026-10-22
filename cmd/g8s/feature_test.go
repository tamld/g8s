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
	if report.FeatureCount != 10 {
		t.Errorf("expected 10 features, got %d", report.FeatureCount)
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
	if len(features) != 10 {
		t.Errorf("expected 10 features, got %d", len(features))
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
