package verifier

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// =============================================================================
// Guarantee 1: Class resolution cannot be steered outside the registry.
// Feed path sets: case variants (DOCS/x.md, Docs/x.go), unicode look-alikes
// in extensions (x.mD, x.mD .go), a path that IS a directory (docs/),
// traversal shapes (docs/../cmd/x.go, ././README.md, docs//x.md), 64KB paths,
// NUL bytes, empty strings mixed with valid paths, a path list where ONE entry
// matches docs and the rest match nothing.
// Verify: verdict is either a registered class by the documented all-paths rule,
// or unregistered — never a registered class that the rule table does not justify,
// never a panic, exit semantics per the CLI contract.
// =============================================================================

func TestRedtest_Guarantee1_CaseVariants(t *testing.T) {
	v := NewVerifier()

	cases := []struct {
		name      string
		paths     []string
		wantClass string
		wantReg   bool
	}{
		{
			name:      "Uppercase directory DOCS/x.md does not match lowercase docs/**",
			paths:     []string{"DOCS/x.md"},
			wantClass: "unregistered",
			wantReg:   false,
		},
		{
			name:      "Titlecase directory Docs/x.go does not match docs/** or *_test.go",
			paths:     []string{"Docs/x.go"},
			wantClass: "unregistered",
			wantReg:   false,
		},
		{
			name:      "Uppercase extension in non-docs directory OTHER/x.MD",
			paths:     []string{"OTHER/x.MD"},
			wantClass: "unregistered",
			wantReg:   false,
		},
		{
			name:      "Mixed case Docs/x.MD does not match docs/**",
			paths:     []string{"Docs/x.MD"},
			wantClass: "unregistered",
			wantReg:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			class, reg := v.registry.Resolve(tc.paths)
			if reg != tc.wantReg {
				t.Fatalf("paths %v: got registered=%v, want %v", tc.paths, reg, tc.wantReg)
			}
			if class.Name != tc.wantClass {
				t.Fatalf("paths %v: got class=%q, want %q", tc.paths, class.Name, tc.wantClass)
			}

			verdict, err := v.Verify(TaskRef{ID: "t1", ReceiptID: "r1", AllowedPaths: tc.paths}, TaskRef{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if verdict.Registered != tc.wantReg || verdict.Class != tc.wantClass {
				t.Fatalf("verdict mismatch: registered=%v, class=%q", verdict.Registered, verdict.Class)
			}
		})
	}
}

func TestRedtest_Guarantee1_UnicodeLookalikes(t *testing.T) {
	v := NewVerifier()

	cases := []struct {
		name  string
		paths []string
	}{
		{
			name:  "extension x.mD with capital D",
			paths: []string{"x.mD"},
		},
		{
			name:  "unicode Cyrillic small letter a in x.mа",
			paths: []string{"x.m\u0430"}, // Cyrillic 'а' (U+0430) instead of ASCII 'a'
		},
		{
			name:  "trailing non-breaking space x.md\\u00A0",
			paths: []string{"x.md\u00A0"},
		},
		{
			name:  "double extension with space x.mD .go",
			paths: []string{"x.mD .go"},
		},
		{
			name:  "unicode Greek omicron in dоcs/x.md",
			paths: []string{"d\u03BFcs/x.md"}, // Greek omicron (U+03BF) instead of ASCII 'o'
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			class, reg := v.registry.Resolve(tc.paths)
			if reg {
				t.Fatalf("paths %v must NOT resolve to registered class, got class=%q", tc.paths, class.Name)
			}
			if class.Name != "unregistered" {
				t.Fatalf("paths %v: got class=%q, want unregistered", tc.paths, class.Name)
			}

			verdict, err := v.Verify(TaskRef{ID: "t1", ReceiptID: "r1", AllowedPaths: tc.paths}, TaskRef{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if verdict.Registered {
				t.Fatalf("verdict for %v should be unregistered, got registered=true", tc.paths)
			}
		})
	}
}

func TestRedtest_Guarantee1_DirectoryPath(t *testing.T) {
	v := NewVerifier()

	t.Run("Bare docs/ directory matches docs/**", func(t *testing.T) {
		class, reg := v.registry.Resolve([]string{"docs/"})
		if !reg || class.Name != "docs" {
			t.Fatalf("docs/ must resolve to docs class per globstar rules, got reg=%v, class=%q", reg, class.Name)
		}
	})

	t.Run("Unregistered directory cmd/ resolves unregistered", func(t *testing.T) {
		class, reg := v.registry.Resolve([]string{"cmd/"})
		if reg || class.Name != "unregistered" {
			t.Fatalf("cmd/ must resolve to unregistered, got reg=%v, class=%q", reg, class.Name)
		}
	})

	t.Run("Tools directory tools/ matches test class", func(t *testing.T) {
		class, reg := v.registry.Resolve([]string{"tools/"})
		if !reg || class.Name != "test" {
			t.Fatalf("tools/ must resolve to test class per tools/** rule, got reg=%v, class=%q", reg, class.Name)
		}
	})
}

func TestRedtest_Guarantee1_TraversalShapes(t *testing.T) {
	v := NewVerifier()

	t.Run("Traversal escaping docs into cmd/x.go resolves unregistered", func(t *testing.T) {
		// docs/../cmd/x.go cleans to cmd/x.go which is NOT docs
		class, reg := v.registry.Resolve([]string{"docs/../cmd/x.go"})
		if reg || class.Name != "unregistered" {
			t.Fatalf("docs/../cmd/x.go must resolve to unregistered, got reg=%v, class=%q", reg, class.Name)
		}
	})

	t.Run("Redundant relative dot ././README.md cleans to README.md and matches docs", func(t *testing.T) {
		class, reg := v.registry.Resolve([]string{"././README.md"})
		if !reg || class.Name != "docs" {
			t.Fatalf("././README.md must resolve to docs, got reg=%v, class=%q", reg, class.Name)
		}
	})

	t.Run("Double slash docs//x.md cleans to docs/x.md and matches docs", func(t *testing.T) {
		class, reg := v.registry.Resolve([]string{"docs//x.md"})
		if !reg || class.Name != "docs" {
			t.Fatalf("docs//x.md must resolve to docs, got reg=%v, class=%q", reg, class.Name)
		}
	})

	t.Run("Upward traversal ../../etc/passwd resolves unregistered", func(t *testing.T) {
		class, reg := v.registry.Resolve([]string{"../../etc/passwd"})
		if reg || class.Name != "unregistered" {
			t.Fatalf("../../etc/passwd must resolve to unregistered, got reg=%v, class=%q", reg, class.Name)
		}
	})
}

func TestRedtest_Guarantee1_64KBPaths(t *testing.T) {
	v := NewVerifier()

	// 64KB long path
	longName := strings.Repeat("a", 65536)

	t.Run("64KB filename resolves unregistered without panic", func(t *testing.T) {
		class, reg := v.registry.Resolve([]string{longName})
		if reg || class.Name != "unregistered" {
			t.Fatalf("64KB path must resolve to unregistered, got reg=%v, class=%q", reg, class.Name)
		}
	})

	t.Run("64KB path inside docs resolves docs without panic", func(t *testing.T) {
		docsLong := filepath.Join("docs", longName+".md")
		class, reg := v.registry.Resolve([]string{docsLong})
		if !reg || class.Name != "docs" {
			t.Fatalf("64KB docs path must resolve to docs, got reg=%v, class=%q", reg, class.Name)
		}
	})
}

func TestRedtest_Guarantee1_NULBytes(t *testing.T) {
	v := NewVerifier()

	cases := []struct {
		name  string
		paths []string
	}{
		{
			name:  "NUL byte in filename docs/x\\x00.md",
			paths: []string{"docs/x\x00.md"},
		},
		{
			name:  "NUL byte after README.md\\x00",
			paths: []string{"README.md\x00"},
		},
		{
			name:  "NUL byte in directory name doc\\x00s/x.md",
			paths: []string{"doc\x00s/x.md"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on NUL byte path: %v", r)
				}
			}()
			// NUL bytes inside filepath patterns should not panic and shouldn't falsely match
			class, reg := v.registry.Resolve(tc.paths)
			// If it matches docs/**, verify it is strictly justified; if it doesn't, must be unregistered
			if reg && class.Name != "docs" {
				t.Fatalf("unexpected registered class %q", class.Name)
			}
		})
	}
}

