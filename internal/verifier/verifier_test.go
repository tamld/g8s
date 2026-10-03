package verifier

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/telemetry"
)

// TestTableDrivenVerifier covers all 9 required test cases from Brief H1:
//  1. unregistered-class floor
//  2. hard-without-citation refused
//  3. self-grade refusal
//  4. docs slice (all-prose paths) resolves docs
//  5. test slice (brief + *_test.go) resolves test NOT docs
//  6. mixed code path -> unregistered
//  7. malformed YAML -> unregistered not panic
//  8. script check ok/fail paths
//  9. go-test check runner-error => not-run
func TestTableDrivenVerifier(t *testing.T) {
	tests := []struct {
		name          string
		yamlData      string
		target        TaskRef
		caller        TaskRef
		mockRunner    CommandRunner
		wantErr       error
		wantClass     string
		wantReg       bool
		wantOutcome   Outcome
		wantCheckFail bool
	}{
		{
			name: "1. unregistered-class floor (unknown paths fall back to unregistered)",
			yamlData: `
classes:
  - name: docs
    status: advisory
    founding_catch: "#208"
    priority: 10
    paths:
      - "*.md"
`,
			target: TaskRef{
				ID:           "task-unreg",
				ReceiptID:    "rcpt-1",
				AllowedPaths: []string{"unknown/nonmatching/path.txt"},
			},
			wantClass:   "unregistered",
			wantReg:     false,
			wantOutcome: OutcomeUnregistered,
		},
		{
			name: "2. hard-without-citation refused (hard status without valid citation dropped from registry)",
			yamlData: `
classes:
  - name: docs
    status: hard
    founding_catch: "no-valid-citation"
    priority: 10
    paths:
      - "*.md"
`,
			target: TaskRef{
				ID:           "task-hard-nocite",
				ReceiptID:    "rcpt-1",
				AllowedPaths: []string{"README.md"},
			},
			wantClass:   "unregistered",
			wantReg:     false,
			wantOutcome: OutcomeUnregistered,
		},
		{
			name: "3. self-grade refusal (caller equal to target refused with typed error)",
			yamlData: `
classes:
  - name: docs
    status: advisory
    founding_catch: "#208"
    priority: 10
    paths:
      - "*.md"
`,
			target: TaskRef{
				ID:           "task-self",
				ReceiptID:    "rcpt-1",
				AllowedPaths: []string{"README.md"},
			},
			caller: TaskRef{
				ID: "task-self",
			},
			wantErr: ErrSelfGrade,
		},
		{
			name: "4. docs slice (all-prose paths) resolves docs",
			yamlData: `
classes:
  - name: docs
    status: advisory
    founding_catch: "#208"
    priority: 10
    paths:
      - "*.md"
      - "docs/**"
      - "plans/**"
      - "skills/**"
      - "assets/**"
      - "offer/**"
    checks:
      - type: script
        run: tools/ci_doc_contract_check.sh
  - name: test
    status: advisory
    founding_catch: "#510"
    priority: 20
    paths:
      - "*_test.go"
      - "**/*_test.go"
      - "tools/**"
      - "plans/**"
`,
			target: TaskRef{
				ID:        "task-docs",
				ReceiptID: "rcpt-docs",
				AllowedPaths: []string{
					"README.md",
					"docs/architecture.md",
					"plans/261002-factory/plan.md",
					"skills/manifest.json",
				},
			},
			mockRunner: func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
				return []byte("ok"), nil, 0, nil
			},
			wantClass:   "docs",
			wantReg:     true,
			wantOutcome: OutcomePass,
		},
		{
			name: "5. test slice (brief + *_test.go) resolves test NOT docs",
			yamlData: `
classes:
  - name: docs
    status: advisory
    founding_catch: "#208"
    priority: 10
    paths:
      - "*.md"
      - "docs/**"
      - "plans/**"
      - "skills/**"
      - "assets/**"
      - "offer/**"
  - name: test
    status: advisory
    founding_catch: "#510"
    priority: 20
    paths:
      - "*_test.go"
      - "**/*_test.go"
      - "tools/**"
      - "plans/**"
    checks:
      - type: go-test
        packages: []
`,
			target: TaskRef{
				ID:        "task-test",
				ReceiptID: "rcpt-test",
				AllowedPaths: []string{
					"plans/261002-factory/brief-H1-verifier-registry.md",
					"cmd/g8s/verify_test.go",
				},
			},
			mockRunner: func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
				return []byte("ok"), nil, 0, nil
			},
			wantClass:   "test",
			wantReg:     true,
			wantOutcome: OutcomePass,
		},
		{
			name: "6. mixed code path -> unregistered",
			yamlData: `
classes:
  - name: docs
    status: advisory
    founding_catch: "#208"
    priority: 10
    paths:
      - "*.md"
      - "docs/**"
  - name: test
    status: advisory
    founding_catch: "#510"
    priority: 20
    paths:
      - "*_test.go"
      - "**/*_test.go"
`,
			target: TaskRef{
				ID:        "task-mixed",
				ReceiptID: "rcpt-mixed",
				AllowedPaths: []string{
					"internal/verifier/verifier.go",
					"internal/verifier/verifier_test.go",
				},
			},
			wantClass:   "unregistered",
			wantReg:     false,
			wantOutcome: OutcomeUnregistered,
		},
		{
			name: "7. malformed YAML -> unregistered not panic",
			yamlData: `
classes:
  ::: this is invalid yaml [[[
  bad syntax !!!
`,
			target: TaskRef{
				ID:           "task-malformed",
				ReceiptID:    "rcpt-1",
				AllowedPaths: []string{"README.md"},
			},
			wantClass:   "unregistered",
			wantReg:     false,
			wantOutcome: OutcomeUnregistered,
		},
		{
			name: "8a. script check ok path",
			yamlData: `
classes:
  - name: docs
    status: advisory
    founding_catch: "#208"
    priority: 10
    paths:
      - "*.md"
    checks:
      - type: script
        run: tools/ci_doc_contract_check.sh
`,
			target: TaskRef{
				ID:           "task-script-ok",
				ReceiptID:    "rcpt-1",
				AllowedPaths: []string{"README.md"},
			},
			mockRunner: func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
				return []byte("all contracts passed"), nil, 0, nil
			},
			wantClass:   "docs",
			wantReg:     true,
			wantOutcome: OutcomePass,
		},
		{
			name: "8b. script check fail path",
			yamlData: `
classes:
  - name: docs
    status: advisory
    founding_catch: "#208"
    priority: 10
    paths:
      - "*.md"
    checks:
      - type: script
        run: tools/ci_doc_contract_check.sh
`,
			target: TaskRef{
				ID:           "task-script-fail",
				ReceiptID:    "rcpt-1",
				AllowedPaths: []string{"README.md"},
			},
			mockRunner: func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
				return nil, []byte("contract drift error"), 1, nil
			},
			wantClass:     "docs",
			wantReg:       true,
			wantOutcome:   OutcomeFail,
			wantCheckFail: true,
		},
		{
			name: "9. go-test check runner-error => not-run",
			yamlData: `
classes:
  - name: test
    status: advisory
    founding_catch: "#510"
    priority: 20
    paths:
      - "*_test.go"
    checks:
      - type: go-test
        packages: ["./cmd/g8s"]
`,
			target: TaskRef{
				ID:           "task-runner-err",
				ReceiptID:    "rcpt-1",
				AllowedPaths: []string{"main_test.go"},
			},
			mockRunner: func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
				// Simulates infrastructure / runner error (e.g. binary not found)
				return nil, nil, -1, errors.New("exec: \"go\": executable file not found in $PATH")
			},
			wantClass:   "test",
			wantReg:     true,
			wantOutcome: OutcomeNotRun,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := []Option{
				WithRegistryData([]byte(tt.yamlData)),
			}
			if tt.mockRunner != nil {
				opts = append(opts, WithCommandRunner(tt.mockRunner))
			}
			// Mock repo root to a temp directory with a dummy script so file exists
			tmpDir := t.TempDir()
			dummyScript := filepath.Join(tmpDir, "tools/ci_doc_contract_check.sh")
			_ = os.MkdirAll(filepath.Dir(dummyScript), 0o755)
			_ = os.WriteFile(dummyScript, []byte("#!/bin/sh\nexit 0\n"), 0o755)
			opts = append(opts, WithRepoRoot(tmpDir))

			var emittedEvents []Verdict
			opts = append(opts, WithTelemetrySink(func(taskID string, v Verdict) {
				emittedEvents = append(emittedEvents, v)
			}))

			v := NewVerifier(opts...)
			verdict, err := v.Verify(tt.target, tt.caller)

			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tt.wantErr)
				}
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if verdict.Class != tt.wantClass {
				t.Errorf("class = %q, want %q", verdict.Class, tt.wantClass)
			}
			if verdict.Registered != tt.wantReg {
				t.Errorf("registered = %v, want %v", verdict.Registered, tt.wantReg)
			}
			if verdict.Outcome != tt.wantOutcome {
				t.Errorf("outcome = %q, want %q", verdict.Outcome, tt.wantOutcome)
			}

			// Verify telemetry was emitted
			if len(emittedEvents) != 1 {
				t.Errorf("expected 1 telemetry event emitted, got %d", len(emittedEvents))
			} else {
				ev := emittedEvents[0]
				if ev.Class != verdict.Class || ev.Outcome != verdict.Outcome {
					t.Errorf("telemetry payload mismatch: got class=%s outcome=%s", ev.Class, ev.Outcome)
				}
			}

			if tt.wantCheckFail {
				hasFail := false
				for _, c := range verdict.Checks {
					if !c.OK {
						hasFail = true
						break
					}
				}
				if !hasFail {
					t.Errorf("expected at least one check to fail in checks: %+v", verdict.Checks)
				}
			}
		})
	}
}

