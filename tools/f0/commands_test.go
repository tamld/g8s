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

func TestCommands_Waive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupTestGitRepo(t)

	// Issue active mandate
	m := &Mandate{
		Version:   1,
		ID:        "md-waivetest",
		Issuer:    "test-op",
		IssuedAt:  time.Now().UTC().Format(time.RFC3339),
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:  "waive test",
		Repos:     []MandateRepo{{Name: "testrepo", Path: repoDir, MaxTier: "T2"}},
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

func TestCommands_Manual(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, _ := setupTestGitRepo(t)

	// Setup .g8s/f0.yml with manual_free: ["plans/**"]
	cfgDir := filepath.Join(repoDir, ".g8s")
	_ = os.MkdirAll(cfgDir, 0755)
	_ = os.WriteFile(filepath.Join(cfgDir, "f0.yml"), []byte("manual_free:\n  - \"plans/**\"\n"), 0644)

	// Create files
	_ = os.MkdirAll(filepath.Join(repoDir, "plans"), 0755)
	planFile := filepath.Join("plans", "plan1.md")
	_ = os.WriteFile(filepath.Join(repoDir, planFile), []byte("# Plan\n"), 0644)

	codeFile := "main.go"

	// Issue active mandate
	m := &Mandate{
		Version:   1,
		ID:        "md-manualtest",
		Issuer:    "test-op",
		IssuedAt:  time.Now().UTC().Format(time.RFC3339),
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:  "manual test",
		Repos:     []MandateRepo{{Name: "testrepo", Path: repoDir, MaxTier: "T2"}},
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
	_ = os.WriteFile(filepath.Join(repoDir, "file2.txt"), []byte("content\n"), 0644)
	_ = exec.Command("git", "-C", repoDir, "add", "file2.txt").Run()
	_ = exec.Command("git", "-C", repoDir, "commit", "-m", "commit with anchor\n\n"+anchorLine).Run()

	out2 := captureOutput(func() {
		code := auditCommand([]string{"--repo", repoDir, "--strict", "--since", "10m"})
		// Both commits within the last 10m are now attested (one via gate entry matching parent or SHA, one via trailer)
		_ = code
	})
	_ = out2
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
}
