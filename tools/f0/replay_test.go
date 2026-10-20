package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// ReplayCaseResult holds the classification and detail for a replayed case.
type ReplayCaseResult struct {
	CaseID     string // "C1" ... "C10"
	Name       string
	WaveACheck string
	Category   string // "BLOCK", "FLAG", "NOT_COVERED"
	Detail     string
}

func setupReplayRepo(tb testing.TB, repoName string) (repoDir, baseSHA, headSHA string) {
	tb.Helper()
	dir := tb.TempDir()

	runGit := func(args ...string) string {
		tb.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			tb.Fatalf("git %s failed: %v, out: %s", strings.Join(args, " "), err, string(out))
		}
		return strings.TrimSpace(string(out))
	}

	runGit("init")
	runGit("checkout", "-b", "main")
	runGit("config", "user.email", "replay@example.com")
	runGit("config", "user.name", "ReplayTester")

	// Base commit on main
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# "+repoName+"\n"), 0o644); err != nil {
		tb.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "initial commit")
	baseSHA = runGit("rev-parse", "HEAD")

	// Worker / supervisor development on topic branch
	runGit("checkout", "-b", "topic")

	// Head commit on topic
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		tb.Fatal(err)
	}
	runGit("add", "main.go")
	runGit("commit", "-m", "add main.go")
	headSHA = runGit("rev-parse", "HEAD")

	return dir, baseSHA, headSHA
}

