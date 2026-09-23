package supervisor

import (
	"context"
	"strings"
	"testing"
)

func TestStubWorktreePool_AcquireRelease(t *testing.T) {
	pool := StubWorktreePool{}
	ctx := context.Background()

	wt, err := pool.Acquire(ctx, "task-123")
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	expectedID := "stub-task-123"
	expectedBranch := "stub/task-123"
	if wt.ID != expectedID {
		t.Errorf("expected ID %q, got %q", expectedID, wt.ID)
	}
	if wt.Branch != expectedBranch {
		t.Errorf("expected Branch %q, got %q", expectedBranch, wt.Branch)
	}
	if wt.Path != "" || wt.BaseSHA != "" {
		t.Errorf("expected empty Path and BaseSHA, got Path=%q BaseSHA=%q", wt.Path, wt.BaseSHA)
	}

	if err := pool.Release(ctx, wt, false); err != nil {
		t.Errorf("Release(keep=false) failed: %v", err)
	}
	if err := pool.Release(ctx, wt, true); err != nil {
		t.Errorf("Release(keep=true) failed: %v", err)
	}
}

func TestRunOutcome_String(t *testing.T) {
	cases := []struct {
		outcome  RunOutcome
		expected string
	}{
		{RunSucceeded, "SUCCEEDED"},
		{RunFailed, "FAILED"},
		{RunEscalated, "ESCALATED"},
		{RunOutcome(999), "UNKNOWN"},
	}

	for _, tc := range cases {
		if got := tc.outcome.String(); got != tc.expected {
			t.Errorf("RunOutcome(%d).String() = %q, want %q", tc.outcome, got, tc.expected)
		}
	}
}

func TestTaskEnvelope_String(t *testing.T) {
	cases := []struct {
		envelope TaskEnvelope
		expected string
	}{
		{TaskEnvelope{SelectedFields: []string{"patch", "diff_stat"}}, "envelope{patch,diff_stat}"},
		{TaskEnvelope{SelectedFields: []string{}}, "envelope{}"},
		{TaskEnvelope{SelectedFields: nil}, "envelope{}"},
	}

	for _, tc := range cases {
		if got := tc.envelope.String(); got != tc.expected {
			t.Errorf("TaskEnvelope.String() = %q, want %q", got, tc.expected)
		}
	}
}

func TestVerdict_String(t *testing.T) {
	cases := []struct {
		verdict  Verdict
		expected string
	}{
		{VerdictPass, "PASS"},
		{VerdictRevise, "REVISE"},
		{VerdictFail, "FAIL"},
		{Verdict(999), "UNKNOWN"},
	}

	for _, tc := range cases {
		if got := tc.verdict.String(); got != tc.expected {
			t.Errorf("Verdict(%d).String() = %q, want %q", tc.verdict, got, tc.expected)
		}
	}
}

func TestDefaultPollerConfig(t *testing.T) {
	cfg := DefaultPollerConfig()
	if cfg.Interval <= 0 {
		t.Errorf("expected positive Interval, got %v", cfg.Interval)
	}
	if cfg.StaleThreshold <= 0 {
		t.Errorf("expected positive StaleThreshold, got %v", cfg.StaleThreshold)
	}
	if cfg.SilenceThreshold <= 0 {
		t.Errorf("expected positive SilenceThreshold, got %v", cfg.SilenceThreshold)
	}
	if strings.TrimSpace(cfg.HeartbeatDir) == "" {
		t.Errorf("expected non-empty HeartbeatDir, got %q", cfg.HeartbeatDir)
	}
	if cfg.Clock == nil {
		t.Error("expected non-nil Clock in DefaultPollerConfig")
	}
}

func TestSupervisor_InitDefaults(t *testing.T) {
	s := &Supervisor{}
	s.initDefaults()

	if s.Clock == nil {
		t.Error("expected default Clock")
	}
	if s.Logger == nil {
		t.Error("expected default Logger")
	}
	if s.RCAFn == nil {
		t.Error("expected default RCAFn")
	}
	if s.Reviewer == nil {
		t.Error("expected default Reviewer")
	}
	if s.Enforcer == nil {
		t.Error("expected default Enforcer")
	}
	if s.MetricsStore == nil {
		t.Error("expected default MetricsStore")
	}
	if s.Config.MaxAttemptsPerApproach != 3 {
		t.Errorf("expected MaxAttemptsPerApproach 3, got %d", s.Config.MaxAttemptsPerApproach)
	}
	if s.Config.MaxApproaches != 3 {
		t.Errorf("expected MaxApproaches 3, got %d", s.Config.MaxApproaches)
	}
	if len(s.Envelope.SelectedFields) == 0 {
		t.Error("expected non-empty Envelope.SelectedFields")
	}
}
