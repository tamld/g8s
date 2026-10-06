package lane

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/config"
)

// ============================================================================
// Dimension 1: Class Registry Ordering
// Rules:
//  - Lowest-priority-number matching entry wins (first match by priority).
//  - Ties broken deterministically by name alphabetically.
//  - Priority at 0 beats positive priorities.
//  - Negative-int priorities supported; lower number (more negative) wins.
//  - Large negative integer boundaries handled without overflow or parse error.
// ============================================================================

func TestDimension1_ClassRegistryOrdering(t *testing.T) {
	tests := []struct {
		name       string
		rule       string
		yaml       string
		queryPaths []string
		wantClass  string
		wantEffort string
	}{
		{
			name: "higher_priority_lower_number_wins",
			rule: "Rule: When two classes match the same path, lowest priority number wins",
			yaml: `
schema_version: "effort-classes.v1"
classes:
  - name: high_prio
    default_effort: high
    priority: 5
    paths: ["src/**"]
  - name: low_prio
    default_effort: low
    priority: 50
    paths: ["src/**"]
`,
			queryPaths: []string{"src/core/main.go"},
			wantClass:  "high_prio",
			wantEffort: config.EffortHigh,
		},
		{
			name: "equal_priority_alphabetical_tie_break",
			rule: "Rule: When priorities are identical, alphabetical class name tie-break decides",
			yaml: `
schema_version: "effort-classes.v1"
classes:
  - name: zeta_class
    default_effort: low
    priority: 20
    paths: ["shared/**"]
  - name: alpha_class
    default_effort: high
    priority: 20
    paths: ["shared/**"]
`,
			queryPaths: []string{"shared/util.go"},
			wantClass:  "alpha_class",
			wantEffort: config.EffortHigh,
		},
		{
			name: "priority_zero_beats_positive_priority",
			rule: "Rule: Priority 0 is valid and beats positive priorities (0 < 10)",
			yaml: `
schema_version: "effort-classes.v1"
classes:
  - name: standard_task
    default_effort: low
    priority: 10
    paths: ["pkg/**"]
  - name: zero_task
    default_effort: max
    priority: 0
    paths: ["pkg/**"]
`,
			queryPaths: []string{"pkg/service.go"},
			wantClass:  "zero_task",
			wantEffort: config.EffortMax,
		},
		{
			name: "negative_priority_beats_zero_and_positive",
			rule: "Rule: Negative integer priority is valid and beats zero and positive priorities (-5 < 0 < 5)",
			yaml: `
schema_version: "effort-classes.v1"
classes:
  - name: pos_class
    default_effort: low
    priority: 5
    paths: ["sec/**"]
  - name: zero_class
    default_effort: medium
    priority: 0
    paths: ["sec/**"]
  - name: neg_class
    default_effort: xhigh
    priority: -5
    paths: ["sec/**"]
`,
			queryPaths: []string{"sec/auth.go"},
			wantClass:  "neg_class",
			wantEffort: config.EffortXHigh,
		},
		{
			name: "equal_negative_priorities_tie_break_alphabetically",
			rule: "Rule: Equal negative priorities tie-break by class name alphabetically",
			yaml: `
schema_version: "effort-classes.v1"
classes:
  - name: beta_neg
    default_effort: medium
    priority: -10
    paths: ["sec/**"]
  - name: alpha_neg
    default_effort: high
    priority: -10
    paths: ["sec/**"]
`,
			queryPaths: []string{"sec/token.go"},
			wantClass:  "alpha_neg",
			wantEffort: config.EffortHigh,
		},
		{
			name: "large_negative_priority_boundary",
			rule: "Rule: Large negative integer priority (-1000000) parses and beats less negative (-10)",
			yaml: `
schema_version: "effort-classes.v1"
classes:
  - name: urgent_critical
    default_effort: max
    priority: -1000000
    paths: ["critical/**"]
  - name: regular_critical
    default_effort: high
    priority: -10
    paths: ["critical/**"]
`,
			queryPaths: []string{"critical/panic.go"},
			wantClass:  "urgent_critical",
			wantEffort: config.EffortMax,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// tc.rule documents the exact rule verified
			ec, err := ParseEffortClasses([]byte(tc.yaml))
			if err != nil {
				t.Fatalf("%s: ParseEffortClasses failed: %v", tc.rule, err)
			}
			gotClass, gotEffort := ec.ResolveClass(tc.queryPaths)
			if gotClass != tc.wantClass {
				t.Errorf("%s: gotClass = %q, want %q", tc.rule, gotClass, tc.wantClass)
			}
			if gotEffort != tc.wantEffort {
				t.Errorf("%s: gotEffort = %q, want %q", tc.rule, gotEffort, tc.wantEffort)
			}
		})
	}
}

// ============================================================================
// Dimension 2: Fail-Open Floor
// Rules:
//  - A path matching NO class resolves to ("unregistered", config.EffortMedium).
//  - Multiple paths where any path falls outside matching globs fail open to medium.
//  - Empty class list resolves to ("unregistered", config.EffortMedium).
//  - Nil *EffortClasses receiver resolves to ("unregistered", config.EffortMedium).
//  - Missing or non-existent configuration file resolves to unregistered / medium.
//  - ResolveClassForRoots on sibling without registry resolves to unregistered / medium.
// ============================================================================

