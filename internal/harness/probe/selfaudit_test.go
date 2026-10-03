package probe

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/dispatch"
	"github.com/tamld/g8s/internal/orchestrator"
)

// TestDefaultSuite_SelfAuditProbesRegistered verifies that DefaultSuite contains
// all four required self-audit probes by name, each with a non-empty incident citation.
func TestDefaultSuite_SelfAuditProbesRegistered(t *testing.T) {
	suite := DefaultSuite()
	if suite == nil {
		t.Fatal("DefaultSuite() returned nil")
	}

	requiredProbes := map[string]string{
		"refusal-echo":       "#443",
		"worktree-discard":   "#443", // #443-f2 / #450
		"sanitizer-fidelity": "#434", // #434 / #445
		"receipt-bypass":     "#",    // #359 / #366
	}

	found := make(map[string]*Probe)
	for _, p := range suite.Probes {
		if _, ok := requiredProbes[p.Name]; ok {
			found[p.Name] = p
		}
	}

	for name, expectedCitation := range requiredProbes {
		p, exists := found[name]
		if !exists {
			t.Errorf("probe %q missing from DefaultSuite()", name)
			continue
		}

		if p.Category != CategorySelfAudit {
			t.Errorf("probe %q category = %q, want %q", name, p.Category, CategorySelfAudit)
		}

		if strings.TrimSpace(p.Description) == "" {
			t.Errorf("probe %q has empty description", name)
		}

		if !strings.Contains(p.Description, expectedCitation) {
			t.Errorf("probe %q description %q does not contain incident citation %q", name, p.Description, expectedCitation)
		}

		if p.ExpectedOutcome != OutcomeClassCompleted {
			t.Errorf("probe %q expected outcome = %q, want %q", name, p.ExpectedOutcome, OutcomeClassCompleted)
		}

		if p.Runner == nil {
			t.Errorf("probe %q has nil Runner", name)
		}
	}
}

// TestEvalList_SurfacesSelfAuditProbes verifies that probe.List surfaces all four probes.
func TestEvalList_SurfacesSelfAuditProbes(t *testing.T) {
	suite := DefaultSuite()
	lines := List(suite)

	expectedNames := []string{"refusal-echo", "worktree-discard", "sanitizer-fidelity", "receipt-bypass"}
	for _, name := range expectedNames {
		matched := false
		for _, line := range lines {
			if strings.Contains(line, name) && strings.Contains(line, string(CategorySelfAudit)) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("probe.List(suite) did not surface self-audit probe %q", name)
		}
	}

	filtered := FilterSuite(suite, nil, string(CategorySelfAudit))
	if len(filtered.Probes) != 4 {
		t.Errorf("FilterSuite for category %q returned %d probes, want 4", CategorySelfAudit, len(filtered.Probes))
	}
}

// TestSelfAudit_RefusalEcho_Predicate tests the predicate logic for refusal-echo (#443).
func TestSelfAudit_RefusalEcho_Predicate(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name      string
		stream    string
		wantPass  bool
		errSubstr string
	}{
		{
			name:     "recovered-run: mid-stream refusal echo + clean SUCCESS result",
			stream:   RecoveredRunStreamShape,
			wantPass: true,
		},
		{
			name: "clean SUCCESS without any refusal echo",
			stream: "{\"event\":\"init\",\"init\":{\"model\":\"gemini-3.8-flash-high\"}}\n" +
				"{\"event\":\"result\",\"result\":{\"status\":\"SUCCESS\",\"response\":\"Clean task response.\"}}\n",
			wantPass: true,
		},
		{
			name: "refusal in final deliverable response must fail",
			stream: "{\"event\":\"init\",\"init\":{\"model\":\"gemini-3.8-flash-high\"}}\n" +
				"{\"event\":\"result\",\"result\":{\"status\":\"SUCCESS\",\"response\":\"This request was blocked by Gemini's filters.\"}}\n",
			wantPass:  false,
			errSubstr: "blocked",
		},
		{
			name: "prompt-level refusal without result event must fail",
			stream: "{\"event\":\"init\",\"init\":{\"model\":\"gemini-3.8-flash-high\"}}\n" +
				"{\"event\":\"step_update\",\"step_update\":{\"step_type\":\"agent_response\",\"text_delta\":\"This request was blocked by Gemini's filters.\"}}\n",
			wantPass:  false,
			errSubstr: "blocked",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outcome, err := AuditRefusalEcho(ctx, tc.stream)
			if tc.wantPass {
				if err != nil {
					t.Fatalf("AuditRefusalEcho unexpected error: %v", err)
				}
				if outcome != OutcomeClassCompleted {
					t.Errorf("outcome = %q, want %q", outcome, OutcomeClassCompleted)
				}
			} else {
				if err == nil {
					t.Fatalf("AuditRefusalEcho expected error, got outcome %q", outcome)
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.errSubstr)
				}
			}
		})
	}
}