func TestRedtest_Guarantee1_EmptyStringsMixedWithValidPaths(t *testing.T) {
	v := NewVerifier()

	cases := []struct {
		name  string
		paths []string
	}{
		{
			name:  "README.md mixed with empty string",
			paths: []string{"README.md", ""},
		},
		{
			name:  "empty string mixed with docs/x.md",
			paths: []string{"", "docs/x.md"},
		},
		{
			name:  "only empty string",
			paths: []string{""},
		},
		{
			name:  "multiple empty strings",
			paths: []string{"", ""},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			class, reg := v.registry.Resolve(tc.paths)
			if reg {
				t.Fatalf("paths %v with empty string must be unregistered, got %q", tc.paths, class.Name)
			}
			if class.Name != "unregistered" {
				t.Fatalf("paths %v: got class=%q, want unregistered", tc.paths, class.Name)
			}

			verdict, err := v.Verify(TaskRef{ID: "t1", ReceiptID: "r1", AllowedPaths: tc.paths}, TaskRef{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if verdict.Registered {
				t.Fatalf("verdict must be unregistered, got registered=true")
			}
		})
	}
}

func TestRedtest_Guarantee1_PartialMatchDocsAndNothing(t *testing.T) {
	v := NewVerifier()

	cases := []struct {
		name  string
		paths []string
	}{
		{
			name:  "docs/x.md and random unmatched file",
			paths: []string{"docs/x.md", "unknown/file.xyz"},
		},
		{
			name:  "README.md and Go production code",
			paths: []string{"README.md", "cmd/g8s/main.go"},
		},
		{
			name:  "plans/plan.md and Makefile",
			paths: []string{"plans/plan.md", "Makefile"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			class, reg := v.registry.Resolve(tc.paths)
			if reg {
				t.Fatalf("partial match %v must NOT register, got class=%q", tc.paths, class.Name)
			}
			if class.Name != "unregistered" {
				t.Fatalf("paths %v: got class=%q, want unregistered", tc.paths, class.Name)
			}

			verdict, err := v.Verify(TaskRef{ID: "t1", ReceiptID: "r1", AllowedPaths: tc.paths}, TaskRef{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if verdict.Registered || verdict.Outcome != OutcomeUnregistered {
				t.Fatalf("expected unregistered verdict, got %+v", verdict)
			}
		})
	}
}

// =============================================================================
// Guarantee 2: The unregistered floor cannot be flipped by input shape.
// A receipt whose allowed_paths is an empty JSON array, a JSON string (not array),
// an array of non-strings, JSON with a huge array (10k paths) — verify
// unregistered/human-acceptance or a typed error, never class=docs.
// =============================================================================

func TestRedtest_Guarantee2_EmptyJSONArray(t *testing.T) {
	v := NewVerifier()

	// Receipt payload with empty JSON array []
	payload := `{"receipt_id":"rcpt-empty","allowed_paths":[]}`
	var req struct {
		ReceiptID    string   `json:"receipt_id"`
		AllowedPaths []string `json:"allowed_paths"`
	}
	if err := json.Unmarshal([]byte(payload), &req); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	target := TaskRef{
		ID:           "task-empty",
		ReceiptID:    req.ReceiptID,
		AllowedPaths: req.AllowedPaths,
	}

	verdict, err := v.Verify(target, TaskRef{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.Registered {
		t.Fatalf("empty allowed_paths must never be registered, got class=%q", verdict.Class)
	}
	if verdict.Class != "unregistered" {
		t.Fatalf("got class=%q, want unregistered", verdict.Class)
	}
	if verdict.Outcome != OutcomeUnregistered {
		t.Fatalf("got outcome=%q, want unregistered", verdict.Outcome)
	}
}

func TestRedtest_Guarantee2_JSONStringNotArray(t *testing.T) {
	v := NewVerifier()

	// Receipt payload where allowed_paths is a JSON string instead of an array
	payload := `{"receipt_id":"rcpt-str","allowed_paths":"docs/README.md"}`
	var req struct {
		ReceiptID    string   `json:"receipt_id"`
		AllowedPaths []string `json:"allowed_paths"`
	}
	err := json.Unmarshal([]byte(payload), &req)
	if err == nil {
		t.Fatal("expected json.Unmarshal to fail when allowed_paths is a string")
	}

	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("expected *json.UnmarshalTypeError, got %T: %v", err, err)
	}

	// When unmarshal fails, AllowedPaths stays nil/empty
	target := TaskRef{
		ID:           "task-str",
		ReceiptID:    req.ReceiptID,
		AllowedPaths: req.AllowedPaths,
	}
	verdict, vErr := v.Verify(target, TaskRef{})
	if vErr != nil {
		t.Fatalf("unexpected error: %v", vErr)
	}
	if verdict.Class == "docs" || verdict.Registered {
		t.Fatalf("must never resolve to docs on malformed JSON string, got class=%q", verdict.Class)
	}
}

func TestRedtest_Guarantee2_ArrayOfNonStrings(t *testing.T) {
	v := NewVerifier()

	// Receipt payload where allowed_paths contains numbers, booleans, objects
	payloads := []string{
		`{"receipt_id":"rcpt-num","allowed_paths":[123, 456]}`,
		`{"receipt_id":"rcpt-bool","allowed_paths":[true, false]}`,
		`{"receipt_id":"rcpt-obj","allowed_paths":[{"file":"docs/x.md"}]}`,
		`{"receipt_id":"rcpt-null","allowed_paths":[null]}`,
	}

	for _, payload := range payloads {
		var req struct {
			ReceiptID    string   `json:"receipt_id"`
			AllowedPaths []string `json:"allowed_paths"`
		}
		err := json.Unmarshal([]byte(payload), &req)
		if err == nil && len(req.AllowedPaths) > 0 && req.AllowedPaths[0] != "" {
			t.Fatalf("expected unmarshal to fail or produce empty/null string for payload: %s", payload)
		}

		target := TaskRef{
			ID:           "task-nonstring",
			ReceiptID:    req.ReceiptID,
			AllowedPaths: req.AllowedPaths,
		}
		verdict, vErr := v.Verify(target, TaskRef{})
		if vErr != nil {
			t.Fatalf("unexpected verify error: %v", vErr)
		}
		if verdict.Class == "docs" || verdict.Registered {
			t.Fatalf("must never resolve to docs for non-string payload %s: got class=%q", payload, verdict.Class)
		}
	}
}

func TestRedtest_Guarantee2_HugeArray10KPaths(t *testing.T) {
	v := NewVerifier()

	// 10,000 non-docs paths
	const pathCount = 10000
	paths := make([]string, pathCount)
	for i := 0; i < pathCount; i++ {
		paths[i] = fmt.Sprintf("non_docs_dir_%d/file_%d.txt", i%10, i)
	}

	payloadBytes, err := json.Marshal(map[string]any{
		"receipt_id":    "rcpt-huge",
		"allowed_paths": paths,
	})
	if err != nil {
		t.Fatalf("json marshal failed: %v", err)
	}

	var req struct {
		ReceiptID    string   `json:"receipt_id"`
		AllowedPaths []string `json:"allowed_paths"`
	}
	if err := json.Unmarshal(payloadBytes, &req); err != nil {
		t.Fatalf("json unmarshal failed: %v", err)
	}

	target := TaskRef{
		ID:           "task-huge",
		ReceiptID:    req.ReceiptID,
		AllowedPaths: req.AllowedPaths,
	}

	verdict, err := v.Verify(target, TaskRef{})
	if err != nil {
		t.Fatalf("verify failed on 10k paths: %v", err)
	}
	if verdict.Registered {
		t.Fatalf("10k non-docs paths must NOT be registered, got class=%q", verdict.Class)
	}
	if verdict.Class != "unregistered" {
		t.Fatalf("got class=%q, want unregistered", verdict.Class)
	}
}

// =============================================================================
// Guarantee 3: Hard-status promotion stays citation-gated.
// Craft registry variants: founding_catch = "#", "#abc", "incident:",
// "INCIDENT:x", "#123 #456" (two), whitespace-only, a 1MB citation.
// Verify status:hard without a valid citation degrades to unregistered
// (fail-closed) in EVERY malformed case — and that a valid #<digits>
// citation still loads hard (the gate is not over-tight).
// =============================================================================

func TestRedtest_Guarantee3_MalformedCitationsDegradeToUnregistered(t *testing.T) {
	malformedCitations := []struct {
		name     string
		citation string
	}{
		{name: "bare hash", citation: "#"},
		{name: "hash with non-digits", citation: "#abc"},
		{name: "bare incident prefix", citation: "incident:"},
		{name: "uppercase incident prefix", citation: "INCIDENT:x"},
		{name: "whitespace only", citation: "   "},
		{name: "empty string", citation: ""},
	}

	for _, tc := range malformedCitations {
		t.Run(tc.name, func(t *testing.T) {
			yamlData := fmt.Sprintf(`
classes:
  - name: test-hard
    status: hard
    founding_catch: %q
    priority: 10
    paths:
      - "*.md"
`, tc.citation)

			reg, err := ParseRegistry([]byte(yamlData))
			if err != nil {
				// ParseRegistry may return an error on empty or malformed; that is fail-closed
				return
			}
			// Verify status:hard entry was dropped / degraded to unregistered
			for _, c := range reg.Classes() {
				if c.Name == "test-hard" {
					t.Fatalf("citation %q: expected test-hard to be dropped from registry, but found: %+v", tc.citation, c)
				}
			}

			// Resolving paths under this registry must yield unregistered
			class, regOk := reg.Resolve([]string{"README.md"})
			if regOk || class.Name != "unregistered" {
				t.Fatalf("citation %q: expected unregistered resolve, got class=%q, reg=%v", tc.citation, class.Name, regOk)
			}
		})
	}
}

func TestRedtest_Guarantee3_1MBCitation(t *testing.T) {
	// A 1MB citation
	oneMB := strings.Repeat("x", 1024*1024)
	yamlData := fmt.Sprintf(`
classes:
  - name: test-hard
    status: hard
    founding_catch: %q
    priority: 10
    paths:
      - "*.md"
`, oneMB)

	reg, _ := ParseRegistry([]byte(yamlData))
	// Either error or dropped: must not register test-hard
	if reg != nil {
		for _, c := range reg.Classes() {
			if c.Name == "test-hard" {
				t.Fatalf("1MB citation must not load as hard class")
			}
		}
		class, regOk := reg.Resolve([]string{"README.md"})
		if regOk || class.Name != "unregistered" {
			t.Fatalf("expected unregistered for 1MB citation, got class=%q reg=%v", class.Name, regOk)
		}
	}
}

func TestRedtest_Guarantee3_ValidCitationsPreserveHardStatus(t *testing.T) {
	validCitations := []struct {
		name     string
		citation string
	}{
		{name: "standard issue citation", citation: "#123"},
		{name: "large issue number", citation: "#999999"},
		{name: "incident slug", citation: "incident:cve-2026-001"},
		{name: "incident with dashes and underscores", citation: "incident:infra_drift-2026_09"},
	}

	for _, tc := range validCitations {
		t.Run(tc.name, func(t *testing.T) {
			yamlData := fmt.Sprintf(`
classes:
  - name: test-hard
    status: hard
    founding_catch: %q
    priority: 10
    paths:
      - "*.md"
`, tc.citation)

			reg, err := ParseRegistry([]byte(yamlData))
			if err != nil {
				t.Fatalf("citation %q: unexpected parse error: %v", tc.citation, err)
			}
			found := false
			for _, c := range reg.Classes() {
				if c.Name == "test-hard" {
					found = true
					if c.Status != StatusHard {
						t.Fatalf("citation %q: got status=%q, want hard", tc.citation, c.Status)
					}
				}
			}
			if !found {
				t.Fatalf("citation %q: valid citation must load as hard class, but was dropped", tc.citation)
			}

			class, regOk := reg.Resolve([]string{"README.md"})
			if !regOk || class.Status != StatusHard {
				t.Fatalf("expected Resolve to return hard class for valid citation %q, got reg=%v, status=%v", tc.citation, regOk, class.Status)
			}
		})
	}
}

// BUG(REDTEST): status: hard with founding_catch = "#123 #456" (two citations)
// is accepted instead of degrading to unregistered (fail-closed) because
// citationIssueRegex = regexp.MustCompile(`^#\d+`) matches the prefix #123
// without asserting an end anchor ($) or single token boundary.
// Repro:
// 1. Create registry YAML with status: hard and founding_catch: "#123 #456"
// 2. ParseRegistry([]byte(yamlData)) -> succeeds and loads Class with status: hard!
// 3. Expected per Brief Guarantee 3: degrade to unregistered (fail-closed).
func TestRedtest_Guarantee3_TwoCitationsMalformed(t *testing.T) {
	yamlData := `
classes:
  - name: test-hard
    status: hard
    founding_catch: "#123 #456"
    priority: 10
    paths:
      - "*.md"
`
	reg, err := ParseRegistry([]byte(yamlData))
	if err != nil {
		return // dropped/error is acceptable fail-closed behavior
	}

	for _, c := range reg.Classes() {
		if c.Name == "test-hard" {
			t.Fatalf("malformed citation '#123 #456' (two citations) must degrade to unregistered, but was loaded as status=%q", c.Status)
		}
	}

	class, regOk := reg.Resolve([]string{"README.md"})
	if regOk && class.Status == StatusHard {
		t.Fatalf("Resolve must not return status: hard for '#123 #456'")
	}
}

// =============================================================================
// Guarantee 4: The self-grade guard cannot be bypassed.
// Verify with caller == target (typed refusal), caller set but target
// differing only by case/whitespace, empty caller with a target
// (allowed — supervisor context), and the --as-task flag parsing with
// weird values.
// =============================================================================

func TestRedtest_Guarantee4_SelfGradeRefusal_ExactMatch(t *testing.T) {
	v := NewVerifier()

	target := TaskRef{
		ID:           "task-worker-42",
		ReceiptID:    "rcpt-1",
		AllowedPaths: []string{"README.md"},
	}
	caller := TaskRef{
		ID: "task-worker-42",
	}

	_, err := v.Verify(target, caller)
	if err == nil {
		t.Fatal("expected error when caller == target, got nil")
	}

	if !errors.Is(err, ErrSelfGrade) {
		t.Fatalf("expected errors.Is(err, ErrSelfGrade), got: %v", err)
	}

	var selfGradeErr *SelfGradeError
	if !errors.As(err, &selfGradeErr) {
		t.Fatalf("expected *SelfGradeError typed error, got %T: %v", err, err)
	}
	if selfGradeErr.CallerID != "task-worker-42" || selfGradeErr.TargetID != "task-worker-42" {
		t.Fatalf("unexpected SelfGradeError fields: %+v", selfGradeErr)
	}
}

func TestRedtest_Guarantee4_EmptyCaller_SupervisorContext(t *testing.T) {
	v := NewVerifier()

	target := TaskRef{
		ID:           "task-worker-42",
		ReceiptID:    "rcpt-1",
		AllowedPaths: []string{"README.md"},
	}
	caller := TaskRef{
		ID: "", // empty caller: supervisor context
	}

	verdict, err := v.Verify(target, caller)
	if err != nil {
		t.Fatalf("empty caller in supervisor context must be allowed, got error: %v", err)
	}
	if verdict.Class != "docs" {
		t.Fatalf("expected docs class, got %q", verdict.Class)
	}
}

// BUG(REDTEST): Self-grade guard can be bypassed when caller differs from target
// only by case (e.g. caller="TASK-42", target="task-42") or leading/trailing
// whitespace (e.g. caller="task-42 ", target="task-42") because verifier.go:367
// performs raw string equality `caller.ID != "" && caller.ID == target.ID`
// without case normalization (strings.EqualFold) or whitespace trimming (strings.TrimSpace).
// Repro:
// 1. target = TaskRef{ID: "task-42", AllowedPaths: ["README.md"], ReceiptID: "r1"}
// 2. caller = TaskRef{ID: "TASK-42"} (or "task-42 ")
// 3. v.Verify(target, caller) -> succeeds without returning ErrSelfGrade!
func TestRedtest_Guarantee4_SelfGradeBypass_CaseAndWhitespace(t *testing.T) {
	v := NewVerifier()
	target := TaskRef{
		ID:           "task-42",
		ReceiptID:    "rcpt-1",
		AllowedPaths: []string{"README.md"},
	}

	bypassAttempts := []struct {
		name     string
		callerID string
	}{
		{name: "uppercase caller", callerID: "TASK-42"},
		{name: "trailing space caller", callerID: "task-42 "},
		{name: "leading space caller", callerID: " task-42"},
		{name: "mixed case caller", callerID: "Task-42"},
		{name: "surrounding whitespace caller", callerID: "\ttask-42\n"},
	}

	for _, tc := range bypassAttempts {
		t.Run(tc.name, func(t *testing.T) {
			caller := TaskRef{ID: tc.callerID}
			_, err := v.Verify(target, caller)
			if err == nil {
				t.Fatalf("self-grade guard bypassed with caller %q for target %q: expected ErrSelfGrade", tc.callerID, target.ID)
			}
			if !errors.Is(err, ErrSelfGrade) {
				t.Fatalf("caller %q: expected ErrSelfGrade, got %v", tc.callerID, err)
			}
		})
	}
}

func TestRedtest_Guarantee4_AsTaskFlagParsing_WeirdValues(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantTask   string
		wantAsTask string
		wantErr    bool
	}{
		{
			name:       "empty as-task flag value",
			args:       []string{"--task", "t-1", "--as-task", ""},
			wantTask:   "t-1",
			wantAsTask: "",
		},
		{
			name:       "whitespace only as-task flag",
			args:       []string{"--task", "t-1", "--as-task", "   "},
			wantTask:   "t-1",
			wantAsTask: "   ",
		},
		{
			name:       "flag looking string as value",
			args:       []string{"--task", "t-1", "--as-task=--help"},
			wantTask:   "t-1",
			wantAsTask: "--help",
		},
		{
			name:       "NUL byte in as-task",
			args:       []string{"--task", "t-1", "--as-task", "t-1\x00"},
			wantTask:   "t-1",
			wantAsTask: "t-1\x00",
		},
		{
			name:       "newlines in as-task",
			args:       []string{"--task", "t-1", "--as-task", "t-1\nt-2"},
			wantTask:   "t-1",
			wantAsTask: "t-1\nt-2",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("verify", flag.ContinueOnError)
			taskFlag := fs.String("task", "", "")
			asTaskFlag := fs.String("as-task", "", "")
			err := fs.Parse(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("args %v: got error=%v, wantErr=%v", tc.args, err, tc.wantErr)
			}
			if !tc.wantErr {
				if *taskFlag != tc.wantTask {
					t.Errorf("got task=%q, want %q", *taskFlag, tc.wantTask)
				}
				if *asTaskFlag != tc.wantAsTask {
					t.Errorf("got as-task=%q, want %q", *asTaskFlag, tc.wantAsTask)
				}
			}
		})
	}
}