// TestSeededRegistry verifies that .g8s/verifier-classes.yml contains exactly two advisory
// classes ('docs' and 'test') with founding catches and valid checks.
func TestSeededRegistry(t *testing.T) {
	reg, err := LoadRegistry(DefaultRegistryPath)
	if err != nil {
		t.Fatalf("failed to load seeded registry from %s: %v", DefaultRegistryPath, err)
	}

	classes := reg.Classes()
	if len(classes) != 2 {
		t.Fatalf("expected exactly 2 seeded classes, got %d", len(classes))
	}

	classMap := make(map[string]Class)
	for _, c := range classes {
		classMap[c.Name] = c
	}

	// 1. docs class checks
	docs, exists := classMap["docs"]
	if !exists {
		t.Fatal("seeded registry missing 'docs' class")
	}
	if docs.Status != StatusAdvisory {
		t.Errorf("docs status = %q, want advisory", docs.Status)
	}
	if !isValidCitation(docs.FoundingCatch) {
		t.Errorf("docs founding catch %q is not a valid citation", docs.FoundingCatch)
	}
	if len(docs.Checks) != 2 {
		t.Errorf("docs checks count = %d, want 2", len(docs.Checks))
	}
	if docs.Checks[0].Run != "tools/ci_doc_contract_check.sh" {
		t.Errorf("docs check[0] run = %q, want tools/ci_doc_contract_check.sh", docs.Checks[0].Run)
	}
	if docs.Checks[1].Run != "tools/ci_link_integrity.sh" {
		t.Errorf("docs check[1] run = %q, want tools/ci_link_integrity.sh", docs.Checks[1].Run)
	}

	// 2. test class checks
	testCls, exists := classMap["test"]
	if !exists {
		t.Fatal("seeded registry missing 'test' class")
	}
	if testCls.Status != StatusAdvisory {
		t.Errorf("test status = %q, want advisory", testCls.Status)
	}
	if !isValidCitation(testCls.FoundingCatch) {
		t.Errorf("test founding catch %q is not a valid citation", testCls.FoundingCatch)
	}
	if len(testCls.Checks) != 1 {
		t.Fatalf("test checks count = %d, want 1", len(testCls.Checks))
	}
	if testCls.Checks[0].Type != CheckTypeGoTest {
		t.Errorf("test check[0] type = %q, want %q", testCls.Checks[0].Type, CheckTypeGoTest)
	}
}

