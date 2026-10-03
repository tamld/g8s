package lane_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/tamld/g8s/internal/lane"
)

// TestPlan36Contract_TableDriven tests the exact contract defined in plans/260927-s6-gate-lanes/plan.md:36:
// docs->D, feat->F, trust-path->S, mixed docs+code->F, unknown->F, empty->F.
func TestPlan36Contract_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		input    lane.Input
		wantLane lane.Lane
	}{
		{
			name: "docs->D (markdown files)",
			input: lane.Input{
				Paths:   []string{"README.md", "docs/architecture.md"},
				Summary: "docs: update system architecture overview",
			},
			wantLane: lane.LaneDocs,
		},
		{
			name: "docs->D (docs directory non-md assets)",
			input: lane.Input{
				Paths:   []string{"docs/diagram.png", "docs/spec.txt"},
				Summary: "add diagram asset to docs",
			},
			wantLane: lane.LaneDocs,
		},
		{
			name: "feat->F (standard feature change)",
			input: lane.Input{
				Paths:   []string{"internal/analyzer/analyzer.go", "internal/analyzer/types.go"},
				Summary: "feat(analyzer): add cyclomatic complexity metric",
			},
			wantLane: lane.LaneFeature,
		},
		{
			name: "trust-path->S (touching receipt surface)",
			input: lane.Input{
				Paths:   []string{"internal/receipt/receipt.go"},
				Summary: "feat: update receipt emission schema",
			},
			wantLane: lane.LaneSecurity,
		},
		{
			name: "trust-path->S (touching worker process isolation)",
			input: lane.Input{
				Paths:   []string{"internal/worker/proc_linux.go"},
				Summary: "feat: tune process group isolation signals",
			},
			wantLane: lane.LaneSecurity,
		},
		{
			name: "trust-path->S (touching server auth/listen surface)",
			input: lane.Input{
				Paths:   []string{"internal/server/server.go"},
				Summary: "feat: add api token header validation",
			},
			wantLane: lane.LaneSecurity,
		},
		{
			name: "trust-path->S (touching harness containment)",
			input: lane.Input{
				Paths:   []string{"internal/harness/probe/selfaudit.go"},
				Summary: "feat: tighten harness probe boundaries",
			},
			wantLane: lane.LaneSecurity,
		},
		{
			name: "trust-path->S (touching memory poison surface)",
			input: lane.Input{
				Paths:   []string{"internal/memory/hybrid.go"},
				Summary: "feat: sanitize vector embedding memory promotions",
			},
			wantLane: lane.LaneSecurity,
		},
		{
			name: "trust-path->S (touching dispatch sanitizer)",
			input: lane.Input{
				Paths:   []string{"internal/dispatch/dispatch.go"},
				Summary: "feat: add additional literal sanitization rules",
			},
			wantLane: lane.LaneSecurity,
		},
		{
			name: "mixed docs+code->F (must pay full feature gate bundle)",
			input: lane.Input{
				Paths:   []string{"docs/overview.md", "internal/analyzer/analyzer.go"},
				Summary: "feat: add analyzer with documentation",
			},
			wantLane: lane.LaneFeature,
		},
		{
			name: "unknown->F (unrecognized path and generic summary)",
			input: lane.Input{
				Paths:   []string{"scripts/custom-tool.py"},
				Summary: "update helper script",
			},
			wantLane: lane.LaneFeature,
		},
		{
			name: "empty->F (empty paths slice)",
			input: lane.Input{
				Paths:   []string{},
				Summary: "empty changeset",
			},
			wantLane: lane.LaneFeature,
		},
		{
			name: "empty->F (nil paths slice)",
			input: lane.Input{
				Paths:   nil,
				Summary: "",
			},
			wantLane: lane.LaneFeature,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := lane.Route(tc.input)
			if got != tc.wantLane {
				t.Errorf("lane.Route(%+v) = %q (%s); want %q (%s)",
					tc.input, got, got.Name(), tc.wantLane, tc.wantLane.Name())
			}
		})
	}
}