func TestDimension2_FailOpenFloor(t *testing.T) {
	t.Run("path_matching_no_class_resolves_to_unregistered_medium", func(t *testing.T) {
		// Rule: Unregistered tasks fail open to unregistered / medium
		yaml := `
schema_version: "effort-classes.v1"
classes:
  - name: docs
    default_effort: low
    priority: 10
    paths: ["docs/**"]
`
		ec, err := ParseEffortClasses([]byte(yaml))
		if err != nil {
			t.Fatalf("ParseEffortClasses failed: %v", err)
		}
		cls, eff := ec.ResolveClass([]string{"cmd/g8s/main.go"})
		if cls != UnregisteredClassName || eff != config.EffortMedium {
			t.Errorf("ResolveClass = (%q, %q), want (%q, %q)", cls, eff, UnregisteredClassName, config.EffortMedium)
		}
	})

	t.Run("partial_path_match_fails_open_all_must_match", func(t *testing.T) {
		// Rule: ALL write-scope paths must fall inside class globs; partial match fails open
		yaml := `
schema_version: "effort-classes.v1"
classes:
  - name: docs
    default_effort: low
    priority: 10
    paths: ["docs/**"]
`
		ec, err := ParseEffortClasses([]byte(yaml))
		if err != nil {
			t.Fatalf("ParseEffortClasses failed: %v", err)
		}
		// docs/readme.md matches, but internal/daemon.go does not
		cls, eff := ec.ResolveClass([]string{"docs/readme.md", "internal/daemon.go"})
		if cls != UnregisteredClassName || eff != config.EffortMedium {
			t.Errorf("multi-path partial match = (%q, %q), want (%q, %q)", cls, eff, UnregisteredClassName, config.EffortMedium)
		}
	})

	t.Run("empty_class_list_in_config_fails_open", func(t *testing.T) {
		// Rule: Empty classes list parses successfully and resolves to unregistered / medium
		yaml := `
schema_version: "effort-classes.v1"
classes: []
`
		ec, err := ParseEffortClasses([]byte(yaml))
		if err != nil {
			t.Fatalf("ParseEffortClasses failed on empty classes: %v", err)
		}
		cls, eff := ec.ResolveClass([]string{"docs/any.md"})
		if cls != UnregisteredClassName || eff != config.EffortMedium {
			t.Errorf("empty classes ResolveClass = (%q, %q), want (%q, %q)", cls, eff, UnregisteredClassName, config.EffortMedium)
		}
	})

	t.Run("nil_effortclasses_receiver_fails_open", func(t *testing.T) {
		// Rule: Nil *EffortClasses receiver safely returns unregistered / medium without panicking
		var nilEC *EffortClasses
		cls, eff := nilEC.ResolveClass([]string{"any/file.go"})
		if cls != UnregisteredClassName || eff != config.EffortMedium {
			t.Errorf("nilEC.ResolveClass = (%q, %q), want (%q, %q)", cls, eff, UnregisteredClassName, config.EffortMedium)
		}
	})

	t.Run("empty_paths_slice_fails_open", func(t *testing.T) {
		// Rule: Empty or nil paths slice resolves to unregistered / medium
		ec := &EffortClasses{
			SchemaVersion: CurrentEffortClassSchemaVersion,
			Classes: []EffortClass{
				{Name: "feature", DefaultEffort: config.EffortHigh, Priority: 10, Paths: []string{"tools/**"}},
			},
		}
		c1, e1 := ec.ResolveClass(nil)
		if c1 != UnregisteredClassName || e1 != config.EffortMedium {
			t.Errorf("ResolveClass(nil) = (%q, %q), want (%q, %q)", c1, e1, UnregisteredClassName, config.EffortMedium)
		}
		c2, e2 := ec.ResolveClass([]string{})
		if c2 != UnregisteredClassName || e2 != config.EffortMedium {
			t.Errorf("ResolveClass([]) = (%q, %q), want (%q, %q)", c2, e2, UnregisteredClassName, config.EffortMedium)
		}
	})

	t.Run("missing_file_load_fails_open_without_error", func(t *testing.T) {
		// Rule: Non-existent effort-classes config file returns empty resolver and no error (fail-open)
		tempDir := t.TempDir()
		missingPath := filepath.Join(tempDir, "non_existent_effort_classes.yml")
		ec, err := LoadEffortClassesFile(missingPath)
		if err != nil {
			t.Fatalf("LoadEffortClassesFile on missing file returned error: %v", err)
		}
		if ec == nil {
			t.Fatal("expected non-nil empty resolver")
		}
		cls, eff := ec.ResolveClass([]string{"any/source.go"})
		if cls != UnregisteredClassName || eff != config.EffortMedium {
			t.Errorf("missing config resolution = (%q, %q), want (%q, %q)", cls, eff, UnregisteredClassName, config.EffortMedium)
		}
	})

	t.Run("package_level_resolve_class_missing_fallback", func(t *testing.T) {
		// Rule: Package-level ResolveClass succeeds on empty paths by failing open
		cls, eff := ResolveClass(nil)
		if cls != UnregisteredClassName || eff != config.EffortMedium {
			t.Errorf("ResolveClass(nil) = (%q, %q), want (%q, %q)", cls, eff, UnregisteredClassName, config.EffortMedium)
		}
	})

	t.Run("resolve_class_for_roots_empty_inputs_fail_open", func(t *testing.T) {
		// Rule: ResolveClassForRoots with empty paths or roots falls back to unregistered / medium
		cls, eff, err := ResolveClassForRoots(nil, nil)
		if err != nil {
			t.Fatalf("unexpected error on nil inputs: %v", err)
		}
		if cls != UnregisteredClassName || eff != config.EffortMedium {
			t.Errorf("ResolveClassForRoots(nil, nil) = (%q, %q), want (%q, %q)", cls, eff, UnregisteredClassName, config.EffortMedium)
		}
	})

	t.Run("resolve_class_for_roots_sibling_without_registry_fails_open", func(t *testing.T) {
		// Rule: Sibling worktree without its own .g8s/effort-classes.yml must not be accidentally
		// classified by the g8s repo registry; it fails open to unregistered / medium (Windows incident PR #562)
		tempSib := t.TempDir()
		paths := []string{filepath.Join(tempSib, "docs", "intro.md")}
		roots := []string{tempSib}

		cls, eff, err := ResolveClassForRoots(paths, roots)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cls != UnregisteredClassName || eff != config.EffortMedium {
			t.Errorf("sibling without registry ResolveClassForRoots = (%q, %q), want (%q, %q)", cls, eff, UnregisteredClassName, config.EffortMedium)
		}
	})
}

// ============================================================================
// Dimension 3: Glob Semantics
// Rules:
//  - Patterns from live .g8s/effort-classes.yml tested against near-miss paths.
//  - "plans/**/brief-R[0-9]*" matches "plans/261006-v016-cut/brief-R1-lane-effort.md".
//  - "brief-RC1" matches "plans/**/brief-RC*", does NOT match "brief-R[0-9]*".
//  - "brief-R1" matches "plans/**/brief-R[0-9]*", does NOT match "brief-RC*".
//  - Near-miss non-digit "brief-RX1" or "brief-R" fails open to unregistered / medium.
//  - Nested directory depth matches globstar "**" across arbitrary segment counts.
//  - Trailing slash on path or pattern cleans and matches correctly.
//  - Cross-platform case-sensitivity: filepath.Match on Windows is case-insensitive
//    (Windows oracle / issue #517 catch).
// ============================================================================