// TestCitationValidation tests the citation regex pattern matching.
func TestCitationValidation(t *testing.T) {
	valid := []string{
		"#208",
		"#510",
		"#208 (docs drift)",
		"#510 — red team vacuous seeds",
		"incident:vacuous-seeds",
		"incident:drift-check",
		"incident:seed_123 (details)",
	}
	for _, c := range valid {
		if !isValidCitation(c) {
			t.Errorf("expected citation %q to be valid", c)
		}
	}

	invalid := []string{
		"",
		" ",
		"none",
		"208",
		"#",
		"#abc",
		"incident:",
		"issue 208",
		"bugfix",
	}
	for _, c := range invalid {
		if isValidCitation(c) {
			t.Errorf("expected citation %q to be invalid", c)
		}
	}
}

// TestDuplicateClassesRefused verifies that if two entries have duplicate names, both are refused.
func TestDuplicateClassesRefused(t *testing.T) {
	yamlData := `
classes:
  - name: docs
    status: advisory
    founding_catch: "#208"
    priority: 10
    paths: ["*.md"]
  - name: docs
    status: advisory
    founding_catch: "#208"
    priority: 20
    paths: ["*.txt"]
`
	reg, err := ParseRegistry([]byte(yamlData))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(reg.Classes()) != 0 {
		t.Errorf("expected duplicate class 'docs' to be refused, got %d classes", len(reg.Classes()))
	}
}

