package featuremap

import (
	"os"
	"path/filepath"
	"testing"
)

func sampleValidYAML() string {
	return `schema_version: "1.0"
features:
  - id: "FEAT-A"
    title: "Feature A"
    persona_story: "As a user, I want A"
    srs_reqs: ["FR-1"]
    openspec_delta: "DELTA-01"
    code_paths: ["internal/a/**"]
    invariants: ["Invariant A"]
    visible_tests: ["internal/a/a_test.go", "go test ./internal/a/..."]
    verifier_class: "test"
    blast_radius:
      depends_on: []
      direct_consumers: ["FEAT-B"]
  - id: "FEAT-B"
    title: "Feature B"
    persona_story: "As a user, I want B"
    srs_reqs: ["FR-2"]
    openspec_delta: "DELTA-02"
    code_paths: ["internal/b/**"]
    invariants: ["Invariant B"]
    visible_tests: ["internal/b/b_test.go"]
    holdout_tests: ["internal/b/holdout_test.go"]
    verifier_class: "test"
    blast_radius:
      depends_on: ["FEAT-A"]
      direct_consumers: ["FEAT-C"]
  - id: "FEAT-C"
    title: "Feature C"
    persona_story: "As a user, I want C"
    srs_reqs: ["FR-3"]
    code_paths: ["internal/c/**"]
    invariants: ["Invariant C"]
    visible_tests: ["internal/c/c_test.go"]
    verifier_class: "test"
    blast_radius:
      depends_on: ["FEAT-B"]
      direct_consumers: []
`
}

func TestFeature_LoadAndParse(t *testing.T) {
	fm, err := Parse([]byte(sampleValidYAML()))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if fm.SchemaVersion != "1.0" {
		t.Errorf("expected schema_version 1.0, got %s", fm.SchemaVersion)
	}
	if len(fm.Features) != 3 {
		t.Fatalf("expected 3 features, got %d", len(fm.Features))
	}

	// Invalid YAML
	_, err = Parse([]byte("features: [unclosed"))
	if err == nil {
		t.Error("expected error parsing invalid YAML, got nil")
	}

	// Test Load from temp file
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "feature-map.yml")
	if err := os.WriteFile(path, []byte(sampleValidYAML()), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}
	if len(loaded.Features) != 3 {
		t.Errorf("expected 3 features, got %d", len(loaded.Features))
	}

	// Non-existent file
	_, err = Load(filepath.Join(tmpDir, "missing.yml"))
	if err == nil {
		t.Error("expected error loading missing file, got nil")
	}
}

func TestFeature_GetFeature(t *testing.T) {
	fm, err := Parse([]byte(sampleValidYAML()))
	if err != nil {
		t.Fatal(err)
	}

	node, ok := fm.GetFeature("FEAT-B")
	if !ok || node == nil {
		t.Fatal("expected to find FEAT-B")
	}
	if node.Title != "Feature B" {
		t.Errorf("expected title 'Feature B', got %q", node.Title)
	}

	_, ok = fm.GetFeature("NON-EXISTENT")
	if ok {
		t.Error("expected not found for NON-EXISTENT")
	}

	var nilFM *FeatureMap
	_, ok = nilFM.GetFeature("FEAT-A")
	if ok {
		t.Error("expected nilFM.GetFeature to return false")
	}
}

