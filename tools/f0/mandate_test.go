package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActionTierClassification(t *testing.T) {
	// Standard T1 actions
	t1Actions := []string{"push-branch", "open-pr", "comment", "close-issue", "close"}
	for _, act := range t1Actions {
		tier := ClassifyActionTier("/tmp/repo", "repo", act, nil, nil)
		if tier != "T1" {
			t.Errorf("action %q: expected T1, got %q", act, tier)
		}
	}

	// Standard T2 merge without sensitive paths
	tier := ClassifyActionTier("/tmp/repo", "repo", "merge", []string{"foo.go", "bar/baz.txt"}, nil)
	if tier != "T2" {
		t.Errorf("merge with normal files: expected T2, got %q", tier)
	}

	// Standard T4 actions
	t4Actions := []string{"publish", "release", "tag", "force-push", "delete-ref", "credential", "config", "edit-hooks"}
	for _, act := range t4Actions {
		tier := ClassifyActionTier("/tmp/repo", "repo", act, nil, nil)
		if tier != "T4" {
			t.Errorf("action %q: expected T4, got %q", act, tier)
		}
	}

	// Unknown actions default to T4 (deny by default)
	unknown := ClassifyActionTier("/tmp/repo", "repo", "arbitrary-unknown-action", nil, nil)
	if unknown != "T4" {
		t.Errorf("unknown action: expected T4, got %q", unknown)
	}
}

func TestT3Escalation(t *testing.T) {
	t.Run("escalation via excluded_paths", func(t *testing.T) {
		excluded := []string{"internal/receipt/**", "secrets/*"}

		// Merge touching excluded path -> T3
		tier := ClassifyActionTier("/tmp/repo", "repo", "merge", []string{"internal/receipt/proof.go"}, excluded)
		if tier != "T3" {
			t.Errorf("expected T3 via internal/receipt/**, got %q", tier)
		}

		tier = ClassifyActionTier("/tmp/repo", "repo", "merge", []string{"secrets/token.txt"}, excluded)
		if tier != "T3" {
			t.Errorf("expected T3 via secrets/*, got %q", tier)
		}

		// Merge touching non-excluded path -> stays T2
		tier = ClassifyActionTier("/tmp/repo", "repo", "merge", []string{"pkg/util.go"}, excluded)
		if tier != "T2" {
			t.Errorf("expected T2 for non-excluded path, got %q", tier)
		}
	})

	t.Run("escalation via repo trust-boundaries.yml", func(t *testing.T) {
		tmpDir := t.TempDir()
		g8sDir := filepath.Join(tmpDir, ".g8s")
		if err := os.MkdirAll(g8sDir, 0o755); err != nil {
			t.Fatalf("mkdir .g8s: %v", err)
		}
		tbFile := filepath.Join(g8sDir, "trust-boundaries.yml")
		tbContent := `trust_boundaries:
  - "custom/sensitive/*"
`
		if err := os.WriteFile(tbFile, []byte(tbContent), 0o644); err != nil {
			t.Fatalf("write trust-boundaries.yml: %v", err)
		}

		// Path matching trust-boundaries.yml -> T3
		tier := ClassifyActionTier(tmpDir, "custom-repo", "merge", []string{"custom/sensitive/config.json"}, nil)
		if tier != "T3" {
			t.Errorf("expected T3 for trust-boundaries match, got %q", tier)
		}

		// Path NOT matching trust-boundaries.yml -> T2
		tier = ClassifyActionTier(tmpDir, "custom-repo", "merge", []string{"other/normal.go"}, nil)
		if tier != "T2" {
			t.Errorf("expected T2 for non-matching path, got %q", tier)
		}
	})

	t.Run("missing trust-boundaries.yml in sibling repo vs g8s", func(t *testing.T) {
		tmpDir := t.TempDir()
		siblingRepoDir := filepath.Join(tmpDir, "sibling-tool")
		if err := os.MkdirAll(siblingRepoDir, 0o755); err != nil {
			t.Fatalf("mkdir sibling-tool: %v", err)
		}

		// Sibling repo without .g8s/trust-boundaries.yml: NO escalation, stays T2
		tier := ClassifyActionTier(siblingRepoDir, "sibling-tool", "merge", []string{"internal/receipt/foo.go"}, nil)
		if tier != "T2" {
			t.Errorf("expected T2 for sibling repo without trust-boundaries.yml, got %q", tier)
		}

		// g8s repo without .g8s/trust-boundaries.yml: uses DefaultTrustBoundaries -> T3
		g8sRepoDir := filepath.Join(tmpDir, "g8s")
		if err := os.MkdirAll(g8sRepoDir, 0o755); err != nil {
			t.Fatalf("mkdir g8s: %v", err)
		}

		tierG8s := ClassifyActionTier(g8sRepoDir, "g8s", "merge", []string{"internal/receipt/foo.go"}, nil)
		if tierG8s != "T3" {
			t.Errorf("expected T3 for g8s repo via DefaultTrustBoundaries, got %q", tierG8s)
		}

		// Non-sensitive file in g8s repo -> stays T2
		tierG8sNormal := ClassifyActionTier(g8sRepoDir, "g8s", "merge", []string{"pkg/other.go"}, nil)
		if tierG8sNormal != "T2" {
			t.Errorf("expected T2 for normal file in g8s repo, got %q", tierG8sNormal)
		}
	})
}