// TestSelfAudit_WorktreeDiscard_Predicate tests the predicate logic for worktree-discard (#443-f2, #450).
func TestSelfAudit_WorktreeDiscard_Predicate(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available in PATH")
	}

	ctx := context.Background()

	t.Run("audit helper passes end-to-end", func(t *testing.T) {
		outcome, err := AuditWorktreeDiscard(ctx)
		if err != nil {
			t.Fatalf("AuditWorktreeDiscard failed: %v", err)
		}
		if outcome != OutcomeClassCompleted {
			t.Errorf("outcome = %q, want %q", outcome, OutcomeClassCompleted)
		}
	})

	t.Run("sa-002 extended hostile lifecycle preserves worktree and deliverables", func(t *testing.T) {
		probe := NewWorktreeDiscardProbe()
		outcome, err := probe.Runner(ctx, nil)
		if err != nil {
			t.Fatalf("sa-002 probe runner failed under hostile lifecycle: %v", err)
		}
		if outcome != OutcomeClassCompleted {
			t.Errorf("outcome = %q, want %q", outcome, OutcomeClassCompleted)
		}
	})

	t.Run("table of keep modes", func(t *testing.T) {
		tests := []struct {
			name          string
			keep          bool
			makeDirty     func(t *testing.T, wtPath string)
			wantDirExists bool
		}{
			{
				name: "dirty untracked file preserved when keep=true",
				keep: true,
				makeDirty: func(t *testing.T, wtPath string) {
					if err := os.WriteFile(filepath.Join(wtPath, "new.txt"), []byte("payload"), 0o644); err != nil {
						t.Fatalf("write file: %v", err)
					}
				},
				wantDirExists: true,
			},
			{
				name: "dirty worktree removed when keep=false",
				keep: false,
				makeDirty: func(t *testing.T, wtPath string) {
					if err := os.WriteFile(filepath.Join(wtPath, "new.txt"), []byte("payload"), 0o644); err != nil {
						t.Fatalf("write file: %v", err)
					}
				},
				wantDirExists: false,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				tmpDir := t.TempDir()
				repoDir := filepath.Join(tmpDir, "repo")
				if err := os.MkdirAll(repoDir, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				cmds := [][]string{
					{"git", "init", "-q", "-b", "master"},
					{"git", "config", "user.email", "audit@test"},
					{"git", "config", "user.name", "audit"},
					{"git", "config", "commit.gpgsign", "false"},
					{"git", "commit", "--allow-empty", "-m", "init", "-q"},
				}
				for _, c := range cmds {
					cmd := exec.Command(c[0], c[1:]...)
					cmd.Dir = repoDir
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("%v: %v (%s)", c, err, out)
					}
				}

				pool, err := orchestrator.NewPool(orchestrator.PoolOptions{
					Repo: repoDir,
					Root: filepath.Join(tmpDir, "worktrees"),
				})
				if err != nil {
					t.Fatalf("NewPool: %v", err)
				}

				wt, err := pool.Acquire(ctx, "sub-test")
				if err != nil {
					t.Fatalf("Acquire: %v", err)
				}

				tc.makeDirty(t, wt.Path)

				if err := pool.Release(ctx, wt, tc.keep); err != nil {
					t.Fatalf("Release: %v", err)
				}

				_, statErr := os.Stat(wt.Path)
				dirExists := !os.IsNotExist(statErr)
				if dirExists != tc.wantDirExists {
					t.Errorf("dirExists = %v, want %v", dirExists, tc.wantDirExists)
				}
			})
		}
	})
}

