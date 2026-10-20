package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSlice06b2_NormalizeExtraAndGateLedgerVerify proves that structs like
// []GateCheckResult inside Entry.Extra are canonicalized by normalizeExtra
// so that f0 ledger verify succeeds 100% after EvaluateGate.
func TestSlice06b2_NormalizeExtraAndGateLedgerVerify(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupTestGitRepo(t)

	ledger, err := OpenRepoLedger(repoDir)
	if err != nil {
		t.Fatalf("OpenRepoLedger failed: %v", err)
	}

	// Setup config with verify_commands and ci: none
	cfgDir := filepath.Join(repoDir, ".g8s")
	_ = os.MkdirAll(cfgDir, 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "f0.yml"), []byte("ci: none\nverify_commands:\n  - [\"go\", \"test\", \"./...\"]\n"), 0o644)

	// Issue active mandate
	m := &Mandate{
		Version:     1,
		ID:          "md-normextra",
		Issuer:      "alice",
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "norm-extra-test",
		Repos:       []MandateRepo{{Name: "testrepo", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 5},
	}
	if err := IssueMandate(m); err != nil {
		t.Fatalf("IssueMandate failed: %v", err)
	}

	// Add clean PASS evidence matching verify_commands
	if err := ledger.Append(&Entry{
		ID:           "ev-pass1",
		Kind:         "go-test",
		Repo:         repoDir,
		SHA:          headSHA,
		Status:       "PASS",
		Dirty:        false,
		Cmd:          []string{"go", "test", "./..."},
		ItemsChecked: 10,
	}); err != nil {
		t.Fatalf("Append ev-pass1 failed: %v", err)
	}

	// Run EvaluateGate (which appends a "gate" entry with []GateCheckResult in Extra["checks"])
	output, err := EvaluateGate(GateOptions{
		RepoDir: repoDir,
		Action:  "merge",
		HeadSHA: headSHA,
		BaseRef: "main",
	})
	if err != nil {
		t.Fatalf("EvaluateGate failed: %v", err)
	}
	if output.Overall != "PASS" {
		t.Fatalf("expected gate overall PASS, got %s", output.Overall)
	}

	// Verify ledger hash chain integrity
	report, err := ledger.Verify(repoDir)
	if err != nil {
		t.Fatalf("ledger.Verify failed after EvaluateGate: %v", err)
	}
	if report.EntriesCount < 2 {
		t.Fatalf("expected at least 2 entries in ledger, got %d", report.EntriesCount)
	}
}

// TestSlice06b2_TreeSHAEquivalence_CommitAmend proves that when a commit is amended
// (changing HEAD SHA but preserving HEAD^{tree}), G1 remains PASS via TreeSHA equivalence.
func TestSlice06b2_TreeSHAEquivalence_CommitAmend(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupTestGitRepo(t)

	ledger, err := OpenRepoLedger(repoDir)
	if err != nil {
		t.Fatalf("OpenRepoLedger failed: %v", err)
	}

	// Setup config with verify_commands and ci: none
	cfgDir := filepath.Join(repoDir, ".g8s")
	_ = os.MkdirAll(cfgDir, 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "f0.yml"), []byte("ci: none\nverify_commands:\n  - [\"go\", \"test\", \"./...\"]\n"), 0o644)

	// Resolve tree SHA of head
	treeOut, err := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD^{tree}").Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD^{tree}: %v", err)
	}
	treeSHA := strings.TrimSpace(string(treeOut))

	// Record clean PASS test evidence at headSHA and treeSHA
	if err := ledger.Append(&Entry{
		ID:           "ev-tree1",
		Kind:         "go-test",
		Repo:         repoDir,
		SHA:          headSHA,
		TreeSHA:      treeSHA,
		Status:       "PASS",
		Dirty:        false,
		Cmd:          []string{"go", "test", "./..."},
		ItemsChecked: 5,
	}); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Amend commit without changing content (tree remains identical, commit SHA changes)
	cmd := exec.Command("git", "-C", repoDir, "commit", "--amend", "-m", "amended commit message")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit --amend failed: %v\n%s", err, string(out))
	}

	newHeadOut, err := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse new HEAD: %v", err)
	}
	newHeadSHA := strings.TrimSpace(string(newHeadOut))

	newTreeOut, err := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD^{tree}").Output()
	if err != nil {
		t.Fatalf("rev-parse new HEAD^{tree}: %v", err)
	}
	newTreeSHA := strings.TrimSpace(string(newTreeOut))

	if newHeadSHA == headSHA {
		t.Fatalf("expected new HEAD SHA to differ from old head SHA")
	}
	if newTreeSHA != treeSHA {
		t.Fatalf("expected tree SHA to remain unchanged after amend (%s vs %s)", treeSHA, newTreeSHA)
	}

	// Check G1 on newHeadSHA
	m := &Mandate{
		Version:     1,
		ID:          "md-treesha",
		Issuer:      "alice",
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "treesha-test",
		Repos:       []MandateRepo{{Name: "testrepo", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 5},
	}
	_ = IssueMandate(m)

	output, err := EvaluateGate(GateOptions{
		RepoDir: repoDir,
		Action:  "merge",
		HeadSHA: newHeadSHA,
		BaseRef: newHeadSHA, // empty diff
	})
	if err != nil {
		t.Fatalf("EvaluateGate error: %v", err)
	}

	var g1Check *GateCheckResult
	for _, c := range output.Checks {
		if c.ID == "G1" {
			g1Check = &c
			break
		}
	}
	if g1Check == nil || g1Check.Status != "PASS" {
		t.Fatalf("expected G1 PASS across commit amend via tree_sha, got: %+v", g1Check)
	}
}

