package featuremap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobPrimitives(t *testing.T) {
	if got := NormalizePattern("./internal//worker/"); got != "internal/worker/**" {
		t.Fatalf("NormalizePattern = %q, want internal/worker/**", got)
	}
	if !MatchPath("internal/**/foo_*.go", "internal/a/b/foo_test.go") {
		t.Fatal("expected MatchPath true for ** wildcard")
	}
	if MatchPath("internal/worker/*.go", "internal/worker/sub/a.go") {
		t.Fatal("expected MatchPath false across subdirectories without **")
	}
	if !MatchPath("cmd/g8s/[a-z]?\\*.go", "cmd/g8s/ab*.go") {
		t.Fatal("expected MatchPath true for class, ?, and escaped *")
	}

	if !GlobsOverlap("internal/worker/**", "internal/**/runner.go") {
		t.Fatal("expected GlobsOverlap true")
	}
	if GlobsOverlap("internal/worker/**", "internal/store/**") {
		t.Fatal("expected GlobsOverlap false for disjoint packages")
	}
	if !GlobsOverlap("cmd/g8s/[a-c]*.go", "cmd/g8s/b_test.go") {
		t.Fatal("expected GlobsOverlap true for character class vs literal")
	}
	if !GlobsOverlap("cmd/g8s/[a-c]*.go", "cmd/g8s/[b-d]*.go") {
		t.Fatal("expected GlobsOverlap true for class vs class")
	}
	if !GlobsOverlap("cmd/g8s/?.go", "cmd/g8s/a.go") {
		t.Fatal("expected GlobsOverlap true for ? vs literal")
	}

	if !GlobCovers("internal/worker/**", "internal/worker/runner.go") {
		t.Fatal("expected GlobCovers true")
	}
	if GlobCovers("internal/worker/*.go", "internal/worker/**") {
		t.Fatal("expected GlobCovers false when inner has **")
	}

	ps := NewPathSet([]string{"cmd/g8s/**", "!cmd/g8s/main.go", ""})
	if ps.Empty() {
		t.Fatal("expected non-empty PathSet")
	}
	if !ps.Match("cmd/g8s/feature.go") {
		t.Fatal("expected feature.go to match PathSet")
	}
	if ps.Match("cmd/g8s/main.go") {
		t.Fatal("expected excluded main.go not to match PathSet")
	}
	if ps.CoversPattern("cmd/g8s/main.go") {
		t.Fatal("expected CoversPattern false for excluded file")
	}
	if !ps.CoversPattern("cmd/g8s/feature.go") {
		t.Fatal("expected CoversPattern true for included file")
	}
	if _, ok := ps.OverlapsPattern("cmd/g8s/main.go"); ok {
		t.Fatal("expected OverlapsPattern false for excluded file")
	}
	if _, ok := ps.OverlapsPattern("cmd/g8s/sub/**"); !ok {
		t.Fatal("expected OverlapsPattern true for included subpath")
	}
}