// TestHotfixRouting tests explicit hotfix markers routing to Lane 0.
func TestHotfixRouting(t *testing.T) {
	tests := []struct {
		name     string
		input    lane.Input
		wantLane lane.Lane
	}{
		{
			name: "hotfix: prefix in summary",
			input: lane.Input{
				Paths:   []string{"internal/analyzer/analyzer.go"},
				Summary: "hotfix: resolve nil pointer dereference on startup",
			},
			wantLane: lane.LaneHotfix,
		},
		{
			name: "[hotfix] tag in summary",
			input: lane.Input{
				Paths:   []string{"internal/analyzer/analyzer.go"},
				Summary: "[hotfix] emergency lock contention fix",
			},
			wantLane: lane.LaneHotfix,
		},
		{
			name: "HOTFIX uppercase prefix",
			input: lane.Input{
				Paths:   []string{"internal/analyzer/analyzer.go"},
				Summary: "HOTFIX: prevent loop panic",
			},
			wantLane: lane.LaneHotfix,
		},
		{
			name: "hotfix/ branch style prefix",
			input: lane.Input{
				Paths:   []string{"internal/analyzer/analyzer.go"},
				Summary: "hotfix/fix-deadlock",
			},
			wantLane: lane.LaneHotfix,
		},
		{
			name: "standard fix: routes to Lane F (not hotfix)",
			input: lane.Input{
				Paths:   []string{"internal/analyzer/analyzer.go"},
				Summary: "fix: correct typo in variable name",
			},
			wantLane: lane.LaneFeature,
		},
		{
			name: "hotfix touching trust boundary MUST route to Lane S (safety floor)",
			input: lane.Input{
				Paths:   []string{"internal/receipt/receipt.go"},
				Summary: "hotfix: critical receipt verification patch",
			},
			wantLane: lane.LaneSecurity,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := lane.Route(tc.input)
			if got != tc.wantLane {
				t.Errorf("lane.Route(%+v) = %q; want %q", tc.input, got, tc.wantLane)
			}
		})
	}
}

// TestRefactorRouting tests pure refactor declarations routing to Lane R.
func TestRefactorRouting(t *testing.T) {
	tests := []struct {
		name     string
		input    lane.Input
		wantLane lane.Lane
	}{
		{
			name: "refactor: prefix in summary",
			input: lane.Input{
				Paths:   []string{"internal/analyzer/analyzer.go"},
				Summary: "refactor: extract helper function for score calculation",
			},
			wantLane: lane.LaneRefactor,
		},
		{
			name: "refactor(scope): conventional commit in summary",
			input: lane.Input{
				Paths:   []string{"internal/analyzer/analyzer.go"},
				Summary: "refactor(analyzer): decompose complex loop into submethods",
			},
			wantLane: lane.LaneRefactor,
		},
		{
			name: "[refactor] bracketed prefix",
			input: lane.Input{
				Paths:   []string{"internal/analyzer/analyzer.go"},
				Summary: "[refactor] clean up unused variables",
			},
			wantLane: lane.LaneRefactor,
		},
		{
			name: "refactor path declaration",
			input: lane.Input{
				Paths:   []string{"refactor/legacy_cleanup.go"},
				Summary: "mechanical type migration",
			},
			wantLane: lane.LaneRefactor,
		},
		{
			name: "docs only with refactor summary routes to Lane D (docs gate bundle)",
			input: lane.Input{
				Paths:   []string{"docs/index.md", "README.md"},
				Summary: "refactor: restructure documentation table of contents",
			},
			wantLane: lane.LaneDocs,
		},
		{
			name: "refactor touching trust boundary MUST route to Lane S (safety floor)",
			input: lane.Input{
				Paths:   []string{"internal/server/server.go"},
				Summary: "refactor: extract route registration helpers",
			},
			wantLane: lane.LaneSecurity,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := lane.Route(tc.input)
			if got != tc.wantLane {
				t.Errorf("lane.Route(%+v) = %q; want %q", tc.input, got, tc.wantLane)
			}
		})
	}
}

