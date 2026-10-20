package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGitCmd(tb testing.TB, dir string, args ...string) string {
	tb.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		tb.Fatalf("git %s failed: %v, out: %s", strings.Join(args, " "), err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func TestSignal1_TokenRewrite_C3(t *testing.T) {
	repoDir, _, _ := setupReplayRepo(t, "c3-repo")

	// C3: Worker delivers pkg/artifact.go with "To-Do" token rewrite
	artifactPath := filepath.Join(repoDir, "pkg", "artifact.go")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, []byte("package pkg\n// To-Do: implement artifact validation\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runGitCmd(t, repoDir, "add", "pkg/artifact.go")
	runGitCmd(t, repoDir, "commit", "-m", "worker delivery with To-Do rewrite")
	headSHA := runGitCmd(t, repoDir, "rev-parse", "HEAD")

	violations, err := DetectGateAppeasement(repoDir, "", headSHA)
	if err != nil {
		t.Fatalf("DetectGateAppeasement error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(violations), violations)
	}
	if violations[0].Kind != "token-rewrite" {
		t.Errorf("expected kind token-rewrite, got %s", violations[0].Kind)
	}
	if violations[0].File != "pkg/artifact.go" {
		t.Errorf("expected file pkg/artifact.go, got %s", violations[0].File)
	}
	if !strings.Contains(violations[0].Detail, "To-Do") {
		t.Errorf("expected detail to mention To-Do, got %s", violations[0].Detail)
	}

	res, err := CheckGateAppeasement(repoDir, "", headSHA, "", nil)
	if err != nil {
		t.Fatalf("CheckGateAppeasement error: %v", err)
	}
	if res.Status != "FAIL" {
		t.Fatalf("expected G4 FAIL, got %s (%s)", res.Status, res.Detail)
	}
	if !strings.Contains(res.Detail, "token-rewrite") || !strings.Contains(res.Detail, "hint: use sanctioned guard allowlistPaths") {
		t.Errorf("unexpected detail: %s", res.Detail)
	}
}

func TestSignal1_OtherTokenRewrites(t *testing.T) {
	tokens := []string{"to_do", "To-Be-Determined", "Place-Holder"}
	for _, tok := range tokens {
		t.Run(tok, func(t *testing.T) {
			repoDir, _, _ := setupReplayRepo(t, "token-repo")
			testFile := filepath.Join(repoDir, "feature.go")
			if err := os.WriteFile(testFile, []byte("package main\n// "+tok+": finish later\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGitCmd(t, repoDir, "add", "feature.go")
			runGitCmd(t, repoDir, "commit", "-m", "commit with token")
			headSHA := runGitCmd(t, repoDir, "rev-parse", "HEAD")

			violations, err := DetectGateAppeasement(repoDir, "", headSHA)
			if err != nil {
				t.Fatalf("DetectGateAppeasement error: %v", err)
			}
			if len(violations) != 1 || violations[0].Kind != "token-rewrite" {
				t.Fatalf("expected 1 token-rewrite violation for %s, got: %+v", tok, violations)
			}
		})
	}
}

func TestSignal2_UnreadConfigKey_C4(t *testing.T) {
	repoDir, _, _ := setupReplayRepo(t, "c4-repo")

	// C4: Invented unread YAML keys in .g8s/trust-boundaries.yml
	tbFile := filepath.Join(repoDir, ".g8s", "trust-boundaries.yml")
	if err := os.MkdirAll(filepath.Dir(tbFile), 0o755); err != nil {
		t.Fatal(err)
	}
	c4Content, err := os.ReadFile(filepath.Join("testdata", "replay", "c4_unread_keys.yml"))
	if err != nil {
		t.Fatalf("reading c4 fixture: %v", err)
	}
	if err := os.WriteFile(tbFile, c4Content, 0o644); err != nil {
		t.Fatal(err)
	}

	runGitCmd(t, repoDir, "add", ".g8s/trust-boundaries.yml")
	runGitCmd(t, repoDir, "commit", "-m", "add unread keys")
	headSHA := runGitCmd(t, repoDir, "rev-parse", "HEAD")

	violations, err := DetectGateAppeasement(repoDir, "", headSHA)
	if err != nil {
		t.Fatalf("DetectGateAppeasement error: %v", err)
	}
	if len(violations) != 3 {
		t.Fatalf("expected 3 unread-config-key violations for c4, got %d: %+v", len(violations), violations)
	}
	keysFound := make(map[string]bool)
	for _, v := range violations {
		if v.Kind != "unread-config-key" {
			t.Errorf("expected kind unread-config-key, got %s", v.Kind)
		}
		if v.File != ".g8s/trust-boundaries.yml" {
			t.Errorf("expected file .g8s/trust-boundaries.yml, got %s", v.File)
		}
		for _, k := range []string{"schema_version", "profile", "rules"} {
			if strings.Contains(v.Detail, `"`+k+`"`) {
				keysFound[k] = true
			}
		}
	}
	if len(keysFound) != 3 {
		t.Errorf("expected schema_version, profile, and rules in violations, got keys: %+v", keysFound)
	}

	res, err := CheckGateAppeasement(repoDir, "", headSHA, "", nil)
	if err != nil {
		t.Fatalf("CheckGateAppeasement error: %v", err)
	}
	if res.Status != "FAIL" {
		t.Fatalf("expected G4 FAIL, got %s (%s)", res.Status, res.Detail)
	}
}

func TestSignal2_ValidConfig_Passes(t *testing.T) {
	repoDir, _, _ := setupReplayRepo(t, "valid-config-repo")

	// Valid .g8s/trust-boundaries.yml
	tbFile := filepath.Join(repoDir, ".g8s", "trust-boundaries.yml")
	if err := os.MkdirAll(filepath.Dir(tbFile), 0o755); err != nil {
		t.Fatal(err)
	}
	validTB := "trust_boundaries:\n  - \"internal/receipt/*\"\n"
	if err := os.WriteFile(tbFile, []byte(validTB), 0o644); err != nil {
		t.Fatal(err)
	}

	// Valid .g8s/f0.yml
	f0File := filepath.Join(repoDir, ".g8s", "f0.yml")
	validF0 := "ci: none\nverify_commands:\n  - [\"go\", \"test\", \"./...\"]\nmanual_free:\n  - \"docs/*\"\n"
	if err := os.WriteFile(f0File, []byte(validF0), 0o644); err != nil {
		t.Fatal(err)
	}

	runGitCmd(t, repoDir, "add", ".g8s/trust-boundaries.yml", ".g8s/f0.yml")
	runGitCmd(t, repoDir, "commit", "-m", "add valid configs")
	headSHA := runGitCmd(t, repoDir, "rev-parse", "HEAD")

	violations, err := DetectGateAppeasement(repoDir, "", headSHA)
	if err != nil {
		t.Fatalf("DetectGateAppeasement error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected 0 violations for valid config, got: %+v", violations)
	}

	res, err := CheckGateAppeasement(repoDir, "", headSHA, "", nil)
	if err != nil {
		t.Fatalf("CheckGateAppeasement error: %v", err)
	}
	if res.Status != "PASS" {
		t.Fatalf("expected G4 PASS, got %s (%s)", res.Status, res.Detail)
	}
}

func TestSignal3_TestSkip(t *testing.T) {
	// Case 1: Modifying guard file AND adding t.Skip in test file -> violation!
	t.Run("GuardModifiedAndTestSkip", func(t *testing.T) {
		repoDir, _, _ := setupReplayRepo(t, "skip-guard-repo")

		guardPath := filepath.Join(repoDir, "internal", "verifier", "diff_guard.go")
		if err := os.MkdirAll(filepath.Dir(guardPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(guardPath, []byte("package verifier\nfunc Guard() bool { return true }\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		testPath := filepath.Join(repoDir, "internal", "verifier", "diff_guard_test.go")
		testContent := "package verifier\nimport \"testing\"\nfunc TestGuard(t *testing.T) {\n\tt.Skip(\"bypassing guard\")\n}\n"
		if err := os.WriteFile(testPath, []byte(testContent), 0o644); err != nil {
			t.Fatal(err)
		}

		runGitCmd(t, repoDir, "add", "internal/verifier/diff_guard.go", "internal/verifier/diff_guard_test.go")
		runGitCmd(t, repoDir, "commit", "-m", "update guard and skip test")
		headSHA := runGitCmd(t, repoDir, "rev-parse", "HEAD")

		violations, err := DetectGateAppeasement(repoDir, "", headSHA)
		if err != nil {
			t.Fatalf("DetectGateAppeasement error: %v", err)
		}
		foundSkip := false
		for _, v := range violations {
			if v.Kind == "test-skip" {
				foundSkip = true
				if v.File != "internal/verifier/diff_guard_test.go" {
					t.Errorf("expected test file, got %s", v.File)
				}
				if !strings.Contains(v.Detail, "internal/verifier/diff_guard.go") {
					t.Errorf("expected guard file in detail, got %s", v.Detail)
				}
			}
		}
		if !foundSkip {
			t.Fatalf("expected test-skip violation, got: %+v", violations)
		}
	})

	// Case 2: Adding t.Skip in test file without modifying guard file -> no violation
	t.Run("NonGuardModifiedAndTestSkip", func(t *testing.T) {
		repoDir, _, _ := setupReplayRepo(t, "skip-regular-repo")

		featurePath := filepath.Join(repoDir, "pkg", "feature.go")
		if err := os.MkdirAll(filepath.Dir(featurePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(featurePath, []byte("package pkg\nfunc Feature() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		testPath := filepath.Join(repoDir, "pkg", "feature_test.go")
		testContent := "package pkg\nimport \"testing\"\nfunc TestFeature(t *testing.T) {\n\tt.Skip(\"normal skip\")\n}\n"
		if err := os.WriteFile(testPath, []byte(testContent), 0o644); err != nil {
			t.Fatal(err)
		}

		runGitCmd(t, repoDir, "add", "pkg/feature.go", "pkg/feature_test.go")
		runGitCmd(t, repoDir, "commit", "-m", "update feature and skip test")
		headSHA := runGitCmd(t, repoDir, "rev-parse", "HEAD")

		violations, err := DetectGateAppeasement(repoDir, "", headSHA)
		if err != nil {
			t.Fatalf("DetectGateAppeasement error: %v", err)
		}
		for _, v := range violations {
			if v.Kind == "test-skip" {
				t.Fatalf("unexpected test-skip violation: %+v", v)
			}
		}
	})
}

func TestG4_OverrideHandling(t *testing.T) {
	repoDir, _, _ := setupReplayRepo(t, "override-repo")
	t.Setenv("HOME", t.TempDir())

	// Create commit with appeasement violation
	fPath := filepath.Join(repoDir, "pkg", "artifact.go")
	_ = os.MkdirAll(filepath.Dir(fPath), 0o755)
	_ = os.WriteFile(fPath, []byte("package pkg\n// To-Do: rewrite\n"), 0o644)
	runGitCmd(t, repoDir, "add", "pkg/artifact.go")
	runGitCmd(t, repoDir, "commit", "-m", "commit with appeasement")
	headSHA := runGitCmd(t, repoDir, "rev-parse", "HEAD")

	// 1. Without override: FAIL
	resNoOverride, err := CheckGateAppeasement(repoDir, "", headSHA, "", nil)
	if err != nil {
		t.Fatalf("CheckGateAppeasement error: %v", err)
	}
	if resNoOverride.Status != "FAIL" {
		t.Fatalf("expected FAIL without override, got %s", resNoOverride.Status)
	}

	// 2. With overrideReason passed directly: PASS and debits
	overrideReason := "sanctioned temporary refactor"
	resOverride, err := CheckGateAppeasement(repoDir, "", headSHA, overrideReason, nil)
	if err != nil {
		t.Fatalf("CheckGateAppeasement error: %v", err)
	}
	if resOverride.Status != "PASS" {
		t.Fatalf("expected PASS with override, got %s (%s)", resOverride.Status, resOverride.Detail)
	}
	if !strings.Contains(resOverride.Detail, "appeasement overridden (debited): "+overrideReason) {
		t.Fatalf("unexpected detail: %s", resOverride.Detail)
	}

	// 3. With override in ledgerEntries: PASS without calling RecordDebit again
	ledgerEntries := []Entry{
		{
			ID:     "ev-override-1",
			Kind:   "g4-override",
			SHA:    headSHA,
			Status: "PASS",
			Extra: map[string]any{
				"reason": "ledger recorded override",
			},
		},
	}
	resLedger, err := CheckGateAppeasement(repoDir, "", headSHA, "", ledgerEntries)
	if err != nil {
		t.Fatalf("CheckGateAppeasement error: %v", err)
	}
	if resLedger.Status != "PASS" {
		t.Fatalf("expected PASS from ledger override, got %s (%s)", resLedger.Status, resLedger.Detail)
	}
	if !strings.Contains(resLedger.Detail, "ledger recorded override") {
		t.Fatalf("unexpected detail: %s", resLedger.Detail)
	}
}

func TestG4_CleanRepo_Passes(t *testing.T) {
	repoDir, _, headSHA := setupReplayRepo(t, "clean-repo")

	res, err := CheckGateAppeasement(repoDir, "", headSHA, "", nil)
	if err != nil {
		t.Fatalf("CheckGateAppeasement error: %v", err)
	}
	if res.Status != "PASS" {
		t.Fatalf("expected PASS for clean repo, got %s (%s)", res.Status, res.Detail)
	}
	if res.Detail != "no gate-appeasement signals detected" {
		t.Fatalf("unexpected detail: %s", res.Detail)
	}
}