func TestMandateIssuance(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repoPath := t.TempDir()

	// 1. Valid issuance
	args := []string{
		"issue",
		"--issuer", "alice",
		"--campaign", "261020-f0-test",
		"--repo", "test-repo=" + repoPath + ":T2",
		"--exclude", "internal/receipt/**",
		"--budget", "5",
		"--ttl", "12h",
	}
	code := mandateCommand(args)
	if code != 0 {
		t.Fatalf("expected mandate issue to succeed, got exit %d", code)
	}

	// Verify global ledger entry
	ledger, err := OpenGlobalLedger()
	if err != nil {
		t.Fatalf("open global ledger: %v", err)
	}
	entries, err := ledger.ReadEntries()
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 ledger entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Kind != "mandate-issue" || e.Status != "PASS" || e.ItemsChecked != 1 {
		t.Errorf("unexpected entry metadata: kind=%s status=%s items=%d", e.Kind, e.Status, e.ItemsChecked)
	}

	// Verify LoadActiveMandate
	m, err := LoadActiveMandate()
	if err != nil {
		t.Fatalf("load active mandate: %v", err)
	}
	if m == nil {
		t.Fatalf("expected active mandate, got nil")
	}
	if m.Issuer != "alice" || m.Campaign != "261020-f0-test" {
		t.Errorf("unexpected mandate: %+v", m)
	}
	if len(m.Repos) != 1 || m.Repos[0].Name != "test-repo" || m.Repos[0].MaxTier != "T2" {
		t.Errorf("unexpected repos in mandate: %+v", m.Repos)
	}
	if m.ErrorBudget.Limit != 5 {
		t.Errorf("expected budget limit 5, got %d", m.ErrorBudget.Limit)
	}

	// 2. Issuance refuses T4
	codeT4 := mandateCommand([]string{
		"issue",
		"--issuer", "bob",
		"--campaign", "t4-attempt",
		"--repo", "test-repo=" + repoPath + ":T4",
	})
	if codeT4 == 0 {
		t.Fatalf("expected mandate issue with T4 to fail, got exit 0")
	}

	// 3. Issuance refuses invalid tier
	codeInvalid := mandateCommand([]string{
		"issue",
		"--issuer", "bob",
		"--campaign", "invalid-tier-attempt",
		"--repo", "test-repo=" + repoPath + ":T99",
	})
	if codeInvalid == 0 {
		t.Fatalf("expected mandate issue with T99 to fail, got exit 0")
	}
}

func TestMandateCheckPermissions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repoPath := t.TempDir()

	code := mandateCommand([]string{
		"issue",
		"--issuer", "alice",
		"--campaign", "delegation-test",
		"--repo", "myrepo=" + repoPath + ":T2",
		"--exclude", "critical/**",
		"--budget", "3",
	})
	if code != 0 {
		t.Fatalf("issue failed: exit %d", code)
	}

	// Check T1 action -> Allowed, exit 0
	res, err := CheckMandate(repoPath, "push-branch", nil)
	if err != nil {
		t.Fatalf("check push-branch: %v", err)
	}
	if !res.Allowed || res.RequiredTier != "T1" || res.MaxTier != "T2" {
		t.Errorf("unexpected check result: %+v", res)
	}

	code = mandateCommand([]string{"check", "--repo", repoPath, "--action", "push-branch"})
	if code != 0 {
		t.Errorf("expected exit 0 for allowed push-branch, got %d", code)
	}

	// Check T2 merge -> Allowed, exit 0
	res, err = CheckMandate(repoPath, "merge", []string{"src/app.go"})
	if err != nil {
		t.Fatalf("check merge: %v", err)
	}
	if !res.Allowed || res.RequiredTier != "T2" || res.MaxTier != "T2" {
		t.Errorf("unexpected check result: %+v", res)
	}

	code = mandateCommand([]string{"check", "--repo", repoPath, "--action", "merge", "--paths", "src/app.go"})
	if code != 0 {
		t.Errorf("expected exit 0 for allowed merge, got %d", code)
	}

	// Check T3 merge (touching critical/**) -> NOT allowed, exit 3
	res, err = CheckMandate(repoPath, "merge", []string{"critical/key.go"})
	if err != nil {
		t.Fatalf("check merge critical: %v", err)
	}
	if res.Allowed || res.RequiredTier != "T3" || res.MaxTier != "T2" {
		t.Errorf("expected allowed=false for T3 vs T2, got: %+v", res)
	}

	code = mandateCommand([]string{"check", "--repo", repoPath, "--action", "merge", "--paths", "critical/key.go"})
	if code != 3 {
		t.Errorf("expected exit 3 for disallowed T3 merge, got %d", code)
	}

	// Check T4 action (tag / release) -> NOT allowed, exit 3
	res, err = CheckMandate(repoPath, "tag", nil)
	if err != nil {
		t.Fatalf("check tag: %v", err)
	}
	if res.Allowed || res.RequiredTier != "T4" {
		t.Errorf("expected allowed=false for T4 tag, got: %+v", res)
	}

	code = mandateCommand([]string{"check", "--repo", repoPath, "--action", "tag"})
	if code != 3 {
		t.Errorf("expected exit 3 for T4 tag, got %d", code)
	}
}