// TestDenyByDefault_BrokenBundles tests requirement 4:
// unparseable bundles / missing file => all lanes fall back to F EXCEPT trust-paths still route S.
func TestDenyByDefault_BrokenBundles(t *testing.T) {
	corruptedYAML := []byte("lanes: [corrupted syntax ::: broken")

	routerCorrupt := lane.NewRouter(
		lane.WithBundlesData(corruptedYAML),
		lane.WithTrustBoundaries([]string{"internal/receipt/*"}),
	)

	routerMissing := lane.NewRouter(
		lane.WithBundlesPath("/nonexistent/path/bundles.yml"),
		lane.WithTrustBoundaries([]string{"internal/receipt/*"}),
	)

	cases := []struct {
		name     string
		input    lane.Input
		wantLane lane.Lane
	}{
		{
			name: "docs change falls back to F under broken bundles",
			input: lane.Input{
				Paths:   []string{"docs/readme.md"},
				Summary: "docs: update guide",
			},
			wantLane: lane.LaneFeature,
		},
		{
			name: "hotfix change falls back to F under broken bundles",
			input: lane.Input{
				Paths:   []string{"internal/worker/worker.go"},
				Summary: "hotfix: resolve emergency panic",
			},
			wantLane: lane.LaneFeature,
		},
		{
			name: "refactor change falls back to F under broken bundles",
			input: lane.Input{
				Paths:   []string{"internal/worker/worker.go"},
				Summary: "refactor: clean up loop",
			},
			wantLane: lane.LaneFeature,
		},
		{
			name: "regular feature change falls back to F under broken bundles",
			input: lane.Input{
				Paths:   []string{"internal/worker/worker.go"},
				Summary: "feat: add new worker queue",
			},
			wantLane: lane.LaneFeature,
		},
		{
			name: "SAFETY FLOOR: trust-boundary path STILL routes to S under broken bundles",
			input: lane.Input{
				Paths:   []string{"internal/receipt/store.go"},
				Summary: "chore: tweak receipt store internal",
			},
			wantLane: lane.LaneSecurity,
		},
		{
			name: "SAFETY FLOOR: trust-boundary path with hotfix STILL routes to S under broken bundles",
			input: lane.Input{
				Paths:   []string{"internal/receipt/store.go"},
				Summary: "hotfix: emergency receipt patch",
			},
			wantLane: lane.LaneSecurity,
		},
	}

	for _, r := range []*lane.Router{routerCorrupt, routerMissing} {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got := r.Route(tc.input)
				if got != tc.wantLane {
					t.Errorf("broken bundles Route(%+v) = %q; want %q", tc.input, got, tc.wantLane)
				}
			})
		}
	}
}

