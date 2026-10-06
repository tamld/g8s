package lane

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/telemetry"
)

// TestLoadEffortClasses_SeededRegistry loads the repository's seeded .g8s/effort-classes.yml
// and validates that all seeded classes are present and well-formed.
func TestLoadEffortClasses_SeededRegistry(t *testing.T) {
	ec, err := LoadEffortClasses()
	if err != nil {
		t.Fatalf("LoadEffortClasses() failed: %v", err)
	}
	if ec == nil {
		t.Fatal("LoadEffortClasses() returned nil")
	}
	if ec.SchemaVersion != CurrentEffortClassSchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", ec.SchemaVersion, CurrentEffortClassSchemaVersion)
	}
	if len(ec.Classes) == 0 {
		t.Fatal("expected seeded classes, got 0")
	}

	classMap := make(map[string]EffortClass)
	for _, c := range ec.Classes {
		classMap[c.Name] = c
	}

	// Verify docs, test, red-cell, feature classes
	expected := []struct {
		name       string
		wantEffort string
	}{
		{"docs", config.EffortLow},
		{"test", config.EffortMedium},
		{"feature", config.EffortHigh},
		{"red-cell", config.EffortHigh},
	}
	for _, tc := range expected {
		c, ok := classMap[tc.name]
		if !ok {
			t.Errorf("missing seeded class %q", tc.name)
			continue
		}
		if c.DefaultEffort != tc.wantEffort {
			t.Errorf("class %q default_effort = %q, want %q", tc.name, c.DefaultEffort, tc.wantEffort)
		}
		if len(c.Paths) == 0 {
			t.Errorf("class %q has no paths", tc.name)
		}
	}
}