func TestMandateEffectiveMaxTierT0(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repoPath := t.TempDir()

	// 1. Missing mandate -> T0, exit 3
	res, err := CheckMandate(repoPath, "push-branch", nil)
	if err != nil {
		t.Fatalf("check missing mandate: %v", err)
	}
	if res.Allowed || res.MaxTier != "T0" || res.Reason != "no active mandate" {
		t.Errorf("expected T0 for missing mandate, got %+v", res)
	}
	code := mandateCommand([]string{"check", "--repo", repoPath, "--action", "push-branch"})
	if code != 3 {
		t.Errorf("expected exit 3 for missing mandate, got %d", code)
	}

	// 2. Unlisted repo -> T0, exit 3
	code = mandateCommand([]string{
		"issue",
		"--issuer", "alice",
		"--campaign", "unlisted-test",
		"--repo", "listed-repo=" + repoPath + ":T2",
	})
	if code != 0 {
		t.Fatalf("issue failed: %d", code)
	}

	otherRepo := filepath.Join(t.TempDir(), "other-repo")
	res, err = CheckMandate(otherRepo, "push-branch", nil)
	if err != nil {
		t.Fatalf("check unlisted repo: %v", err)
	}
	if res.Allowed || res.MaxTier != "T0" || res.Reason != "repo not in mandate" {
		t.Errorf("expected T0 for unlisted repo, got %+v", res)
	}
	code = mandateCommand([]string{"check", "--repo", otherRepo, "--action", "push-branch"})
	if code != 3 {
		t.Errorf("expected exit 3 for unlisted repo, got %d", code)
	}

	// 3. Expired mandate -> T0, exit 3
	now := time.Now().UTC()
	expiredMandate := &Mandate{
		Version:   1,
		ID:        newMandateID(),
		Issuer:    "alice",
		IssuedAt:  now.Add(-2 * time.Hour).Format(time.RFC3339),
		ExpiresAt: now.Add(-1 * time.Hour).Format(time.RFC3339), // Expired 1h ago
		Campaign:  "expired-campaign",
		Repos: []MandateRepo{
			{Name: "listed-repo", Path: repoPath, MaxTier: "T2"},
		},
		ErrorBudget: ErrorBudget{Limit: 3},
	}
	if err := IssueMandate(expiredMandate); err != nil {
		t.Fatalf("issuing expired mandate: %v", err)
	}

	res, err = CheckMandate(repoPath, "push-branch", nil)
	if err != nil {
		t.Fatalf("check expired mandate: %v", err)
	}
	if res.Allowed || res.MaxTier != "T0" || res.Reason != "mandate expired" {
		t.Errorf("expected T0 for expired mandate, got %+v", res)
	}
	code = mandateCommand([]string{"check", "--repo", repoPath, "--action", "push-branch"})
	if code != 3 {
		t.Errorf("expected exit 3 for expired mandate, got %d", code)
	}
}

