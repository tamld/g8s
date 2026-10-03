package lane_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/lane"
)

// TestAdversarialInputs_EmptyAndWhitespace tests routing behavior on empty,
// whitespace-only, boundary, and non-standard inputs.
func TestAdversarialInputs_EmptyAndWhitespace(t *testing.T) {
	tests := []struct {
		name     string
		input    lane.Input
		wantLane lane.Lane
	}{
		{
			name: "nil paths with empty summary -> LaneFeature",
			input: lane.Input{
				Paths:   nil,
				Summary: "",
			},
			wantLane: lane.LaneFeature,
		},
		{
			name: "empty slice paths with whitespace summary -> LaneFeature",
			input: lane.Input{
				Paths:   []string{},
				Summary: "   \t\n  ",
			},
			wantLane: lane.LaneFeature,
		},
		{
			name: "empty string path element -> LaneFeature",
			input: lane.Input{
				Paths:   []string{""},
				Summary: "docs: empty path",
			},
			wantLane: lane.LaneFeature,
		},
		{
			name: "dot path element -> LaneFeature",
			input: lane.Input{
				Paths:   []string{"."},
				Summary: "docs: dot path",
			},
			wantLane: lane.LaneFeature,
		},
		{
			name: "bare docs directory path -> LaneDocs",
			input: lane.Input{
				Paths:   []string{"docs"},
				Summary: "update documentation bundle",
			},
			wantLane: lane.LaneDocs,
		},
		{
			name: "uppercase .MD extension -> LaneDocs",
			input: lane.Input{
				Paths:   []string{"specs/SPEC.MD"},
				Summary: "update spec document",
			},
			wantLane: lane.LaneDocs,
		},
		{
			name: "exact hotfix keyword summary '0' -> LaneHotfix",
			input: lane.Input{
				Paths:   []string{"internal/worker/pool.go"},
				Summary: "0",
			},
			wantLane: lane.LaneHotfix,
		},
		{
			name: "exact hotfix keyword summary 'hotfix' -> LaneHotfix",
			input: lane.Input{
				Paths:   []string{"internal/worker/pool.go"},
				Summary: "  hotfix  ",
			},
			wantLane: lane.LaneHotfix,
		},
		{
			name: "lane prefix 'lane:0' -> LaneHotfix",
			input: lane.Input{
				Paths:   []string{"internal/worker/pool.go"},
				Summary: "lane:0",
			},
			wantLane: lane.LaneHotfix,
		},
		{
			name: "lane prefix 'lane: 0' -> LaneHotfix",
			input: lane.Input{
				Paths:   []string{"internal/worker/pool.go"},
				Summary: "lane: 0",
			},
			wantLane: lane.LaneHotfix,
		},
		{
			name: "lane prefix 'lane:hotfix' -> LaneHotfix",
			input: lane.Input{
				Paths:   []string{"internal/worker/pool.go"},
				Summary: "lane:hotfix",
			},
			wantLane: lane.LaneHotfix,
		},
		{
			name: "parenthesized hotfix prefix '(hotfix)' -> LaneHotfix",
			input: lane.Input{
				Paths:   []string{"internal/worker/pool.go"},
				Summary: "(hotfix) fix memory leak",
			},
			wantLane: lane.LaneHotfix,
		},
		{
			name: "hyphenated hotfix prefix 'hotfix - ' -> LaneHotfix",
			input: lane.Input{
				Paths:   []string{"internal/worker/pool.go"},
				Summary: "hotfix - fix race condition",
			},
			wantLane: lane.LaneHotfix,
		},
		{
			name: "space hotfix prefix 'hotfix ' -> LaneHotfix",
			input: lane.Input{
				Paths:   []string{"internal/worker/pool.go"},
				Summary: "hotfix urgent nil check",
			},
			wantLane: lane.LaneHotfix,
		},
		{
			name: "exact refactor keyword summary 'r' -> LaneRefactor",
			input: lane.Input{
				Paths:   []string{"internal/worker/pool.go"},
				Summary: "r",
			},
			wantLane: lane.LaneRefactor,
		},
		{
			name: "exact refactor keyword summary 'refactor' -> LaneRefactor",
			input: lane.Input{
				Paths:   []string{"internal/worker/pool.go"},
				Summary: "  refactor  ",
			},
			wantLane: lane.LaneRefactor,
		},
		{
			name: "lane prefix 'lane:r' -> LaneRefactor",
			input: lane.Input{
				Paths:   []string{"internal/worker/pool.go"},
				Summary: "lane:r",
			},
			wantLane: lane.LaneRefactor,
		},
		{
			name: "lane prefix 'lane: r' -> LaneRefactor",
			input: lane.Input{
				Paths:   []string{"internal/worker/pool.go"},
				Summary: "lane: r",
			},
			wantLane: lane.LaneRefactor,
		},
		{
			name: "lane prefix 'lane:refactor' -> LaneRefactor",
			input: lane.Input{
				Paths:   []string{"internal/worker/pool.go"},
				Summary: "lane:refactor",
			},
			wantLane: lane.LaneRefactor,
		},
		{
			name: "parenthesized refactor prefix '(refactor)' -> LaneRefactor",
			input: lane.Input{
				Paths:   []string{"internal/worker/pool.go"},
				Summary: "(refactor) extract method",
			},
			wantLane: lane.LaneRefactor,
		},
		{
			name: "hyphenated refactor prefix 'refactor - ' -> LaneRefactor",
			input: lane.Input{
				Paths:   []string{"internal/worker/pool.go"},
				Summary: "refactor - split interface",
			},
			wantLane: lane.LaneRefactor,
		},
		{
			name: "space refactor prefix 'refactor ' -> LaneRefactor",
			input: lane.Input{
				Paths:   []string{"internal/worker/pool.go"},
				Summary: "refactor deduplicate helpers",
			},
			wantLane: lane.LaneRefactor,
		},
		{
			name: "dash refactor prefix 'refactor-' -> LaneRefactor",
			input: lane.Input{
				Paths:   []string{"internal/worker/pool.go"},
				Summary: "refactor-dead-code-removal",
			},
			wantLane: lane.LaneRefactor,
		},
		{
			name: "conventional commit chore(refactor) -> LaneRefactor",
			input: lane.Input{
				Paths:   []string{"internal/worker/pool.go"},
				Summary: "chore(refactor): inline variable",
			},
			wantLane: lane.LaneRefactor,
		},
		{
			name: "subpath within /refactor/ directory -> LaneRefactor",
			input: lane.Input{
				Paths:   []string{"internal/refactor/migration.go"},
				Summary: "move legacy types",
			},
			wantLane: lane.LaneRefactor,
		},
		{
			name: "mixed paths with refactor directory and non-refactor -> LaneFeature",
			input: lane.Input{
				Paths:   []string{"refactor/clean.go", "internal/worker/pool.go"},
				Summary: "update codebase",
			},
			wantLane: lane.LaneFeature,
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

// TestTrustBoundaries_AdversarialAndEdgeCases tests pattern matching logic for
// exact matches, bare directory prefixes, trailing slashes, globstars, and config fallbacks.
func TestTrustBoundaries_AdversarialAndEdgeCases(t *testing.T) {
	t.Run("Exact path matching", func(t *testing.T) {
		r := lane.NewRouter(lane.WithTrustBoundaries([]string{"internal/receipt/receipt.go"}))
		if got := r.Route(lane.Input{Paths: []string{"internal/receipt/receipt.go"}}); got != lane.LaneSecurity {
			t.Errorf("expected LaneSecurity for exact match, got %q", got)
		}
		if got := r.Route(lane.Input{Paths: []string{"internal/receipt/other.go"}}); got != lane.LaneFeature {
			t.Errorf("expected LaneFeature for non-matching path, got %q", got)
		}
	})

	t.Run("Trailing slash directory pattern", func(t *testing.T) {
		r := lane.NewRouter(lane.WithTrustBoundaries([]string{"internal/receipt/"}))
		if got := r.Route(lane.Input{Paths: []string{"internal/receipt/store.go"}}); got != lane.LaneSecurity {
			t.Errorf("expected LaneSecurity for trailing slash prefix, got %q", got)
		}
		if got := r.Route(lane.Input{Paths: []string{"internal/receipt_extended/store.go"}}); got != lane.LaneFeature {
			t.Errorf("expected LaneFeature for sibling directory, got %q", got)
		}
	})

	t.Run("Bare directory pattern without slash or glob", func(t *testing.T) {
		r := lane.NewRouter(lane.WithTrustBoundaries([]string{"internal/server"}))
		if got := r.Route(lane.Input{Paths: []string{"internal/server/handler.go"}}); got != lane.LaneSecurity {
			t.Errorf("expected LaneSecurity for bare dir match, got %q", got)
		}
		if got := r.Route(lane.Input{Paths: []string{"internal/server_extended/handler.go"}}); got != lane.LaneFeature {
			t.Errorf("expected LaneFeature for sibling directory, got %q", got)
		}
	})

	t.Run("Globstar matching zero, one, and multi levels", func(t *testing.T) {
		r := lane.NewRouter(lane.WithTrustBoundaries([]string{"src/**/sec/*.go"}))

		matches := []string{
			"src/sec/auth.go",
			"src/sub/sec/auth.go",
			"src/a/b/c/sec/auth.go",
		}
		for _, p := range matches {
			if got := r.Route(lane.Input{Paths: []string{p}}); got != lane.LaneSecurity {
				t.Errorf("expected LaneSecurity for %q, got %q", p, got)
			}
		}

		nonMatches := []string{
			"other/sec/auth.go",
			"src/sec/auth.txt",
			"src",
		}
		for _, p := range nonMatches {
			if got := r.Route(lane.Input{Paths: []string{p}}); got != lane.LaneFeature {
				t.Errorf("expected LaneFeature for %q, got %q", p, got)
			}
		}
	})

	t.Run("Globstar segment boundary edge cases", func(t *testing.T) {
		r := lane.NewRouter(lane.WithTrustBoundaries([]string{"a/b/c/d"}))
		// File path shorter than pattern
		if got := r.Route(lane.Input{Paths: []string{"a/b"}}); got != lane.LaneFeature {
			t.Errorf("expected LaneFeature when file path shorter than pattern, got %q", got)
		}
	})

	t.Run("Empty trust boundaries fallback to DefaultTrustBoundaries", func(t *testing.T) {
		// When explicit empty slice is provided, matchesTrustBoundary falls back to DefaultTrustBoundaries
		r := lane.NewRouter(lane.WithTrustBoundaries([]string{}))
		if got := r.Route(lane.Input{Paths: []string{"internal/receipt/receipt.go"}}); got != lane.LaneSecurity {
			t.Errorf("expected LaneSecurity under DefaultTrustBoundaries fallback, got %q", got)
		}
	})

	t.Run("Trust boundaries YAML parser error handling", func(t *testing.T) {
		// Corrupted YAML falls back to DefaultTrustBoundaries
		corrupted := []byte("trust_boundaries: [broken yaml :::")
		r := lane.NewRouter(lane.WithTrustBoundariesData(corrupted))
		if got := r.Route(lane.Input{Paths: []string{"internal/receipt/receipt.go"}}); got != lane.LaneSecurity {
			t.Errorf("expected LaneSecurity under corrupted TB config fallback, got %q", got)
		}

		// Empty trust boundaries section falls back to DefaultTrustBoundaries
		emptyConfig := []byte("trust_boundaries:\n")
		rEmpty := lane.NewRouter(lane.WithTrustBoundariesData(emptyConfig))
		if got := rEmpty.Route(lane.Input{Paths: []string{"internal/receipt/receipt.go"}}); got != lane.LaneSecurity {
			t.Errorf("expected LaneSecurity under empty TB config, got %q", got)
		}

		// Valid YAML with section terminator and comments
		validWithExtra := []byte(`
# Comment header
trust_boundaries:
  - "internal/receipt/*"
  - 'internal/sec/*' # inline comment
other_section:
  key: value
`)
		rValid := lane.NewRouter(lane.WithTrustBoundariesData(validWithExtra))
		if got := rValid.Route(lane.Input{Paths: []string{"internal/sec/token.go"}}); got != lane.LaneSecurity {
			t.Errorf("expected LaneSecurity for internal/sec/*, got %q", got)
		}
	})

	t.Run("Trust boundaries scanner error on overlong line", func(t *testing.T) {
		var buf bytes.Buffer
		buf.WriteString("trust_boundaries:\n  - ")
		buf.WriteString(strings.Repeat("a", 70000))
		buf.WriteString("\n")

		r := lane.NewRouter(lane.WithTrustBoundariesData(buf.Bytes()))
		// Scanner error causes parse error -> fallback to DefaultTrustBoundaries
		if got := r.Route(lane.Input{Paths: []string{"internal/receipt/store.go"}}); got != lane.LaneSecurity {
			t.Errorf("expected LaneSecurity via DefaultTrustBoundaries fallback on scanner error, got %q", got)
		}
	})

	t.Run("TrustBoundaries() returns a defensive copy", func(t *testing.T) {
		r := lane.NewRouter()
		tb := r.TrustBoundaries()
		origLen := len(tb)
		tb = append(tb, "injected/pattern")
		_ = tb
		if len(r.TrustBoundaries()) != origLen {
			t.Errorf("TrustBoundaries() returned slice mutation modified internal router state")
		}
	})
}

// TestBundles_DegradationAndAdversarialParsing tests parsing lane bundles YAML
// with edge cases, corrupt data, unexpected keys, and path discovery.
func TestBundles_DegradationAndAdversarialParsing(t *testing.T) {
	t.Run("YAML with preamble and comments before lanes section", func(t *testing.T) {
		yamlData := []byte(`
version: 1
metadata:
  description: "test bundles"
lanes:
  0:
    gates:
      - build
    name: "hotfix"
    description: 'hotfix description # not a comment'
`)
		r := lane.NewRouter(lane.WithBundlesData(yamlData))
		b, ok := r.GetBundle(lane.LaneHotfix)
		if !ok {
			t.Fatalf("expected LaneHotfix bundle to load")
		}
		if b.Name != "hotfix" {
			t.Errorf("expected bundle name 'hotfix', got %q", b.Name)
		}
		if b.Description != "hotfix description # not a comment" {
			t.Errorf("expected description preserved without comment strip, got %q", b.Description)
		}
		if len(b.Gates) != 1 || b.Gates[0] != "build" {
			t.Errorf("expected gate 'build', got %v", b.Gates)
		}
	})

	t.Run("YAML with invalid lane key returns error and falls back to deny-by-default", func(t *testing.T) {
		invalidLaneYAML := []byte(`
lanes:
  INVALID_LANE:
    name: bad
    gates:
      - g1
`)
		r := lane.NewRouter(lane.WithBundlesData(invalidLaneYAML))
		if got := r.Route(lane.Input{Paths: []string{"docs/readme.md"}}); got != lane.LaneFeature {
			t.Errorf("expected fallback to LaneFeature on invalid lane key, got %q", got)
		}
	})

	t.Run("YAML with unexpected content outside lane definition", func(t *testing.T) {
		danglingYAML := []byte(`
lanes:
    name: dangling_property_before_any_lane_key
`)
		r := lane.NewRouter(lane.WithBundlesData(danglingYAML))
		if got := r.Route(lane.Input{Paths: []string{"docs/readme.md"}}); got != lane.LaneFeature {
			t.Errorf("expected fallback to LaneFeature on unexpected content, got %q", got)
		}
	})

	t.Run("YAML with empty lanes section", func(t *testing.T) {
		emptyLanesYAML := []byte("lanes:\n")
		r := lane.NewRouter(lane.WithBundlesData(emptyLanesYAML))
		if got := r.Route(lane.Input{Paths: []string{"docs/readme.md"}}); got != lane.LaneFeature {
			t.Errorf("expected fallback to LaneFeature on empty lanes, got %q", got)
		}
	})

	t.Run("YAML scanner error on overlong line", func(t *testing.T) {
		var buf bytes.Buffer
		buf.WriteString("lanes:\n  0:\n    name: ")
		buf.WriteString(strings.Repeat("b", 70000))
		buf.WriteString("\n")

		r := lane.NewRouter(lane.WithBundlesData(buf.Bytes()))
		if got := r.Route(lane.Input{Paths: []string{"docs/readme.md"}}); got != lane.LaneFeature {
			t.Errorf("expected fallback to LaneFeature on scanner error, got %q", got)
		}
	})

	t.Run("GetBundle and Bundles on uninitialized or broken router", func(t *testing.T) {
		r := lane.NewRouter(lane.WithBundlesPath("/nonexistent/file.yml"))
		if _, ok := r.GetBundle(lane.LaneHotfix); ok {
			t.Errorf("expected GetBundle to return false on missing bundles")
		}
		bundles := r.Bundles()
		if len(bundles) != 0 {
			t.Errorf("expected Bundles to return empty map, got %d entries", len(bundles))
		}
	})

	t.Run("Discovery with existing absolute path", func(t *testing.T) {
		absPath, err := filepath.Abs("../../.g8s/lane-bundles.yml")
		if err != nil {
			t.Skipf("cannot resolve absolute path: %v", err)
		}
		if _, err := os.Stat(absPath); err != nil {
			t.Skipf("file does not exist at %s: %v", absPath, err)
		}
		r := lane.NewRouter(lane.WithBundlesPath(absPath))
		if _, ok := r.GetBundle(lane.LaneHotfix); !ok {
			t.Errorf("expected bundle to load with absolute path %s", absPath)
		}
	})

	t.Run("Discovery with relative path directly in working directory", func(t *testing.T) {
		// "lane.go" exists directly in current test package directory
		r := lane.NewRouter(lane.WithBundlesPath("lane.go"))
		// Loading lane.go as bundles YAML fails to parse, triggering bundlesErr
		if got := r.Route(lane.Input{Paths: []string{"docs/readme.md"}}); got != lane.LaneFeature {
			t.Errorf("expected fallback to LaneFeature when bundles file is invalid Go code, got %q", got)
		}
	})

	t.Run("Discovery with directory path as bundles path", func(t *testing.T) {
		// "." exists as a directory; os.ReadFile fails
		r := lane.NewRouter(lane.WithBundlesPath("."))
		if got := r.Route(lane.Input{Paths: []string{"docs/readme.md"}}); got != lane.LaneFeature {
			t.Errorf("expected fallback to LaneFeature when bundles path is a directory, got %q", got)
		}
	})

	t.Run("Discovery with nonexistent relative path traversing to root", func(t *testing.T) {
		r := lane.NewRouter(lane.WithBundlesPath("nonexistent_deeply_nested_file_xyz_12345.yml"))
		if got := r.Route(lane.Input{Paths: []string{"docs/readme.md"}}); got != lane.LaneFeature {
			t.Errorf("expected fallback to LaneFeature when bundles file is missing, got %q", got)
		}
	})

	t.Run("Trust boundaries discovery with relative, directory, and absolute paths", func(t *testing.T) {
		absTB, err := filepath.Abs("../../.g8s/trust-boundaries.yml")
		if err == nil {
			rAbs := lane.NewRouter(lane.WithTrustBoundariesPath(absTB))
			if got := rAbs.Route(lane.Input{Paths: []string{"internal/receipt/receipt.go"}}); got != lane.LaneSecurity {
				t.Errorf("expected LaneSecurity with absolute trust-boundaries path")
			}
		}

		// Directory path fails os.ReadFile, keeps DefaultTrustBoundaries
		rDir := lane.NewRouter(lane.WithTrustBoundariesPath("."))
		if got := rDir.Route(lane.Input{Paths: []string{"internal/receipt/receipt.go"}}); got != lane.LaneSecurity {
			t.Errorf("expected LaneSecurity when trust path is a directory")
		}

		// Nonexistent relative path keeps DefaultTrustBoundaries
		rMissing := lane.NewRouter(lane.WithTrustBoundariesPath("nonexistent_tb_file_12345.yml"))
		if got := rMissing.Route(lane.Input{Paths: []string{"internal/receipt/receipt.go"}}); got != lane.LaneSecurity {
			t.Errorf("expected LaneSecurity when trust path is missing")
		}
	})
}

// TestSuggest_AdversarialAndEdgeCases tests Layer 2 ambiguity resolution
// edge cases, empty paths, non-standard sources, and refusal rules.
func TestSuggest_AdversarialAndEdgeCases(t *testing.T) {
	r := lane.NewRouter()

	t.Run("Suggest LaneDocs for empty paths slice is refused", func(t *testing.T) {
		input := lane.Input{
			Paths:   []string{},
			Summary: "docs: empty changeset",
		}
		resLane, accepted := r.Suggest(input, lane.LaneDocs, "jev")
		if accepted {
			t.Fatalf("expected Suggest of LaneDocs for empty paths to be refused")
		}
		if resLane != lane.LaneFeature {
			t.Errorf("expected fallback to LaneFeature, got %q", resLane)
		}
	})

	t.Run("Suggest LaneFeature for empty paths slice is accepted", func(t *testing.T) {
		input := lane.Input{
			Paths:   []string{},
			Summary: "empty change",
		}
		resLane, accepted := r.Suggest(input, lane.LaneFeature, "llm-assist")
		if !accepted || resLane != lane.LaneFeature {
			t.Errorf("expected Suggest of LaneFeature for empty paths to be accepted, got %q, %v", resLane, accepted)
		}
	})

	t.Run("Suggest LaneHotfix for pure code change is accepted", func(t *testing.T) {
		input := lane.Input{
			Paths:   []string{"internal/analyzer/analyzer.go"},
			Summary: "suppress edge case panic",
		}
		resLane, accepted := r.Suggest(input, lane.LaneHotfix, "supervisor")
		if !accepted || resLane != lane.LaneHotfix {
			t.Errorf("expected Suggest of LaneHotfix to be accepted, got %q, %v", resLane, accepted)
		}
	})

	t.Run("Suggest with empty source string or arbitrary source", func(t *testing.T) {
		input := lane.Input{
			Paths:   []string{"internal/analyzer/analyzer.go"},
			Summary: "clean up variable names",
		}
		resLane, accepted := r.Suggest(input, lane.LaneRefactor, "")
		if !accepted || resLane != lane.LaneRefactor {
			t.Errorf("expected Suggest with empty source to be accepted, got %q, %v", resLane, accepted)
		}
	})

	t.Run("Suggest when suggestion matches deterministic route", func(t *testing.T) {
		input := lane.Input{
			Paths:   []string{"README.md"},
			Summary: "docs: update readme",
		}
		resLane, accepted := r.Suggest(input, lane.LaneDocs, "jev")
		if !accepted || resLane != lane.LaneDocs {
			t.Errorf("expected Suggest of matching lane to be accepted, got %q, %v", resLane, accepted)
		}
	})
}

// TestParseLane_AndHelpers_Adversarial tests string parsing, trimming,
// case sensitivity, and helper methods.
func TestParseLane_AndHelpers_Adversarial(t *testing.T) {
	tests := []struct {
		input    string
		wantLane lane.Lane
		wantErr  bool
	}{
		{"  0  ", lane.LaneHotfix, false},
		{"  HOTFIX  ", lane.LaneHotfix, false},
		{"  d  ", lane.LaneDocs, false},
		{"  DOCS  ", lane.LaneDocs, false},
		{"  doc  ", lane.LaneDocs, false},
		{"  r  ", lane.LaneRefactor, false},
		{"  REFACTOR  ", lane.LaneRefactor, false},
		{"  f  ", lane.LaneFeature, false},
		{"  FEATURE  ", lane.LaneFeature, false},
		{"  FEAT  ", lane.LaneFeature, false},
		{"  FIX  ", lane.LaneFeature, false},
		{"  s  ", lane.LaneSecurity, false},
		{"  SECURITY  ", lane.LaneSecurity, false},
		{"  SEC  ", lane.LaneSecurity, false},
		{"  P0  ", lane.LaneSecurity, false},
		{"  unknown  ", "", true},
		{"  ", "", true},
		{"1", "", true},
		{"2", "", true},
	}

	for _, tc := range tests {
		got, err := lane.ParseLane(tc.input)
		if (err != nil) != tc.wantErr {
			t.Errorf("ParseLane(%q) err = %v, wantErr = %v", tc.input, err, tc.wantErr)
		}
		if got != tc.wantLane {
			t.Errorf("ParseLane(%q) = %q, want %q", tc.input, got, tc.wantLane)
		}
	}

	// Verify Lane.Name() fallback for non-canonical lane
	customLane := lane.Lane("custom-lane-id")
	if customLane.Name() != "custom-lane-id" {
		t.Errorf("customLane.Name() = %q, want %q", customLane.Name(), "custom-lane-id")
	}
	if customLane.IsValid() {
		t.Errorf("customLane.IsValid() should be false")
	}
}