// TestSelfAudit_SanitizerFidelity_Predicate tests the predicate logic for sanitizer-fidelity (#434, #445).
func TestSelfAudit_SanitizerFidelity_Predicate(t *testing.T) {
	if err := AuditSanitizerFidelity(); err != nil {
		t.Fatalf("AuditSanitizerFidelity failed: %v", err)
	}

	tests := []struct {
		name     string
		input    string
		validate func(t *testing.T, in, out string)
	}{
		{
			name:  "case 1: exact issue repro JSON with public literals and escapes",
			input: `{"code":"def public_fixture(u):\n    token = False\n    return u.password is None and token is False\n","quoted_data":"fixture-payload-0042"}`,
			validate: func(t *testing.T, in, out string) {
				if out != in {
					t.Fatalf("case 1: want byte-exact identical output, got %q", out)
				}
			},
		},
		{
			name:  "case 2: escaped quote inside secret preserves JSONL validity",
			input: `{"type":"item.completed","text":"set password: abc\"def\" here"}`,
			validate: func(t *testing.T, in, out string) {
				if !strings.Contains(out, "password=<REDACTED>") {
					t.Errorf("case 2: expected password=<REDACTED>, got %q", out)
				}
				if strings.Contains(out, `abc"def"`) || strings.Contains(out, `abc\"def\"`) {
					t.Errorf("case 2: secret value leaked in %q", out)
				}
			},
		},
		{
			name:  "case 3: source_lines array with public literal",
			input: `{"source_lines":["def public_fixture(u):","    token = False","    return u.password is None and token is False"]}`,
			validate: func(t *testing.T, in, out string) {
				if out != in {
					t.Fatalf("case 3: want byte-exact unchanged output, got %q", out)
				}
			},
		},
		{
			name:  "case 4: public literal keeps following escape and content",
			input: `{"code":"token = False\ngood = True"}`,
			validate: func(t *testing.T, in, out string) {
				if out != in {
					t.Fatalf("case 4: want byte-exact unchanged output, got %q", out)
				}
			},
		},
		{
			name:  "case 5: public literal function arguments unchanged",
			input: `{"code":"x = f(token=False, retries=0, timeout=None)"}`,
			validate: func(t *testing.T, in, out string) {
				if out != in {
					t.Fatalf("case 5: want byte-exact unchanged output, got %q", out)
				}
			},
		},
		{
			name:  "case 6: non-public secret followed by escape is redacted",
			input: `{"cred":"credential=example-value-77\nnext chunk"}`,
			validate: func(t *testing.T, in, out string) {
				if !strings.Contains(out, "credential=<REDACTED>") && !strings.Contains(out, "cred=<REDACTED>") {
					t.Errorf("case 6: expected credential redaction, got %q", out)
				}
				if strings.Contains(out, "example-value-77") {
					t.Errorf("case 6: secret leaked in %q", out)
				}
			},
		},
		{
			name:  "case 7: URL fully redacted preserving trailing escape",
			input: "{\"dsn\":\"postgresql://user:pass@db.example.com/sales\\n\"}",
			validate: func(t *testing.T, in, out string) {
				if !strings.Contains(out, "postgresql://<REDACTED>") {
					t.Errorf("case 7: expected postgresql redaction, got %q", out)
				}
				if strings.Contains(out, "user:pass") {
					t.Errorf("case 7: credentials leaked in %q", out)
				}
				if !strings.HasSuffix(out, `\n"}`) {
					t.Errorf("case 7: trailing escape missing in %q", out)
				}
			},
		},
		{
			name:  "case 8: escaped quotes around the value normalized",
			input: `password:\"example-value-77\"`,
			validate: func(t *testing.T, in, out string) {
				if out != "password=<REDACTED>" {
					t.Errorf("case 8: want password=<REDACTED>, got %q", out)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := dispatch.SanitizeOutput(tc.input)
			tc.validate(t, tc.input, out)
		})
	}
}

// TestSelfAudit_ReceiptBypass_Predicate tests the predicate logic for receipt-bypass (#359, #366).
func TestSelfAudit_ReceiptBypass_Predicate(t *testing.T) {
	ctx := context.Background()

	outcome, err := AuditReceiptBypass(ctx)
	if err != nil {
		t.Fatalf("AuditReceiptBypass failed: %v", err)
	}
	if outcome != OutcomeClassCompleted {
		t.Errorf("outcome = %q, want %q", outcome, OutcomeClassCompleted)
	}

	pathCases := []struct {
		name      string
		allowed   []string
		candidate string
		wantPass  bool
	}{
		{"exact match", []string{"internal/harness/probe.go"}, "internal/harness/probe.go", true},
		{"wildcard glob in dir", []string{"internal/harness/*"}, "internal/harness/probe.go", true},
		{"wildcard glob subfile", []string{"src/*.go"}, "src/main.go", true},
		{"globstar recursive match", []string{"src/**"}, "src/a/b/c/main.go", true},
		{"sibling dir rejected", []string{"internal/harness/*"}, "internal/secret/keys.txt", false},
		{"unrelated dir rejected", []string{"internal/harness/*"}, "cmd/g8s/main.go", false},
		{"traversal parent rejected", []string{"*"}, "../../etc/passwd", false},
		{"traversal relative rejected", []string{"internal/harness/*"}, "../internal/harness/probe.go", false},
		{"absolute path Unix rejected", []string{"*"}, "/etc/passwd", false},
		{"absolute path Windows rejected", []string{"*"}, "C:/Windows/System32/cmd.exe", false},
		{"empty allowed paths permits all", nil, "internal/foo.go", true},
	}

	for _, tc := range pathCases {
		t.Run(tc.name, func(t *testing.T) {
			got := VerifyReceiptPathScope(tc.allowed, tc.candidate)
			if got != tc.wantPass {
				t.Errorf("VerifyReceiptPathScope(%v, %q) = %v, want %v", tc.allowed, tc.candidate, got, tc.wantPass)
			}
		})
	}
}

// TestSelfAudit_RunSuite_AllPass runs the complete self-audit suite through RunSuite.
func TestSelfAudit_RunSuite_AllPass(t *testing.T) {
	suite := &ProbeSuite{
		Name:        "Self-Audit Test Suite",
		Description: "Verification of the 4 harness self-audit probes",
		Probes:      SelfAuditProbes(),
	}

	pri, err := RunSuite(context.Background(), suite, &StaticProvider{Compliant: true})
	if err != nil {
		t.Fatalf("RunSuite failed: %v", err)
	}

	if pri.TotalProbes != 4 {
		t.Errorf("TotalProbes = %d, want 4", pri.TotalProbes)
	}
	if pri.PassedProbes != 4 {
		for _, d := range pri.Details {
			if !d.Passed {
				t.Errorf("probe %s failed: actual=%q err=%s", d.Name, d.ActualOutcome, d.Error)
			}
		}
		t.Fatalf("PassedProbes = %d, want 4", pri.PassedProbes)
	}
}

func TestSelfAudit_AuditChild_Terminate(t *testing.T) {
	child := &auditChild{done: make(chan struct{})}
	child.Terminate(time.Millisecond)
	if child.PID() != 42 {
		t.Errorf("expected 42, got %d", child.PID())
	}
	if child.WaitCode() != 0 {
		t.Errorf("expected 0, got %d", child.WaitCode())
	}
}

func TestSelfAudit_ProbeConstructors(t *testing.T) {
	ctx := context.Background()

	pEcho := NewRefusalEchoProbe()
	if pEcho.ID != "sa-001" || pEcho.Runner == nil {
		t.Errorf("unexpected pEcho: %+v", pEcho)
	}

	pSanitizer := NewSanitizerFidelityProbe()
	if pSanitizer.ID != "sa-003" || pSanitizer.Runner == nil {
		t.Errorf("unexpected pSanitizer: %+v", pSanitizer)
	}
	outcome, err := pSanitizer.Runner(ctx, nil)
	if err != nil || outcome != OutcomeClassCompleted {
		t.Errorf("pSanitizer runner: %v, %v", outcome, err)
	}

	pReceipt := NewReceiptBypassProbe()
	if pReceipt.ID != "sa-004" || pReceipt.Runner == nil {
		t.Errorf("unexpected pReceipt: %+v", pReceipt)
	}
	outcome, err = pReceipt.Runner(ctx, nil)
	if err != nil || outcome != OutcomeClassCompleted {
		t.Errorf("pReceipt runner: %v, %v", outcome, err)
	}
}

func TestSelfAudit_PathMatches_Globstar(t *testing.T) {
	// Exercise globstar matching branches
	if !pathMatches("a/b/c/d.go", "**/*.go") {
		t.Errorf("expected match for **/*.go")
	}
	if !pathMatches("a/b/c/d.go", "a/**") {
		t.Errorf("expected match for a/**")
	}
	if pathMatches("a/b/c/d.go", "b/**") {
		t.Errorf("unexpected match for b/**")
	}
	if !pathMatches("foo/bar", "foo") {
		t.Errorf("expected prefix dir match for foo")
	}
}
