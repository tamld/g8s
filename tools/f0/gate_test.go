package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupTestGitRepo(t *testing.T) (repoDir string, baseSHA string, headSHA string) {
	t.Helper()
	dir := t.TempDir()

	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s failed: %v, out: %s", strings.Join(args, " "), err, string(out))
		}
		return strings.TrimSpace(string(out))
	}

	runGit("init")
	runGit("checkout", "-b", "main")
	runGit("config", "user.email", "tester@example.com")
	runGit("config", "user.name", "Tester")

	// Commit 1: base
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "initial commit")
	baseSHA = runGit("rev-parse", "HEAD")

	// Commit 2: head
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "main.go")
	runGit("commit", "-m", "add main.go")
	headSHA = runGit("rev-parse", "HEAD")

	return dir, baseSHA, headSHA
}

func TestGate_G0(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, baseSHA, headSHA := setupTestGitRepo(t)

	// Issue a mandate with T2 max tier
	m := &Mandate{
		Version:   1,
		ID:        "md-g0test1",
		Issuer:    "test-operator",
		IssuedAt:  time.Now().UTC().Format(time.RFC3339),
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:  "g0 testing",
		Repos: []MandateRepo{
			{Name: "testrepo", Path: repoDir, MaxTier: "T2"},
		},
		ExcludedPaths: []string{"secrets/**"},
		ErrorBudget:   ErrorBudget{Limit: 3},
	}
	if err := IssueMandate(m); err != nil {
		t.Fatalf("issuing mandate: %v", err)
	}

	// 1. Regular merge action allowed at T2
	resAllowed, err := CheckMandate(repoDir, "merge", []string{"main.go"})
	if err != nil || !resAllowed.Allowed {
		t.Errorf("expected allowed=true for T2 merge, got %+v (err=%v)", resAllowed, err)
	}

	// 2. Publish requires T4 -> denied
	resDenied, err := CheckMandate(repoDir, "publish", []string{"main.go"})
	if err != nil || resDenied.Allowed {
		t.Errorf("expected allowed=false for T4 publish, got %+v (err=%v)", resDenied, err)
	}

	// 3. Diff containing excluded path escalates to T3 -> denied for T2 mandate
	resT3, err := CheckMandate(repoDir, "merge", []string{"secrets/pass.txt"})
	if err != nil || resT3.Allowed {
		t.Errorf("expected allowed=false for T3 escalation on excluded path, got %+v (err=%v)", resT3, err)
	}
	if resT3.RequiredTier != "T3" {
		t.Errorf("expected required_tier=T3, got %s", resT3.RequiredTier)
	}

	_ = baseSHA
	_ = headSHA
}

func TestGate_G1(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, baseSHA, headSHA := setupTestGitRepo(t)

	ledger, err := OpenRepoLedger(repoDir)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		CI:             "none",
		VerifyCommands: [][]string{{"go", "test", "./..."}},
	}

	// Helper to run G1 check logic directly
	checkG1 := func() (bool, string) {
		entries, _ := ledger.ReadEntries()
		for i := len(entries) - 1; i >= 0; i-- {
			e := &entries[i]
			if e.Status != "PASS" || e.Dirty {
				continue
			}
			if !strings.HasPrefix(e.SHA, headSHA) && !strings.HasPrefix(headSHA, e.SHA) {
				continue
			}
			if matchVerifyCommand(e.Cmd, cfg.VerifyCommands) {
				return true, e.ID
			}
		}
		return false, ""
	}

	// 1. Empty ledger -> G1 fails
	if ok, _ := checkG1(); ok {
		t.Error("expected G1 to fail on empty ledger")
	}

	// 2. Dirty entry -> G1 fails
	_ = ledger.Append(&Entry{
		ID:     "ev-dirty",
		SHA:    headSHA,
		Status: "PASS",
		Dirty:  true,
		Cmd:    []string{"go", "test", "./..."},
	})
	if ok, _ := checkG1(); ok {
		t.Error("expected G1 to reject dirty entry")
	}

	// 3. Non-canonical cmd -> G1 fails
	_ = ledger.Append(&Entry{
		ID:     "ev-noncanon",
		SHA:    headSHA,
		Status: "PASS",
		Dirty:  false,
		Cmd:    []string{"echo", "test"},
	})
	if ok, _ := checkG1(); ok {
		t.Error("expected G1 to reject non-canonical command")
	}

	// 4. VACUOUS entry -> G1 fails
	_ = ledger.Append(&Entry{
		ID:     "ev-vacuous",
		SHA:    headSHA,
		Status: "VACUOUS",
		Dirty:  false,
		Cmd:    []string{"go", "test", "./..."},
	})
	if ok, _ := checkG1(); ok {
		t.Error("expected G1 to reject VACUOUS entry")
	}

	// 5. Wrong SHA entry -> G1 fails
	_ = ledger.Append(&Entry{
		ID:     "ev-wrongsha",
		SHA:    baseSHA,
		Status: "PASS",
		Dirty:  false,
		Cmd:    []string{"go", "test", "./..."},
	})
	if ok, _ := checkG1(); ok {
		t.Error("expected G1 to reject wrong SHA entry")
	}

	// 6. Canonical go test ./... with injected -json -> G1 PASSES
	_ = ledger.Append(&Entry{
		ID:     "ev-canonical",
		SHA:    headSHA,
		Status: "PASS",
		Dirty:  false,
		Cmd:    []string{"go", "test", "-json", "./..."},
	})
	ok, evID := checkG1()
	if !ok || evID != "ev-canonical" {
		t.Errorf("expected G1 to accept canonical go test with -json, got ok=%v, evID=%s", ok, evID)
	}

	// 7. Canonical with leading env vars -> G1 PASSES
	_ = ledger.Append(&Entry{
		ID:     "ev-envvar",
		SHA:    headSHA,
		Status: "PASS",
		Dirty:  false,
		Cmd:    []string{"env", "CGO_ENABLED=0", "go", "test", "-json", "./..."},
	})
	ok, evID = checkG1()
	if !ok || evID != "ev-envvar" {
		t.Errorf("expected G1 to accept go test with env prefix, got ok=%v, evID=%s", ok, evID)
	}
}

