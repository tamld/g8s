package premise

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func buildFixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("go.mod", "module example.com/fix\n\ngo 1.22\n")
	mustWrite("cmd/g8s/main.go", `package main

import "os"

func main() {
	switch os.Args[1] {
	case "init":
	case "doctor":
	case "help", "--help", "-h":
	}
}
`)
	mustWrite("cmd/g8s/sub.go", `package main

func sub(arg string) {
	switch arg {
	case "triage":
	}
}
`)
	mustWrite("internal/sample/sample.go", `package sample

type Widget struct{}

const MaxWidgets = 10

func ComputeWidget() int { return 42 }
`)
	mustWrite("internal/sample/sample_test.go", `package sample

import "testing"

func TestWidgetPass(t *testing.T) {
	if ComputeWidget() != 42 {
		t.Fatal("bad")
	}
}
`)
	mustWrite("manifest.json", `{
  "specifications": [
    {"id": "DELTA-01", "path": "spec/openspec/01.md"},
    {"id": "DELTA-02", "path": "spec/openspec/02.md"}
  ]
}`)
	mustWrite(".g8s/feature-map.yml", `schema_version: "1.0"
features:
  - id: FEAT-INIT-01
    title: Init
    persona_story: story
    srs_reqs: [REQ-1]
    code_paths: [cmd/g8s/main.go]
    invariants: [inv]
    visible_tests: [internal/sample/sample_test.go::TestWidgetPass]
    verifier_class: VC-UNIT
`)
	return dir
}

func TestCheckPremisesComprehensive(t *testing.T) {
	repo := buildFixtureRepo(t)
	text := strings.Join([]string{
		"The g8s CLI is missing init, doctor, uninstall and update commands.",
		"The `triage` command is missing.",
		"The CLI exposes 2 top-level commands.",
		"The repository registers 2 OpenSpec deltas.",
		"DELTA-01 is registered in manifest.json.",
		"DELTA-99 is already registered in manifest.json.",
		"FEAT-INIT-01 exists in the feature map.",
		"FEAT-NONEXISTENT-99 exists in the feature map.",
		"File `internal/sample/sample.go` exists.",
		"File `internal/sample/*.go` exists.",
		"File `internal/sample/missing.go` exists.",
		"Function `ComputeWidget()` exists.",
		"Function `MissingFunc()` exists.",
		"TestWidgetPass passes.",
		"TestNonExistent passes.",
		"CI run #42 is failing with exit code 1 and sha256:abcdef1234567890.",
		"The architecture is clean and modular.",
		"Implement the missing commands now.",
	}, " ")

	rep, err := Check(context.Background(), text, Options{
		RepoRoot: repo,
		RunTests: false,
	})
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if !rep.Contradicted() || rep.Verdict != VerdictContradicted {
		t.Fatalf("expected CONTRADICTED verdict, got %s (summary: %+v)", rep.Verdict, rep.Summary)
	}
	if len(rep.ContradictedClaims()) == 0 {
		t.Fatal("expected non-empty ContradictedClaims()")
	}
	if rep.Summary.MechanicalVerified == 0 || rep.Summary.Observational == 0 || rep.Summary.Unverified == 0 {
		t.Fatalf("expected all tiers populated, got %+v", rep.Summary)
	}

	// Verify specific command claims: init/doctor CONTRADICTED, uninstall/update MECHANICAL_VERIFIED.
	bySubj := map[string]Claim{}
	for _, c := range rep.Claims {
		if c.Kind == KindCommandExists {
			bySubj[c.Subject] = c
		}
	}
	if bySubj["init"].Status != StatusContradicted || bySubj["doctor"].Status != StatusContradicted {
		t.Fatalf("expected init and doctor CONTRADICTED, got init=%s doctor=%s", bySubj["init"].Status, bySubj["doctor"].Status)
	}
	if bySubj["uninstall"].Status != StatusMechanicalVerified || bySubj["update"].Status != StatusMechanicalVerified {
		t.Fatalf("expected uninstall and update MECHANICAL_VERIFIED, got uninstall=%s update=%s", bySubj["uninstall"].Status, bySubj["update"].Status)
	}
	if !strings.Contains(bySubj["triage"].Note, "subcommand") {
		t.Errorf("expected triage note to mention subcommand, got %q", bySubj["triage"].Note)
	}
}

func TestCheckWithRunTestsAndLLMDecompose(t *testing.T) {
	repo := buildFixtureRepo(t)

	complete := func(ctx context.Context, system, prompt string) (string, error) {
		return "```json\n" + `{
			"claims": [
				{"text": "CLI command init is present", "kind": "command_exists", "subject": "g8s init", "asserted": "present"},
				{"text": "CLI exposes 2 commands", "kind": "command_count", "subject": "commands", "asserted": "equals", "value": 2},
				{"text": "Prose claim from LLM", "kind": "prose", "subject": "ignored", "asserted": "stated"},
				{"text": "Bad kind", "kind": "invalid_kind", "subject": "x", "asserted": "present"},
				{"text": "Bad assertion", "kind": "file_exists", "subject": "go.mod", "asserted": "equals"}
			]
		}` + "\n```", nil
	}

	rep, err := Check(context.Background(), "TestWidgetPass passes.", Options{
		RepoRoot:        repo,
		RunTests:        true,
		TestTimeout:     30 * time.Second,
		Complete:        complete,
		DecomposerLabel: "mock-llm",
	})
	if err != nil {
		t.Fatalf("Check with RunTests+LLM failed: %v", err)
	}
	if rep.Verdict != VerdictClear {
		t.Fatalf("expected CLEAR verdict, got %s (%+v)", rep.Verdict, rep.Claims)
	}
	if rep.Decomposer != "deterministic+mock-llm" {
		t.Errorf("Decomposer = %q, want deterministic+mock-llm", rep.Decomposer)
	}

	// Verify LLM error fallback adds a warning and still succeeds.
	repWarn, err := Check(context.Background(), "File `go.mod` exists.", Options{
		RepoRoot: repo,
		Complete: func(ctx context.Context, system, prompt string) (string, error) {
			return "", errors.New("llm offline")
		},
	})
	if err != nil || len(repWarn.Warnings) == 0 {
		t.Fatalf("expected warning on LLM failure, err=%v warnings=%v", err, repWarn.Warnings)
	}

	// Empty input error.
	if _, err := Check(context.Background(), "   ", Options{RepoRoot: repo}); err == nil {
		t.Fatal("expected error on empty input")
	}
}