func TestDimension3_GlobSemantics(t *testing.T) {
	ec, err := LoadEffortClasses()
	if err != nil {
		t.Fatalf("LoadEffortClasses() failed: %v", err)
	}

	table := []struct {
		name       string
		rule       string
		path       string
		wantClass  string
		wantEffort string
	}{
		{
			name:       "exact_target_brief_r1_lane_effort_matches_red_cell",
			rule:       "Rule: 'plans/**/brief-R[0-9]*' matches 'plans/261006-v016-cut/brief-R1-lane-effort.md'",
			path:       "plans/261006-v016-cut/brief-R1-lane-effort.md",
			wantClass:  "red-cell",
			wantEffort: config.EffortHigh,
		},
		{
			name:       "brief_rc1_matches_brief_rc_glob",
			rule:       "Rule: 'plans/**/brief-RC*' matches 'plans/261006-v016-cut/brief-RC1-test.md'",
			path:       "plans/261006-v016-cut/brief-RC1-test.md",
			wantClass:  "red-cell",
			wantEffort: config.EffortHigh,
		},
		{
			name:       "brief_r1_matches_brief_r_digit_glob",
			rule:       "Rule: 'plans/**/brief-R[0-9]*' matches 'plans/261006-v016-cut/brief-R1.md'",
			path:       "plans/261006-v016-cut/brief-R1.md",
			wantClass:  "red-cell",
			wantEffort: config.EffortHigh,
		},
		{
			name:       "near_miss_brief_rx_fails_open",
			rule:       "Rule: 'brief-RX1.md' has non-digit and non-C suffix, matching neither brief-RC* nor brief-R[0-9]* -> unregistered",
			path:       "plans/261006-v016-cut/brief-RX1.md",
			wantClass:  UnregisteredClassName,
			wantEffort: config.EffortMedium,
		},
		{
			name:       "near_miss_brief_bare_r_fails_open",
			rule:       "Rule: 'brief-R.md' lacks character after 'brief-R', failing both brief-RC* and brief-R[0-9]* -> unregistered",
			path:       "plans/261006-v016-cut/brief-R.md",
			wantClass:  UnregisteredClassName,
			wantEffort: config.EffortMedium,
		},
		{
			name:       "factory_wave_brief_f1_ordinal_fails_open",
			rule:       "Rule: Factory wave brief 'brief-F1-baked-effort.md' is not red-cell or discovery -> unregistered",
			path:       "plans/261006-v016-cut/brief-F1-baked-effort.md",
			wantClass:  UnregisteredClassName,
			wantEffort: config.EffortMedium,
		},
		{
			name:       "deeply_nested_brief_r9_matches",
			rule:       "Rule: Globstar '**' matches arbitrary nested directory depth 'plans/a/b/c/d/brief-R9-deep.md'",
			path:       "plans/a/b/c/d/brief-R9-deep.md",
			wantClass:  "red-cell",
			wantEffort: config.EffortHigh,
		},
		{
			name:       "deeply_nested_docs_matches",
			rule:       "Rule: 'docs/**' matches arbitrary nested directory depth 'docs/decisions/internal/arch.md'",
			path:       "docs/decisions/internal/arch.md",
			wantClass:  "docs",
			wantEffort: config.EffortLow,
		},
		{
			name:       "deeply_nested_tools_matches",
			rule:       "Rule: 'tools/**' matches nested feature tools 'tools/ci/scripts/verifier.sh'",
			path:       "tools/ci/scripts/verifier.sh",
			wantClass:  "feature",
			wantEffort: config.EffortHigh,
		},
		{
			name:       "root_test_file_matches_test_class",
			rule:       "Rule: '*_test.go' matches root test file 'root_test.go'",
			path:       "root_test.go",
			wantClass:  "test",
			wantEffort: config.EffortMedium,
		},
		{
			name:       "nested_test_file_matches_test_class",
			rule:       "Rule: '**/*_test.go' matches nested test file 'internal/lane/nested_test.go'",
			path:       "internal/lane/nested_test.go",
			wantClass:  "test",
			wantEffort: config.EffortMedium,
		},
		{
			name:       "trailing_slash_on_directory_path_matches_globstar",
			rule:       "Rule: Path with trailing slash 'docs/' cleans to 'docs' and matches 'docs/**'",
			path:       "docs/",
			wantClass:  "docs",
			wantEffort: config.EffortLow,
		},
		{
			name:       "trailing_slash_on_tools_directory_matches",
			rule:       "Rule: Path with trailing slash 'tools/' cleans to 'tools' and matches 'tools/**'",
			path:       "tools/",
			wantClass:  "feature",
			wantEffort: config.EffortHigh,
		},
		{
			name:       "windows_style_backslash_path_normalized",
			rule:       "Rule: Windows-style backslash 'docs\\architecture.md' is normalized to slash and matches 'docs/**'",
			path:       "docs\\architecture.md",
			wantClass:  "docs",
			wantEffort: config.EffortLow,
		},
		{
			name:       "redteam_wildcard_directory_matches",
			rule:       "Rule: 'plans/*redteam*/**' matches 'plans/261002-redteam/notes.txt'",
			path:       "plans/261002-redteam/notes.txt",
			wantClass:  "red-cell",
			wantEffort: config.EffortHigh,
		},
		{
			name:       "discovery_plan_matches_discovery_class",
			rule:       "Rule: 'plans/*discovery*/**' matches 'plans/discovery-pass1/report.md'",
			path:       "plans/discovery-pass1/report.md",
			wantClass:  "discovery",
			wantEffort: config.EffortHigh,
		},
	}

	for _, tc := range table {
		t.Run(tc.name, func(t *testing.T) {
			gotClass, gotEffort := ec.ResolveClass([]string{tc.path})
			if gotClass != tc.wantClass {
				t.Errorf("%s: ResolveClass(%q) class = %q, want %q", tc.rule, tc.path, gotClass, tc.wantClass)
			}
			if gotEffort != tc.wantEffort {
				t.Errorf("%s: ResolveClass(%q) effort = %q, want %q", tc.rule, tc.path, gotEffort, tc.wantEffort)
			}
		})
	}
}

// TestGlobSemantics_WindowsOracle_PathMatchVsFilepathMatch tests the repo invariant
// that path globs must have deterministic case-sensitivity across all operating systems.
// In internal/verifier/verifier.go:1093-1098, path.Match is mandated because filepath.Match
// is case-insensitive on Windows, which allows uppercase paths (e.g., DOCS/x.md) to match
// lowercase patterns (docs/**).
// In internal/lane/lane.go lines 533 & 552, filepath.Match is invoked instead of path.Match.
func TestGlobSemantics_WindowsOracle_PathMatchVsFilepathMatch(t *testing.T) {
	// Rule: Path matching for slash-normalized paths must use path.Match, not filepath.Match,
	// to prevent Windows runners from case-insensitively widening effort classes (issue #517 oracle).
	// Expected: lane.go uses path.Match for glob matching.
	// Actual: lane.go lines 533 and 552 call filepath.Match which on Windows folds case.
	t.Skip("FINDING-R1-1: lane.go uses filepath.Match which is case-insensitive on Windows (issue #517 oracle)")
}

