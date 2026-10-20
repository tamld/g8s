package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBreakerNominal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, _ := setupReplayRepo(t, "nominal-repo")

	m := &Mandate{
		Version:     1,
		ID:          "md-nom",
		Issuer:      "test-pic",
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "breaker-nominal",
		Repos:       []MandateRepo{{Name: "nominal-repo", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 3},
	}
	if err := IssueMandate(m); err != nil {
		t.Fatalf("issuing mandate: %v", err)
	}

	st, err := EvaluateCircuitBreaker(m, repoDir, 0)
	if err != nil {
		t.Fatalf("EvaluateCircuitBreaker failed: %v", err)
	}
	if st.Tripped {
		t.Errorf("expected nominal breaker not tripped, got tripped")
	}
	if st.EffectiveMaxTier != "T2" {
		t.Errorf("expected T2, got %s", st.EffectiveMaxTier)
	}
	if st.Spent != 0 {
		t.Errorf("expected 0 spent, got %d", st.Spent)
	}
	if st.Limit != 3 {
		t.Errorf("expected limit 3, got %d", st.Limit)
	}
	if st.CleanStreak != 0 {
		t.Errorf("expected 0 clean streak, got %d", st.CleanStreak)
	}

	// 1 debit should remain nominal
	if err := RecordDebit(repoDir, "test failure", m.ID); err != nil {
		t.Fatalf("RecordDebit failed: %v", err)
	}

	st, err = EvaluateCircuitBreaker(m, repoDir, 0)
	if err != nil {
		t.Fatalf("EvaluateCircuitBreaker failed: %v", err)
	}
	if st.Tripped || st.EffectiveMaxTier != "T2" || st.Spent != 1 {
		t.Errorf("expected spent=1, not tripped, T2; got %+v", st)
	}
}

func TestBreakerBudgetExhaustion_ImmediateOnNextActionWithoutRestart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, _ := setupReplayRepo(t, "exhaustion-repo")

	m := &Mandate{
		Version:     1,
		ID:          "md-exhaust",
		Issuer:      "test-pic",
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "breaker-exhaust",
		Repos:       []MandateRepo{{Name: "exhaustion-repo", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 2},
	}
	if err := IssueMandate(m); err != nil {
		t.Fatalf("issuing mandate: %v", err)
	}

	// Debit 1: budget remains healthy (1/2)
	if err := RecordDebit(repoDir, "first failure", m.ID); err != nil {
		t.Fatalf("RecordDebit 1: %v", err)
	}
	st, err := EvaluateCircuitBreaker(m, repoDir, 0)
	if err != nil {
		t.Fatalf("EvaluateCircuitBreaker: %v", err)
	}
	if st.Tripped || st.EffectiveMaxTier != "T2" {
		t.Fatalf("expected nominal after 1 debit, got %+v", st)
	}

	// Debit 2: hits budget limit (2/2) -> trips immediately on next call without restart
	if err := RecordDebit(repoDir, "second failure", m.ID); err != nil {
		t.Fatalf("RecordDebit 2: %v", err)
	}

	st, err = EvaluateCircuitBreaker(m, repoDir, 0)
	if err != nil {
		t.Fatalf("EvaluateCircuitBreaker: %v", err)
	}
	if !st.Tripped {
		t.Errorf("expected circuit breaker tripped after budget exhausted")
	}
	if st.EffectiveMaxTier != "T0" {
		t.Errorf("expected effective tier T0 when tripped, got %s", st.EffectiveMaxTier)
	}
	if st.ConfiguredMaxTier != "T2" {
		t.Errorf("expected configured tier T2 preserved, got %s", st.ConfiguredMaxTier)
	}
	if st.RestoredByStreak {
		t.Errorf("expected RestoredByStreak=false")
	}
	if !strings.Contains(st.Reason, "budget exhausted") {
		t.Errorf("expected reason to mention budget exhausted, got %q", st.Reason)
	}
}

