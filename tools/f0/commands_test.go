package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func captureOutput(f func()) string {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	f()

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func captureStderr(f func()) string {
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	f()

	_ = w.Close()
	os.Stderr = oldStderr

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func TestCommands_Waive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupTestGitRepo(t)

	// Issue active mandate
	m := &Mandate{
		Version:     1,
		ID:          "md-waivetest",
		Issuer:      "test-op",
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "waive test",
		Repos:       []MandateRepo{{Name: "testrepo", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 3},
	}
	_ = IssueMandate(m)

	out := captureOutput(func() {
		code := waiveCommand([]string{
			"--repo", repoDir,
			"--pr", "42",
			"--check", "flaky-test",
			"--issue", "https://github.com/tamld/g8s/issues/99",
			"--reason", "upstream issue",
			"--head-sha", headSHA,
		})
		if code != 0 {
			t.Errorf("expected exit code 0, got %d", code)
		}
	})

	if !strings.Contains(out, "waiver recorded") {
		t.Errorf("expected waiver recorded in output, got: %s", out)
	}

	// Verify repo ledger entry
	ledger, err := OpenRepoLedger(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := ledger.ReadEntries()
	found := false
	for _, e := range entries {
		if e.Kind == "waiver" && e.Extra["check"] == "flaky-test" {
			found = true
			break
		}
	}
	if !found {
		t.Error("waiver entry not found in repo ledger")
	}

	// Verify global ledger debit
	spent, _ := MandateBudgetSpent(m)
	if spent != 1 {
		t.Errorf("expected spent=1 after waiver, got %d", spent)
	}
}

func TestCommands_Waive_MissingFlags(t *testing.T) {
	repoDir, _, headSHA := setupTestGitRepo(t)

	// Missing --pr
	errPr := captureStderr(func() {
		code := waiveCommand([]string{"--repo", repoDir, "--check", "test-check", "--issue", "https://issue", "--reason", "some reason"})
		if code != 1 {
			t.Errorf("expected exit code 1 for missing --pr, got %d", code)
		}
	})
	if !strings.Contains(errPr, "--pr <N> is required") {
		t.Errorf("expected '--pr <N> is required', got: %s", errPr)
	}

	// Invalid --pr
	errInvPr := captureStderr(func() {
		code := waiveCommand([]string{"--repo", repoDir, "--pr", "abc", "--check", "test-check", "--issue", "https://issue", "--reason", "some reason"})
		if code != 1 {
			t.Errorf("expected exit code 1 for invalid --pr, got %d", code)
		}
	})
	if !strings.Contains(errInvPr, "invalid --pr") {
		t.Errorf("expected 'invalid --pr', got: %s", errInvPr)
	}

	// Missing --check
	errCheck := captureStderr(func() {
		code := waiveCommand([]string{"--repo", repoDir, "--pr", "1", "--issue", "https://issue", "--reason", "some reason"})
		if code != 1 {
			t.Errorf("expected exit code 1 for missing --check, got %d", code)
		}
	})
	if !strings.Contains(errCheck, "--check <name> is required") {
		t.Errorf("expected '--check <name> is required', got: %s", errCheck)
	}

	// Missing --issue
	errIssue := captureStderr(func() {
		code := waiveCommand([]string{"--repo", repoDir, "--pr", "1", "--check", "test-check", "--reason", "some reason"})
		if code != 1 {
			t.Errorf("expected exit code 1 for missing --issue, got %d", code)
		}
	})
	if !strings.Contains(errIssue, "--issue <url> is required") {
		t.Errorf("expected '--issue <url> is required', got: %s", errIssue)
	}

	// Missing --reason
	errReason := captureStderr(func() {
		code := waiveCommand([]string{"--repo", repoDir, "--pr", "1", "--check", "test-check", "--issue", "https://issue", "--head-sha", headSHA})
		if code != 1 {
			t.Errorf("expected exit code 1 for missing --reason, got %d", code)
		}
	})
	if !strings.Contains(errReason, "--reason <text> is required") {
		t.Errorf("expected '--reason <text> is required', got: %s", errReason)
	}
}

func TestCommands_Manual(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, _ := setupTestGitRepo(t)

	// Setup .g8s/f0.yml with manual_free: ["plans/**"]
	cfgDir := filepath.Join(repoDir, ".g8s")
	_ = os.MkdirAll(cfgDir, 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "f0.yml"), []byte("manual_free:\n  - \"plans/**\"\n"), 0o644)

	// Create files
	_ = os.MkdirAll(filepath.Join(repoDir, "plans"), 0o755)
	planFile := filepath.Join("plans", "plan1.md")
	_ = os.WriteFile(filepath.Join(repoDir, planFile), []byte("# Plan\n"), 0o644)

	codeFile := "main.go"

	// Issue active mandate
	m := &Mandate{
		Version:     1,
		ID:          "md-manualtest",
		Issuer:      "test-op",
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "manual test",
		Repos:       []MandateRepo{{Name: "testrepo", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 5},
	}
	_ = IssueMandate(m)

	// 1. Tag plans/plan1.md -> free (no debit)
	out1 := captureOutput(func() {
		code := manualCommand([]string{
			"--repo", repoDir,
			"--file", planFile,
			"--reason", "supervisor authoring plan",
		})
		if code != 0 {
			t.Errorf("expected exit code 0 for free manual tag, got %d", code)
		}
	})
	if !strings.Contains(out1, "free=true") {
		t.Errorf("expected free=true in output, got: %s", out1)
	}
	spent, _ := MandateBudgetSpent(m)
	if spent != 0 {
		t.Errorf("expected 0 spent for manual_free path, got %d", spent)
	}

	// 2. Tag main.go -> not free (costs 1 debit)
	out2 := captureOutput(func() {
		code := manualCommand([]string{
			"--repo", repoDir,
			"--file", codeFile,
			"--reason", "manual fix",
		})
		if code != 0 {
			t.Errorf("expected exit code 0 for manual tag, got %d", code)
		}
	})
	if !strings.Contains(out2, "free=false") {
		t.Errorf("expected free=false in output, got: %s", out2)
	}
	spent, _ = MandateBudgetSpent(m)
	if spent != 1 {
		t.Errorf("expected 1 spent for non-free manual tag, got %d", spent)
	}
}

func TestCommands_Manual_MissingFlags(t *testing.T) {
	repoDir, _, _ := setupTestGitRepo(t)

	// Missing --file
	errFile := captureStderr(func() {
		code := manualCommand([]string{"--repo", repoDir, "--reason", "some reason"})
		if code != 1 {
			t.Errorf("expected exit code 1 for missing --file, got %d", code)
		}
	})
	if !strings.Contains(errFile, "--file <rel> is required") {
		t.Errorf("expected missing file error, got: %s", errFile)
	}

	// Missing --reason
	errReason := captureStderr(func() {
		code := manualCommand([]string{"--repo", repoDir, "--file", "main.go"})
		if code != 1 {
			t.Errorf("expected exit code 1 for missing --reason, got %d", code)
		}
	})
	if !strings.Contains(errReason, "--reason <text> is required") {
		t.Errorf("expected missing reason error, got: %s", errReason)
	}

	// Non-existent file git hash failure
	errNonExist := captureStderr(func() {
		code := manualCommand([]string{"--repo", repoDir, "--file", "nonexistent.go", "--reason", "test"})
		if code != 1 {
			t.Errorf("expected exit code 1 for nonexistent file, got %d", code)
		}
	})
	if !strings.Contains(errNonExist, "computing blob hash") {
		t.Errorf("expected error computing blob hash, got: %s", errNonExist)
	}
}

func TestCommands_Anchor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupTestGitRepo(t)

	ledger, _ := OpenRepoLedger(repoDir)
	_ = ledger.Append(&Entry{
		ID:     "ev-for-anchor",
		SHA:    headSHA,
		Status: "PASS",
		Cmd:    []string{"go", "test", "./..."},
	})

	out := captureOutput(func() {
		code := anchorCommand([]string{"--repo", repoDir})
		if code != 0 {
			t.Errorf("expected exit code 0, got %d", code)
		}
	})

	if !strings.HasPrefix(strings.TrimSpace(out), "F0-Ledger-Head:") {
		t.Errorf("expected F0-Ledger-Head trailer, got: %s", out)
	}
}

func TestCommands_Audit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupTestGitRepo(t)

	// 1. Initial commit has neither gate PASS nor trailer -> audit detects 2 unattested commits
	out1 := captureOutput(func() {
		code := auditCommand([]string{"--repo", repoDir, "--strict"})
		if code != 1 {
			t.Errorf("expected exit code 1 with --strict when unattested commits exist, got %d", code)
		}
	})
	if !strings.Contains(out1, "un-attested commit(s) found") {
		t.Errorf("expected un-attested commits found, got: %s", out1)
	}

	// 2. Record gate PASS entry for headSHA
	ledger, _ := OpenRepoLedger(repoDir)
	_ = ledger.Append(&Entry{
		ID:     "ev-gatepass",
		Kind:   "gate",
		SHA:    headSHA,
		Status: "PASS",
		Cmd:    []string{"gate", "check"},
	})

	// Add an anchored commit on top
	anchorLine, _ := ledger.AnchorLine()
	_ = os.WriteFile(filepath.Join(repoDir, "file2.txt"), []byte("content\n"), 0o644)
	_ = exec.Command("git", "-C", repoDir, "add", "file2.txt").Run()
	_ = exec.Command("git", "-C", repoDir, "commit", "-m", "commit with anchor\n\n"+anchorLine).Run()

	out2 := captureOutput(func() {
		code := auditCommand([]string{"--repo", repoDir, "--strict", "--since", "10m"})
		// Both commits within the last 10m are now attested (one via gate entry matching parent or SHA, one via trailer)
		_ = code
	})
	_ = out2

	// 3. Test with --since 30d and --strict and --json
	out3 := captureOutput(func() {
		code := auditCommand([]string{"--repo", repoDir, "--since", "30d", "--strict", "--json"})
		if code != 0 {
			t.Errorf("expected exit code 0 when all attested, got %d", code)
		}
	})
	if !strings.Contains(out3, "[]") {
		t.Errorf("expected empty json array for all attested commits, got: %s", out3)
	}
}

func TestCommands_Status(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupTestGitRepo(t)

	ledger, _ := OpenRepoLedger(repoDir)
	_ = ledger.Append(&Entry{
		ID:     "ev-st-01",
		SHA:    headSHA,
		Status: "PASS",
		Cmd:    []string{"go", "test", "./..."},
	})

	// 1. Report block format
	outBlock := captureOutput(func() {
		code := statusCommand([]string{"--repo", repoDir, "--report-block"})
		if code != 0 {
			t.Errorf("expected exit code 0, got %d", code)
		}
	})
	if !strings.Contains(outBlock, "<!-- f0:status -->") || !strings.Contains(outBlock, "<!-- /f0:status -->") {
		t.Errorf("expected report block markers, got: %s", outBlock)
	}
	if !strings.Contains(outBlock, "ev-st-01") {
		t.Errorf("expected ev-st-01 in report block, got: %s", outBlock)
	}

	// 2. JSON format
	outJSON := captureOutput(func() {
		code := statusCommand([]string{"--repo", repoDir, "--json"})
		if code != 0 {
			t.Errorf("expected exit code 0, got %d", code)
		}
	})
	if !strings.Contains(outJSON, `"repo":`) || !strings.Contains(outJSON, `"evidence_at_head":`) {
		t.Errorf("expected json status output, got: %s", outJSON)
	}

	// 3. Plain text terminal output (without active mandate)
	outPlain1 := captureOutput(func() {
		code := statusCommand([]string{"--repo", repoDir})
		if code != 0 {
			t.Errorf("expected exit code 0, got %d", code)
		}
	})
	if !strings.Contains(outPlain1, "Repository:") || !strings.Contains(outPlain1, "Active Mandate: none") {
		t.Errorf("expected plain text status output with no mandate, got: %s", outPlain1)
	}

	// Issue active mandate for plain text output test with mandate
	m := &Mandate{
		Version:     1,
		ID:          "md-status-cmd",
		Issuer:      "test-op",
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "status cmd test",
		Repos:       []MandateRepo{{Name: "testrepo", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 5},
	}
	_ = IssueMandate(m)

	// 4. Plain text terminal output (with active mandate)
	outPlain2 := captureOutput(func() {
		code := statusCommand([]string{"--repo", repoDir})
		if code != 0 {
			t.Errorf("expected exit code 0, got %d", code)
		}
	})
	if !strings.Contains(outPlain2, "Active Mandate: md-status-cmd") {
		t.Errorf("expected active mandate in plain text status output, got: %s", outPlain2)
	}
}

func TestCommands_Gate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupTestGitRepo(t)

	// Issue active mandate
	m := &Mandate{
		Version:     1,
		ID:          "md-gatetest",
		Issuer:      "test-op",
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "gate test",
		Repos:       []MandateRepo{{Name: "testrepo", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 3},
	}
	_ = IssueMandate(m)

	// Valid gate check with JSON output
	out := captureOutput(func() {
		code := gateCommand([]string{
			"check",
			"--repo", repoDir,
			"--action", "merge",
			"--head-sha", headSHA,
			"--base", "main",
			"--pr-body", "clean body",
			"--json",
		})
		_ = code
	})
	if !strings.Contains(out, `"overall":`) {
		t.Errorf("expected json output with overall field, got: %s", out)
	}

	// gateCommand(nil) -> usage error (code 1)
	errNil := captureStderr(func() {
		code := gateCommand(nil)
		if code != 1 {
			t.Errorf("expected exit code 1 for gateCommand(nil), got %d", code)
		}
	})
	if !strings.Contains(errNil, "usage: f0 gate") {
		t.Errorf("expected usage message, got: %s", errNil)
	}

	// gateCommand([]string{"unknown"}) -> unknown subcommand (code 1)
	errUnknown := captureStderr(func() {
		code := gateCommand([]string{"unknown"})
		if code != 1 {
			t.Errorf("expected exit code 1 for gateCommand(unknown), got %d", code)
		}
	})
	if !strings.Contains(errUnknown, "unknown subcommand") {
		t.Errorf("expected unknown subcommand message, got: %s", errUnknown)
	}

	// runGateCheck([]string{"--repo", repoDir}) -> missing --action error branch (code 1)
	errAction := captureStderr(func() {
		code := runGateCheck([]string{"--repo", repoDir})
		if code != 1 {
			t.Errorf("expected exit code 1 for missing --action, got %d", code)
		}
	})
	if !strings.Contains(errAction, "--action is required") {
		t.Errorf("expected '--action is required' error, got: %s", errAction)
	}
}

func TestCommands_DispatchAndUsage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupTestGitRepo(t)

	// printUsage()
	errUsage := captureStderr(func() {
		printUsage()
	})
	if !strings.Contains(errUsage, "Usage:") || !strings.Contains(errUsage, "f0 run") {
		t.Errorf("expected full usage string, got: %s", errUsage)
	}

	// ledgerCommand(nil) and unknown
	errLedgerNil := captureStderr(func() {
		if code := ledgerCommand(nil); code != 1 {
			t.Errorf("expected exit code 1 for ledgerCommand(nil), got %d", code)
		}
	})
	if !strings.Contains(errLedgerNil, "usage: f0 ledger") {
		t.Errorf("expected usage message, got: %s", errLedgerNil)
	}

	errLedgerUnknown := captureStderr(func() {
		if code := ledgerCommand([]string{"unknown"}); code != 1 {
			t.Errorf("expected exit code 1 for ledgerCommand(unknown), got %d", code)
		}
	})
	if !strings.Contains(errLedgerUnknown, "unknown subcommand") {
		t.Errorf("expected unknown subcommand, got: %s", errLedgerUnknown)
	}

	// Test ledgerCommand subcommands: verify, list, show
	ledger, _ := OpenRepoLedger(repoDir)
	_ = ledger.Append(&Entry{
		ID:     "ev-cmd-test",
		SHA:    headSHA,
		Status: "PASS",
		Cmd:    []string{"echo", "hi"},
	})

	_ = captureOutput(func() {
		_ = ledgerCommand([]string{"verify", "--repo", repoDir})
		_ = ledgerCommand([]string{"list", "--repo", repoDir, "--json"})
		_ = ledgerCommand([]string{"list", "--repo", repoDir})
		_ = ledgerCommand([]string{"show", "ev-cmd-test", "--repo", repoDir})
	})
	_ = captureStderr(func() {
		_ = ledgerCommand([]string{"show"})
	})

	// mandateCommand(nil) and unknown
	errMandateNil := captureStderr(func() {
		if code := mandateCommand(nil); code != 1 {
			t.Errorf("expected exit code 1 for mandateCommand(nil), got %d", code)
		}
	})
	if !strings.Contains(errMandateNil, "usage: f0 mandate") {
		t.Errorf("expected usage message, got: %s", errMandateNil)
	}

	errMandateUnknown := captureStderr(func() {
		if code := mandateCommand([]string{"unknown"}); code != 1 {
			t.Errorf("expected exit code 1 for mandateCommand(unknown), got %d", code)
		}
	})
	if !strings.Contains(errMandateUnknown, "unknown subcommand") {
		t.Errorf("expected unknown subcommand, got: %s", errMandateUnknown)
	}

	// mandateCommand show & check
	_ = captureOutput(func() {
		_ = mandateCommand([]string{"show", "--json"})
		_ = mandateCommand([]string{"check", "--repo", repoDir, "--action", "commit", "--json"})
	})

	// hookCommand(nil) and unknown
	errHookNil := captureStderr(func() {
		if code := hookCommand(nil); code != 1 {
			t.Errorf("expected exit code 1 for hookCommand(nil), got %d", code)
		}
	})
	if !strings.Contains(errHookNil, "usage: f0 hook") {
		t.Errorf("expected usage message, got: %s", errHookNil)
	}

	errHookUnknown := captureStderr(func() {
		if code := hookCommand([]string{"unknown"}); code != 1 {
			t.Errorf("expected exit code 1 for hookCommand(unknown), got %d", code)
		}
	})
	if !strings.Contains(errHookUnknown, "unknown subcommand") {
		t.Errorf("expected unknown subcommand, got: %s", errHookUnknown)
	}
}

func TestCommands_MandateIssueOptionsAndErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, _ := setupTestGitRepo(t)

	// Valid mandate issue
	_ = captureOutput(func() {
		code := mandateCommand([]string{
			"issue",
			"--issuer", "test-operator",
			"--campaign", "commands test campaign",
			"--repo", "testrepo=" + repoDir + ":T2",
			"--exclude", "docs/**, vendor/**",
			"--budget", "5",
			"--ttl", "12h",
		})
		if code != 0 {
			t.Errorf("expected exit code 0 for valid mandate issue, got %d", code)
		}
	})

	// Missing issuer
	_ = captureStderr(func() {
		code := mandateCommand([]string{"issue"})
		if code != 1 {
			t.Errorf("expected exit code 1 for missing issuer, got %d", code)
		}
	})

	// Missing campaign
	_ = captureStderr(func() {
		code := mandateCommand([]string{"issue", "--issuer", "op"})
		if code != 1 {
			t.Errorf("expected exit code 1 for missing campaign, got %d", code)
		}
	})

	// Missing repo
	_ = captureStderr(func() {
		code := mandateCommand([]string{"issue", "--issuer", "op", "--campaign", "c"})
		if code != 1 {
			t.Errorf("expected exit code 1 for missing repo, got %d", code)
		}
	})

	// Invalid repo format (no =)
	_ = captureStderr(func() {
		code := mandateCommand([]string{"issue", "--issuer", "op", "--campaign", "c", "--repo", "invalid"})
		if code != 1 {
			t.Errorf("expected exit code 1 for repo format without =, got %d", code)
		}
	})

	// Invalid repo format (no :)
	_ = captureStderr(func() {
		code := mandateCommand([]string{"issue", "--issuer", "op", "--campaign", "c", "--repo", "r=invalid"})
		if code != 1 {
			t.Errorf("expected exit code 1 for repo format without colon, got %d", code)
		}
	})

	// Invalid tier T4
	_ = captureStderr(func() {
		code := mandateCommand([]string{"issue", "--issuer", "op", "--campaign", "c", "--repo", "r=.:T4"})
		if code != 1 {
			t.Errorf("expected exit code 1 for T4 tier, got %d", code)
		}
	})

	// Invalid tier unknown
	_ = captureStderr(func() {
		code := mandateCommand([]string{"issue", "--issuer", "op", "--campaign", "c", "--repo", "r=.:T99"})
		if code != 1 {
			t.Errorf("expected exit code 1 for unknown tier, got %d", code)
		}
	})

	// Invalid budget
	_ = captureStderr(func() {
		code := mandateCommand([]string{"issue", "--issuer", "op", "--campaign", "c", "--repo", "r=.:T2", "--budget", "bad"})
		if code != 1 {
			t.Errorf("expected exit code 1 for bad budget, got %d", code)
		}
	})

	// Invalid ttl
	_ = captureStderr(func() {
		code := mandateCommand([]string{"issue", "--issuer", "op", "--campaign", "c", "--repo", "r=.:T2", "--ttl", "badttl"})
		if code != 1 {
			t.Errorf("expected exit code 1 for bad ttl, got %d", code)
		}
	})

	// Show plain text
	_ = captureOutput(func() {
		code := mandateCommand([]string{"show"})
		if code != 0 {
			t.Errorf("expected exit code 0 for mandate show, got %d", code)
		}
	})

	// Check plain text - action permitted (push-branch requires T1, mandate allows up to T2)
	_ = captureOutput(func() {
		code := mandateCommand([]string{"check", "--repo", repoDir, "--action", "push-branch"})
		if code != 0 {
			t.Errorf("expected exit code 0 for mandate check push-branch, got %d", code)
		}
	})

	// Check action requiring higher tier (commit requires T4) -> returns 3
	_ = captureOutput(func() {
		code := mandateCommand([]string{"check", "--repo", repoDir, "--action", "commit"})
		if code != 3 {
			t.Errorf("expected exit code 3 for action requiring T4, got %d", code)
		}
	})

	// Check missing action
	_ = captureStderr(func() {
		code := mandateCommand([]string{"check", "--repo", repoDir})
		if code != 1 {
			t.Errorf("expected exit code 1 for missing action, got %d", code)
		}
	})
}