// TestLaneS_Invariants tests the strict security invariants:
// 1. Lane S from a suggestion is ALWAYS refused (design :43).
// 2. Machine-matched P0 (Lane S) is NEVER downgraded by any suggestion (ADR-0024 Layer 3).
func TestLaneS_Invariants(t *testing.T) {
	r := lane.NewRouter()

	t.Run("Suggestion of Lane S is ALWAYS refused", func(t *testing.T) {
		inputs := []lane.Input{
			{Paths: []string{"README.md"}, Summary: "docs: update"},
			{Paths: []string{"internal/analyzer/analyzer.go"}, Summary: "feat: add metric"},
			{Paths: []string{"internal/analyzer/analyzer.go"}, Summary: "hotfix: fix bug"},
			{Paths: []string{"internal/analyzer/analyzer.go"}, Summary: "refactor: clean up"},
		}

		sources := []string{"jev", "llm-assist", "supervisor", "test-agent"}

		for _, input := range inputs {
			for _, src := range sources {
				resLane, accepted := r.Suggest(input, lane.LaneSecurity, src)
				if accepted {
					t.Fatalf("INVARIANT VIOLATION: Suggest accepted Lane S for input %+v from source %q", input, src)
				}
				deterministic := r.Route(input)
				if resLane != deterministic {
					t.Fatalf("Expected fallback to deterministic route %q, got %q", deterministic, resLane)
				}
			}
		}
	})

	t.Run("Trust boundary (Lane S) CANNOT be downgraded by any suggestion", func(t *testing.T) {
		trustInput := lane.Input{
			Paths:   []string{"internal/receipt/receipt.go"},
			Summary: "refactor: simplify receipt hashing",
		}

		suggestions := []lane.Lane{
			lane.LaneHotfix,
			lane.LaneDocs,
			lane.LaneRefactor,
			lane.LaneFeature,
			lane.Lane("custom"),
		}

		for _, sugg := range suggestions {
			resLane, accepted := r.Suggest(trustInput, sugg, "jev")
			if accepted {
				t.Fatalf("INVARIANT VIOLATION: Suggest downgraded trust-boundary path to %q", sugg)
			}
			if resLane != lane.LaneSecurity {
				t.Fatalf("INVARIANT VIOLATION: resulting lane for trust boundary was %q, must remain Lane S", resLane)
			}
		}
	})
}

// TestJevAssistedAmbiguityHook tests Layer 2 suggestion behavior:
// Suggest(input, suggestion, source) (Lane, bool).
func TestJevAssistedAmbiguityHook(t *testing.T) {
	r := lane.NewRouter()

	t.Run("Accepted suggestion for ambiguous code change", func(t *testing.T) {
		// An ambiguous commit summary without explicit "refactor:" prefix routes to F deterministically.
		input := lane.Input{
			Paths:   []string{"internal/worker/pool.go"},
			Summary: "simplify loop implementation and reduce allocations",
		}

		// Deterministic should be F
		if det := r.Route(input); det != lane.LaneFeature {
			t.Fatalf("Expected deterministic route LaneFeature, got %q", det)
		}

		// Jev with AST context suggests LaneRefactor
		resLane, accepted := r.Suggest(input, lane.LaneRefactor, "jev")
		if !accepted {
			t.Fatalf("Expected Suggest to accept LaneRefactor for pure code change")
		}
		if resLane != lane.LaneRefactor {
			t.Fatalf("Expected LaneRefactor, got %q", resLane)
		}
	})

	t.Run("Refused suggestion of LaneDocs for code changes", func(t *testing.T) {
		// A suggestion of LaneDocs when code files are touched must be refused.
		input := lane.Input{
			Paths:   []string{"internal/worker/pool.go"},
			Summary: "update pool commentary and tweak logic",
		}

		resLane, accepted := r.Suggest(input, lane.LaneDocs, "jev")
		if accepted {
			t.Fatalf("INVARIANT VIOLATION: Suggest accepted LaneDocs for change containing code files")
		}
		if resLane != lane.LaneFeature {
			t.Fatalf("Expected deterministic fallback LaneFeature, got %q", resLane)
		}
	})

	t.Run("Refused suggestion for non-existent lane", func(t *testing.T) {
		input := lane.Input{
			Paths:   []string{"internal/worker/pool.go"},
			Summary: "feat: add feature",
		}

		resLane, accepted := r.Suggest(input, lane.Lane("UNKNOWN_LANE"), "jev")
		if accepted {
			t.Fatalf("Expected Suggest to refuse unknown lane")
		}
		if resLane != lane.LaneFeature {
			t.Fatalf("Expected fallback LaneFeature, got %q", resLane)
		}
	})

	t.Run("Refused suggestion when bundles configuration is broken", func(t *testing.T) {
		brokenRouter := lane.NewRouter(
			lane.WithBundlesData([]byte("corrupt yml ::::")),
		)
		input := lane.Input{
			Paths:   []string{"internal/worker/pool.go"},
			Summary: "refactor worker",
		}
		resLane, accepted := brokenRouter.Suggest(input, lane.LaneRefactor, "jev")
		if accepted {
			t.Fatalf("Expected Suggest to refuse suggestion when bundles config is broken")
		}
		if resLane != lane.LaneFeature {
			t.Fatalf("Expected fallback LaneFeature, got %q", resLane)
		}
	})
}