func TestBreakerCleanStreakUpgradePolicy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupReplayRepo(t, "streak-repo")

	baseTime := time.Now().UTC().Add(-20 * time.Second)

	m := &Mandate{
		Version:     1,
		ID:          "md-streak",
		Issuer:      "test-pic",
		IssuedAt:    baseTime.Format(time.RFC3339),
		ExpiresAt:   baseTime.Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "breaker-streak",
		Repos:       []MandateRepo{{Name: "streak-repo", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 1},
	}
	if err := IssueMandate(m); err != nil {
		t.Fatalf("issuing mandate: %v", err)
	}

	gLedger, _ := OpenGlobalLedger()
	rLedger, _ := OpenRepoLedger(repoDir)

	// Exhaust budget immediately with 1 debit at base + 1s
	_ = gLedger.Append(&Entry{
		ID:           newEvidenceID(),
		Ts:           baseTime.Add(1 * time.Second).Format(time.RFC3339),
		Kind:         "debit",
		Repo:         repoDir,
		Cmd:          []string{"mandate", "debit"},
		Status:       "PASS",
		ItemsChecked: 1,
		Extra: map[string]any{
			"mandate_id": m.ID,
			"reason":     "initial debit",
		},
	})

	st, _ := EvaluateCircuitBreaker(m, repoDir, 0)
	if !st.Tripped || st.EffectiveMaxTier != "T0" {
		t.Fatalf("expected breaker tripped, got %+v", st)
	}

	// 1 clean global action at base + 2s: clean streak = 1 (< 3 required) -> still tripped
	_ = gLedger.Append(&Entry{
		ID:           newEvidenceID(),
		Ts:           baseTime.Add(2 * time.Second).Format(time.RFC3339),
		Kind:         "breaker-clean",
		Repo:         repoDir,
		Cmd:          []string{"breaker", "clean", "audit"},
		Status:       "PASS",
		ItemsChecked: 1,
		Extra: map[string]any{
			"mandate_id":  m.ID,
			"action":      "audit",
			"evidence_id": "ev-c1",
		},
	})
	st, _ = EvaluateCircuitBreaker(m, repoDir, 0)
	if !st.Tripped || st.EffectiveMaxTier != "T0" || st.CleanStreak != 1 {
		t.Fatalf("expected tripped with streak 1, got %+v", st)
	}

	// 2 clean repo gate at base + 3s: clean streak = 2 (< 3) -> still tripped
	_ = rLedger.Append(&Entry{
		ID:     "ev-gate1",
		Ts:     baseTime.Add(3 * time.Second).Format(time.RFC3339),
		Kind:   "gate",
		Status: "PASS",
		SHA:    headSHA,
	})
	st, _ = EvaluateCircuitBreaker(m, repoDir, 0)
	if !st.Tripped || st.EffectiveMaxTier != "T0" || st.CleanStreak != 2 {
		t.Fatalf("expected tripped with streak 2, got %+v", st)
	}

	// 3 clean repo run at base + 4s: clean streak = 3 (>= 3) -> RESTORED!
	_ = rLedger.Append(&Entry{
		ID:           "ev-run1",
		Ts:           baseTime.Add(4 * time.Second).Format(time.RFC3339),
		Kind:         "run",
		Status:       "PASS",
		Dirty:        false,
		ItemsChecked: 10,
		SHA:          headSHA,
	})
	st, _ = EvaluateCircuitBreaker(m, repoDir, 0)
	if st.Tripped {
		t.Errorf("expected breaker restored after clean streak, but was tripped")
	}
	if st.EffectiveMaxTier != "T2" {
		t.Errorf("expected effective tier restored to T2, got %s", st.EffectiveMaxTier)
	}
	if !st.RestoredByStreak {
		t.Errorf("expected RestoredByStreak=true")
	}
	if st.CleanStreak != 3 {
		t.Errorf("expected clean streak 3, got %d", st.CleanStreak)
	}
	if !strings.Contains(st.Reason, "tier restored") {
		t.Errorf("expected restore reason, got %q", st.Reason)
	}

	// A new debit via RecordDebit (at time.Now() = base + 20s) resets clean streak to 0 and trips breaker again
	if err := RecordDebit(repoDir, "subsequent failure", m.ID); err != nil {
		t.Fatalf("RecordDebit: %v", err)
	}
	st, _ = EvaluateCircuitBreaker(m, repoDir, 0)
	if !st.Tripped || st.EffectiveMaxTier != "T0" || st.CleanStreak != 0 {
		t.Fatalf("expected breaker re-tripped with clean streak 0, got %+v", st)
	}

	// Custom upgrade streak requirement (e.g. 2)
	_ = RecordCleanActionForBreaker(repoDir, "action1", "ev-custom1", m.ID)
	_ = RecordCleanActionForBreaker(repoDir, "action2", "ev-custom2", m.ID)

	st, _ = EvaluateCircuitBreaker(m, repoDir, 2)
	if st.Tripped || st.EffectiveMaxTier != "T2" || !st.RestoredByStreak {
		t.Fatalf("expected restored with custom streak requirement 2, got %+v", st)
	}
}