func TestValidateV2ContractAndErrors(t *testing.T) {
	dir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(dir, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustMkdir("internal/alpha")
	mustMkdir("internal/beta")
	mustWrite("internal/alpha/a_test.go", "package alpha\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n")
	mustWrite("internal/beta/b_test.go", "package beta\nimport \"testing\"\nfunc TestB(t *testing.T) {}\n")
	mustWrite("cmd/g8s/main.go", "package main\n")
	mustWrite("orphan.txt", "orphan\n")

	fm := &FeatureMap{
		SchemaVersion: SchemaV2,
		Hotspots: []Hotspot{
			{ID: "HOT-MAIN", Paths: []string{"cmd/g8s/main.go"}, Policy: PolicySerialize},
			{ID: "HOT-SPEC", Paths: []string{"spec/**"}, Policy: PolicyAppendOnly},
		},
		Features: []FeatureNode{
			{
				ID:              "FEAT-ALPHA-01",
				Title:           "Alpha",
				PersonaStory:    "As an operator I want alpha",
				SRSReqs:         []string{"REQ-A-1"},
				CodePaths:       []string{"internal/alpha/a_test.go"},
				OwnedPaths:      []string{"internal/alpha/**"},
				SharedPaths:     []string{"cmd/g8s/main.go", "spec/**"},
				CollisionDomain: "dom-alpha",
				Interfaces: &Interfaces{
					CLI:           []string{"alpha"},
					MCPTools:      []string{"g8s_alpha"},
					EnvelopeKinds: []string{"alpha_result"},
					GoPackages:    []string{"internal/alpha"},
				},
				Invariants:    []string{"inv-a"},
				VisibleTests:  []string{"internal/alpha/a_test.go::TestA"},
				VerifierClass: "VC-UNIT",
			},
			{
				ID:              "FEAT-BETA-01",
				Title:           "Beta",
				PersonaStory:    "As an operator I want beta",
				SRSReqs:         []string{"REQ-B-1"},
				CodePaths:       []string{"internal/beta/b_test.go"},
				OwnedPaths:      []string{"internal/beta/**"},
				SharedPaths:     []string{"cmd/g8s/main.go", "spec/**"},
				CollisionDomain: "dom-beta",
				Interfaces: &Interfaces{
					CLI:           []string{"beta"},
					MCPTools:      []string{"g8s_beta"},
					EnvelopeKinds: []string{"beta_result"},
					GoPackages:    []string{"internal/beta"},
				},
				Invariants:    []string{"inv-b"},
				VisibleTests:  []string{"internal/beta/b_test.go::TestB"},
				VerifierClass: "VC-UNIT",
			},
		},
	}

	// Non-strict: orphan.txt is a warning, report is valid.
	rep := ValidateWithOptions(fm, dir, ValidateOptions{
		Strict:       false,
		TrackedFiles: []string{"internal/alpha/a_test.go", "internal/beta/b_test.go", "cmd/g8s/main.go", "orphan.txt"},
	})
	if !rep.Valid {
		t.Fatalf("expected Valid=true in non-strict mode, errors: %v", rep.Errors)
	}
	if rep.Coverage == nil || len(rep.Coverage.OrphanFiles) != 1 || rep.Coverage.OrphanFiles[0] != "orphan.txt" {
		t.Fatalf("unexpected coverage: %+v", rep.Coverage)
	}

	// Strict: orphan.txt triggers error.
	strictRep := ValidateWithOptions(fm, dir, ValidateOptions{
		Strict:       true,
		TrackedFiles: []string{"internal/alpha/a_test.go", "internal/beta/b_test.go", "cmd/g8s/main.go", "orphan.txt"},
	})
	if strictRep.Valid {
		t.Fatal("expected Valid=false in strict mode with orphan.txt")
	}

	// Filesystem fallback in ListTrackedFiles (non-git temp dir).
	walked, err := ListTrackedFiles(dir)
	if err != nil || len(walked) == 0 {
		t.Fatalf("ListTrackedFiles fallback failed: err=%v len=%d", err, len(walked))
	}

	// Error cases: overlapping owned_paths, duplicate interfaces, bad hotspot policy, unmapped shared_path.
	badFM := &FeatureMap{
		SchemaVersion: SchemaV2,
		Hotspots: []Hotspot{
			{ID: "HOT-1", Paths: []string{"cmd/g8s/main.go"}, Policy: "owner:UNKNOWN-FEAT"},
			{ID: "HOT-1", Paths: []string{"cmd/g8s/**"}, Policy: "bad-policy"},
		},
		Features: []FeatureNode{
			{
				ID:          "FEAT-1",
				Title:       "F1",
				OwnedPaths:  []string{"internal/alpha/**", "../escape"},
				SharedPaths: []string{"unregistered/hotspot.go"},
				Interfaces: &Interfaces{
					CLI:        []string{"dup-cli"},
					GoPackages: []string{"internal/nonexistent"},
				},
			},
			{
				ID:         "FEAT-2",
				Title:      "F2",
				OwnedPaths: []string{"internal/alpha/sub/**"},
				Interfaces: &Interfaces{
					CLI: []string{"dup-cli"},
				},
			},
		},
	}
	badRep := ValidateWithOptions(badFM, dir, ValidateOptions{Strict: true, TrackedFiles: []string{}})
	if badRep.Valid {
		t.Fatal("expected badFM to fail validation")
	}
	joined := strings.Join(badRep.Errors, "\n")
	for _, substr := range []string{
		"duplicate hotspot id",
		"hotspot overlap",
		"references unknown feature",
		"invalid policy",
		"escapes repo root",
		"shared_path \"unregistered/hotspot.go\" does not reference a declared hotspot",
		"interface collision",
		"owned_paths overlap",
	} {
		if !strings.Contains(joined, substr) {
			t.Errorf("expected error containing %q, got:\n%s", substr, joined)
		}
	}
}

func TestCompareScopesAndPolicies(t *testing.T) {
	fm := &FeatureMap{
		SchemaVersion: SchemaV2,
		Hotspots: []Hotspot{
			{ID: "HOT-MAIN", Paths: []string{"cmd/g8s/main.go"}, Policy: PolicySerialize},
			{ID: "HOT-APPEND", Paths: []string{"spec/openspec/README.md"}, Policy: PolicyAppendOnly},
			{ID: "HOT-OWNED", Paths: []string{"internal/shared/reg.go"}, Policy: "owner:FEAT-A"},
		},
		Features: []FeatureNode{
			{
				ID:              "FEAT-A",
				Title:           "A",
				OwnedPaths:      []string{"internal/a/**"},
				CollisionDomain: "dom-a",
			},
			{
				ID:              "FEAT-B",
				Title:           "B",
				OwnedPaths:      []string{"internal/b/**"},
				CollisionDomain: "dom-b",
			},
			{
				ID:              "FEAT-A2",
				Title:           "A2",
				OwnedPaths:      []string{"internal/a2/**"},
				CollisionDomain: "dom-a",
			},
		},
	}

	// 1. Disjoint + append-only hotspot -> parallel-safe.
	rep1, err := CompareScopes(fm, "w1", ParseScope("internal/a/foo.go, spec/openspec/README.md"), "w2", ParseScope("internal/b/bar.go, spec/openspec/README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if rep1.Recommendation != RecommendParallelSafe {
		t.Fatalf("expected parallel-safe, got %s (%v)", rep1.Recommendation, rep1.Reasons)
	}

	// 2. Disjoint features touching serialize hotspot -> split.
	rep2, err := CompareScopes(fm, "w1", []string{"internal/a/foo.go", "cmd/g8s/main.go"}, "w2", []string{"internal/b/bar.go", "cmd/g8s/main.go"})
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Recommendation != RecommendSplit {
		t.Fatalf("expected split, got %s", rep2.Recommendation)
	}

	// 3. Owner-governed hotspot -> split.
	rep3, err := CompareScopes(fm, "w1", []string{"internal/a/foo.go", "internal/shared/reg.go"}, "w2", []string{"internal/b/bar.go", "internal/shared/reg.go"})
	if err != nil {
		t.Fatal(err)
	}
	if rep3.Recommendation != RecommendSplit {
		t.Fatalf("expected split for owner hotspot, got %s", rep3.Recommendation)
	}

	// 4. Same collision domain -> serialize.
	rep4, err := CompareScopes(fm, "w1", []string{"internal/a/foo.go"}, "w2", []string{"internal/a2/bar.go"})
	if err != nil {
		t.Fatal(err)
	}
	if rep4.Recommendation != RecommendSerialize {
		t.Fatalf("expected serialize for shared collision_domain, got %s", rep4.Recommendation)
	}

	// 5. Empty scope error.
	if _, err := CompareScopes(fm, "w1", nil, "w2", []string{"internal/b/bar.go"}); err == nil {
		t.Fatal("expected error for empty scope")
	}
}