// TestBundleConfig_TrackedFileVerification verifies that the loaded configuration matches
// the exact tracked .g8s/lane-bundles.yml file in the repo.
func TestBundleConfig_TrackedFileVerification(t *testing.T) {
	r := lane.NewRouter()

	expectedLanes := []struct {
		id            lane.Lane
		name          string
		expectedGates []string
	}{
		{
			id:            lane.LaneHotfix,
			name:          "hotfix",
			expectedGates: []string{"build", "targeted-tests", "retro-gate"},
		},
		{
			id:            lane.LaneDocs,
			name:          "docs",
			expectedGates: []string{"G2", "G3", "doc-contract"},
		},
		{
			id:            lane.LaneRefactor,
			name:          "refactor",
			expectedGates: []string{"G2", "G4", "G5"},
		},
		{
			id:            lane.LaneFeature,
			name:          "feature",
			expectedGates: []string{"G1", "G2", "G4", "G5", "G6"},
		},
		{
			id:            lane.LaneSecurity,
			name:          "security",
			expectedGates: []string{"G1", "G2", "G3", "G4", "G5", "G6", "doc-contract", "redaction-scan", "review"},
		},
	}

	bundles := r.Bundles()
	if len(bundles) != 5 {
		t.Fatalf("Expected 5 bundles, got %d", len(bundles))
	}

	for _, exp := range expectedLanes {
		b, ok := r.GetBundle(exp.id)
		if !ok {
			t.Fatalf("Bundle %q not found in router", exp.id)
		}
		if b.Name != exp.name {
			t.Errorf("Bundle %q name = %q, want %q", exp.id, b.Name, exp.name)
		}
		if len(b.Gates) != len(exp.expectedGates) {
			t.Fatalf("Bundle %q gates count = %d, want %d: %v vs %v",
				exp.id, len(b.Gates), len(exp.expectedGates), b.Gates, exp.expectedGates)
		}
		for i, g := range exp.expectedGates {
			if b.Gates[i] != g {
				t.Errorf("Bundle %q gate[%d] = %q, want %q", exp.id, i, b.Gates[i], g)
			}
		}
	}
}

// TestTrustBoundaries_TrackedFileVerification verifies that default router loads the patterns
// from .g8s/trust-boundaries.yml.
func TestTrustBoundaries_TrackedFileVerification(t *testing.T) {
	r := lane.NewRouter()
	patterns := r.TrustBoundaries()

	expectedSubstrings := []string{
		"internal/receipt/*",
		"internal/worker/proc_*",
		"internal/server/*",
		"internal/harness/*",
		"internal/memory/*",
		"internal/dispatch/*",
	}

	for _, exp := range expectedSubstrings {
		found := false
		for _, pat := range patterns {
			if pat == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected pattern %q in trust boundaries, got %v", exp, patterns)
		}
	}
}