func TestCommands_HookPreToolAndStopExecution(t *testing.T) {
	// Hook pre-tool with piped JSON
	runWithStdin := func(input string, fn func()) {
		oldStdin := os.Stdin
		r, w, _ := os.Pipe()
		os.Stdin = r
		go func() {
			_, _ = w.Write([]byte(input))
			_ = w.Close()
		}()
		fn()
		os.Stdin = oldStdin
	}

	_ = captureOutput(func() {
		runWithStdin(`{"tool_call":{"name":"read_file"}}`, func() {
			code := hookCommand([]string{"pre-tool"})
			if code != 0 {
				t.Errorf("expected 0 for pre-tool allow, got %d", code)
			}
		})
	})

	_ = captureOutput(func() {
		runWithStdin(`{}`, func() {
			code := hookCommand([]string{"stop"})
			if code != 0 {
				t.Errorf("expected 0 for stop hook, got %d", code)
			}
		})
	})
}

func TestCommands_HelperFunctions(t *testing.T) {
	repoDir, _, headSHA := setupTestGitRepo(t)

	// gitFileExistsAt
	if !gitFileExistsAt(repoDir, headSHA, "main.go") {
		t.Error("expected main.go to exist at headSHA")
	}
	if gitFileExistsAt(repoDir, headSHA, "nonexistent.go") {
		t.Error("expected nonexistent.go to not exist at headSHA")
	}

	// extractRepoOwnerAndName
	tests := []struct {
		url       string
		wantOwner string
		wantRepo  string
	}{
		{"git@github.com:tamld/g8s.git", "tamld", "g8s"},
		{"https://github.com/tamld/g8s.git", "tamld", "g8s"},
		{"ssh://git@github.com/tamld/g8s", "tamld", "g8s"},
		{"git@gitlab.com:org/sub/repo.git", "", ""},
		{"invalid", "", ""},
	}
	for _, tc := range tests {
		o, r := extractRepoOwnerAndName(tc.url)
		if o != tc.wantOwner || r != tc.wantRepo {
			t.Errorf("extractRepoOwnerAndName(%q) = (%q, %q), want (%q, %q)", tc.url, o, r, tc.wantOwner, tc.wantRepo)
		}
	}

	// extractPRNumber
	if n := extractPRNumber("gh api /repos/owner/repo/pulls/123/merge"); n != 123 {
		t.Errorf("expected 123, got %d", n)
	}
	if n := extractPRNumber("https://github.com/owner/repo/pull/456"); n != 456 {
		t.Errorf("expected 456, got %d", n)
	}
	if n := extractPRNumber("gh pr checkout --pr 789"); n != 789 {
		t.Errorf("expected 789, got %d", n)
	}
	if n := extractPRNumber("gh pr merge 101 --squash"); n != 101 {
		t.Errorf("expected 101, got %d", n)
	}
	if n := extractPRNumber("gh pr merge -R owner/repo 202"); n != 202 {
		t.Errorf("expected 202, got %d", n)
	}
	if n := extractPRNumber("echo no pr"); n != 0 {
		t.Errorf("expected 0, got %d", n)
	}

	// extractRepoFlag
	tmp := t.TempDir()
	if r := extractRepoFlag("gh pr merge 123 --repo " + tmp); r != tmp {
		t.Errorf("expected %s, got %s", tmp, r)
	}
	if r := extractRepoFlag("gh pr merge 123 -R=" + tmp); r != tmp {
		t.Errorf("expected %s, got %s", tmp, r)
	}

	// extractContentString
	if s := extractContentString("hello"); s != "hello" {
		t.Errorf("expected 'hello', got %q", s)
	}
	sliceInput := []any{map[string]any{"text": "line1"}, "line2"}
	if s := extractContentString(sliceInput); s != "line1\nline2" {
		t.Errorf("expected 'line1\\nline2', got %q", s)
	}
	if s := extractContentString(12345); s != "12345" {
		t.Errorf("expected '12345', got %q", s)
	}
	if s := extractContentString(nil); s != "" {
		t.Errorf("expected empty string for nil, got %q", s)
	}

	// getMapString
	m := map[string]any{"key1": "val1", "key2": 42}
	if v := getMapString(m, "key1"); v != "val1" {
		t.Errorf("expected 'val1', got %q", v)
	}
	if v := getMapString(m, "missing", "key1"); v != "val1" {
		t.Errorf("expected 'val1' as fallback, got %q", v)
	}
	if v := getMapString(nil, "key"); v != "" {
		t.Errorf("expected empty string for nil map, got %q", v)
	}

	// runCommand usage and execution
	_ = captureStderr(func() {
		code := runCommand(nil)
		if code != 1 {
			t.Errorf("expected exit code 1 for runCommand(nil), got %d", code)
		}
	})

	_ = captureOutput(func() {
		code := runCommand([]string{"--", "echo", "hello-run"})
		if code != 0 {
			t.Errorf("expected 0 for echo via runCommand, got %d", code)
		}
	})

	// detectKind
	if k := detectKind([]string{"go", "test", "./..."}); k != "go-test" {
		t.Errorf("expected go-test, got %s", k)
	}
	if k := detectKind([]string{"cargo", "test"}); k != "cargo-test" {
		t.Errorf("expected cargo-test, got %s", k)
	}
	if k := detectKind([]string{"npm", "test"}); k != "npm-test" {
		t.Errorf("expected npm-test, got %s", k)
	}
	if k := detectKind([]string{"node", "--test"}); k != "node-test" {
		t.Errorf("expected node-test, got %s", k)
	}
	if k := detectKind([]string{"did-verify", "--files", "a.go"}); k != "did-verify" {
		t.Errorf("expected did-verify, got %s", k)
	}
	if k := detectKind([]string{"g8s", "offer", "check"}); k != "g8s-offer-check" {
		t.Errorf("expected g8s-offer-check, got %s", k)
	}
	if k := detectKind([]string{"g8s", "deliver"}); k != "g8s-deliver" {
		t.Errorf("expected g8s-deliver, got %s", k)
	}
	if k := detectKind([]string{"g8s", "verify"}); k != "g8s-verify" {
		t.Errorf("expected g8s-verify, got %s", k)
	}
	if k := detectKind([]string{"make", "build"}); k != "generic" {
		t.Errorf("expected generic, got %s", k)
	}

	// stripEnv
	stripped := stripEnv([]string{"ENV1=val1", "ENV2=val2", "go", "test"})
	if len(stripped) != 2 || stripped[0] != "go" {
		t.Errorf("expected stripped cmd [go test], got %v", stripped)
	}

	// newEvidenceID
	if id := newEvidenceID(); !strings.HasPrefix(id, "ev-") {
		t.Errorf("expected ev- prefix, got %s", id)
	}

	// tierRank
	if tierRank("T0") >= tierRank("T1") || tierRank("T1") >= tierRank("T2") || tierRank("T2") >= tierRank("T3") || tierRank("T3") >= tierRank("T4") {
		t.Error("unexpected tier ranks")
	}
	if tierRank("unknown") != 999 {
		t.Error("expected 999 for unknown tier rank")
	}

	// isDryRun
	if !isDryRun(Entry{Extra: map[string]any{"dry_run": true}}) {
		t.Error("expected true for dry_run: true")
	}
	if isDryRun(Entry{Extra: map[string]any{"dry_run": false}}) {
		t.Error("expected false for dry_run: false")
	}
	if isDryRun(Entry{}) {
		t.Error("expected false for empty entry")
	}

	// getExtraStringSlice
	if s := getExtraStringSlice(nil); s != nil {
		t.Errorf("expected nil for nil input, got %v", s)
	}
	if s := getExtraStringSlice([]string{"x", "y"}); len(s) != 2 {
		t.Errorf("expected 2 elements for []string, got %v", s)
	}
	if s := getExtraStringSlice([]any{"x", "y"}); len(s) != 2 {
		t.Errorf("expected 2 elements for []any, got %v", s)
	}
	if s := getExtraStringSlice(42); s != nil {
		t.Errorf("expected nil for non-slice, got %v", s)
	}

	// getExtraStringMap
	if m := getExtraStringMap(nil); m != nil {
		t.Errorf("expected nil for nil input, got %v", m)
	}
	if m := getExtraStringMap(map[string]string{"k": "v"}); m["k"] != "v" {
		t.Errorf("expected map with key k, got %v", m)
	}
	if m := getExtraStringMap(map[string]any{"k": "v"}); m["k"] != "v" {
		t.Errorf("expected map with key k, got %v", m)
	}
	if m := getExtraStringMap(42); m != nil {
		t.Errorf("expected nil for non-map, got %v", m)
	}

	// anchorCommand error with invalid repo
	_ = captureStderr(func() {
		code := anchorCommand([]string{"--repo", "/nonexistent/path/for/repo"})
		if code != 1 {
			t.Errorf("expected exit code 1 for invalid repo anchor, got %d", code)
		}
	})

	// FakeForge and GetForgeAdapter
	ff := NewFakeForge()
	ff.Issues["https://github.com/tamld/g8s/issues/1"] = FakeIssue{
		Title:    "Title",
		Body:     "Body",
		Open:     true,
		SameRepo: true,
	}
	sameRepo, open, text, err := ff.VerifyIssue(".", "https://github.com/tamld/g8s/issues/1/")
	if err != nil || !sameRepo || !open || text != "Title\nBody" {
		t.Errorf("VerifyIssue with trailing slash failed: %v, %v, %v, %v", sameRepo, open, text, err)
	}
	_, _, _, err = ff.VerifyIssue(".", "https://github.com/tamld/g8s/issues/999")
	if err == nil {
		t.Error("expected error for nonexistent issue")
	}

	_, _, _, err = ff.ResolvePR(".", 999)
	if err == nil {
		t.Error("expected error for nonexistent PR")
	}

	checks, _ := ff.ListChecks(".", 999, "")
	if len(checks) != 0 {
		t.Errorf("expected 0 checks, got %d", len(checks))
	}

	_, _, err = ff.VerifyWorkflowRun(".", "run-999")
	if err == nil {
		t.Error("expected error for nonexistent workflow run")
	}

	// GetForgeAdapter with F0_FAKE_FORGE_JSON
	t.Setenv("F0_FAKE_FORGE_JSON", "/nonexistent/fake.json")
	SetGlobalForge(nil)
	if _, err := GetForgeAdapter("."); err == nil {
		t.Error("expected error reading nonexistent fake forge JSON")
	}

	fakeJSONFile := filepath.Join(t.TempDir(), "fake_forge.json")
	_ = os.WriteFile(fakeJSONFile, []byte("{invalid-json"), 0o644)
	t.Setenv("F0_FAKE_FORGE_JSON", fakeJSONFile)
	if _, err := GetForgeAdapter("."); err == nil {
		t.Error("expected error unmarshaling invalid fake forge JSON")
	}

	_ = os.WriteFile(fakeJSONFile, []byte(`{"prs":{}}`), 0o644)
	t.Setenv("F0_FAKE_FORGE_JSON", fakeJSONFile)
	if adapter, err := GetForgeAdapter("."); err != nil || adapter == nil {
		t.Errorf("expected valid adapter from fake forge JSON, got %v, %v", adapter, err)
	}

	t.Setenv("F0_FAKE_FORGE_JSON", "")
	if adapter, err := GetForgeAdapter("."); err != nil || adapter == nil {
		t.Errorf("expected GitHubForge when F0_FAKE_FORGE_JSON empty, got %v, %v", adapter, err)
	}
}