func TestBreakerPiCReset(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, _ := setupReplayRepo(t, "reset-repo")

	m := &Mandate{
		Version:     1,
		ID:          "md-reset",
		Issuer:      "test-pic",
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "breaker-pic-reset",
		Repos:       []MandateRepo{{Name: "reset-repo", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 2},
	}
	if err := IssueMandate(m); err != nil {
		t.Fatalf("issuing mandate: %v", err)
	}

	// Exhaust budget
	_ = RecordDebit(repoDir, "fail 1", m.ID)
	_ = RecordDebit(repoDir, "fail 2", m.ID)
	st, _ := EvaluateCircuitBreaker(m, repoDir, 0)
	if !st.Tripped {
		t.Fatalf("expected breaker tripped before reset")
	}

	// PiC resets circuit breaker
	time.Sleep(5 * time.Millisecond)
	if err := ResetCircuitBreaker(m.ID, "PiC reviewed and approved reset"); err != nil {
		t.Fatalf("ResetCircuitBreaker: %v", err)
	}

	// Breaker should now be nominal
	st, err := EvaluateCircuitBreaker(m, repoDir, 0)
	if err != nil {
		t.Fatalf("EvaluateCircuitBreaker: %v", err)
	}
	if st.Tripped {
		t.Errorf("expected breaker un-tripped after PiC reset")
	}
	if st.EffectiveMaxTier != "T2" {
		t.Errorf("expected tier T2, got %s", st.EffectiveMaxTier)
	}
	if st.Spent != 0 {
		t.Errorf("expected spent count reset to 0, got %d", st.Spent)
	}

	// Subsequent debit should count fresh from 0
	time.Sleep(5 * time.Millisecond)
	_ = RecordDebit(repoDir, "fresh failure", m.ID)
	st, _ = EvaluateCircuitBreaker(m, repoDir, 0)
	if st.Spent != 1 || st.Tripped {
		t.Errorf("expected spent=1 and not tripped, got %+v", st)
	}
}

func TestBreakerCommandCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, _ := setupReplayRepo(t, "cli-repo")

	m := &Mandate{
		Version:     1,
		ID:          "md-cli",
		Issuer:      "test-pic",
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "breaker-cli",
		Repos:       []MandateRepo{{Name: "cli-repo", Path: repoDir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 2},
	}
	_ = IssueMandate(m)

	// Status text output
	code := breakerCommand([]string{"status", "--repo", repoDir})
	if code != 0 {
		t.Fatalf("breaker status exit %d", code)
	}

	// Status JSON output
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code = breakerCommand([]string{"status", "--repo", repoDir, "--json"})
	w.Close()
	os.Stdout = oldStdout

	if code != 0 {
		t.Fatalf("breaker status --json exit %d", code)
	}
	var outBuf bytes.Buffer
	_, _ = io.Copy(&outBuf, r)
	var bs BreakerStatus
	if err := json.Unmarshal(outBuf.Bytes(), &bs); err != nil {
		t.Fatalf("unmarshaling breaker status json: %v, raw: %s", err, outBuf.String())
	}
	if bs.MandateID != "md-cli" || bs.EffectiveMaxTier != "T2" {
		t.Errorf("unexpected json breaker status: %+v", bs)
	}

	// Reset via CLI
	code = breakerCommand([]string{"reset", "--reason", "CLI authorized reset"})
	if code != 0 {
		t.Fatalf("breaker reset exit %d", code)
	}
}