func TestMandateCacheTamper(t *testing.T) {
	t.Run("cache exists without ledger entry", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())

		cachePath, err := MandateCachePath()
		if err != nil {
			t.Fatalf("cache path: %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		fakeMandate := `{"version":1,"id":"md-fake1234","issuer":"attacker","campaign":"fake"}`
		if err := os.WriteFile(cachePath, []byte(fakeMandate), 0o644); err != nil {
			t.Fatalf("write fake cache: %v", err)
		}

		_, err = LoadActiveMandate()
		if err == nil || !strings.Contains(err.Error(), "mandate cache tampered: cache exists without ledger entry") {
			t.Fatalf("expected tamper error, got: %v", err)
		}
	})

	t.Run("cache file modified on disk vs global ledger", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())

		repoPath := t.TempDir()
		code := mandateCommand([]string{
			"issue",
			"--issuer", "alice",
			"--campaign", "tamper-test",
			"--repo", "repo=" + repoPath + ":T2",
		})
		if code != 0 {
			t.Fatalf("issue failed: %d", code)
		}

		// Mandate is valid now
		m, err := LoadActiveMandate()
		if err != nil || m == nil {
			t.Fatalf("expected valid mandate, got err: %v", err)
		}

		// Tamper with cache file: escalate tier to T3
		cachePath, _ := MandateCachePath()
		data, err := os.ReadFile(cachePath)
		if err != nil {
			t.Fatalf("read cache: %v", err)
		}
		tampered := strings.Replace(string(data), `"max_tier": "T2"`, `"max_tier": "T3"`, 1)
		if err := os.WriteFile(cachePath, []byte(tampered), 0o644); err != nil {
			t.Fatalf("write tampered cache: %v", err)
		}

		// Now LoadActiveMandate must fail with "mandate cache tampered"
		_, err = LoadActiveMandate()
		if err == nil || !strings.Contains(err.Error(), "mandate cache tampered") {
			t.Fatalf("expected tamper error, got: %v", err)
		}

		// CheckMandate must also fail
		res, checkErr := CheckMandate(repoPath, "push-branch", nil)
		if checkErr == nil || !strings.Contains(checkErr.Error(), "mandate cache tampered") {
			t.Fatalf("expected check tamper error, got res=%+v err=%v", res, checkErr)
		}
	})
}

func TestMandateBudgetExhaustion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repoPath := t.TempDir()
	code := mandateCommand([]string{
		"issue",
		"--issuer", "alice",
		"--campaign", "budget-test",
		"--repo", "repo=" + repoPath + ":T2",
		"--budget", "2",
	})
	if code != 0 {
		t.Fatalf("issue failed: %d", code)
	}

	m, err := LoadActiveMandate()
	if err != nil || m == nil {
		t.Fatalf("load mandate: %v", err)
	}

	// 0 debits
	spent, err := MandateBudgetSpent(m)
	if err != nil || spent != 0 {
		t.Fatalf("expected 0 spent, got %d, err: %v", spent, err)
	}

	res, err := CheckMandate(repoPath, "push-branch", nil)
	if err != nil || !res.Allowed {
		t.Fatalf("expected push-branch allowed with 0 debits, got %+v, err: %v", res, err)
	}

	// Record 1 debit
	if err := RecordDebit(repoPath, "debit 1", m.ID); err != nil {
		t.Fatalf("record debit 1: %v", err)
	}
	spent, err = MandateBudgetSpent(m)
	if err != nil || spent != 1 {
		t.Fatalf("expected 1 spent, got %d, err: %v", spent, err)
	}
	res, err = CheckMandate(repoPath, "push-branch", nil)
	if err != nil || !res.Allowed {
		t.Fatalf("expected push-branch allowed with 1 debit, got %+v, err: %v", res, err)
	}

	// Record 2nd debit (limit is 2)
	if err := RecordDebit(repoPath, "debit 2", m.ID); err != nil {
		t.Fatalf("record debit 2: %v", err)
	}
	spent, err = MandateBudgetSpent(m)
	if err != nil || spent != 2 {
		t.Fatalf("expected 2 spent, got %d, err: %v", spent, err)
	}

	// Effective max tier drops to T0
	res, err = CheckMandate(repoPath, "push-branch", nil)
	if err != nil {
		t.Fatalf("check after exhaustion: %v", err)
	}
	if res.Allowed || res.MaxTier != "T0" || res.Reason != "error budget exhausted" {
		t.Errorf("expected budget exhausted -> T0, got %+v", res)
	}

	code = mandateCommand([]string{"check", "--repo", repoPath, "--action", "push-branch"})
	if code != 3 {
		t.Errorf("expected exit 3 for budget exhaustion, got %d", code)
	}
}

func TestMandateShowCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// No mandate yet
	code := mandateCommand([]string{"show"})
	if code != 0 {
		t.Errorf("expected show with no mandate to exit 0, got %d", code)
	}
	code = mandateCommand([]string{"show", "--json"})
	if code != 0 {
		t.Errorf("expected show --json with no mandate to exit 0, got %d", code)
	}

	repoPath := t.TempDir()
	code = mandateCommand([]string{
		"issue",
		"--issuer", "alice",
		"--campaign", "show-test",
		"--repo", "repo=" + repoPath + ":T2",
		"--exclude", "critical/**",
		"--budget", "3",
	})
	if code != 0 {
		t.Fatalf("issue failed: %d", code)
	}

	code = mandateCommand([]string{"show"})
	if code != 0 {
		t.Errorf("expected show to exit 0, got %d", code)
	}

	code = mandateCommand([]string{"show", "--json"})
	if code != 0 {
		t.Errorf("expected show --json to exit 0, got %d", code)
	}
}
