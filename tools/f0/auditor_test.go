package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type mockAuditor struct {
	name    string
	verdict AuditVerdict
	err     error
}

func (m *mockAuditor) Name() string {
	return m.name
}

func (m *mockAuditor) Audit(digest *SessionDigest, repoEntries []Entry) (AuditVerdict, error) {
	return m.verdict, m.err
}

func TestBuildSessionDigest_LedgerOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupReplayRepo(t, "digest-repo")

	m := &Mandate{
		Version:     1,
		ID:          "md-digest",
		Issuer:      "test-pic",
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "digest-test",
		Repos:       []MandateRepo{{Name: "digest-repo", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 3},
	}
	if err := IssueMandate(m); err != nil {
		t.Fatalf("IssueMandate: %v", err)
	}

	rLedger, err := OpenRepoLedger(repoDir)
	if err != nil {
		t.Fatalf("OpenRepoLedger: %v", err)
	}
	gLedger, err := OpenGlobalLedger()
	if err != nil {
		t.Fatalf("OpenGlobalLedger: %v", err)
	}

	// 1. Evidence run entry
	_ = rLedger.Append(&Entry{
		ID:           "ev-testrun",
		Ts:           time.Now().UTC().Format(time.RFC3339),
		Kind:         "go-test",
		Status:       "PASS",
		ItemsChecked: 15,
		SHA:          headSHA,
		Cmd:          []string{"go", "test", "./..."},
	})

	// 2. Waiver entry
	_ = rLedger.Append(&Entry{
		ID:           "ev-waiver1",
		Ts:           time.Now().UTC().Format(time.RFC3339),
		Kind:         "waiver",
		Status:       "PASS",
		ItemsChecked: 1,
		SHA:          headSHA,
		Extra: map[string]any{
			"check":  "ci/lint",
			"reason": "upstream outage",
		},
	})

	// 3. Manual tag entry
	_ = rLedger.Append(&Entry{
		ID:           "ev-manual1",
		Ts:           time.Now().UTC().Format(time.RFC3339),
		Kind:         "manual",
		Status:       "PASS",
		ItemsChecked: 1,
		SHA:          headSHA,
		Extra: map[string]any{
			"file":   "README.md",
			"reason": "manual release note sync",
		},
	})

	// 4. Override entry
	_ = gLedger.Append(&Entry{
		ID:           "ev-override1",
		Ts:           time.Now().UTC().Format(time.RFC3339),
		Kind:         "override",
		Status:       "PASS",
		ItemsChecked: 1,
		Extra: map[string]any{
			"reason": "emergency bypass approved",
		},
	})

	// 5. Revert entry
	_ = rLedger.Append(&Entry{
		ID:           "ev-revert1",
		Ts:           time.Now().UTC().Format(time.RFC3339),
		Kind:         "revert",
		Status:       "PASS",
		ItemsChecked: 1,
		SHA:          headSHA,
		Extra: map[string]any{
			"reason": "revert flaky patch",
		},
	})

	// Build digest strictly from ledgers
	digest, err := BuildSessionDigest(repoDir, nil)
	if err != nil {
		t.Fatalf("BuildSessionDigest: %v", err)
	}

	if digest.MandateID != "md-digest" {
		t.Errorf("expected MandateID md-digest, got %s", digest.MandateID)
	}
	if digest.CurrentTier != "T2" {
		t.Errorf("expected CurrentTier T2, got %s", digest.CurrentTier)
	}
	if digest.BudgetSpent != 0 {
		t.Errorf("expected BudgetSpent 0, got %d", digest.BudgetSpent)
	}
	if digest.BudgetLimit != 3 {
		t.Errorf("expected BudgetLimit 3, got %d", digest.BudgetLimit)
	}
	if digest.AuditStamp != "UNAUDITED" {
		t.Errorf("expected AuditStamp UNAUDITED, got %s", digest.AuditStamp)
	}

	// Verify evidence IDs
	foundRun := false
	for _, id := range digest.EvidenceIDs {
		if id == "ev-testrun" {
			foundRun = true
			break
		}
	}
	if !foundRun {
		t.Errorf("expected ev-testrun in EvidenceIDs: %+v", digest.EvidenceIDs)
	}

	// Verify waivers
	if len(digest.Waivers) != 1 || digest.Waivers[0] != "ev-waiver1" {
		t.Errorf("expected [ev-waiver1] in Waivers, got %+v", digest.Waivers)
	}

	// Verify manual tags
	if len(digest.ManualTags) != 1 || digest.ManualTags[0] != "ev-manual1" {
		t.Errorf("expected [ev-manual1] in ManualTags, got %+v", digest.ManualTags)
	}

	// Verify overrides
	if len(digest.Overrides) != 1 || digest.Overrides[0] != "ev-override1" {
		t.Errorf("expected [ev-override1] in Overrides, got %+v", digest.Overrides)
	}

	// Verify reverts
	if len(digest.Reverts) != 1 || digest.Reverts[0] != "ev-revert1" {
		t.Errorf("expected [ev-revert1] in Reverts, got %+v", digest.Reverts)
	}

	// Verify actions collected
	if len(digest.Actions) == 0 {
		t.Errorf("expected non-empty Actions in digest")
	}
}