// ============================================================================
// Dimension 4: Effort Level Validation
// Rules:
//  - Class entries with unknown default_effort strings return EffortClassValidationError.
//  - Class entries with empty string default_effort return EffortClassValidationError.
//  - Class entries with a level not on the ladder (e.g. "adaptive", "dynamic") return EffortClassValidationError.
//  - Class entries with uppercase effort ("HIGH", "Medium") return EffortClassValidationError (strict lowercase).
//  - Class entries with empty or whitespace-only name return EffortClassValidationError.
//  - Error message format verifies Class and Field fields accurately.
// ============================================================================

func TestDimension4_EffortLevelValidation(t *testing.T) {
	tests := []struct {
		name          string
		rule          string
		yaml          string
		wantErrField  string
		wantErrClass  string
		wantErrSubstr string
	}{
		{
			name: "unknown_effort_level_turbo",
			rule: "Rule: Unknown effort level string 'turbo' is refused with validation error naming class and field",
			yaml: `
schema_version: "effort-classes.v1"
classes:
  - name: my_class
    default_effort: turbo
    priority: 10
    paths: ["src/**"]
`,
			wantErrField:  "default_effort",
			wantErrClass:  "my_class",
			wantErrSubstr: `unknown effort level "turbo"`,
		},
		{
			name: "unknown_effort_level_extreme",
			rule: "Rule: Unknown effort level string 'extreme' is refused",
			yaml: `
schema_version: "effort-classes.v1"
classes:
  - name: fast_lane
    default_effort: extreme
    priority: 10
    paths: ["fast/**"]
`,
			wantErrField:  "default_effort",
			wantErrClass:  "fast_lane",
			wantErrSubstr: `unknown effort level "extreme"`,
		},
		{
			name: "empty_string_effort_level",
			rule: "Rule: Empty default_effort string is refused as not on the ladder",
			yaml: `
schema_version: "effort-classes.v1"
classes:
  - name: empty_effort_class
    default_effort: ""
    priority: 10
    paths: ["src/**"]
`,
			wantErrField:  "default_effort",
			wantErrClass:  "empty_effort_class",
			wantErrSubstr: `unknown effort level ""`,
		},
		{
			name: "adaptive_posture_outside_ladder",
			rule: "Rule: 'adaptive' is a provider posture, NOT a valid class ladder level, and must be refused",
			yaml: `
schema_version: "effort-classes.v1"
classes:
  - name: adaptive_class
    default_effort: adaptive
    priority: 10
    paths: ["src/**"]
`,
			wantErrField:  "default_effort",
			wantErrClass:  "adaptive_class",
			wantErrSubstr: `unknown effort level "adaptive"`,
		},
		{
			name: "dynamic_posture_outside_ladder",
			rule: "Rule: 'dynamic' is a provider posture, NOT a valid class ladder level, and must be refused",
			yaml: `
schema_version: "effort-classes.v1"
classes:
  - name: dynamic_class
    default_effort: dynamic
    priority: 10
    paths: ["src/**"]
`,
			wantErrField:  "default_effort",
			wantErrClass:  "dynamic_class",
			wantErrSubstr: `unknown effort level "dynamic"`,
		},
		{
			name: "uppercase_effort_level_rejected",
			rule: "Rule: Effort levels must be strictly canonical lowercase ('HIGH' rejected)",
			yaml: `
schema_version: "effort-classes.v1"
classes:
  - name: upper_class
    default_effort: HIGH
    priority: 10
    paths: ["src/**"]
`,
			wantErrField:  "default_effort",
			wantErrClass:  "upper_class",
			wantErrSubstr: `unknown effort level "HIGH"`,
		},
		{
			name: "empty_class_name_refused",
			rule: "Rule: Empty class name is refused with validation error",
			yaml: `
schema_version: "effort-classes.v1"
classes:
  - name: ""
    default_effort: low
    priority: 10
    paths: ["src/**"]
`,
			wantErrField:  "name",
			wantErrSubstr: "has empty name",
		},
		{
			name: "whitespace_only_class_name_refused",
			rule: "Rule: Whitespace-only class name is trimmed and refused",
			yaml: `
schema_version: "effort-classes.v1"
classes:
  - name: "   "
    default_effort: low
    priority: 10
    paths: ["src/**"]
`,
			wantErrField:  "name",
			wantErrSubstr: "has empty name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseEffortClasses([]byte(tc.yaml))
			if err == nil {
				t.Fatalf("%s: expected validation error, got nil", tc.rule)
			}
			var valErr *EffortClassValidationError
			if !errors.As(err, &valErr) {
				t.Fatalf("%s: expected *EffortClassValidationError, got %T (%v)", tc.rule, err, err)
			}
			if tc.wantErrField != "" && valErr.Field != tc.wantErrField {
				t.Errorf("%s: valErr.Field = %q, want %q", tc.rule, valErr.Field, tc.wantErrField)
			}
			if tc.wantErrClass != "" && valErr.Class != tc.wantErrClass {
				t.Errorf("%s: valErr.Class = %q, want %q", tc.rule, valErr.Class, tc.wantErrClass)
			}
			if tc.wantErrSubstr != "" && !strings.Contains(valErr.Error(), tc.wantErrSubstr) {
				t.Errorf("%s: valErr.Error() %q does not contain %q", tc.rule, valErr.Error(), tc.wantErrSubstr)
			}
		})
	}
}

// TestEffortClassValidationError_ErrorString tests the formatting branches of EffortClassValidationError.
func TestEffortClassValidationError_ErrorString(t *testing.T) {
	// Rule: When Class is populated, error includes class name
	e1 := &EffortClassValidationError{Class: "my-class", Field: "default_effort", Msg: "invalid"}
	want1 := `effort class "my-class": field "default_effort": invalid`
	if e1.Error() != want1 {
		t.Errorf("e1.Error() = %q, want %q", e1.Error(), want1)
	}

	// Rule: When Class is empty, error omits class prefix
	e2 := &EffortClassValidationError{Class: "", Field: "name", Msg: "empty"}
	want2 := `field "name": empty`
	if e2.Error() != want2 {
		t.Errorf("e2.Error() = %q, want %q", e2.Error(), want2)
	}
}

// ============================================================================
// Dimension 5: Signal Model (effortsignal.go)
// Rules:
//  - Precedence chain: explicit -> declared -> registry -> floor.
//  - Hypothesis matrix v1:
//      blast=high ∧ loc>200 -> high
//      blast=high -> at least medium (elevates registry < medium to medium; registry >= medium stands)
//      blast=low -> low (any LOC)
//      otherwise registry result (or medium floor) stands
//  - Zero-signal defaults: empty blast and loc=0 falls back to registry, then floor medium.
//  - Contradictory declared vs registry pairs: declared low overrides registry high;
//    declared high overrides registry low; blast=high with loc<=200 preserves registry high/xhigh.
//  - Boundary values for numeric loc (0, 1, 200, 201, max, negative where type allows).
//  - Realignment rule interplay: SignalResult.Effort delivers winning effort level to submit-layer realignment.
//  - OverrideDown calculation: true only when explicit flag is lower than winning non-explicit baseline.
//  - EffortSignalsPayload serialization: all fields faithfully reflected.
//  - IsValidBlastRadius: case-insensitive, whitespace-trimmed, rejects unknown.
// ============================================================================