// TestUnknownCheckTypeRefused verifies that an entry with an unknown check type is unregistered.
func TestUnknownCheckTypeRefused(t *testing.T) {
	yamlData := `
classes:
  - name: custom
    status: advisory
    founding_catch: "#123"
    priority: 10
    paths: ["*.md"]
    checks:
      - type: unknown-linter
        run: tools/foo.sh
`
	reg, err := ParseRegistry([]byte(yamlData))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(reg.Classes()) != 0 {
		t.Errorf("expected class with unknown check type to be unregistered, got %d classes", len(reg.Classes()))
	}
}

// TestReadOnlyTaskUnregistered verifies that a task without receipt_id or allowed paths resolves to unregistered.
func TestReadOnlyTaskUnregistered(t *testing.T) {
	v := NewVerifier(WithRegistryData([]byte(`
classes:
  - name: docs
    status: advisory
    founding_catch: "#208"
    priority: 10
    paths: ["*.md"]
`)))
	// No receipt
	verdict, err := v.Verify(TaskRef{ID: "task-ro", ReceiptID: ""}, TaskRef{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.Class != "unregistered" || verdict.Registered != false {
		t.Errorf("verdict = %+v, want unregistered/false", verdict)
	}

	// Empty allowed paths
	verdict2, err := v.Verify(TaskRef{ID: "task-empty", ReceiptID: "rcpt-1", AllowedPaths: []string{}}, TaskRef{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict2.Class != "unregistered" || verdict2.Registered != false {
		t.Errorf("verdict = %+v, want unregistered/false", verdict2)
	}
}

// TestLiveTelemetryIntegration verifies that the default telemetry ingestion path works with a SQLite engine.
func TestLiveTelemetryIntegration(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "telemetry.db")
	t.Setenv("G8S_TELEMETRY_DB", dbPath)

	v := NewVerifier(WithRegistryData([]byte(`
classes:
  - name: docs
    status: advisory
    founding_catch: "#208"
    priority: 10
    paths: ["*.md"]
`)))

	target := TaskRef{
		ID:           "task-tel-1",
		ReceiptID:    "rcpt-1",
		AllowedPaths: []string{"README.md"},
	}

	verdict, err := v.Verify(target, TaskRef{})
	if err != nil {
		t.Fatalf("unexpected verify error: %v", err)
	}

	// Query telemetry database to verify the event was ingested
	cfg := telemetry.DefaultTelemetryConfig()
	cfg.DBPath = dbPath
	eng, err := telemetry.NewTelemetryEngine(cfg)
	if err != nil {
		t.Fatalf("open telemetry engine: %v", err)
	}
	defer eng.Close()

	// Wait briefly for async ingestion queue
	time.Sleep(100 * time.Millisecond)

	tid := "task-tel-1"
	events, err := eng.QueryEvents(context.Background(), telemetry.TraceFilter{
		TaskID: &tid,
	})
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least 1 telemetry event in database")
	}

	found := false
	for _, ev := range events {
		if ev.EventType == telemetry.TraceEventVerifierVerdict {
			found = true
			if ev.Payload["class"] != verdict.Class {
				t.Errorf("payload class = %v, want %v", ev.Payload["class"], verdict.Class)
			}
			break
		}
	}
	if !found {
		t.Errorf("verifier_verdict event not found in %v", events)
	}
}

// TestPackageLevelFunctions tests default package-level Verify and Resolve functions.
func TestPackageLevelFunctions(t *testing.T) {
	cls, ok := Resolve([]string{"README.md"})
	if !ok || cls.Name != "docs" {
		t.Errorf("expected docs resolution from package-level Resolve, got cls=%+v, ok=%v", cls, ok)
	}

	verdict, err := Verify(TaskRef{
		ID:           "task-pkg",
		ReceiptID:    "", // read-only
		AllowedPaths: nil,
	}, TaskRef{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.Class != "unregistered" {
		t.Errorf("verdict.Class = %q, want unregistered", verdict.Class)
	}
}

// TestDerivePackagesFromPaths tests package derivation rules.
func TestDerivePackagesFromPaths(t *testing.T) {
	// 1. Both non-test and test files: prefers non-test files
	pkgs1 := derivePackagesFromPaths([]string{
		"cmd/g8s/verify.go",
		"cmd/g8s/verify_test.go",
		"internal/verifier/verifier.go",
	})
	if len(pkgs1) != 2 || pkgs1[0] != "./cmd/g8s" || pkgs1[1] != "./internal/verifier" {
		t.Errorf("pkgs1 = %v, want [./cmd/g8s ./internal/verifier]", pkgs1)
	}

	// 2. Only test files: derives from test files
	pkgs2 := derivePackagesFromPaths([]string{
		"cmd/g8s/verify_test.go",
		"plans/brief.md",
	})
	if len(pkgs2) != 1 || pkgs2[0] != "./cmd/g8s" {
		t.Errorf("pkgs2 = %v, want [./cmd/g8s]", pkgs2)
	}

	// 3. Root directory Go file
	pkgs3 := derivePackagesFromPaths([]string{"main.go"})
	if len(pkgs3) != 1 || pkgs3[0] != "." {
		t.Errorf("pkgs3 = %v, want [.]", pkgs3)
	}

	// 4. No Go files: nil
	pkgs4 := derivePackagesFromPaths([]string{"docs/guide.md", "tools/ci.sh"})
	if len(pkgs4) != 0 {
		t.Errorf("pkgs4 = %v, want empty", pkgs4)
	}
}

// TestHardClassWithValidCitation verifies that a hard class with a valid citation loads and runs.
func TestHardClassWithValidCitation(t *testing.T) {
	yamlData := `
classes:
  - name: security
    status: hard
    founding_catch: "incident:cve-2026-001"
    priority: 5
    paths: ["internal/receipt/**"]
    checks:
      - type: script
        run: tools/nonexistent_runner_test.sh
`
	v := NewVerifier(
		WithRegistryData([]byte(yamlData)),
		WithRepoRoot(t.TempDir()), // nonexistent script will trigger runner error
	)

	cls, ok := v.registry.Resolve([]string{"internal/receipt/receipt.go"})
	if !ok || cls.Name != "security" {
		t.Fatalf("expected security class to resolve, got %+v, ok=%v", cls, ok)
	}
	if cls.Status != StatusHard {
		t.Errorf("status = %q, want hard", cls.Status)
	}

	verdict, err := v.Verify(TaskRef{
		ID:           "task-sec",
		ReceiptID:    "rcpt-sec",
		AllowedPaths: []string{"internal/receipt/receipt.go"},
	}, TaskRef{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Runner error because script doesn't exist -> OutcomeNotRun (fail-open)
	if verdict.Outcome != OutcomeNotRun {
		t.Errorf("verdict.Outcome = %q, want not-run", verdict.Outcome)
	}
}