// TestParseEffortClasses_Table tests parsing, validation, and error cases.
func TestParseEffortClasses_Table(t *testing.T) {
	tests := []struct {
		name        string
		yaml        string
		wantErr     bool
		errContains string
	}{
		{
			name: "valid minimal",
			yaml: `
schema_version: "effort-classes.v1"
classes:
  - name: docs
    default_effort: low
    priority: 10
    paths:
      - "*.md"
`,
			wantErr: false,
		},
		{
			name: "valid flow paths",
			yaml: `
schema_version: "effort-classes.v1"
classes:
  - name: test
    default_effort: medium
    priority: 20
    paths: ["*_test.go", "**/*_test.go"]
`,
			wantErr: false,
		},
		{
			name: "wrong schema version",
			yaml: `
schema_version: "effort-classes.v2"
classes:
  - name: docs
    default_effort: low
    priority: 10
    paths: ["*.md"]
`,
			wantErr:     true,
			errContains: "unsupported schema version",
		},
		{
			name: "empty class name refused",
			yaml: `
schema_version: "effort-classes.v1"
classes:
  - name: ""
    default_effort: low
    priority: 10
    paths: ["*.md"]
`,
			wantErr:     true,
			errContains: "name",
		},
		{
			name: "unknown effort level refused",
			yaml: `
schema_version: "effort-classes.v1"
classes:
  - name: docs
    default_effort: turbo
    priority: 10
    paths: ["*.md"]
`,
			wantErr:     true,
			errContains: "default_effort",
		},
		{
			name: "unknown effort level invalid naming class and field",
			yaml: `
schema_version: "effort-classes.v1"
classes:
  - name: analytics
    default_effort: superhigh
    priority: 5
    paths: ["analytics/**"]
`,
			wantErr:     true,
			errContains: `effort class "analytics": field "default_effort": unknown effort level "superhigh"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ec, err := ParseEffortClasses([]byte(tc.yaml))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (ec=%+v)", ec)
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.errContains)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if ec == nil {
					t.Fatal("expected non-nil EffortClasses")
				}
			}
		})
	}
}

// TestResolveClass_PriorityOrdering asserts first match by lowest priority number wins.
func TestResolveClass_PriorityOrdering(t *testing.T) {
	yaml := `
schema_version: "effort-classes.v1"
classes:
  - name: catch_all_docs
    default_effort: low
    priority: 50
    paths:
      - "docs/**"
  - name: critical_docs
    default_effort: high
    priority: 10
    paths:
      - "docs/security/**"
`
	ec, err := ParseEffortClasses([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseEffortClasses failed: %v", err)
	}

	// docs/security/audit.md matches both catch_all_docs and critical_docs
	// critical_docs has priority 10 < 50, so it must win.
	cls, eff := ec.ResolveClass([]string{"docs/security/audit.md"})
	if cls != "critical_docs" {
		t.Errorf("class = %q, want critical_docs", cls)
	}
	if eff != config.EffortHigh {
		t.Errorf("effort = %q, want %q", eff, config.EffortHigh)
	}

	// docs/general.md only matches catch_all_docs
	cls2, eff2 := ec.ResolveClass([]string{"docs/general.md"})
	if cls2 != "catch_all_docs" {
		t.Errorf("class = %q, want catch_all_docs", cls2)
	}
	if eff2 != config.EffortLow {
		t.Errorf("effort = %q, want %q", eff2, config.EffortLow)
	}
}

// TestResolveClass_TieBreakAlphabetical asserts ties in priority are broken deterministically by name.
func TestResolveClass_TieBreakAlphabetical(t *testing.T) {
	yaml := `
schema_version: "effort-classes.v1"
classes:
  - name: beta
    default_effort: medium
    priority: 20
    paths:
      - "common/**"
  - name: alpha
    default_effort: high
    priority: 20
    paths:
      - "common/**"
`
	ec, err := ParseEffortClasses([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseEffortClasses failed: %v", err)
	}

	cls, _ := ec.ResolveClass([]string{"common/file.go"})
	if cls != "alpha" {
		t.Errorf("class = %q, want alpha (alphabetical tie-break)", cls)
	}
}

// TestResolveClass_AllPathsMustMatch asserts that if one path is outside globs, the class does not match.
func TestResolveClass_AllPathsMustMatch(t *testing.T) {
	yaml := `
schema_version: "effort-classes.v1"
classes:
  - name: docs
    default_effort: low
    priority: 10
    paths:
      - "*.md"
`
	ec, err := ParseEffortClasses([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseEffortClasses failed: %v", err)
	}

	// Mixed paths: one .md and one .go -> must not match docs
	cls, eff := ec.ResolveClass([]string{"README.md", "main.go"})
	if cls != "unregistered" {
		t.Errorf("class = %q, want unregistered", cls)
	}
	if eff != config.EffortMedium {
		t.Errorf("effort = %q, want medium", eff)
	}
}

// TestResolveClass_EmptyPathsAndUnregistered asserts fallback to unregistered / medium.
func TestResolveClass_EmptyPathsAndUnregistered(t *testing.T) {
	ec := &EffortClasses{
		SchemaVersion: CurrentEffortClassSchemaVersion,
		Classes: []EffortClass{
			{Name: "docs", DefaultEffort: "low", Priority: 10, Paths: []string{"*.md"}},
		},
	}

	// Empty paths
	c1, e1 := ec.ResolveClass(nil)
	if c1 != "unregistered" || e1 != config.EffortMedium {
		t.Errorf("ResolveClass(nil) = (%q, %q), want (unregistered, medium)", c1, e1)
	}

	// Nil receiver
	var nilEC *EffortClasses
	c2, e2 := nilEC.ResolveClass([]string{"README.md"})
	if c2 != "unregistered" || e2 != config.EffortMedium {
		t.Errorf("nilEC.ResolveClass = (%q, %q), want (unregistered, medium)", c2, e2)
	}

	// No match
	c3, e3 := ec.ResolveClass([]string{"internal/worker/worker.go"})
	if c3 != "unregistered" || e3 != config.EffortMedium {
		t.Errorf("unregistered match = (%q, %q), want (unregistered, medium)", c3, e3)
	}
}

// TestLoadEffortClasses_MissingFile_FailOpen asserts that a missing file returns
// an empty resolver that falls back to unregistered / medium.
func TestLoadEffortClasses_MissingFile_FailOpen(t *testing.T) {
	tempDir := t.TempDir()
	nonExistent := filepath.Join(tempDir, "missing.yml")

	ec, err := LoadEffortClassesFile(nonExistent)
	if err != nil {
		t.Fatalf("LoadEffortClassesFile on missing file returned error: %v", err)
	}
	if ec == nil {
		t.Fatal("expected non-nil empty resolver")
	}

	cls, eff := ec.ResolveClass([]string{"anything.go"})
	if cls != "unregistered" || eff != config.EffortMedium {
		t.Errorf("missing file resolution = (%q, %q), want (unregistered, medium)", cls, eff)
	}
}

// TestOverrideLogic asserts override-down detection and silent override-up / equal.
func TestOverrideLogic(t *testing.T) {
	// Canonical ladder: none < minimal < low < medium < high < xhigh < max

	// Override DOWN cases: flag < class default
	downCases := []struct {
		flagEffort   string
		classDefault string
	}{
		{config.EffortLow, config.EffortMedium},
		{config.EffortLow, config.EffortHigh},
		{config.EffortMedium, config.EffortHigh},
		{config.EffortMinimal, config.EffortLow},
		{config.EffortNone, config.EffortHigh},
	}
	for _, tc := range downCases {
		if !IsOverrideDown(tc.flagEffort, tc.classDefault) {
			t.Errorf("IsOverrideDown(%q, %q) = false, want true", tc.flagEffort, tc.classDefault)
		}
	}

	// Override UP or SAME cases: flag >= class default -> NOT override down (silent)
	notDownCases := []struct {
		flagEffort   string
		classDefault string
	}{
		{config.EffortHigh, config.EffortMedium},   // override up
		{config.EffortHigh, config.EffortLow},      // override up
		{config.EffortMedium, config.EffortLow},    // override up
		{config.EffortMax, config.EffortHigh},      // override up
		{config.EffortMedium, config.EffortMedium}, // same
		{config.EffortHigh, config.EffortHigh},     // same
		{config.EffortLow, config.EffortLow},       // same
	}
	for _, tc := range notDownCases {
		if IsOverrideDown(tc.flagEffort, tc.classDefault) {
			t.Errorf("IsOverrideDown(%q, %q) = true, want false (override up or equal must be silent)", tc.flagEffort, tc.classDefault)
		}
	}
}

// TestSeededResolution asserts the seeded rules match properly.
func TestSeededResolution(t *testing.T) {
	// 1. docs -> low
	cls, eff := ResolveClass([]string{"docs/architecture.md"})
	if cls != "docs" || eff != config.EffortLow {
		t.Errorf("docs/architecture.md = (%q, %q), want (docs, low)", cls, eff)
	}

	// 2. test -> medium
	cls, eff = ResolveClass([]string{"internal/lane/effortclass_test.go"})
	if cls != "test" || eff != config.EffortMedium {
		t.Errorf("effortclass_test.go = (%q, %q), want (test, medium)", cls, eff)
	}

	// 3. tools -> feature -> high
	cls, eff = ResolveClass([]string{"tools/ci_doc_contract_check.sh"})
	if cls != "feature" || eff != config.EffortHigh {
		t.Errorf("tools/ci_doc_contract_check.sh = (%q, %q), want (feature, high)", cls, eff)
	}

	// 4. plans red-cell -> red-cell -> high
	cls, eff = ResolveClass([]string{"plans/261002-redteam/brief-R1-retry-signal-trust.md"})
	if cls != "red-cell" || eff != config.EffortHigh {
		t.Errorf("plans/261002-redteam/... = (%q, %q), want (red-cell, high)", cls, eff)
	}

	// 5. production Go code -> unregistered -> medium
	cls, eff = ResolveClass([]string{"internal/worker/worker.go"})
	if cls != "unregistered" || eff != config.EffortMedium {
		t.Errorf("internal/worker/worker.go = (%q, %q), want (unregistered, medium)", cls, eff)
	}
}

// TestTelemetryEventFields_Present verifies that TraceEvent contains all required
// telemetry fields per W2 (class, effort_requested, effort_applied, effort_mismatch,
// input_tokens, output_tokens, duration_seconds) and serializes them properly.
func TestTelemetryEventFields_Present(t *testing.T) {
	ev := telemetry.TraceEvent{
		ID:              "trace-123",
		TaskID:          "task-456",
		EventType:       telemetry.TraceEventTaskCompleted,
		Class:           "docs",
		EffortClass:     "docs",
		EffortRequested: config.EffortLow,
		EffortApplied:   config.EffortLow,
		EffortMismatch:  false,
		InputTokens:     1024,
		OutputTokens:    256,
		DurationSeconds: 12.34,
		Payload: map[string]any{
			"class":            "docs",
			"effort_class":     "docs",
			"effort_requested": config.EffortLow,
			"effort_applied":   config.EffortLow,
			"effort_mismatch":  false,
			"input_tokens":     1024,
			"output_tokens":    256,
			"duration_seconds": 12.34,
		},
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	requiredKeys := []string{
		"class",
		"effort_class",
		"effort_requested",
		"effort_applied",
		"input_tokens",
		"output_tokens",
		"duration_seconds",
	}
	for _, k := range requiredKeys {
		if _, ok := parsed[k]; !ok {
			t.Errorf("TraceEvent JSON missing key %q", k)
		}
	}

	payloadMap, ok := parsed["payload"].(map[string]any)
	if !ok {
		t.Fatalf("TraceEvent JSON payload is not a map: %v", parsed["payload"])
	}
	for _, k := range requiredKeys {
		if _, ok := payloadMap[k]; !ok {
			t.Errorf("TraceEvent Payload missing key %q", k)
		}
	}
}