func TestDimension5_SignalModel(t *testing.T) {
	table := []struct {
		name              string
		rule              string
		explicit          string
		blast             string
		loc               int
		regClass          string
		regEffort         string
		wantEffort        string
		wantSource        string
		wantDeclaredSug   string
		wantRegistrySug   string
		wantFloorSug      string
		wantOverrideDown  bool
		winningRealignLvl string // Realignment rule: model matches applied effort
	}{
		{
			name:              "zero_signals_registry_stands",
			rule:              "Rule: Zero declared signals (blast='', loc=0) preserves registry suggestion",
			explicit:          "",
			blast:             "",
			loc:               0,
			regClass:          "docs",
			regEffort:         config.EffortLow,
			wantEffort:        config.EffortLow,
			wantSource:        EffortSourceRegistry,
			wantDeclaredSug:   "",
			wantRegistrySug:   config.EffortLow,
			wantFloorSug:      config.EffortMedium,
			wantOverrideDown:  false,
			winningRealignLvl: config.EffortLow,
		},
		{
			name:              "zero_signals_unregistered_falls_to_floor",
			rule:              "Rule: Zero declared signals and unregistered class falls to medium floor",
			explicit:          "",
			blast:             "",
			loc:               0,
			regClass:          UnregisteredClassName,
			regEffort:         config.EffortMedium,
			wantEffort:        config.EffortMedium,
			wantSource:        EffortSourceFloor,
			wantDeclaredSug:   "",
			wantRegistrySug:   "",
			wantFloorSug:      config.EffortMedium,
			wantOverrideDown:  false,
			winningRealignLvl: config.EffortMedium,
		},
		{
			name:              "contradictory_declared_low_overrides_registry_high",
			rule:              "Rule: Declared low (blast=low) overrides registry high; source is declared",
			explicit:          "",
			blast:             BlastLow,
			loc:               50,
			regClass:          "feature",
			regEffort:         config.EffortHigh,
			wantEffort:        config.EffortLow,
			wantSource:        EffortSourceDeclared,
			wantDeclaredSug:   config.EffortLow,
			wantRegistrySug:   config.EffortHigh,
			wantFloorSug:      config.EffortMedium,
			wantOverrideDown:  false,
			winningRealignLvl: config.EffortLow,
		},
		{
			name:              "contradictory_declared_high_overrides_registry_low",
			rule:              "Rule: Declared high (blast=high ∧ loc>200) overrides registry low; source is declared",
			explicit:          "",
			blast:             BlastHigh,
			loc:               250,
			regClass:          "docs",
			regEffort:         config.EffortLow,
			wantEffort:        config.EffortHigh,
			wantSource:        EffortSourceDeclared,
			wantDeclaredSug:   config.EffortHigh,
			wantRegistrySug:   config.EffortLow,
			wantFloorSug:      config.EffortMedium,
			wantOverrideDown:  false,
			winningRealignLvl: config.EffortHigh,
		},
		{
			name:              "blast_high_loc_boundary_200_elevates_low_to_medium",
			rule:              "Rule: blast=high ∧ loc=200 elevates registry low to medium; source is declared",
			explicit:          "",
			blast:             BlastHigh,
			loc:               200,
			regClass:          "docs",
			regEffort:         config.EffortLow,
			wantEffort:        config.EffortMedium,
			wantSource:        EffortSourceDeclared,
			wantDeclaredSug:   config.EffortMedium,
			wantRegistrySug:   config.EffortLow,
			wantFloorSug:      config.EffortMedium,
			wantOverrideDown:  false,
			winningRealignLvl: config.EffortMedium,
		},
		{
			name:              "blast_high_loc_boundary_201_is_high",
			rule:              "Rule: blast=high ∧ loc=201 (boundary + 1) resolves to high; source is declared",
			explicit:          "",
			blast:             BlastHigh,
			loc:               201,
			regClass:          "docs",
			regEffort:         config.EffortLow,
			wantEffort:        config.EffortHigh,
			wantSource:        EffortSourceDeclared,
			wantDeclaredSug:   config.EffortHigh,
			wantRegistrySug:   config.EffortLow,
			wantFloorSug:      config.EffortMedium,
			wantOverrideDown:  false,
			winningRealignLvl: config.EffortHigh,
		},
		{
			name:              "blast_high_loc_1_positive_minimum_is_at_least_medium",
			rule:              "Rule: blast=high ∧ loc=1 (positive minimum) elevates registry low to medium",
			explicit:          "",
			blast:             BlastHigh,
			loc:               1,
			regClass:          "docs",
			regEffort:         config.EffortLow,
			wantEffort:        config.EffortMedium,
			wantSource:        EffortSourceDeclared,
			wantDeclaredSug:   config.EffortMedium,
			wantRegistrySug:   config.EffortLow,
			wantFloorSug:      config.EffortMedium,
			wantOverrideDown:  false,
			winningRealignLvl: config.EffortMedium,
		},
		{
			name:              "blast_high_loc_max_int_is_high",
			rule:              "Rule: blast=high ∧ loc=math.MaxInt boundary resolves to high without overflow",
			explicit:          "",
			blast:             BlastHigh,
			loc:               math.MaxInt,
			regClass:          "docs",
			regEffort:         config.EffortLow,
			wantEffort:        config.EffortHigh,
			wantSource:        EffortSourceDeclared,
			wantDeclaredSug:   config.EffortHigh,
			wantRegistrySug:   config.EffortLow,
			wantFloorSug:      config.EffortMedium,
			wantOverrideDown:  false,
			winningRealignLvl: config.EffortHigh,
		},
		{
			name:              "blast_high_loc_negative_one_evaluates_as_at_least_medium",
			rule:              "Rule: blast=high ∧ loc=-1 (negative boundary) satisfies loc<=200, elevating low to medium",
			explicit:          "",
			blast:             BlastHigh,
			loc:               -1,
			regClass:          "docs",
			regEffort:         config.EffortLow,
			wantEffort:        config.EffortMedium,
			wantSource:        EffortSourceDeclared,
			wantDeclaredSug:   config.EffortMedium,
			wantRegistrySug:   config.EffortLow,
			wantFloorSug:      config.EffortMedium,
			wantOverrideDown:  false,
			winningRealignLvl: config.EffortMedium,
		},
		{
			name:              "blast_high_loc_min_int_evaluates_as_at_least_medium",
			rule:              "Rule: blast=high ∧ loc=math.MinInt satisfies loc<=200 without overflow",
			explicit:          "",
			blast:             BlastHigh,
			loc:               math.MinInt,
			regClass:          "docs",
			regEffort:         config.EffortLow,
			wantEffort:        config.EffortMedium,
			wantSource:        EffortSourceDeclared,
			wantDeclaredSug:   config.EffortMedium,
			wantRegistrySug:   config.EffortLow,
			wantFloorSug:      config.EffortMedium,
			wantOverrideDown:  false,
			winningRealignLvl: config.EffortMedium,
		},
		{
			name:              "blast_high_loc_100_registry_high_stands",
			rule:              "Rule: blast=high ∧ loc<=200: registry >= medium stands (registry high preserved)",
			explicit:          "",
			blast:             BlastHigh,
			loc:               100,
			regClass:          "feature",
			regEffort:         config.EffortHigh,
			wantEffort:        config.EffortHigh,
			wantSource:        EffortSourceRegistry,
			wantDeclaredSug:   config.EffortMedium,
			wantRegistrySug:   config.EffortHigh,
			wantFloorSug:      config.EffortMedium,
			wantOverrideDown:  false,
			winningRealignLvl: config.EffortHigh,
		},
		{
			name:              "blast_high_loc_100_registry_xhigh_stands",
			rule:              "Rule: blast=high ∧ loc<=200: registry >= medium stands (registry xhigh preserved)",
			explicit:          "",
			blast:             BlastHigh,
			loc:               100,
			regClass:          "critical",
			regEffort:         config.EffortXHigh,
			wantEffort:        config.EffortXHigh,
			wantSource:        EffortSourceRegistry,
			wantDeclaredSug:   config.EffortMedium,
			wantRegistrySug:   config.EffortXHigh,
			wantFloorSug:      config.EffortMedium,
			wantOverrideDown:  false,
			winningRealignLvl: config.EffortXHigh,
		},
		{
			name:              "blast_high_loc_100_unregistered_stands_as_declared_medium",
			rule:              "Rule: blast=high ∧ loc<=200 with unregistered registry: declared medium wins",
			explicit:          "",
			blast:             BlastHigh,
			loc:               100,
			regClass:          UnregisteredClassName,
			regEffort:         config.EffortMedium,
			wantEffort:        config.EffortMedium,
			wantSource:        EffortSourceDeclared,
			wantDeclaredSug:   config.EffortMedium,
			wantRegistrySug:   "",
			wantFloorSug:      config.EffortMedium,
			wantOverrideDown:  false,
			winningRealignLvl: config.EffortMedium,
		},
		{
			name:              "blast_medium_registry_stands",
			rule:              "Rule: blast=medium produces no declared suggestion; registry stands",
			explicit:          "",
			blast:             BlastMedium,
			loc:               100,
			regClass:          "docs",
			regEffort:         config.EffortLow,
			wantEffort:        config.EffortLow,
			wantSource:        EffortSourceRegistry,
			wantDeclaredSug:   "",
			wantRegistrySug:   config.EffortLow,
			wantFloorSug:      config.EffortMedium,
			wantOverrideDown:  false,
			winningRealignLvl: config.EffortLow,
		},
		{
			name:              "loc_alone_without_blast_registry_stands",
			rule:              "Rule: LOC estimate alone without blast produces no declared suggestion; registry stands",
			explicit:          "",
			blast:             "",
			loc:               500,
			regClass:          "feature",
			regEffort:         config.EffortHigh,
			wantEffort:        config.EffortHigh,
			wantSource:        EffortSourceRegistry,
			wantDeclaredSug:   "",
			wantRegistrySug:   config.EffortHigh,
			wantFloorSug:      config.EffortMedium,
			wantOverrideDown:  false,
			winningRealignLvl: config.EffortHigh,
		},
		{
			name:              "explicit_wins_over_all_sources",
			rule:              "Rule: Explicit flag wins over declared, registry, and floor unconditionally",
			explicit:          config.EffortMax,
			blast:             BlastLow,
			loc:               10,
			regClass:          "docs",
			regEffort:         config.EffortLow,
			wantEffort:        config.EffortMax,
			wantSource:        EffortSourceExplicit,
			wantDeclaredSug:   config.EffortLow,
			wantRegistrySug:   config.EffortLow,
			wantFloorSug:      config.EffortMedium,
			wantOverrideDown:  false,
			winningRealignLvl: config.EffortMax,
		},
		{
			name:              "explicit_override_down_against_declared_high",
			rule:              "Rule: explicit low against winning declared high sets OverrideDown = true",
			explicit:          config.EffortLow,
			blast:             BlastHigh,
			loc:               300,
			regClass:          "docs",
			regEffort:         config.EffortLow,
			wantEffort:        config.EffortLow,
			wantSource:        EffortSourceExplicit,
			wantDeclaredSug:   config.EffortHigh,
			wantRegistrySug:   config.EffortLow,
			wantFloorSug:      config.EffortMedium,
			wantOverrideDown:  true,
			winningRealignLvl: config.EffortLow,
		},
		{
			name:              "explicit_override_down_against_registry_high",
			rule:              "Rule: explicit medium against registry high (no declared signals) sets OverrideDown = true",
			explicit:          config.EffortMedium,
			blast:             "",
			loc:               0,
			regClass:          "feature",
			regEffort:         config.EffortHigh,
			wantEffort:        config.EffortMedium,
			wantSource:        EffortSourceExplicit,
			wantDeclaredSug:   "",
			wantRegistrySug:   config.EffortHigh,
			wantFloorSug:      config.EffortMedium,
			wantOverrideDown:  true,
			winningRealignLvl: config.EffortMedium,
		},
		{
			name:              "explicit_none_overrides_down_against_floor_medium",
			rule:              "Rule: explicit 'none' against unregistered floor medium sets OverrideDown = true (0 < 3)",
			explicit:          config.EffortNone,
			blast:             "",
			loc:               0,
			regClass:          UnregisteredClassName,
			regEffort:         config.EffortMedium,
			wantEffort:        config.EffortNone,
			wantSource:        EffortSourceExplicit,
			wantDeclaredSug:   "",
			wantRegistrySug:   "",
			wantFloorSug:      config.EffortMedium,
			wantOverrideDown:  true,
			winningRealignLvl: config.EffortNone,
		},
	}

	for _, tt := range table {
		t.Run(tt.name, func(t *testing.T) {
			res := ResolveEffortSignals(tt.explicit, tt.blast, tt.loc, tt.regClass, tt.regEffort)

			if res.Effort != tt.wantEffort {
				t.Errorf("%s: Effort = %q, want %q", tt.rule, res.Effort, tt.wantEffort)
			}
			if res.Source != tt.wantSource {
				t.Errorf("%s: Source = %q, want %q", tt.rule, res.Source, tt.wantSource)
			}
			if res.DeclaredSuggestion != tt.wantDeclaredSug {
				t.Errorf("%s: DeclaredSuggestion = %q, want %q", tt.rule, res.DeclaredSuggestion, tt.wantDeclaredSug)
			}
			if res.RegistrySuggestion != tt.wantRegistrySug {
				t.Errorf("%s: RegistrySuggestion = %q, want %q", tt.rule, res.RegistrySuggestion, tt.wantRegistrySug)
			}
			if res.FloorSuggestion != tt.wantFloorSug {
				t.Errorf("%s: FloorSuggestion = %q, want %q", tt.rule, res.FloorSuggestion, tt.wantFloorSug)
			}
			if res.OverrideDown != tt.wantOverrideDown {
				t.Errorf("%s: OverrideDown = %v, want %v", tt.rule, res.OverrideDown, tt.wantOverrideDown)
			}
			if res.Effort != tt.winningRealignLvl {
				t.Errorf("%s: Realignment target effort = %q, want %q", tt.rule, res.Effort, tt.winningRealignLvl)
			}

			// Verify payload representation matches
			payload := res.EffortSignals()
			if payload.BlastRadius != res.BlastRadius {
				t.Errorf("EffortSignals().BlastRadius = %q, want %q", payload.BlastRadius, res.BlastRadius)
			}
			if payload.LOCEstimate != res.LOCEstimate {
				t.Errorf("EffortSignals().LOCEstimate = %d, want %d", payload.LOCEstimate, res.LOCEstimate)
			}
			if payload.DeclaredSuggestion != res.DeclaredSuggestion {
				t.Errorf("EffortSignals().DeclaredSuggestion = %q, want %q", payload.DeclaredSuggestion, res.DeclaredSuggestion)
			}
			if payload.RegistrySuggestion != res.RegistrySuggestion {
				t.Errorf("EffortSignals().RegistrySuggestion = %q, want %q", payload.RegistrySuggestion, res.RegistrySuggestion)
			}
			if payload.OverrideDown != res.OverrideDown {
				t.Errorf("EffortSignals().OverrideDown = %v, want %v", payload.OverrideDown, res.OverrideDown)
			}
		})
	}
}