func TestFeature_DeriveAllowedPaths(t *testing.T) {
	fm, err := Parse([]byte(sampleValidYAML()))
	if err != nil {
		t.Fatal(err)
	}

	// FEAT-A has code_paths ["internal/a/**"] and visible_tests ["internal/a/a_test.go", "go test ./internal/a/..."]
	paths, err := fm.DeriveAllowedPaths("FEAT-A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"internal/a/**", "internal/a/a_test.go"}
	if len(paths) != len(expected) {
		t.Fatalf("expected %d paths, got %v", len(expected), paths)
	}
	for i, p := range expected {
		if paths[i] != p {
			t.Errorf("at %d: expected %q, got %q", i, p, paths[i])
		}
	}

	// FEAT-B has holdout_tests ["internal/b/holdout_test.go"]
	pathsB, err := fm.DeriveAllowedPaths("FEAT-B")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, p := range pathsB {
		if p == "internal/b/holdout_test.go" {
			t.Errorf("holdout test should have been excluded from allowed paths")
		}
	}

	// Unknown feature
	_, err = fm.DeriveAllowedPaths("UNKNOWN")
	if err == nil {
		t.Error("expected error for unknown feature")
	}
}

func TestFeature_ResolveTransitiveFeatures(t *testing.T) {
	fm, err := Parse([]byte(sampleValidYAML()))
	if err != nil {
		t.Fatal(err)
	}

	nodes, err := fm.ResolveTransitiveFeatures("FEAT-A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("expected 3 transitive nodes (A -> B -> C), got %d", len(nodes))
	}
	if nodes[0].ID != "FEAT-A" || nodes[1].ID != "FEAT-B" || nodes[2].ID != "FEAT-C" {
		t.Errorf("unexpected transitive order: %+v", nodes)
	}

	nodesC, err := fm.ResolveTransitiveFeatures("FEAT-C")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodesC) != 1 || nodesC[0].ID != "FEAT-C" {
		t.Errorf("expected only FEAT-C, got %+v", nodesC)
	}

	_, err = fm.ResolveTransitiveFeatures("UNKNOWN")
	if err == nil {
		t.Error("expected error for unknown feature")
	}
}

func TestFeature_Validate_NilAndBasic(t *testing.T) {
	rep := Validate(nil, "")
	if rep.Valid || len(rep.Errors) == 0 {
		t.Fatal("expected invalid report for nil feature map")
	}

	emptyFM := &FeatureMap{
		SchemaVersion: "2.0",
		Features:      []FeatureNode{},
	}
	rep = Validate(emptyFM, "")
	if rep.Valid {
		t.Fatal("expected invalid report for wrong version and empty features")
	}
}

func TestFeature_Validate_MissingFieldsAndCycles(t *testing.T) {
	badYAML := `schema_version: "1.0"
features:
  - id: "F1"
    title: ""
    persona_story: ""
    srs_reqs: []
    code_paths: []
    invariants: ["Inv 1"]
    visible_tests: []
    verifier_class: ""
    blast_radius:
      depends_on: ["F2"]
      direct_consumers: ["F2"]
  - id: "F2"
    title: "Feature 2"
    persona_story: "Story 2"
    srs_reqs: ["FR-2"]
    code_paths: ["internal/f2/**"]
    invariants: ["Inv 2"]
    visible_tests: ["internal/f2/f2_test.go"]
    verifier_class: "test"
    blast_radius:
      depends_on: ["F1"]
      direct_consumers: ["F1"]
`
	fm, err := Parse([]byte(badYAML))
	if err != nil {
		t.Fatal(err)
	}

	rep := Validate(fm, "")
	if rep.Valid {
		t.Fatal("expected invalid report")
	}

	// Should contain unverified invariants for F1
	if len(rep.UnverifiedInvariants) == 0 {
		t.Error("expected UnverifiedInvariants for F1")
	}

	// Should detect cycles in depends_on and direct_consumers
	hasDepCycle := false
	hasConsumerCycle := false
	for _, errStr := range rep.Errors {
		if errStr == "cycle detected in depends_on: F1 -> F2 -> F1" || errStr == "cycle detected in depends_on: F2 -> F1 -> F2" {
			hasDepCycle = true
		}
		if errStr == "cycle detected in direct_consumers: F1 -> F2 -> F1" || errStr == "cycle detected in direct_consumers: F2 -> F1 -> F2" {
			hasConsumerCycle = true
		}
	}
	if !hasDepCycle {
		t.Errorf("expected cycle detection in depends_on, got errors: %v", rep.Errors)
	}
	if !hasConsumerCycle {
		t.Errorf("expected cycle detection in direct_consumers, got errors: %v", rep.Errors)
	}
}

func TestFeature_Validate_WithRepoRoot(t *testing.T) {
	tmpDir := t.TempDir()

	// Setup mock repo
	if err := os.MkdirAll(filepath.Join(tmpDir, "internal", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "internal", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "internal", "c"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "docs", "SRS.md"), []byte("# SRS\n* FR-1\n* FR-2\n* FR-3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "manifest.json"), []byte(`{"deltas": ["DELTA-01", "DELTA-02"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	fm, err := Parse([]byte(sampleValidYAML()))
	if err != nil {
		t.Fatal(err)
	}

	rep := Validate(fm, tmpDir)
	if !rep.Valid {
		t.Fatalf("expected valid report, got errors: %v, orphans: %v", rep.Errors, rep.OrphanPaths)
	}

	// Add an unmapped package under internal
	if err := os.MkdirAll(filepath.Join(tmpDir, "internal", "orphan_pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	repOrphan := Validate(fm, tmpDir)
	if repOrphan.Valid {
		t.Fatal("expected invalid report due to orphan package")
	}
	if len(repOrphan.OrphanPaths) != 1 || repOrphan.OrphanPaths[0] != "internal/orphan_pkg" {
		t.Errorf("expected orphan internal/orphan_pkg, got %v", repOrphan.OrphanPaths)
	}
}

func TestFeature_Validate_BidirectionalEdgeSymmetry(t *testing.T) {
	asymYAML := `schema_version: "1.0"
features:
  - id: "FEAT-BASE"
    title: "Base Feature"
    persona_story: "As a user, I want Base"
    srs_reqs: ["FR-1"]
    code_paths: ["internal/base/**"]
    invariants: ["Inv Base"]
    visible_tests: ["internal/base/base_test.go"]
    verifier_class: "test"
    blast_radius:
      depends_on: []
      direct_consumers: []
  - id: "FEAT-LEAF"
    title: "Leaf Feature"
    persona_story: "As a user, I want Leaf"
    srs_reqs: ["FR-2"]
    code_paths: ["internal/leaf/**"]
    invariants: ["Inv Leaf"]
    visible_tests: ["internal/leaf/leaf_test.go"]
    verifier_class: "test"
    blast_radius:
      depends_on: ["FEAT-BASE"]
      direct_consumers: []
`
	fm, err := Parse([]byte(asymYAML))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	rep := Validate(fm, "")
	if rep.Valid {
		t.Fatal("expected asymmetric blast_radius edge to fail validation")
	}
	foundAsym := false
	for _, e := range rep.Errors {
		if e == "asymmetric blast_radius edge: FEAT-LEAF depends_on FEAT-BASE, but FEAT-BASE.direct_consumers omits FEAT-LEAF" {
			foundAsym = true
			break
		}
	}
	if !foundAsym {
		t.Fatalf("expected asymmetric edge error, got: %v", rep.Errors)
	}
}