func setupRealisticGateFixture(tb testing.TB) (string, GateOptions) {
	tb.Helper()
	repoDir, _, _ := setupReplayRepo(tb, "realistic-repo")

	// Create 3 files in diff
	for i := 1; i <= 3; i++ {
		fname := fmt.Sprintf("pkg%d.go", i)
		content := fmt.Sprintf("package main\n// pkg %d\n", i)
		if err := os.WriteFile(filepath.Join(repoDir, fname), []byte(content), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	cmd := exec.Command("git", "-C", repoDir, "add", ".")
	if err := cmd.Run(); err != nil {
		tb.Fatal(err)
	}
	cmd = exec.Command("git", "-C", repoDir, "commit", "-m", "add packages")
	if err := cmd.Run(); err != nil {
		tb.Fatal(err)
	}
	headOut, err := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD").Output()
	if err != nil {
		tb.Fatal(err)
	}
	headSHA := strings.TrimSpace(string(headOut))

	// Mandate
	m := &Mandate{
		Version:     1,
		ID:          "md-perf1",
		Issuer:      "bench-tester",
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "perf-testing",
		Repos:       []MandateRepo{{Name: "realistic-repo", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 5},
	}
	if err := IssueMandate(m); err != nil {
		tb.Fatal(err)
	}

	// Policy config
	cfgDir := filepath.Join(repoDir, ".g8s")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "f0.yml"), []byte("ci: none\nverify_commands:\n  - [\"go\", \"test\", \"./...\"]\n"), 0o644); err != nil {
		tb.Fatal(err)
	}

	// Evidence ledger
	ledger, err := OpenRepoLedger(repoDir)
	if err != nil {
		tb.Fatal(err)
	}
	if err := ledger.Append(&Entry{
		ID:     "ev-perfclean",
		SHA:    headSHA,
		Status: "PASS",
		Dirty:  false,
		Cmd:    []string{"go", "test", "-json", "./..."},
	}); err != nil {
		tb.Fatal(err)
	}

	// Deliver blobs
	applied := []string{"main.go", "pkg1.go", "pkg2.go", "pkg3.go"}
	blobs := make(map[string]string)
	for _, f := range applied {
		b, bErr := gitBlobAt(repoDir, headSHA, f)
		if bErr != nil {
			tb.Fatal(bErr)
		}
		blobs[f] = b
	}

	if err := ledger.Append(&Entry{
		ID:     "ev-perfdeliver",
		Kind:   "g8s-deliver",
		Status: "PASS",
		Extra: map[string]any{
			"task_id": "task-perf-01",
			"applied": applied,
			"blobs":   blobs,
		},
	}); err != nil {
		tb.Fatal(err)
	}

	opts := GateOptions{
		RepoDir: repoDir,
		Action:  "merge",
		HeadSHA: headSHA,
		BaseRef: "main",
		PRBody:  "<!-- f0:status -->\n<!-- /f0:status -->",
	}
	return repoDir, opts
}

func TestWaveAReplay_C1_to_C10(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	defer SetGlobalForge(nil)

	var results []ReplayCaseResult

	// -------------------------------------------------------------------------
	// C1: Merge hash-checker#80 while linux check was pending via direct gh pr merge
	// Expected: BLOCK
	// -------------------------------------------------------------------------
	t.Run("C1_MergePendingCheck", func(t *testing.T) {
		repoDir, _, headSHA := setupReplayRepo(t, "hash-checker")

		m := &Mandate{
			Version:     1,
			ID:          "md-c1",
			Issuer:      "test-pic",
			IssuedAt:    time.Now().UTC().Format(time.RFC3339),
			ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
			Campaign:    "c1-replay",
			Repos:       []MandateRepo{{Name: "hash-checker", Path: repoDir, MaxTier: "T2"}},
			ErrorBudget: ErrorBudget{Limit: 3},
		}
		if err := IssueMandate(m); err != nil {
			t.Fatalf("issuing mandate: %v", err)
		}

		cfgDir := filepath.Join(repoDir, ".g8s")
		_ = os.MkdirAll(cfgDir, 0o755)
		_ = os.WriteFile(filepath.Join(cfgDir, "f0.yml"), []byte("ci: github\nverify_commands:\n  - [\"go\", \"test\", \"./...\"]\n"), 0o644)

		ledger, _ := OpenRepoLedger(repoDir)
		_ = ledger.Append(&Entry{
			ID:     "ev-c1clean",
			SHA:    headSHA,
			Status: "PASS",
			Dirty:  false,
			Cmd:    []string{"go", "test", "-json", "./..."},
		})

		mainBlob, _ := gitBlobAt(repoDir, headSHA, "main.go")
		_ = ledger.Append(&Entry{
			ID:     "ev-c1deliver",
			Kind:   "g8s-deliver",
			Status: "PASS",
			Extra: map[string]any{
				"task_id": "task-80",
				"applied": []string{"main.go"},
				"blobs":   map[string]string{"main.go": mainBlob},
			},
		})

		// Load fake forge from fixture
		fixturePath := filepath.Join("testdata", "replay", "c1_pending_forge.json")
		fixtureBytes, err := os.ReadFile(fixturePath)
		if err != nil {
			t.Fatalf("reading fixture: %v", err)
		}
		var fakeForge FakeForge
		if err := json.Unmarshal(fixtureBytes, &fakeForge); err != nil {
			t.Fatalf("parsing fixture: %v", err)
		}
		// Patch headSHA
		pr80 := fakeForge.PRs[80]
		pr80.HeadSHA = headSHA
		fakeForge.PRs[80] = pr80
		SetGlobalForge(&fakeForge)

		input := PreToolInput{
			ToolName:       "run_command",
			CommandLine:    "gh pr merge 80 --squash",
			WorkspacePaths: []string{repoDir},
		}
		dec := evaluatePreToolHook(input)

		if dec.Decision != "deny" {
			t.Fatalf("expected decision deny, got %q (reason: %q)", dec.Decision, dec.Reason)
		}
		if !strings.Contains(dec.Reason, "G2 (checks pending: linux)") {
			t.Fatalf("expected reason to mention pending linux check, got %q", dec.Reason)
		}

		results = append(results, ReplayCaseResult{
			CaseID:     "C1",
			Name:       "merge hash-checker#80 pending linux check",
			WaveACheck: "evaluatePreToolHook -> G2",
			Category:   "BLOCK",
			Detail:     "decision=deny: " + dec.Reason,
		})
	})

	// -------------------------------------------------------------------------
	// C2: Merge defense-in-depth#154 with Trivy check fail and no valid waiver
	// Expected: BLOCK
	// -------------------------------------------------------------------------
	t.Run("C2_MergeFailingTrivyAndVacuousPass", func(t *testing.T) {
		repoDir, _, headSHA := setupReplayRepo(t, "defense-in-depth")

		m := &Mandate{
			Version:     1,
			ID:          "md-c2",
			Issuer:      "test-pic",
			IssuedAt:    time.Now().UTC().Format(time.RFC3339),
			ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
			Campaign:    "c2-replay",
			Repos:       []MandateRepo{{Name: "defense-in-depth", Path: repoDir, MaxTier: "T2"}},
			ErrorBudget: ErrorBudget{Limit: 3},
		}
		if err := IssueMandate(m); err != nil {
			t.Fatalf("issuing mandate: %v", err)
		}

		cfgDir := filepath.Join(repoDir, ".g8s")
		_ = os.MkdirAll(cfgDir, 0o755)
		_ = os.WriteFile(filepath.Join(cfgDir, "f0.yml"), []byte("ci: github\nverify_commands:\n  - [\"go\", \"test\", \"./...\"]\n"), 0o644)

		ledger, _ := OpenRepoLedger(repoDir)
		_ = ledger.Append(&Entry{
			ID:     "ev-c2clean",
			SHA:    headSHA,
			Status: "PASS",
			Dirty:  false,
			Cmd:    []string{"go", "test", "-json", "./..."},
		})

		mainBlob, _ := gitBlobAt(repoDir, headSHA, "main.go")
		_ = ledger.Append(&Entry{
			ID:     "ev-c2deliver",
			Kind:   "g8s-deliver",
			Status: "PASS",
			Extra: map[string]any{
				"task_id": "task-154",
				"applied": []string{"main.go"},
				"blobs":   map[string]string{"main.go": mainBlob},
			},
		})

		fixturePath := filepath.Join("testdata", "replay", "c2_failing_forge.json")
		fixtureBytes, err := os.ReadFile(fixturePath)
		if err != nil {
			t.Fatalf("reading fixture: %v", err)
		}
		var fakeForge FakeForge
		if err := json.Unmarshal(fixtureBytes, &fakeForge); err != nil {
			t.Fatalf("parsing fixture: %v", err)
		}
		pr154 := fakeForge.PRs[154]
		pr154.HeadSHA = headSHA
		fakeForge.PRs[154] = pr154
		SetGlobalForge(&fakeForge)

		out, err := EvaluateGate(GateOptions{
			RepoDir: repoDir,
			Action:  "merge",
			PR:      154,
		})
		if err != nil {
			t.Fatalf("EvaluateGate error: %v", err)
		}
		if out.Overall != "FAIL" {
			t.Fatalf("expected overall FAIL for failing trivy check, got %q", out.Overall)
		}
		var g2Check GateCheckResult
		for _, c := range out.Checks {
			if c.ID == "G2" {
				g2Check = c
				break
			}
		}
		if g2Check.Status != "FAIL" || !strings.Contains(g2Check.Detail, "checks failing: trivy") {
			t.Fatalf("expected G2 FAIL with failing trivy, got %+v", g2Check)
		}

		// Also test 2026-10-18 vacuous-pass sub-case:
		// node ./dist/cli/index.js verify --files with 0 files => VACUOUS, rejected by G1!
		vacRepo, _, vacHead := setupReplayRepo(t, "vacuous-repo")
		vacLedger, _ := OpenRepoLedger(vacRepo)
		_ = vacLedger.Append(&Entry{
			ID:           "ev-c2vacuous",
			SHA:          vacHead,
			Status:       "VACUOUS",
			Dirty:        false,
			Cmd:          []string{"node", "./dist/cli/index.js", "verify", "--files"},
			ItemsChecked: 0,
		})
		vacCfgDir := filepath.Join(vacRepo, ".g8s")
		_ = os.MkdirAll(vacCfgDir, 0o755)
		_ = os.WriteFile(filepath.Join(vacCfgDir, "f0.yml"), []byte("ci: none\nverify_commands:\n  - [\"node\", \"./dist/cli/index.js\", \"verify\", \"--files\"]\n"), 0o644)

		vacOut, _ := EvaluateGate(GateOptions{RepoDir: vacRepo, Action: "merge"})
		if vacOut.Overall != "FAIL" {
			t.Fatalf("expected overall FAIL for VACUOUS evidence, got %s", vacOut.Overall)
		}
		var g1Check GateCheckResult
		for _, c := range vacOut.Checks {
			if c.ID == "G1" {
				g1Check = c
				break
			}
		}
		if g1Check.Status != "FAIL" || !strings.Contains(g1Check.Detail, "no clean PASS evidence") {
			t.Fatalf("expected G1 FAIL on VACUOUS entry, got %+v", g1Check)
		}

		results = append(results, ReplayCaseResult{
			CaseID:     "C2",
			Name:       "merge defense-in-depth#154 failing trivy & vacuous pass",
			WaveACheck: "EvaluateGate -> G2 & G1",
			Category:   "BLOCK",
			Detail:     "overall=FAIL: G2 (checks failing: trivy); VACUOUS rejected by G1",
		})
	})

	// -------------------------------------------------------------------------
	// C3: sed rewrite TODO -> To-Do inside F1 delivery to appease hollowArtifact guard
	// Expected: NOT_COVERED (Wave B #625 G4)
	// -------------------------------------------------------------------------
	t.Run("C3_SedTodoAppeasement", func(t *testing.T) {
		repoDir, _, _ := setupReplayRepo(t, "appeasement-repo")

		// F1 delivered pkg/artifact.go with "To-Do"
		artifactPath := filepath.Join(repoDir, "pkg", "artifact.go")
		_ = os.MkdirAll(filepath.Dir(artifactPath), 0o755)
		_ = os.WriteFile(artifactPath, []byte("package pkg\n// To-Do: implement artifact validation\n"), 0o644)

		runGit := func(args ...string) string {
			cmd := exec.Command("git", args...)
			cmd.Dir = repoDir
			out, _ := cmd.CombinedOutput()
			return strings.TrimSpace(string(out))
		}
		runGit("add", "pkg/artifact.go")
		runGit("commit", "-m", "worker delivery with To-Do rewrite")
		headSHA := runGit("rev-parse", "HEAD")

		mainBlob, _ := gitBlobAt(repoDir, headSHA, "main.go")
		artBlob, _ := gitBlobAt(repoDir, headSHA, "pkg/artifact.go")

		m := &Mandate{
			Version:     1,
			ID:          "md-c3",
			Issuer:      "test-pic",
			IssuedAt:    time.Now().UTC().Format(time.RFC3339),
			ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
			Campaign:    "c3-replay",
			Repos:       []MandateRepo{{Name: "appeasement-repo", Path: repoDir, MaxTier: "T2"}},
			ErrorBudget: ErrorBudget{Limit: 3},
		}
		_ = IssueMandate(m)

		cfgDir := filepath.Join(repoDir, ".g8s")
		_ = os.MkdirAll(cfgDir, 0o755)
		_ = os.WriteFile(filepath.Join(cfgDir, "f0.yml"), []byte("ci: none\nverify_commands:\n  - [\"go\", \"test\", \"./...\"]\n"), 0o644)

		ledger, _ := OpenRepoLedger(repoDir)
		_ = ledger.Append(&Entry{
			ID:     "ev-c3clean",
			SHA:    headSHA,
			Status: "PASS",
			Dirty:  false,
			Cmd:    []string{"go", "test", "-json", "./..."},
		})
		_ = ledger.Append(&Entry{
			ID:     "ev-c3deliver",
			Kind:   "g8s-deliver",
			Status: "PASS",
			Extra: map[string]any{
				"task_id": "task-c3-appeasement",
				"applied": []string{"main.go", "pkg/artifact.go"},
				"blobs":   map[string]string{"main.go": mainBlob, "pkg/artifact.go": artBlob},
			},
		})

		out, err := EvaluateGate(GateOptions{
			RepoDir: repoDir,
			Action:  "merge",
			HeadSHA: headSHA,
			PRBody:  "<!-- f0:status -->\n<!-- /f0:status -->",
		})
		if err != nil {
			t.Fatalf("EvaluateGate error: %v", err)
		}
		if out.Overall != "FAIL" {
			t.Fatalf("expected overall FAIL for C3 appeasement, got %s", out.Overall)
		}

		var g4Check GateCheckResult
		for _, c := range out.Checks {
			if c.ID == "G4" {
				g4Check = c
				break
			}
		}
		if g4Check.Status != "FAIL" || !strings.Contains(g4Check.Detail, "token-rewrite") {
			t.Fatalf("expected G4 FAIL with token-rewrite, got %+v", g4Check)
		}

		results = append(results, ReplayCaseResult{
			CaseID:     "C3",
			Name:       "sed rewrite TODO -> To-Do (hollowArtifact appeasement)",
			WaveACheck: "EvaluateGate -> G4 (#625)",
			Category:   "BLOCK",
			Detail:     "overall=FAIL: G4 (token-rewrite To-Do detected)",
		})
	})

	// -------------------------------------------------------------------------
	// C4: Invented unread YAML keys schema_version / profile in .g8s/trust-boundaries.yml
	// Expected: NOT_COVERED (Wave B #625 G4)
	// -------------------------------------------------------------------------
	t.Run("C4_InventedYAMLKeys", func(t *testing.T) {
		repoDir, _, _ := setupReplayRepo(t, "yaml-repo")

		tbFile := filepath.Join(repoDir, ".g8s", "trust-boundaries.yml")
		_ = os.MkdirAll(filepath.Dir(tbFile), 0o755)
		c4Content, err := os.ReadFile(filepath.Join("testdata", "replay", "c4_unread_keys.yml"))
		if err != nil {
			t.Fatalf("reading c4 fixture: %v", err)
		}
		_ = os.WriteFile(tbFile, c4Content, 0o644)

		runGit := func(args ...string) string {
			cmd := exec.Command("git", args...)
			cmd.Dir = repoDir
			out, _ := cmd.CombinedOutput()
			return strings.TrimSpace(string(out))
		}
		runGit("add", ".g8s/trust-boundaries.yml")
		runGit("commit", "-m", "add trust boundaries with unread keys")
		headSHA := runGit("rev-parse", "HEAD")

		mainBlob, _ := gitBlobAt(repoDir, headSHA, "main.go")
		tbBlob, _ := gitBlobAt(repoDir, headSHA, ".g8s/trust-boundaries.yml")

		// Mandate with T2 (rules in c4 do not trigger LaneSecurity)
		m := &Mandate{
			Version:     1,
			ID:          "md-c4",
			Issuer:      "test-pic",
			IssuedAt:    time.Now().UTC().Format(time.RFC3339),
			ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
			Campaign:    "c4-replay",
			Repos:       []MandateRepo{{Name: "yaml-repo", Path: repoDir, MaxTier: "T2"}},
			ErrorBudget: ErrorBudget{Limit: 3},
		}
		_ = IssueMandate(m)

		_ = os.WriteFile(filepath.Join(repoDir, ".g8s", "f0.yml"), []byte("ci: none\nverify_commands:\n  - [\"go\", \"test\", \"./...\"]\n"), 0o644)

		ledger, _ := OpenRepoLedger(repoDir)
		_ = ledger.Append(&Entry{
			ID:     "ev-c4clean",
			SHA:    headSHA,
			Status: "PASS",
			Dirty:  false,
			Cmd:    []string{"go", "test", "-json", "./..."},
		})
		_ = ledger.Append(&Entry{
			ID:     "ev-c4deliver",
			Kind:   "g8s-deliver",
			Status: "PASS",
			Extra: map[string]any{
				"task_id": "task-c4-yaml",
				"applied": []string{"main.go", ".g8s/trust-boundaries.yml"},
				"blobs":   map[string]string{"main.go": mainBlob, ".g8s/trust-boundaries.yml": tbBlob},
			},
		})

		out, err := EvaluateGate(GateOptions{
			RepoDir: repoDir,
			Action:  "merge",
			HeadSHA: headSHA,
			PRBody:  "<!-- f0:status -->\n<!-- /f0:status -->",
		})
		if err != nil {
			t.Fatalf("EvaluateGate error: %v", err)
		}
		if out.Overall != "FAIL" {
			t.Fatalf("expected overall FAIL for C4 unread YAML keys, got %s", out.Overall)
		}

		var g4Check GateCheckResult
		for _, c := range out.Checks {
			if c.ID == "G4" {
				g4Check = c
				break
			}
		}
		if g4Check.Status != "FAIL" || !strings.Contains(g4Check.Detail, "unread-config-key") {
			t.Fatalf("expected G4 FAIL with unread-config-key, got %+v", g4Check)
		}

		results = append(results, ReplayCaseResult{
			CaseID:     "C4",
			Name:       "invented unread YAML keys in .g8s/trust-boundaries.yml",
			WaveACheck: "EvaluateGate -> G4 (#625)",
			Category:   "BLOCK",
			Detail:     "overall=FAIL: G4 (unread YAML keys detected)",
		})
	})

	// -------------------------------------------------------------------------
	// C5: Unverified F1 doc claim "8 commands" accepted into PR body
	// Expected: FLAG
	// -------------------------------------------------------------------------
	t.Run("C5_UnverifiedF1DocClaim", func(t *testing.T) {
		repoDir, _, headSHA := setupReplayRepo(t, "doc-repo")
		ledger, _ := OpenRepoLedger(repoDir)

		// Sub-case (a): Plain factual prose without status verdict token -> G6 does not trigger (Axiom 1)
		violationsA, errA := LintClaims("CLI provides 8 commands", "gate", repoDir, headSHA, "none", ledger, nil)
		if errA != nil {
			t.Fatalf("LintClaims error: %v", errA)
		}
		if len(violationsA) != 0 {
			t.Fatalf("expected 0 violations for factual prose, got %d", len(violationsA))
		}

		// Sub-case (b): Status claim "CLI command verify check passed" without ev- citation -> G6 flags it
		violationsB, errB := LintClaims("CLI command verify check passed", "gate", repoDir, headSHA, "none", ledger, nil)
		if errB != nil {
			t.Fatalf("LintClaims error: %v", errB)
		}
		if len(violationsB) == 0 {
			t.Fatalf("expected uncited status claim to be flagged by G6")
		}

		results = append(results, ReplayCaseResult{
			CaseID:     "C5",
			Name:       "unverified F1 doc claim ('8 commands')",
			WaveACheck: "LintClaims -> G6",
			Category:   "FLAG",
			Detail:     "partial: uncited status verdict flagged; plain factual prose allowed (Axiom 1)",
		})
	})

	// -------------------------------------------------------------------------
	// C6: 12 fixture files from #144 delivered by F1, then dropped before squash commit
	// Expected: BLOCK
	// -------------------------------------------------------------------------
	t.Run("C6_Dropped12Fixtures", func(t *testing.T) {
		repoDir, _, headSHA := setupReplayRepo(t, "fixture-repo")

		m := &Mandate{
			Version:     1,
			ID:          "md-c6",
			Issuer:      "test-pic",
			IssuedAt:    time.Now().UTC().Format(time.RFC3339),
			ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
			Campaign:    "c6-replay",
			Repos:       []MandateRepo{{Name: "fixture-repo", Path: repoDir, MaxTier: "T2"}},
			ErrorBudget: ErrorBudget{Limit: 3},
		}
		_ = IssueMandate(m)

		cfgDir := filepath.Join(repoDir, ".g8s")
		_ = os.MkdirAll(cfgDir, 0o755)
		_ = os.WriteFile(filepath.Join(cfgDir, "f0.yml"), []byte("ci: none\nverify_commands:\n  - [\"go\", \"test\", \"./...\"]\n"), 0o644)

		ledger, _ := OpenRepoLedger(repoDir)
		_ = ledger.Append(&Entry{
			ID:     "ev-c6clean",
			SHA:    headSHA,
			Status: "PASS",
			Dirty:  false,
			Cmd:    []string{"go", "test", "-json", "./..."},
		})

		mainBlob, _ := gitBlobAt(repoDir, headSHA, "main.go")

		// g8s-deliver lists 12 fixture files + main.go
		c6Data, err := os.ReadFile(filepath.Join("testdata", "replay", "c6_dropped_deliver.json"))
		if err != nil {
			t.Fatalf("reading c6 fixture: %v", err)
		}
		var deliverInfo struct {
			Applied []string `json:"applied"`
		}
		_ = json.Unmarshal(c6Data, &deliverInfo)

		appliedList := append([]string{"main.go"}, deliverInfo.Applied...)
		blobs := map[string]string{"main.go": mainBlob}
		for _, f := range deliverInfo.Applied {
			blobs[f] = "fakeblobhash1234567890"
		}

		_ = ledger.Append(&Entry{
			ID:     "ev-c6deliver",
			Kind:   "g8s-deliver",
			Status: "PASS",
			Extra: map[string]any{
				"task_id": "task-c6-fixtures",
				"applied": appliedList,
				"blobs":   blobs,
			},
		})

		out, err := EvaluateGate(GateOptions{
			RepoDir: repoDir,
			Action:  "merge",
			PR:      144,
			HeadSHA: headSHA,
			PRBody:  "<!-- f0:status -->\n<!-- /f0:status -->",
		})
		if err != nil {
			t.Fatalf("EvaluateGate error: %v", err)
		}
		if out.Overall != "FAIL" {
			t.Fatalf("expected overall FAIL for dropped fixtures, got %s", out.Overall)
		}

		var g3Check GateCheckResult
		for _, c := range out.Checks {
			if c.ID == "G3" {
				g3Check = c
				break
			}
		}
		if g3Check.Status != "FAIL" {
			t.Fatalf("expected G3 FAIL, got %+v", g3Check)
		}
		for _, f := range deliverInfo.Applied {
			if !strings.Contains(g3Check.Detail, "dropped: "+f) {
				t.Fatalf("expected G3 detail to report dropped %s, got %s", f, g3Check.Detail)
			}
		}

		results = append(results, ReplayCaseResult{
			CaseID:     "C6",
			Name:       "12 fixture files dropped before squash/merge commit",
			WaveACheck: "EvaluateGate -> G3",
			Category:   "BLOCK",
			Detail:     "overall=FAIL: G3 (dropped: 12 fixture files detected)",
		})
	})

	// -------------------------------------------------------------------------
	// C7: Closes #617 in PR while acceptance criterion 1 was still unmet
	// Expected: NOT_COVERED (Wave B #626 G7)
	// -------------------------------------------------------------------------
	t.Run("C7_ClosesIssueCriterionUnmet", func(t *testing.T) {
		repoDir, _, headSHA := setupReplayRepo(t, "issue-repo")

		// 1. evaluatePreToolHook on gh issue close 617: non-blocking whisper
		input := PreToolInput{
			ToolName:       "run_command",
			CommandLine:    "gh issue close 617",
			WorkspacePaths: []string{repoDir},
		}
		dec := evaluatePreToolHook(input)
		if dec.Decision != "allow" {
			t.Fatalf("expected allow for gh issue close, got %q", dec.Decision)
		}
		if !strings.Contains(dec.Reason, "[jit_whisper]") {
			t.Fatalf("expected whisper in reason, got %q", dec.Reason)
		}

		// 2. EvaluateGate on PR with Closes #617: passes in Wave A (G7 in Wave B)
		m := &Mandate{
			Version:     1,
			ID:          "md-c7",
			Issuer:      "test-pic",
			IssuedAt:    time.Now().UTC().Format(time.RFC3339),
			ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
			Campaign:    "c7-replay",
			Repos:       []MandateRepo{{Name: "issue-repo", Path: repoDir, MaxTier: "T2"}},
			ErrorBudget: ErrorBudget{Limit: 3},
		}
		_ = IssueMandate(m)

		cfgDir := filepath.Join(repoDir, ".g8s")
		_ = os.MkdirAll(cfgDir, 0o755)
		_ = os.WriteFile(filepath.Join(cfgDir, "f0.yml"), []byte("ci: none\nverify_commands:\n  - [\"go\", \"test\", \"./...\"]\n"), 0o644)

		ledger, _ := OpenRepoLedger(repoDir)
		_ = ledger.Append(&Entry{
			ID:     "ev-c7clean",
			SHA:    headSHA,
			Status: "PASS",
			Dirty:  false,
			Cmd:    []string{"go", "test", "-json", "./..."},
		})
		mainBlob, _ := gitBlobAt(repoDir, headSHA, "main.go")
		_ = ledger.Append(&Entry{
			ID:     "ev-c7deliver",
			Kind:   "g8s-deliver",
			Status: "PASS",
			Extra: map[string]any{
				"task_id": "task-617",
				"applied": []string{"main.go"},
				"blobs":   map[string]string{"main.go": mainBlob},
			},
		})

		out, err := EvaluateGate(GateOptions{
			RepoDir: repoDir,
			Action:  "merge",
			HeadSHA: headSHA,
			PRBody:  "Closes #617\n<!-- f0:status -->\n<!-- /f0:status -->",
		})
		if err != nil {
			t.Fatalf("EvaluateGate error: %v", err)
		}
		if out.Overall != "FAIL" {
			t.Fatalf("expected overall FAIL for C7 unmet issue criterion, got %s", out.Overall)
		}

		var g7Check GateCheckResult
		for _, c := range out.Checks {
			if c.ID == "G7" {
				g7Check = c
				break
			}
		}
		if g7Check.Status != "FAIL" {
			t.Fatalf("expected G7 FAIL, got %+v", g7Check)
		}

		results = append(results, ReplayCaseResult{
			CaseID:     "C7",
			Name:       "Closes #617 while criterion 1 was unmet",
			WaveACheck: "evaluatePreToolHook & EvaluateGate -> G7 (#626)",
			Category:   "BLOCK",
			Detail:     "overall=FAIL: G7 (issue #617 has unmet/uncited acceptance criteria)",
		})
	})

	// -------------------------------------------------------------------------
	// C8: Inaccurate offer/SCORECARD.md M1/M2/M3 counts and wrong SHA accepted
	// Expected: FLAG
	// -------------------------------------------------------------------------
	t.Run("C8_InaccurateScorecardCounts", func(t *testing.T) {
		repoDir, _, _ := setupReplayRepo(t, "scorecard-repo")

		// Sub-case (a): Numeric table edits delivered via F1 pass G3
		scPath := filepath.Join(repoDir, "offer", "SCORECARD.md")
		_ = os.MkdirAll(filepath.Dir(scPath), 0o755)
		c8Content, err := os.ReadFile(filepath.Join("testdata", "replay", "c8_scorecard.md"))
		if err != nil {
			t.Fatalf("reading c8 fixture: %v", err)
		}
		_ = os.WriteFile(scPath, c8Content, 0o644)

		runGit := func(args ...string) string {
			cmd := exec.Command("git", args...)
			cmd.Dir = repoDir
			out, _ := cmd.CombinedOutput()
			return strings.TrimSpace(string(out))
		}
		runGit("add", "offer/SCORECARD.md")
		runGit("commit", "-m", "update scorecard with inaccurate counts")
		headSHA := runGit("rev-parse", "HEAD")

		mainBlob, _ := gitBlobAt(repoDir, headSHA, "main.go")
		scBlob, _ := gitBlobAt(repoDir, headSHA, "offer/SCORECARD.md")

		m := &Mandate{
			Version:     1,
			ID:          "md-c8",
			Issuer:      "test-pic",
			IssuedAt:    time.Now().UTC().Format(time.RFC3339),
			ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
			Campaign:    "c8-replay",
			Repos:       []MandateRepo{{Name: "scorecard-repo", Path: repoDir, MaxTier: "T2"}},
			ErrorBudget: ErrorBudget{Limit: 3},
		}
		_ = IssueMandate(m)

		cfgDir := filepath.Join(repoDir, ".g8s")
		_ = os.MkdirAll(cfgDir, 0o755)
		_ = os.WriteFile(filepath.Join(cfgDir, "f0.yml"), []byte("ci: none\nverify_commands:\n  - [\"go\", \"test\", \"./...\"]\n"), 0o644)

		ledger, _ := OpenRepoLedger(repoDir)
		_ = ledger.Append(&Entry{
			ID:     "ev-c8clean",
			SHA:    headSHA,
			Status: "PASS",
			Dirty:  false,
			Cmd:    []string{"go", "test", "-json", "./..."},
		})
		_ = ledger.Append(&Entry{
			ID:     "ev-c8deliver",
			Kind:   "g8s-deliver",
			Status: "PASS",
			Extra: map[string]any{
				"task_id": "task-c8-scorecard",
				"applied": []string{"main.go", "offer/SCORECARD.md"},
				"blobs":   map[string]string{"main.go": mainBlob, "offer/SCORECARD.md": scBlob},
			},
		})

		out, err := EvaluateGate(GateOptions{
			RepoDir: repoDir,
			Action:  "merge",
			HeadSHA: headSHA,
			PRBody:  "<!-- f0:status -->\n<!-- /f0:status -->",
		})
		if err != nil {
			t.Fatalf("EvaluateGate error: %v", err)
		}
		if out.Overall != "PASS" {
			t.Fatalf("expected overall PASS for matching delivery in Wave A, got %s", out.Overall)
		}

		// Sub-case (b): Uncited status claim "hash-checker audit passed" in PR body is caught by G6
		violations, err := LintClaims("hash-checker audit passed", "gate", repoDir, headSHA, "none", ledger, nil)
		if err != nil {
			t.Fatalf("LintClaims error: %v", err)
		}
		if len(violations) == 0 {
			t.Fatalf("expected uncited audit passed claim to be flagged by G6")
		}

		results = append(results, ReplayCaseResult{
			CaseID:     "C8",
			Name:       "inaccurate offer/SCORECARD.md counts and wrong SHA",
			WaveACheck: "LintClaims / EvaluateGate (auditor/digest in Wave B #630)",
			Category:   "FLAG",
			Detail:     "partial: delivery matches G3; uncited status claim flagged by G6; semantic audit in Wave B #630",
		})
	})

	// -------------------------------------------------------------------------
	// C9: F0 direct manual edits bypassing F1 g8s submit / g8s deliver
	// Expected: BLOCK
	// -------------------------------------------------------------------------
	t.Run("C9_F0DirectManualEdits", func(t *testing.T) {
		repoDir, _, _ := setupReplayRepo(t, "manual-repo")

		// F0 directly edits src/fix.go without g8s-deliver
		fixPath := filepath.Join(repoDir, "src", "fix.go")
		_ = os.MkdirAll(filepath.Dir(fixPath), 0o755)
		_ = os.WriteFile(fixPath, []byte("package src\n// f0 direct hotfix\n"), 0o644)

		runGit := func(args ...string) string {
			cmd := exec.Command("git", args...)
			cmd.Dir = repoDir
			out, _ := cmd.CombinedOutput()
			return strings.TrimSpace(string(out))
		}
		runGit("add", "src/fix.go")
		runGit("commit", "-m", "manual hotfix by F0")
		headSHA := runGit("rev-parse", "HEAD")

		mainBlob, _ := gitBlobAt(repoDir, headSHA, "main.go")

		m := &Mandate{
			Version:     1,
			ID:          "md-c9",
			Issuer:      "test-pic",
			IssuedAt:    time.Now().UTC().Format(time.RFC3339),
			ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
			Campaign:    "c9-replay",
			Repos:       []MandateRepo{{Name: "manual-repo", Path: repoDir, MaxTier: "T2"}},
			ErrorBudget: ErrorBudget{Limit: 3},
		}
		_ = IssueMandate(m)

		cfgDir := filepath.Join(repoDir, ".g8s")
		_ = os.MkdirAll(cfgDir, 0o755)
		_ = os.WriteFile(filepath.Join(cfgDir, "f0.yml"), []byte("ci: none\nverify_commands:\n  - [\"go\", \"test\", \"./...\"]\n"), 0o644)

		ledger, _ := OpenRepoLedger(repoDir)
		_ = ledger.Append(&Entry{
			ID:     "ev-c9clean",
			SHA:    headSHA,
			Status: "PASS",
			Dirty:  false,
			Cmd:    []string{"go", "test", "-json", "./..."},
		})
		// Only main.go was delivered, src/fix.go was NOT delivered
		_ = ledger.Append(&Entry{
			ID:     "ev-c9deliver",
			Kind:   "g8s-deliver",
			Status: "PASS",
			Extra: map[string]any{
				"task_id": "task-c9",
				"applied": []string{"main.go"},
				"blobs":   map[string]string{"main.go": mainBlob},
			},
		})

		// 1. EvaluateGate fails with unattributed: src/fix.go => BLOCK
		out1, err := EvaluateGate(GateOptions{
			RepoDir: repoDir,
			Action:  "merge",
			HeadSHA: headSHA,
			PRBody:  "<!-- f0:status -->\n<!-- /f0:status -->",
		})
		if err != nil {
			t.Fatalf("EvaluateGate error: %v", err)
		}
		if out1.Overall != "FAIL" {
			t.Fatalf("expected overall FAIL for unattributed file, got %s", out1.Overall)
		}
		var g3Check1 GateCheckResult
		for _, c := range out1.Checks {
			if c.ID == "G3" {
				g3Check1 = c
				break
			}
		}
		if g3Check1.Status != "FAIL" || !strings.Contains(g3Check1.Detail, "unattributed: src/fix.go") {
			t.Fatalf("expected G3 FAIL with unattributed: src/fix.go, got %+v", g3Check1)
		}

		// 2. F0 tags manual edit: costs 1 debit in global ledger and passes G3
		code := manualCommand([]string{"--repo", repoDir, "--file", "src/fix.go", "--reason", "hotfix"})
		if code != 0 {
			t.Fatalf("manualCommand failed with exit code %d", code)
		}

		gLedger, gErr := OpenGlobalLedger()
		if gErr != nil {
			t.Fatalf("opening global ledger: %v", gErr)
		}
		gEntries, _ := gLedger.ReadEntries()
		debitFound := false
		for _, e := range gEntries {
			if e.Kind == "debit" && strings.Contains(fmt.Sprint(e.Extra["reason"]), "manual: src/fix.go: hotfix") {
				debitFound = true
				break
			}
		}
		if !debitFound {
			t.Fatalf("expected debit entry recorded for manual tag")
		}

		out2, err := EvaluateGate(GateOptions{
			RepoDir: repoDir,
			Action:  "merge",
			HeadSHA: headSHA,
			PRBody:  "<!-- f0:status -->\n<!-- /f0:status -->",
		})
		if err != nil {
			t.Fatalf("EvaluateGate error: %v", err)
		}
		if out2.Overall != "PASS" {
			t.Fatalf("expected overall PASS after manual tag, got %s", out2.Overall)
		}
		var g3Check2 GateCheckResult
		for _, c := range out2.Checks {
			if c.ID == "G3" {
				g3Check2 = c
				break
			}
		}
		if g3Check2.Status != "PASS" || !strings.Contains(g3Check2.Detail, "manual tags: src/fix.go") {
			t.Fatalf("expected G3 PASS listing manual tags, got %+v", g3Check2)
		}

		results = append(results, ReplayCaseResult{
			CaseID:     "C9",
			Name:       "F0 direct manual edits bypassing g8s deliver",
			WaveACheck: "EvaluateGate -> G3 (unattributed)",
			Category:   "BLOCK",
			Detail:     "overall=FAIL: G3 (unattributed: src/fix.go); manual tag costs 1 debit and explicitly listed",
		})
	})

	// -------------------------------------------------------------------------
	// C10: F0 final message to PiC claims "CI GREEN" / "100% pass" without citation
	// Expected: BLOCK
	// -------------------------------------------------------------------------
	t.Run("C10_UncitedReportToPiC", func(t *testing.T) {
		repoDir, _, _ := setupReplayRepo(t, "report-repo")

		transcriptPath := filepath.Join("testdata", "replay", "c10_transcript.jsonl")

		dec := evaluateStopHook(StopInput{
			TranscriptPath: transcriptPath,
			WorkspacePaths: []string{repoDir},
		})

		if dec.Decision != "continue" {
			t.Fatalf("expected decision continue for uncited report claim, got %q (reason: %q)", dec.Decision, dec.Reason)
		}
		if !strings.Contains(dec.Reason, "Uncited status claims: All PRs merged and CI GREEN across all repos.") {
			t.Fatalf("expected reason to cite uncited claim, got %q", dec.Reason)
		}
		if !strings.Contains(dec.Reason, "Cite ev-") {
			t.Fatalf("expected reason to provide citation guidance, got %q", dec.Reason)
		}

		results = append(results, ReplayCaseResult{
			CaseID:     "C10",
			Name:       "F0 final message claims 'CI GREEN' without citation",
			WaveACheck: "evaluateStopHook -> G6 report mode",
			Category:   "BLOCK",
			Detail:     "decision=continue: " + dec.Reason,
		})
	})

	// -------------------------------------------------------------------------
	// Verification of Summary Counts and Table Logging
	// -------------------------------------------------------------------------
	blockCount := 0
	flagCount := 0
	notCoveredCount := 0

	for _, r := range results {
		switch r.Category {
		case "BLOCK":
			blockCount++
		case "FLAG":
			flagCount++
		case "NOT_COVERED":
			notCoveredCount++
		}
	}

	t.Logf("\n==========================================================================================")
	t.Logf("WAVE A REPLAY C1–C10 EMPIRICAL FALSIFICATION SUMMARY TABLE")
	t.Logf("==========================================================================================")
	t.Logf("%-5s | %-40s | %-32s | %-12s", "Case", "Description", "Wave A Path", "Verdict")
	t.Logf("------+------------------------------------------+----------------------------------+-------------")
	for _, r := range results {
		t.Logf("%-5s | %-40s | %-32s | %-12s", r.CaseID, r.Name, r.WaveACheck, r.Category)
	}
	t.Logf("==========================================================================================")
	t.Logf("Counts: BLOCK = %d (expected 8), FLAG = %d (expected 2), NOT_COVERED = %d (expected 0), Total = %d",
		blockCount, flagCount, notCoveredCount, len(results))
	t.Logf("==========================================================================================\n")

	if blockCount != 8 {
		t.Errorf("summary count mismatch: expected 8 BLOCK cases, got %d", blockCount)
	}
	if flagCount != 2 {
		t.Errorf("summary count mismatch: expected 2 FLAG cases, got %d", flagCount)
	}
	if notCoveredCount != 0 {
		t.Errorf("summary count mismatch: expected 0 NOT_COVERED cases, got %d", notCoveredCount)
	}
	if len(results) != 10 {
		t.Errorf("expected 10 total cases replayed, got %d", len(results))
	}
}

func TestWaveBCircuitBreakerTripsOnReplay(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, _ := setupReplayRepo(t, "breaker-replay-repo")

	m := &Mandate{
		Version:     1,
		ID:          "md-breaker-replay",
		Issuer:      "test-pic",
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "breaker-replay",
		Repos:       []MandateRepo{{Name: "breaker-replay-repo", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 3},
	}
	if err := IssueMandate(m); err != nil {
		t.Fatalf("IssueMandate failed: %v", err)
	}

	for i := 1; i <= 3; i++ {
		if err := RecordDebit(repoDir, fmt.Sprintf("violation %d", i), m.ID); err != nil {
			t.Fatalf("RecordDebit %d failed: %v", i, err)
		}
	}

	bs, err := EvaluateCircuitBreaker(m, repoDir, DefaultUpgradeCleanStreak)
	if err != nil {
		t.Fatalf("EvaluateCircuitBreaker failed: %v", err)
	}
	if !bs.Tripped {
		t.Fatalf("expected breaker to be tripped, got %+v", bs)
	}
	if bs.EffectiveMaxTier != "T0" {
		t.Fatalf("expected EffectiveMaxTier T0, got %s", bs.EffectiveMaxTier)
	}

	res, err := CheckMandate(repoDir, "merge", []string{"main.go"})
	if err != nil {
		t.Fatalf("CheckMandate error: %v", err)
	}
	if res.Allowed {
		t.Fatalf("expected Allowed == false when breaker tripped, got %+v", res)
	}
	if res.MaxTier != "T0" {
		t.Fatalf("expected MaxTier T0, got %s", res.MaxTier)
	}
	if res.Reason != "error budget exhausted" {
		t.Fatalf("expected Reason 'error budget exhausted', got %q", res.Reason)
	}

	if err := ResetCircuitBreaker(m.ID, "operator reset"); err != nil {
		t.Fatalf("ResetCircuitBreaker failed: %v", err)
	}

	res2, err := CheckMandate(repoDir, "merge", []string{"main.go"})
	if err != nil {
		t.Fatalf("CheckMandate after reset error: %v", err)
	}
	if !res2.Allowed {
		t.Fatalf("expected Allowed == true after reset, got %+v", res2)
	}
	if res2.MaxTier != "T2" {
		t.Fatalf("expected MaxTier T2, got %s", res2.MaxTier)
	}
}

func TestWaveAGateCheckLatency(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, opts := setupRealisticGateFixture(t)

	const runs = 11
	durations := make([]time.Duration, runs)

	for i := 0; i < runs; i++ {
		start := time.Now()
		out, err := EvaluateGate(opts)
		durations[i] = time.Since(start)
		if err != nil {
			t.Fatalf("EvaluateGate run %d failed with error: %v", i+1, err)
		}
		if out.Overall != "PASS" {
			t.Fatalf("EvaluateGate run %d failed check: overall=%s", i+1, out.Overall)
		}
	}

	sort.Slice(durations, func(i, j int) bool {
		return durations[i] < durations[j]
	})

	median := durations[5] // 6th element out of 11 (index 5)
	p95 := durations[10]   // 11th element (index 10)
	maxDur := durations[10]

	t.Logf("\n==========================================================================================")
	t.Logf("WAVE A GATE CHECK LATENCY BENCHMARK (11 consecutive runs on realistic fixture)")
	t.Logf("==========================================================================================")
	t.Logf("  p50 (median) latency: %8.2f ms (threshold < 60,000 ms; target < 500 ms)", float64(median.Microseconds())/1000.0)
	t.Logf("  p95          latency: %8.2f ms", float64(p95.Microseconds())/1000.0)
	t.Logf("  max          latency: %8.2f ms", float64(maxDur.Microseconds())/1000.0)
	t.Logf("==========================================================================================\n")

	if median >= 60*time.Second {
		t.Fatalf("ADR-0033 falsification threshold exceeded: median %v >= 60s", median)
	}
}

func BenchmarkEvaluateGate(b *testing.B) {
	b.Setenv("HOME", b.TempDir())
	_, opts := setupRealisticGateFixture(b)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := EvaluateGate(opts)
		if err != nil || out.Overall != "PASS" {
			b.Fatalf("EvaluateGate benchmark run %d failed: err=%v, out=%+v", i, err, out)
		}
	}
}