// TestSignalModel_HelpersAndLadder tests helper functions:
// EffortLadderIndex, IsOverrideDown, IsValidBlastRadius.
func TestSignalModel_HelpersAndLadder(t *testing.T) {
	// Rule: EffortLadderIndex returns 0..6 for valid canonical ladder levels and -1 for outside
	ladderLevels := []struct {
		level     string
		wantIndex int
	}{
		{config.EffortNone, 0},
		{config.EffortMinimal, 1},
		{config.EffortLow, 2},
		{config.EffortMedium, 3},
		{config.EffortHigh, 4},
		{config.EffortXHigh, 5},
		{config.EffortMax, 6},
		{"outside_ladder", -1},
		{"", -1},
	}
	for _, tc := range ladderLevels {
		if got := EffortLadderIndex(tc.level); got != tc.wantIndex {
			t.Errorf("EffortLadderIndex(%q) = %d, want %d", tc.level, got, tc.wantIndex)
		}
	}

	// Rule: IsOverrideDown returns true only when flagEffort < classDefault on EffortLadder
	overrideCases := []struct {
		flag   string
		class  string
		wantOD bool
	}{
		{config.EffortNone, config.EffortMinimal, true},
		{config.EffortMinimal, config.EffortLow, true},
		{config.EffortLow, config.EffortMedium, true},
		{config.EffortMedium, config.EffortHigh, true},
		{config.EffortHigh, config.EffortXHigh, true},
		{config.EffortXHigh, config.EffortMax, true},
		{config.EffortMax, config.EffortHigh, false},
		{config.EffortHigh, config.EffortHigh, false},
		{"invalid", config.EffortHigh, false},
		{config.EffortLow, "invalid", false},
	}
	for _, tc := range overrideCases {
		if got := IsOverrideDown(tc.flag, tc.class); got != tc.wantOD {
			t.Errorf("IsOverrideDown(%q, %q) = %v, want %v", tc.flag, tc.class, got, tc.wantOD)
		}
	}

	// Rule: IsValidBlastRadius accepts only low, medium, high (case-insensitive, trimmed)
	blastTests := []struct {
		input string
		want  bool
	}{
		{"low", true},
		{"medium", true},
		{"high", true},
		{"  LOW  ", true},
		{"  Medium  ", true},
		{"HIGH", true},
		{"", false},
		{"   ", false},
		{"extreme", false},
		{"none", false},
		{"superhigh", false},
	}
	for _, tc := range blastTests {
		if got := IsValidBlastRadius(tc.input); got != tc.want {
			t.Errorf("IsValidBlastRadius(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// ============================================================================
// Dimension 6: Schema Handling
// Rules:
//  - Missing schema_version key returns *SchemaVersionError{Got: "", Want: "effort-classes.v1"}.
//  - Wrong schema_version returns *SchemaVersionError.
//  - Forward schema_version ("effort-classes.v1.1", "effort-classes.v2") returns *SchemaVersionError.
//  - Upper/mixed case schema_version returns *SchemaVersionError.
//  - Corrupted YAML syntax or unclosed flow array returns *EffortClassParseError.
//  - SchemaVersionError.Error() and EffortClassParseError.Error() formatting verified.
// ============================================================================

func TestDimension6_SchemaHandling(t *testing.T) {
	tests := []struct {
		name          string
		rule          string
		yaml          string
		wantSchemaErr bool
		wantParseErr  bool
		errContains   string
	}{
		{
			name: "wrong_schema_version_v2",
			rule: "Rule: Unsupported schema version string 'effort-classes.v2' returns SchemaVersionError",
			yaml: `
schema_version: "effort-classes.v2"
classes:
  - name: docs
    default_effort: low
    priority: 10
    paths: ["*.md"]
`,
			wantSchemaErr: true,
			errContains:   `unsupported schema version "effort-classes.v2" (want "effort-classes.v1")`,
		},
		{
			name: "forward_minor_schema_version_v1_1",
			rule: "Rule: Forward minor schema version 'effort-classes.v1.1' is rejected",
			yaml: `
schema_version: "effort-classes.v1.1"
classes:
  - name: docs
    default_effort: low
    priority: 10
    paths: ["*.md"]
`,
			wantSchemaErr: true,
			errContains:   `unsupported schema version "effort-classes.v1.1"`,
		},
		{
			name: "missing_schema_version_entirely",
			rule: "Rule: Missing schema_version line entirely is rejected with SchemaVersionError",
			yaml: `
classes:
  - name: docs
    default_effort: low
    priority: 10
    paths: ["*.md"]
`,
			wantSchemaErr: true,
			errContains:   `unsupported schema version "" (want "effort-classes.v1")`,
		},
		{
			name: "empty_schema_version_string",
			rule: "Rule: Explicit empty schema_version string is rejected with SchemaVersionError",
			yaml: `
schema_version: ""
classes:
  - name: docs
    default_effort: low
    priority: 10
    paths: ["*.md"]
`,
			wantSchemaErr: true,
			errContains:   `unsupported schema version "" (want "effort-classes.v1")`,
		},
		{
			name: "uppercase_schema_version_rejected",
			rule: "Rule: Schema version must be strictly lowercase canonical string",
			yaml: `
schema_version: "EFFORT-CLASSES.V1"
classes:
  - name: docs
    default_effort: low
    priority: 10
    paths: ["*.md"]
`,
			wantSchemaErr: true,
			errContains:   `unsupported schema version "EFFORT-CLASSES.V1"`,
		},
		{
			name: "unclosed_flow_array_returns_parse_error",
			rule: "Rule: Unclosed flow array in paths returns EffortClassParseError",
			yaml: `
schema_version: "effort-classes.v1"
classes:
  - name: docs
    default_effort: low
    priority: 10
    paths: ["*.md"
`,
			wantParseErr: true,
			errContains:  "expected flow array enclosed in []",
		},
		{
			name: "invalid_top_level_line_without_colon",
			rule: "Rule: Top-level line without colon key/value separator returns EffortClassParseError",
			yaml: `
schema_version "effort-classes.v1"
classes:
  - name: docs
    default_effort: low
    priority: 10
    paths: ["*.md"]
`,
			wantParseErr: true,
			errContains:  "invalid top-level line",
		},
		{
			name: "invalid_priority_not_an_int",
			rule: "Rule: Non-integer priority string returns EffortClassParseError",
			yaml: `
schema_version: "effort-classes.v1"
classes:
  - name: docs
    default_effort: low
    priority: not_a_number
    paths: ["*.md"]
`,
			wantParseErr: true,
			errContains:  "invalid priority integer",
		},
		{
			name: "line_outside_class_item",
			rule: "Rule: Property line outside of class item returns EffortClassParseError",
			yaml: `
schema_version: "effort-classes.v1"
classes:
    orphan_property: value
`,
			wantParseErr: true,
			errContains:  "line outside class item",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseEffortClasses([]byte(tc.yaml))
			if err == nil {
				t.Fatalf("%s: expected error, got nil", tc.rule)
			}
			if tc.wantSchemaErr {
				var sErr *SchemaVersionError
				if !errors.As(err, &sErr) {
					t.Fatalf("%s: expected *SchemaVersionError, got %T (%v)", tc.rule, err, err)
				}
			}
			if tc.wantParseErr {
				var pErr *EffortClassParseError
				if !errors.As(err, &pErr) {
					t.Fatalf("%s: expected *EffortClassParseError, got %T (%v)", tc.rule, err, err)
				}
			}
			if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
				t.Errorf("%s: error %q does not contain %q", tc.rule, err.Error(), tc.errContains)
			}
		})
	}
}

// TestSchemaErrorTypes_ErrorString tests the formatting branches of SchemaVersionError and EffortClassParseError.
func TestSchemaErrorTypes_ErrorString(t *testing.T) {
	// Rule: SchemaVersionError.Error() formats Got and Want
	sErr := &SchemaVersionError{Got: "v2", Want: "v1"}
	wantS := `unsupported schema version "v2" (want "v1")`
	if sErr.Error() != wantS {
		t.Errorf("sErr.Error() = %q, want %q", sErr.Error(), wantS)
	}

	// Rule: EffortClassParseError.Error() formats line number when Line > 0
	pErrWithLine := &EffortClassParseError{Line: 12, Msg: "unexpected token"}
	wantP1 := "effort classes parse error at line 12: unexpected token"
	if pErrWithLine.Error() != wantP1 {
		t.Errorf("pErrWithLine.Error() = %q, want %q", pErrWithLine.Error(), wantP1)
	}

	// Rule: EffortClassParseError.Error() formats without line number when Line <= 0
	pErrNoLine := &EffortClassParseError{Line: 0, Msg: "scan failure"}
	wantP2 := "effort classes parse error: scan failure"
	if pErrNoLine.Error() != wantP2 {
		t.Errorf("pErrNoLine.Error() = %q, want %q", pErrNoLine.Error(), wantP2)
	}
}

// ============================================================================
// Multi-Root Precedence & Edge Cases (ResolveClassForRoots)
// Rules:
//  - Deepest matching root wins when paths fall under multiple roots.
//  - Relative paths vs absolute paths reconciled lexically without os.Chdir.
// ============================================================================

func TestResolveClassForRoots_MultiRootEdgeCases(t *testing.T) {
	// Rule: When paths resolve under nested roots, the most specific (deepest root) wins
	parentDir := t.TempDir()
	childDir := filepath.Join(parentDir, "child")
	if err := os.MkdirAll(filepath.Join(childDir, ".g8s"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(parentDir, ".g8s"), 0o755); err != nil {
		t.Fatal(err)
	}

	childYAML := `schema_version: "effort-classes.v1"
classes:
  - name: child-docs
    default_effort: low
    priority: 5
    paths: ["docs/**"]
`
	if err := os.WriteFile(filepath.Join(childDir, ".g8s", "effort-classes.yml"), []byte(childYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	parentYAML := `schema_version: "effort-classes.v1"
classes:
  - name: parent-docs
    default_effort: high
    priority: 5
    paths: ["child/docs/**"]
`
	if err := os.WriteFile(filepath.Join(parentDir, ".g8s", "effort-classes.yml"), []byte(parentYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	// File inside child directory
	path := filepath.Join(childDir, "docs", "guide.md")
	// Pass roots with parent first, child second; child should win because it is deeper
	roots := []string{parentDir, childDir}

	cls, eff, err := ResolveClassForRoots([]string{path}, roots)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cls != "child-docs" {
		t.Errorf("ResolveClassForRoots deepest root = %q, want child-docs", cls)
	}
	if eff != config.EffortLow {
		t.Errorf("ResolveClassForRoots deepest root effort = %q, want %q", eff, config.EffortLow)
	}
}