// TestSlice06b2_PortableBundle_RemoteModeAndTamper tests:
// 1. f0 status --report-block emits clean PASS entries + valid <!-- f0:bundle: ... -->
// 2. EvaluateGate with Mode: "remote" (and Mode: "auto" without local ledger) passes.
// 3. Modifying a delivered file after bundle generation fails G3.
// 4. Tampering with bundle JSON fails G1 and G3.
func TestSlice06b2_PortableBundle_RemoteModeAndTamper(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupTestGitRepo(t)

	// Create and commit a file delivered by worker
	filePath := filepath.Join(repoDir, "src", "feature.go")
	_ = os.MkdirAll(filepath.Dir(filePath), 0o755)
	_ = os.WriteFile(filePath, []byte("package src\n// feature code\n"), 0o644)
	_ = exec.Command("git", "-C", repoDir, "add", "src/feature.go").Run()
	_ = exec.Command("git", "-C", repoDir, "commit", "-m", "add feature").Run()

	headOut, _ := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD").Output()
	headSHA = strings.TrimSpace(string(headOut))

	featureBlob, _ := gitBlobAt(repoDir, headSHA, "src/feature.go")

	ledger, err := OpenRepoLedger(repoDir)
	if err != nil {
		t.Fatalf("OpenRepoLedger: %v", err)
	}

	// 1. Add older failing run (must NOT be in report-block evidence or bundle)
	_ = ledger.Append(&Entry{
		ID:           "ev-fail-old",
		Kind:         "go-test",
		Repo:         repoDir,
		SHA:          headSHA,
		Status:       "FAIL",
		Dirty:        false,
		Cmd:          []string{"go", "test", "./..."},
		ItemsChecked: 1,
	})

	// 2. Add deliver entry
	_ = ledger.Append(&Entry{
		ID:     "ev-del1",
		Kind:   "g8s-deliver",
		Repo:   repoDir,
		SHA:    headSHA,
		Status: "PASS",
		Cmd:    []string{"deliver"},
		Extra: map[string]any{
			"applied": []string{"src/feature.go"},
			"blobs":   map[string]string{"src/feature.go": featureBlob},
		},
	})

	// 3. Add clean PASS test evidence
	_ = ledger.Append(&Entry{
		ID:           "ev-cleanpass",
		Kind:         "go-test",
		Repo:         repoDir,
		SHA:          headSHA,
		Status:       "PASS",
		Dirty:        false,
		Cmd:          []string{"go", "test", "./..."},
		ItemsChecked: 15,
	})

	// Issue mandate
	m := &Mandate{
		Version:     1,
		ID:          "md-portable1",
		Issuer:      "alice",
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "portable-test",
		Repos:       []MandateRepo{{Name: "testrepo", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 5},
	}
	_ = IssueMandate(m)

	// Capture status --report-block
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := statusCommand([]string{"--repo", repoDir, "--report-block"})
	_ = w.Close()
	os.Stdout = oldStdout

	var outBuf bytes.Buffer
	_, _ = io.Copy(&outBuf, r)
	reportBlock := outBuf.String()

	if code != 0 {
		t.Fatalf("statusCommand returned %d", code)
	}

	// Verify fail entry is NOT in evidence list
	if strings.Contains(reportBlock, "ev-fail-old") {
		t.Fatalf("expected reportBlock not to contain failing evidence ev-fail-old")
	}
	if !strings.Contains(reportBlock, "ev-cleanpass") {
		t.Fatalf("expected reportBlock to contain clean PASS evidence ev-cleanpass")
	}

	// Parse portable bundle
	bundle, err := ParsePortableBundle(reportBlock)
	if err != nil {
		t.Fatalf("ParsePortableBundle failed: %v", err)
	}
	if bundle.HeadSHA != headSHA {
		t.Fatalf("expected bundle headSHA %s, got %s", headSHA, bundle.HeadSHA)
	}
	if bundle.Blobs["src/feature.go"] != featureBlob {
		t.Fatalf("expected bundle blob for src/feature.go to match %s", featureBlob)
	}

	// Clone repo to a fresh clone WITHOUT .git/f0/ledger.jsonl
	cloneDir := t.TempDir()
	gitCloneOut, err := exec.Command("git", "clone", repoDir, cloneDir).CombinedOutput()
	if err != nil {
		t.Fatalf("git clone failed: %v\n%s", err, string(gitCloneOut))
	}

	// Ensure no local ledger in clone
	if lPath, _ := RepoLedgerPath(cloneDir); lPath != "" {
		_ = os.Remove(lPath)
	}

	// Configure clone config
	_ = os.MkdirAll(filepath.Join(cloneDir, ".g8s"), 0o755)
	_ = os.WriteFile(filepath.Join(cloneDir, ".g8s", "f0.yml"), []byte("ci: none\nverify_commands:\n  - [\"go\", \"test\", \"./...\"]\nmanual_free:\n  - \"plans/**\"\n"), 0o644)

	// EvaluateGate with Mode: "remote" on clone
	outRemote, err := EvaluateGate(GateOptions{
		RepoDir: cloneDir,
		Action:  "merge",
		HeadSHA: headSHA,
		BaseRef: "HEAD~1",
		Mode:    "remote",
		PRBody:  reportBlock,
	})
	if err != nil {
		t.Fatalf("EvaluateGate remote mode error: %v", err)
	}
	if outRemote.Overall != "PASS" {
		t.Fatalf("expected EvaluateGate in remote mode to PASS, got %+v", outRemote)
	}

	// EvaluateGate with Mode: "auto" (default) on clone (empty ledger -> auto detects bundle)
	outAuto, err := EvaluateGate(GateOptions{
		RepoDir: cloneDir,
		Action:  "merge",
		HeadSHA: headSHA,
		BaseRef: "HEAD~1",
		Mode:    "auto",
		PRBody:  reportBlock,
	})
	if err != nil {
		t.Fatalf("EvaluateGate auto mode error: %v", err)
	}
	if outAuto.Overall != "PASS" {
		t.Fatalf("expected EvaluateGate in auto mode to PASS on bundle, got %+v", outAuto)
	}

	// Tamper: modify delivered file in clone and commit it (changing blob)
	_ = os.WriteFile(filepath.Join(cloneDir, "src", "feature.go"), []byte("package src\n// tampered code\n"), 0o644)
	_ = exec.Command("git", "-C", cloneDir, "commit", "-am", "tamper feature").Run()
	tamperHeadOut, _ := exec.Command("git", "-C", cloneDir, "rev-parse", "HEAD").Output()
	tamperHeadSHA := strings.TrimSpace(string(tamperHeadOut))

	// Recreate bundle matching tamperHeadSHA but containing the original featureBlob
	bundleTamperedFile := &PortableBundle{
		Version:     1,
		HeadSHA:     tamperHeadSHA,
		MandateID:   m.ID,
		MandateTier: "T2",
		Evidence:    bundle.Evidence,
		Blobs:       map[string]string{"src/feature.go": featureBlob},
	}
	bHash, _ := ComputeBundleHash(bundleTamperedFile)
	bundleTamperedFile.BundleSHA256 = bHash
	bBytes, _ := json.Marshal(bundleTamperedFile)
	tamperedReportBlock := "<!-- f0:status -->\n<!-- f0:bundle: " + string(bBytes) + " -->\n<!-- /f0:status -->"

	outTamperedFile, _ := EvaluateGate(GateOptions{
		RepoDir: cloneDir,
		Action:  "merge",
		HeadSHA: tamperHeadSHA,
		BaseRef: "HEAD~1",
		Mode:    "remote",
		PRBody:  tamperedReportBlock,
	})
	var g3Result *GateCheckResult
	for _, c := range outTamperedFile.Checks {
		if c.ID == "G3" {
			g3Result = &c
			break
		}
	}
	if g3Result == nil || g3Result.Status != "FAIL" || !strings.Contains(g3Result.Detail, "altered-after-delivery: src/feature.go") {
		t.Fatalf("expected G3 FAIL altered-after-delivery for tampered file, got: %+v", g3Result)
	}

	// Tamper: modify bundle JSON signature
	tamperedJSONBlock := strings.Replace(reportBlock, featureBlob[:8], "deadbeef", 1)
	outTamperedJSON, _ := EvaluateGate(GateOptions{
		RepoDir: cloneDir,
		Action:  "merge",
		HeadSHA: headSHA,
		BaseRef: "HEAD~1",
		Mode:    "remote",
		PRBody:  tamperedJSONBlock,
	})
	var g1Tamper, g3Tamper *GateCheckResult
	for _, c := range outTamperedJSON.Checks {
		if c.ID == "G1" {
			g1Tamper = &c
		}
		if c.ID == "G3" {
			g3Tamper = &c
		}
	}
	if g1Tamper == nil || g1Tamper.Status != "FAIL" || !strings.Contains(g1Tamper.Detail, "bundle verification failed") {
		t.Fatalf("expected G1 FAIL for tampered bundle hash, got: %+v", g1Tamper)
	}
	if g3Tamper == nil || g3Tamper.Status != "FAIL" || !strings.Contains(g3Tamper.Detail, "bundle verification failed") {
		t.Fatalf("expected G3 FAIL for tampered bundle hash, got: %+v", g3Tamper)
	}
}

// TestSlice06b2_DeclarativeParsers tests custom ParserRule from .g8s/f0.yml.
func TestSlice06b2_DeclarativeParsers(t *testing.T) {
	tempDir := t.TempDir()
	g8sDir := filepath.Join(tempDir, ".g8s")
	_ = os.MkdirAll(g8sDir, 0o755)

	configYml := `
ci: none
parsers:
  - name: "custom-tool"
    argv_prefix: ["tool", "test"]
    count_regex: 'Finished (\d+) tests'
    vacuous_regex: 'zero tests'
  - name: "exit-zero-check"
    argv_prefix: ["lint", "check"]
    pass_on_exit_zero: true
`
	if err := os.WriteFile(filepath.Join(g8sDir, "f0.yml"), []byte(configYml), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cfg, err := LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Parsers) != 2 {
		t.Fatalf("expected 2 parsers, got %d", len(cfg.Parsers))
	}
	if cfg.Parsers[0].Name != "custom-tool" || cfg.Parsers[0].CountRegex == "" {
		t.Fatalf("unexpected parser rule 0: %+v", cfg.Parsers[0])
	}
	if cfg.Parsers[1].Name != "exit-zero-check" || !cfg.Parsers[1].PassOnExitZero {
		t.Fatalf("unexpected parser rule 1: %+v", cfg.Parsers[1])
	}
}

// TestSlice06b2_G6Precision verifies that ordinary code changes like
// "Fixed bug in test runner" are allowed without citations, while
// "All tests passed" is strictly blocked when uncited.
func TestSlice06b2_G6Precision(t *testing.T) {
	tempDir := t.TempDir()
	ledger := OpenLedgerAtPath(filepath.Join(tempDir, "ledger.jsonl"))

	// Ordinary descriptions - MUST PASS
	allowed := []string{
		"Fixed bug in test runner",
		"Resolved lint issue",
		"Fixed problem with gate configuration",
		"Resolved issue in ci pipeline handler",
	}

	for _, text := range allowed {
		violations, err := LintClaims(text, "gate", tempDir, "head1234", "github", ledger, nil)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", text, err)
		}
		if len(violations) > 0 {
			t.Errorf("false positive for %q: got %d violations: %+v", text, len(violations), violations)
		}
	}

	// Status claims - MUST BE BLOCKED without citation
	blocked := []string{
		"All tests passed",
		"test pass",
		"ci green",
		"gate passed",
		"build succeeded",
	}

	for _, text := range blocked {
		violations, err := LintClaims(text, "gate", tempDir, "head1234", "github", ledger, nil)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", text, err)
		}
		if len(violations) == 0 {
			t.Errorf("expected violation for uncited status claim %q, got 0", text)
		}
	}
}