func TestBuildSessionDigest_ZeroAuditors_Unaudited(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, _ := setupReplayRepo(t, "zero-auditors")

	m := &Mandate{
		Version:     1,
		ID:          "md-zero",
		Issuer:      "test-pic",
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "zero-auditors",
		Repos:       []MandateRepo{{Name: "zero-auditors", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 3},
	}
	_ = IssueMandate(m)

	// nil slice
	digest1, err := BuildSessionDigest(repoDir, nil)
	if err != nil {
		t.Fatalf("BuildSessionDigest nil: %v", err)
	}
	if digest1.AuditStamp != "UNAUDITED" {
		t.Errorf("expected UNAUDITED, got %s", digest1.AuditStamp)
	}
	if digest1.AuditVerdict == nil || digest1.AuditVerdict.Status != "UNAUDITED" {
		t.Errorf("expected verdict UNAUDITED, got %+v", digest1.AuditVerdict)
	}

	// empty slice
	digest2, err := BuildSessionDigest(repoDir, []AuditorProvider{})
	if err != nil {
		t.Fatalf("BuildSessionDigest empty: %v", err)
	}
	if digest2.AuditStamp != "UNAUDITED" {
		t.Errorf("expected UNAUDITED, got %s", digest2.AuditStamp)
	}
}

func TestBuildSessionDigest_OneAuditor_Pass(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, _ := setupReplayRepo(t, "pass-auditor")

	m := &Mandate{
		Version:     1,
		ID:          "md-pass",
		Issuer:      "test-pic",
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "pass-auditor",
		Repos:       []MandateRepo{{Name: "pass-auditor", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 3},
	}
	_ = IssueMandate(m)

	auditor := &mockAuditor{
		name: "test-verifier",
		verdict: AuditVerdict{
			Provider: "test-verifier",
			Status:   "PASS",
			Claims: []AuditClaimResult{
				{Claim: "claim1", Status: "PASS"},
			},
		},
	}

	digest, err := BuildSessionDigest(repoDir, []AuditorProvider{auditor})
	if err != nil {
		t.Fatalf("BuildSessionDigest: %v", err)
	}
	if digest.AuditStamp != "PASS" {
		t.Errorf("expected AuditStamp PASS, got %s", digest.AuditStamp)
	}
	if digest.AuditVerdict == nil || digest.AuditVerdict.Status != "PASS" {
		t.Errorf("expected AuditVerdict PASS, got %+v", digest.AuditVerdict)
	}
	if digest.BudgetSpent != 0 {
		t.Errorf("expected 0 spent on pass, got %d", digest.BudgetSpent)
	}
}

func TestBuildSessionDigest_AuditorFail_DebitsBudget(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, _ := setupReplayRepo(t, "fail-auditor")

	m := &Mandate{
		Version:     1,
		ID:          "md-fail",
		Issuer:      "test-pic",
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "fail-auditor",
		Repos:       []MandateRepo{{Name: "fail-auditor", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 2},
	}
	_ = IssueMandate(m)

	failAuditor := &mockAuditor{
		name: "strict-auditor",
		verdict: AuditVerdict{
			Provider: "strict-auditor",
			Status:   "FAIL",
			Claims: []AuditClaimResult{
				{Claim: "doc_accuracy", Status: "FAIL", Reason: "table count mismatch"},
			},
		},
	}

	// Run 1: Auditor fails -> automatically debits error budget
	digest, err := BuildSessionDigest(repoDir, []AuditorProvider{failAuditor})
	if err != nil {
		t.Fatalf("BuildSessionDigest: %v", err)
	}

	if digest.AuditStamp != "FAIL" {
		t.Errorf("expected AuditStamp FAIL, got %s", digest.AuditStamp)
	}
	if digest.BudgetSpent != 1 {
		t.Errorf("expected BudgetSpent 1 after auditor failure, got %d", digest.BudgetSpent)
	}
	if digest.CurrentTier != "T2" {
		t.Errorf("expected tier T2 with 1/2 spent, got %s", digest.CurrentTier)
	}

	// Verify debit recorded in global ledger
	gLedger, _ := OpenGlobalLedger()
	entries, _ := gLedger.ReadEntries()
	foundDebit := false
	for _, e := range entries {
		rStr, _ := e.Extra["reason"].(string)
		if e.Kind == "debit" && strings.Contains(rStr, "auditor FAIL: strict-auditor") {
			foundDebit = true
			break
		}
	}
	if !foundDebit {
		t.Errorf("expected debit entry recorded for auditor failure in global ledger")
	}

	// Run 2: Second auditor failure pushes budget to 2/2 -> trips circuit breaker to T0!
	time.Sleep(5 * time.Millisecond)
	digest2, err := BuildSessionDigest(repoDir, []AuditorProvider{failAuditor})
	if err != nil {
		t.Fatalf("BuildSessionDigest 2: %v", err)
	}
	if digest2.BudgetSpent != 2 {
		t.Errorf("expected BudgetSpent 2, got %d", digest2.BudgetSpent)
	}
	if digest2.CurrentTier != "T0" {
		t.Errorf("expected circuit breaker tripped to T0, got %s", digest2.CurrentTier)
	}
}

func TestBuildSessionDigest_MultipleAuditors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, _ := setupReplayRepo(t, "multi-auditors")

	m := &Mandate{
		Version:     1,
		ID:          "md-multi",
		Issuer:      "test-pic",
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "multi-auditors",
		Repos:       []MandateRepo{{Name: "multi-auditors", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 5},
	}
	_ = IssueMandate(m)

	auditor1 := &mockAuditor{
		name: "auditor-1",
		verdict: AuditVerdict{
			Provider: "auditor-1",
			Status:   "PASS",
		},
	}
	auditor2 := &mockAuditor{
		name: "auditor-2",
		verdict: AuditVerdict{
			Provider: "auditor-2",
			Status:   "FAIL",
			Claims: []AuditClaimResult{
				{Claim: "claim_b", Status: "FAIL", Reason: "discrepancy"},
			},
		},
	}

	digest, err := BuildSessionDigest(repoDir, []AuditorProvider{auditor1, auditor2})
	if err != nil {
		t.Fatalf("BuildSessionDigest: %v", err)
	}

	if digest.AuditStamp != "FAIL" {
		t.Errorf("expected overall FAIL when one auditor fails, got %s", digest.AuditStamp)
	}
	if digest.BudgetSpent != 1 {
		t.Errorf("expected 1 debit recorded, got %d", digest.BudgetSpent)
	}
}

func TestScorecardAuditor_C8_ScorecardMismatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupReplayRepo(t, "c8-scorecard-repo")

	m := &Mandate{
		Version:     1,
		ID:          "md-c8-auditor",
		Issuer:      "test-pic",
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "c8-replay",
		Repos:       []MandateRepo{{Name: "c8-scorecard-repo", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 3},
	}
	_ = IssueMandate(m)

	// Copy the real testdata/replay/c8_scorecard.md into repo
	scPath := filepath.Join(repoDir, "offer", "SCORECARD.md")
	_ = os.MkdirAll(filepath.Dir(scPath), 0o755)
	c8Data, err := os.ReadFile(filepath.Join("testdata", "replay", "c8_scorecard.md"))
	if err != nil {
		t.Fatalf("reading c8 fixture: %v", err)
	}
	if err := os.WriteFile(scPath, c8Data, 0o644); err != nil {
		t.Fatalf("writing scorecard fixture: %v", err)
	}

	// Sub-case (a): Delivery count mismatch (C8 defect: table claims M1=12, but actual deliveries were 10)
	expectedDeliveriesMismatch := map[string]int{
		"M1": 10, // Actual count is 10, but scorecard claims 12
		"M2": 8,
		"M3": 5,
	}

	auditor := NewScorecardAuditor(repoDir, "offer/SCORECARD.md", expectedDeliveriesMismatch, headSHA)
	verdict, err := auditor.Audit(nil, nil)
	if err != nil {
		t.Fatalf("auditor.Audit: %v", err)
	}
	if verdict.Status != "FAIL" {
		t.Fatalf("expected FAIL on count mismatch, got %s", verdict.Status)
	}

	foundM1Fail := false
	for _, c := range verdict.Claims {
		if c.Claim == "delivery_count_M1" && c.Status == "FAIL" {
			foundM1Fail = true
			if !strings.Contains(c.Reason, "12") || !strings.Contains(c.Reason, "10") {
				t.Errorf("expected reason to cite 12 and 10, got %s", c.Reason)
			}
			break
		}
	}
	if !foundM1Fail {
		t.Errorf("expected delivery_count_M1 to fail: %+v", verdict.Claims)
	}

	// Integration with BuildSessionDigest: C8 scorecard mismatch debits error budget automatically
	digest, err := BuildSessionDigest(repoDir, []AuditorProvider{auditor})
	if err != nil {
		t.Fatalf("BuildSessionDigest: %v", err)
	}
	if digest.AuditStamp != "FAIL" {
		t.Errorf("expected digest AuditStamp FAIL, got %s", digest.AuditStamp)
	}
	if digest.BudgetSpent != 1 {
		t.Errorf("expected budget debited to 1, got %d", digest.BudgetSpent)
	}

	// Sub-case (b): SHA mismatch (table counts match, but wrong SHA cited)
	expectedDeliveriesAccurate := map[string]int{
		"M1": 12,
		"M2": 8,
		"M3": 5,
	}
	auditorBadSHA := NewScorecardAuditor(repoDir, "offer/SCORECARD.md", expectedDeliveriesAccurate, "deadbeef1234wrongsha")
	verdictBadSHA, _ := auditorBadSHA.Audit(nil, nil)
	if verdictBadSHA.Status != "FAIL" {
		t.Fatalf("expected FAIL on wrong SHA, got %s", verdictBadSHA.Status)
	}

	foundSHAFail := false
	for _, c := range verdictBadSHA.Claims {
		if c.Claim == "scorecard_sha" && c.Status == "FAIL" {
			foundSHAFail = true
			break
		}
	}
	if !foundSHAFail {
		t.Errorf("expected scorecard_sha claim to fail: %+v", verdictBadSHA.Claims)
	}

	// Sub-case (c): Both counts and SHA match -> PASS
	auditorPass := NewScorecardAuditor(repoDir, "offer/SCORECARD.md", expectedDeliveriesAccurate, headSHA)
	verdictPass, err := auditorPass.Audit(nil, nil)
	if err != nil {
		t.Fatalf("auditorPass.Audit: %v", err)
	}
	if verdictPass.Status != "PASS" {
		t.Errorf("expected PASS when counts and SHA match, got %s (%+v)", verdictPass.Status, verdictPass.Claims)
	}
}

func TestDigestCommandCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, _ := setupReplayRepo(t, "digest-cli-repo")

	m := &Mandate{
		Version:     1,
		ID:          "md-digest-cli",
		Issuer:      "test-pic",
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "digest-cli",
		Repos:       []MandateRepo{{Name: "digest-cli-repo", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 2},
	}
	_ = IssueMandate(m)

	// Plain text CLI output
	code := digestCommand([]string{"--repo", repoDir})
	if code != 0 {
		t.Fatalf("digestCommand plain text exit %d", code)
	}

	// JSON CLI output
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code = digestCommand([]string{"--repo", repoDir, "--json"})
	w.Close()
	os.Stdout = oldStdout

	if code != 0 {
		t.Fatalf("digestCommand json exit %d", code)
	}
	var outBuf bytes.Buffer
	_, _ = io.Copy(&outBuf, r)
	var sd SessionDigest
	if err := json.Unmarshal(outBuf.Bytes(), &sd); err != nil {
		t.Fatalf("unmarshaling digest json: %v, raw: %s", err, outBuf.String())
	}
	if sd.MandateID != "md-digest-cli" || sd.CurrentTier != "T2" || sd.AuditStamp != "UNAUDITED" {
		t.Errorf("unexpected json digest: %+v", sd)
	}
}