// TestParseLane tests ParseLane and Lane methods.
func TestParseLane(t *testing.T) {
	tests := []struct {
		raw      string
		wantLane lane.Lane
		wantErr  bool
	}{
		{"0", lane.LaneHotfix, false},
		{"hotfix", lane.LaneHotfix, false},
		{"D", lane.LaneDocs, false},
		{"docs", lane.LaneDocs, false},
		{"R", lane.LaneRefactor, false},
		{"refactor", lane.LaneRefactor, false},
		{"F", lane.LaneFeature, false},
		{"feature", lane.LaneFeature, false},
		{"feat", lane.LaneFeature, false},
		{"fix", lane.LaneFeature, false},
		{"S", lane.LaneSecurity, false},
		{"security", lane.LaneSecurity, false},
		{"sec", lane.LaneSecurity, false},
		{"p0", lane.LaneSecurity, false},
		{"invalid", "", true},
		{"", "", true},
	}

	for _, tc := range tests {
		got, err := lane.ParseLane(tc.raw)
		if (err != nil) != tc.wantErr {
			t.Errorf("ParseLane(%q) err = %v, wantErr = %v", tc.raw, err, tc.wantErr)
		}
		if got != tc.wantLane {
			t.Errorf("ParseLane(%q) = %q, want %q", tc.raw, got, tc.wantLane)
		}
	}

	if lane.Lane("X").IsValid() {
		t.Errorf("Lane('X').IsValid() should be false")
	}
	if !lane.LaneHotfix.IsValid() {
		t.Errorf("LaneHotfix.IsValid() should be true")
	}
}

// TestRouterConcurrency ensures concurrent Route and Suggest calls are thread-safe.
func TestRouterConcurrency(t *testing.T) {
	r := lane.NewRouter()
	var wg sync.WaitGroup

	inputs := []lane.Input{
		{Paths: []string{"README.md"}, Summary: "docs: update"},
		{Paths: []string{"internal/analyzer/analyzer.go"}, Summary: "feat: add"},
		{Paths: []string{"internal/receipt/receipt.go"}, Summary: "feat: receipt"},
		{Paths: []string{"internal/analyzer/analyzer.go"}, Summary: "refactor: clean"},
		{Paths: []string{"internal/analyzer/analyzer.go"}, Summary: "hotfix: fix"},
	}

	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			inp := inputs[idx%len(inputs)]
			_ = r.Route(inp)
		}(i)

		go func(idx int) {
			defer wg.Done()
			inp := inputs[idx%len(inputs)]
			_, _ = r.Suggest(inp, lane.LaneRefactor, fmt.Sprintf("worker-%d", idx))
		}(i)
	}

	wg.Wait()
}

// TestPackageLevelFunctionsAndMethods tests package-level functions and Lane helper methods.
func TestPackageLevelFunctionsAndMethods(t *testing.T) {
	// Package-level Suggest
	inp := lane.Input{Paths: []string{"internal/worker/pool.go"}, Summary: "simplify"}
	res, ok := lane.Suggest(inp, lane.LaneRefactor, "jev")
	if !ok || res != lane.LaneRefactor {
		t.Errorf("lane.Suggest failed: got %q, %v", res, ok)
	}

	// Lane String and Name methods
	lanes := []lane.Lane{lane.LaneHotfix, lane.LaneDocs, lane.LaneRefactor, lane.LaneFeature, lane.LaneSecurity, lane.Lane("custom")}
	for _, l := range lanes {
		if l.String() != string(l) {
			t.Errorf("l.String() = %q, want %q", l.String(), string(l))
		}
		_ = l.Name()
	}

	// Router options tests
	customTB := []byte("trust_boundaries:\n  - \"src/**/*.sec\"\n")
	rCustom := lane.NewRouter(
		lane.WithTrustBoundariesData(customTB),
		lane.WithTrustBoundariesPath(".g8s/trust-boundaries.yml"),
	)
	if !rCustom.Route(lane.Input{Paths: []string{"src/sub/deep/code.sec"}}).IsValid() {
		t.Errorf("Expected valid lane from custom router")
	}
	if got := rCustom.Route(lane.Input{Paths: []string{"src/sub/deep/code.sec"}}); got != lane.LaneSecurity {
		t.Errorf("Expected LaneSecurity for globstar match, got %q", got)
	}
}