func TestGate_G2(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupTestGitRepo(t)

	ledger, err := OpenRepoLedger(repoDir)
	if err != nil {
		t.Fatal(err)
	}

	forge := NewFakeForge()
	SetGlobalForge(forge)
	defer SetGlobalForge(nil)

	// Issue active mandate
	m := &Mandate{
		Version:   1,
		ID:        "md-g2test",
		Issuer:    "test-op",
		IssuedAt:  time.Now().UTC().Format(time.RFC3339),
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:  "g2 tests",
		Repos:     []MandateRepo{{Name: "testrepo", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 2},
	}
	_ = IssueMandate(m)

	pr := 42

	// Setup config with ci: github
	cfgDir := filepath.Join(repoDir, ".g8s")
	_ = os.MkdirAll(cfgDir, 0755)
	_ = os.WriteFile(filepath.Join(cfgDir, "f0.yml"), []byte("ci: github\nverify_commands:\n  - [\"go\", \"test\", \"./...\"]\n"), 0644)

	// Add G1 PASS evidence so G1 passes
	_ = ledger.Append(&Entry{
		ID:     "ev-g1clean",
		SHA:    headSHA,
		Status: "PASS",
		Dirty:  false,
		Cmd:    []string{"go", "test", "-json", "./..."},
	})

	runGate := func() (overall string, g2Check GateCheckResult) {
		t.Helper()
		// Capture stdout
		oldStdout := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		code := runGateCheck([]string{
			"--repo", repoDir,
			"--action", "merge",
			"--pr", "42",
			"--head-sha", headSHA,
			"--base", "main",
			"--pr-body", "<!-- f0:status -->\n<!-- /f0:status -->",
		})

		_ = w.Close()
		os.Stdout = oldStdout

		var buf [8192]byte
		n, _ := r.Read(buf[:])
		var out GateOutput
		_ = json.Unmarshal(buf[:n], &out)

		for _, c := range out.Checks {
			if c.ID == "G2" {
				g2Check = c
			}
		}
		if code == 0 {
			overall = "PASS"
		} else {
			overall = "FAIL"
		}
		return overall, g2Check
	}

	// 1. Zero checks reported -> FAIL
	forge.Checks[pr] = []ForgeCheck{}
	_, g2 := runGate()
	if g2.Status != "FAIL" || !strings.Contains(g2.Detail, "no checks reported yet") {
		t.Errorf("expected G2 FAIL for zero checks, got %+v", g2)
	}

	// 2. Pending check -> FAIL
	forge.Checks[pr] = []ForgeCheck{
		{Name: "unit-tests", Bucket: "pending"},
	}
	_, g2 = runGate()
	if g2.Status != "FAIL" || !strings.Contains(g2.Detail, "checks pending") {
		t.Errorf("expected G2 FAIL for pending check, got %+v", g2)
	}

	// 3. Failing check without waiver -> FAIL
	forge.Checks[pr] = []ForgeCheck{
		{Name: "lint", Bucket: "fail"},
	}
	_, g2 = runGate()
	if g2.Status != "FAIL" || !strings.Contains(g2.Detail, "checks failing") {
		t.Errorf("expected G2 FAIL for failing check without waiver, got %+v", g2)
	}

	// 4. Failing check with valid waiver -> PASS
	issueURL := "https://github.com/tamld/g8s/issues/100"
	forge.Issues[issueURL] = FakeIssue{
		SameRepo: true,
		Open:     true,
		Title:    "Track flaky lint check",
		Body:     "Discussion on lint failures",
	}
	_ = ledger.Append(&Entry{
		ID:     "ev-waiver1",
		Kind:   "waiver",
		Status: "PASS",
		SHA:    headSHA,
		Extra: map[string]any{
			"pr":       pr,
			"check":    "lint",
			"head_sha": headSHA,
			"issue":    issueURL,
			"reason":   "known flake",
		},
	})
	_ = RecordDebit(repoDir, "waiver: lint", m.ID)

	_, g2 = runGate()
	if g2.Status != "PASS" || !strings.Contains(g2.Detail, "waived: lint") {
		t.Errorf("expected G2 PASS with valid waiver, got %+v", g2)
	}

	// 5. Waiver bound to wrong SHA -> FAIL
	_ = ledger.Append(&Entry{
		ID:     "ev-waiver-wrongsha",
		Kind:   "waiver",
		Status: "PASS",
		SHA:    "wrongsha1234567890",
		Extra: map[string]any{
			"pr":       pr,
			"check":    "lint",
			"head_sha": "wrongsha1234567890",
			"issue":    issueURL,
			"reason":   "known flake",
		},
	})
	// Temporarily override forge check to something only matching second waiver
	forge.Checks[pr] = []ForgeCheck{
		{Name: "integration", Bucket: "fail"},
	}
	_ = ledger.Append(&Entry{
		ID:     "ev-waiver-integ-wrongsha",
		Kind:   "waiver",
		Status: "PASS",
		SHA:    "wrongsha1234567890",
		Extra: map[string]any{
			"pr":       pr,
			"check":    "integration",
			"head_sha": "wrongsha1234567890",
			"issue":    issueURL,
		},
	})
	_, g2 = runGate()
	if g2.Status != "FAIL" {
		t.Errorf("expected G2 FAIL for waiver on wrong SHA, got %+v", g2)
	}

	// 6. Waiver issue in another repo -> FAIL
	forge.Checks[pr] = []ForgeCheck{{Name: "lint", Bucket: "fail"}}
	otherRepoIssue := "https://github.com/otherowner/otherrepo/issues/200"
	forge.Issues[otherRepoIssue] = FakeIssue{
		SameRepo: false,
		Open:     true,
		Title:    "lint",
	}
	_ = ledger.Append(&Entry{
		ID:     "ev-waiver-otherrepo",
		Kind:   "waiver",
		Status: "PASS",
		SHA:    headSHA,
		Extra: map[string]any{
			"pr":       pr,
			"check":    "lint",
			"head_sha": headSHA,
			"issue":    otherRepoIssue,
		},
	})
	_, g2 = runGate()
	if g2.Status != "FAIL" || !strings.Contains(g2.Detail, "another repository") {
		t.Errorf("expected G2 FAIL for waiver in another repo, got %+v", g2)
	}

	// 7. Waiver when budget exhausted -> FAIL
	// Exhaust budget: limit=2, add 2 more debits
	_ = RecordDebit(repoDir, "debit2", m.ID)
	_ = RecordDebit(repoDir, "debit3", m.ID)
	// Now spent is 3 > limit 2
	forge.Issues[issueURL] = FakeIssue{SameRepo: true, Open: true, Title: "lint"}
	_ = ledger.Append(&Entry{
		ID:     "ev-waiver-exhausted",
		Kind:   "waiver",
		Status: "PASS",
		SHA:    headSHA,
		Extra: map[string]any{
			"pr":       pr,
			"check":    "lint",
			"head_sha": headSHA,
			"issue":    issueURL,
		},
	})
	_, g2 = runGate()
	if g2.Status != "FAIL" || !strings.Contains(g2.Detail, "budget exhausted") {
		t.Errorf("expected G2 FAIL when budget exhausted, got %+v", g2)
	}
}

func TestGate_G3(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, baseSHA, headSHA := setupTestGitRepo(t)

	ledger, err := OpenRepoLedger(repoDir)
	if err != nil {
		t.Fatal(err)
	}

	// In setupTestGitRepo:
	// diff between base and head is main.go.
	// Get blob of main.go at head
	mainBlob, err := gitBlobAt(repoDir, headSHA, "main.go")
	if err != nil || mainBlob == "" {
		t.Fatalf("getting blob for main.go: %v", err)
	}

	checkG3 := func(fullText string) (string, GateCheckResult) {
		oldStdout := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		_ = runGateCheck([]string{
			"--repo", repoDir,
			"--action", "merge",
			"--head-sha", headSHA,
			"--base", baseSHA,
			"--pr-body", fullText,
		})

		_ = w.Close()
		os.Stdout = oldStdout

		var buf [8192]byte
		n, _ := r.Read(buf[:])
		var out GateOutput
		_ = json.Unmarshal(buf[:n], &out)

		for _, c := range out.Checks {
			if c.ID == "G3" {
				return out.Overall, c
			}
		}
		return out.Overall, GateCheckResult{ID: "G3", Status: "UNKNOWN"}
	}

	// 1. Unattributed file: main.go in diff but no deliver entry
	_, g3 := checkG3("")
	if g3.Status != "FAIL" || !strings.Contains(g3.Detail, "unattributed: main.go") {
		t.Errorf("expected G3 FAIL for unattributed file, got %+v", g3)
	}

	// 2. Dry-run deliver is excluded -> still FAIL
	_ = ledger.Append(&Entry{
		ID:     "ev-deliver-dry",
		Kind:   "g8s-deliver",
		Status: "PASS",
		Ts:     time.Now().UTC().Format(time.RFC3339),
		Extra: map[string]any{
			"task_id": "task-dry",
			"applied": []string{"main.go"},
			"blobs":   map[string]any{"main.go": mainBlob},
			"dry_run": true,
		},
	})
	_, g3 = checkG3("")
	if g3.Status != "FAIL" || !strings.Contains(g3.Detail, "unattributed: main.go") {
		t.Errorf("expected G3 FAIL when only dry-run deliver exists, got %+v", g3)
	}

	// 3. Valid non-dry-run deliver -> PASS
	_ = ledger.Append(&Entry{
		ID:     "ev-deliver-real",
		Kind:   "g8s-deliver",
		Status: "PASS",
		Ts:     time.Now().UTC().Format(time.RFC3339),
		Extra: map[string]any{
			"task_id": "task-real",
			"applied": []string{"main.go"},
			"blobs":   map[string]any{"main.go": mainBlob},
		},
	})
	_, g3 = checkG3("F0-Tasks: task-real")
	if g3.Status != "PASS" {
		t.Errorf("expected G3 PASS with valid deliver, got %+v", g3)
	}

	// 4. Altered after delivery -> FAIL
	// Simulate deliver recorded hash "different_blob"
	_ = ledger.Append(&Entry{
		ID:     "ev-deliver-diff",
		Kind:   "g8s-deliver",
		Status: "PASS",
		Ts:     time.Now().UTC().Format(time.RFC3339),
		Extra: map[string]any{
			"task_id": "task-diff",
			"applied": []string{"main.go"},
			"blobs":   map[string]any{"main.go": "different_blob_hash_000000"},
		},
	})
	_, g3 = checkG3("F0-Tasks: task-diff")
	if g3.Status != "FAIL" || !strings.Contains(g3.Detail, "altered-after-delivery: main.go") {
		t.Errorf("expected G3 FAIL for altered-after-delivery, got %+v", g3)
	}

	// 5. Dropped file -> FAIL
	// Deliver recorded applied second.go, but second.go is neither in diff nor at head
	_ = ledger.Append(&Entry{
		ID:     "ev-deliver-dropped",
		Kind:   "g8s-deliver",
		Status: "PASS",
		Ts:     time.Now().UTC().Format(time.RFC3339),
		Extra: map[string]any{
			"task_id": "task-drop",
			"applied": []string{"main.go", "second.go"},
			"blobs":   map[string]any{"main.go": mainBlob, "second.go": "fakeblob2"},
		},
	})
	_, g3 = checkG3("F0-Tasks: task-drop")
	if g3.Status != "FAIL" || !strings.Contains(g3.Detail, "dropped: second.go") {
		t.Errorf("expected G3 FAIL for dropped file, got %+v", g3)
	}

	// 6. Manual tag allows altered file and debits budget
	_ = ledger.Append(&Entry{
		ID:     "ev-manual1",
		Kind:   "manual",
		Status: "PASS",
		Extra: map[string]any{
			"file":   "main.go",
			"blob":   mainBlob,
			"reason": "manual patch applied by supervisor",
		},
	})
	_, g3 = checkG3("F0-Tasks: task-diff")
	if g3.Status != "PASS" || !strings.Contains(g3.Detail, "manual tags: main.go") {
		t.Errorf("expected G3 PASS with manual tag covering altered file, got %+v", g3)
	}
}
