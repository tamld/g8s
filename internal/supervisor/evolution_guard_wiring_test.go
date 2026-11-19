package supervisor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/sandbox"
	"github.com/tamld/g8s/internal/verifier"
)

type wiringEvalStore struct {
	tasks   []EvalTaskRow
	metrics map[string]Metrics
}

func newWiringEvalStore(succeeded, escalated, failed int) *wiringEvalStore {
	s := &wiringEvalStore{
		metrics: make(map[string]Metrics),
	}
	add := func(prefix, state string, count int) {
		for i := 0; i < count; i++ {
			id := fmt.Sprintf("%s-%d", prefix, i)
			s.tasks = append(s.tasks, EvalTaskRow{ID: id, State: state})
			m := Metrics{
				EnvelopeScore:        0.7,
				FirstAttemptSuccess:  state == "succeeded",
				AttemptsToSuccess:    1,
				ApproachesToSuccess:  1,
				CycleDurationSeconds: 10.0,
			}
			if state == "escalated" {
				m.EscalationCount = 1
			}
			if state == "failed" {
				m.AttemptsToSuccess = 3
				m.CycleDurationSeconds = 20.0
			}
			s.metrics[id] = m
		}
	}
	add("succ", "succeeded", succeeded)
	add("esc", "escalated", escalated)
	add("fail", "failed", failed)
	return s
}

func (s *wiringEvalStore) ListSupervisorTasks(ctx context.Context) ([]EvalTaskRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.tasks, nil
}

func (s *wiringEvalStore) GetMetrics(ctx context.Context, id string) (Metrics, error) {
	if err := ctx.Err(); err != nil {
		return Metrics{}, err
	}
	m, ok := s.metrics[id]
	if !ok {
		return Metrics{}, fmt.Errorf("missing metrics for %s", id)
	}
	return m, nil
}

type fixedProposalOptimizer struct {
	proposed SupervisorConfig
}

func (o *fixedProposalOptimizer) Propose(current SupervisorConfig, _ []Metrics) SupervisorConfig {
	_ = current
	return o.proposed
}

func TestEvaluationLoop_EvolutionGuardCapsAndRollback(t *testing.T) {
	ctx := context.Background()

	t.Run("halts_monotonically_on_token_cap", func(t *testing.T) {
		store := newWiringEvalStore(5, 2, 3)
		budget := verifier.EvolutionBudget{
			MaxTokensPerCycle:     1000,
			MaxWallClockPerCycle:  time.Hour,
			MaxRetriesPerCycle:    10,
			MaxNonImprovingRounds: 10,
		}
		cfg := DefaultEvaluationConfig()
		cfg.MinimumSampleSize = 2
		cfg.EvolutionBudget = &budget

		loop := NewEvaluationLoop(store, NewHeuristicOptimizer(), cfg)
		proposed := SupervisorConfig{MaxAttemptsPerApproach: 4, MaxApproaches: 3}

		out1, err := loop.RunEvaluationWithUsage(ctx, proposed, 600, 0)
		if err != nil {
			t.Fatalf("round 1 unexpected error: %v", err)
		}
		if out1.EvolutionStatus == nil || out1.EvolutionStatus.TotalTokens != 600 || out1.EvolutionStatus.Halted {
			t.Fatalf("unexpected round 1 status: %+v", out1.EvolutionStatus)
		}

		out2, err := loop.RunEvaluationWithUsage(ctx, proposed, 500, 0)
		if err == nil || !strings.Contains(err.Error(), verifier.CodeEvolutionTokenCap) {
			t.Fatalf("expected %s, got err=%v outcome=%+v", verifier.CodeEvolutionTokenCap, err, out2)
		}
		if out2.Accepted || out2.EvolutionStatus == nil || !out2.EvolutionStatus.Halted {
			t.Fatalf("expected halted rejected outcome, got %+v", out2)
		}
		if out2.EvolutionStatus.HaltCode != verifier.CodeEvolutionTokenCap || out2.EvolutionStatus.EpistemicClass != sandbox.EpistemicMeasuredMetric {
			t.Fatalf("unexpected evolution status: %+v", out2.EvolutionStatus)
		}

		// Monotone halt: subsequent AllowNextRound/RunEvaluation stays halted without running.
		out3, err := loop.RunEvaluationWithUsage(ctx, proposed, 1, 0)
		if err == nil || !strings.Contains(err.Error(), verifier.CodeEvolutionTokenCap) {
			t.Fatalf("expected subsequent round to remain halted, got err=%v out=%+v", err, out3)
		}
	})

	t.Run("halts_on_wall_clock_cap_with_deterministic_clock", func(t *testing.T) {
		store := newWiringEvalStore(5, 2, 3)
		now := time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC)
		budget := verifier.EvolutionBudget{
			MaxTokensPerCycle:     100000,
			MaxWallClockPerCycle:  2 * time.Minute,
			MaxRetriesPerCycle:    10,
			MaxNonImprovingRounds: 10,
		}
		cfg := DefaultEvaluationConfig()
		cfg.MinimumSampleSize = 2
		cfg.Clock = func() time.Time { return now }
		cfg.EvolutionBudget = &budget

		loop := NewEvaluationLoop(store, NewHeuristicOptimizer(), cfg)
		proposed := SupervisorConfig{MaxAttemptsPerApproach: 4, MaxApproaches: 3}

		if _, err := loop.RunEvaluationWithUsage(ctx, proposed, 10, 0); err != nil {
			t.Fatalf("round 1 unexpected error: %v", err)
		}

		now = now.Add(3 * time.Minute)
		out, err := loop.RunEvaluation(ctx, proposed)
		if err == nil || !strings.Contains(err.Error(), verifier.CodeEvolutionWallClockCap) {
			t.Fatalf("expected %s, got err=%v out=%+v", verifier.CodeEvolutionWallClockCap, err, out)
		}
		if !loop.EvolutionStatus().Halted {
			t.Fatalf("expected loop guard halted")
		}
	})

	t.Run("halts_on_retry_cap", func(t *testing.T) {
		store := newWiringEvalStore(5, 2, 3)
		budget := verifier.EvolutionBudget{
			MaxTokensPerCycle:     100000,
			MaxWallClockPerCycle:  time.Hour,
			MaxRetriesPerCycle:    2,
			MaxNonImprovingRounds: 10,
		}
		cfg := DefaultEvaluationConfig()
		cfg.MinimumSampleSize = 2
		cfg.EvolutionBudget = &budget

		loop := NewEvaluationLoop(store, NewHeuristicOptimizer(), cfg)
		proposed := SupervisorConfig{MaxAttemptsPerApproach: 4, MaxApproaches: 3}

		if _, err := loop.RunEvaluationWithUsage(ctx, proposed, 10, 2); err != nil {
			t.Fatalf("round 1 unexpected error: %v", err)
		}
		out, err := loop.RunEvaluationWithUsage(ctx, proposed, 10, 1)
		if err == nil || !strings.Contains(err.Error(), verifier.CodeEvolutionRetryCap) {
			t.Fatalf("expected %s, got err=%v out=%+v", verifier.CodeEvolutionRetryCap, err, out)
		}
	})

	t.Run("halts_on_repeated_optimizer_no_change_oscillation", func(t *testing.T) {
		store := newWiringEvalStore(5, 0, 5)
		budget := verifier.EvolutionBudget{
			MaxTokensPerCycle:     100000,
			MaxWallClockPerCycle:  time.Hour,
			MaxRetriesPerCycle:    10,
			MaxNonImprovingRounds: 2,
		}
		cfg := DefaultEvaluationConfig()
		cfg.MinimumSampleSize = 2
		cfg.EvolutionBudget = &budget

		loop := NewEvaluationLoop(store, NewStubOptimizer(), cfg)
		out1, err := loop.ProposeAndEvaluate(ctx)
		if err != nil {
			t.Fatalf("round 1 unexpected error: %v", err)
		}
		if out1.Accepted || out1.Reason != "optimizer proposed no change" {
			t.Fatalf("expected 'optimizer proposed no change' on round 1, got %+v", out1)
		}

		out2, err := loop.ProposeAndEvaluate(ctx)
		if err == nil || !strings.Contains(err.Error(), verifier.CodeEvolutionOscillationStop) {
			t.Fatalf("expected %s on 2nd non-improving no-change round, got err=%v out=%+v", verifier.CodeEvolutionOscillationStop, err, out2)
		}

		// Monotone halt persists on next call.
		if _, err := loop.ProposeAndEvaluate(ctx); err == nil || !strings.Contains(err.Error(), verifier.CodeEvolutionOscillationStop) {
			t.Fatalf("expected halted state on 3rd call, got %v", err)
		}
	})

	t.Run("resets_non_improving_streak_on_accepted_improvement_and_supports_rollback", func(t *testing.T) {
		// Build a 200-task dataset (50% succeeded, 20% escalated, 30% failed) with a deterministic
		// clock so increasing MaxAttemptsPerApproach achieves p << 0.05 on holdout reliably.
		store := newWiringEvalStore(100, 40, 60)
		budget := verifier.EvolutionBudget{
			MaxTokensPerCycle:     100000,
			MaxWallClockPerCycle:  time.Hour,
			MaxRetriesPerCycle:    10,
			MaxNonImprovingRounds: 2,
			MinImprovementDelta:   1e-6,
		}
		cfg := DefaultEvaluationConfig()
		cfg.HoldoutFraction = 0.9
		cfg.MinimumSampleSize = 5
		fixedNow := time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC)
		cfg.Clock = func() time.Time { return fixedNow }
		cfg.EvolutionBudget = &budget

		opt := &fixedProposalOptimizer{
			proposed: SupervisorConfig{
				MaxAttemptsPerApproach: 3, // equals initial -> non-improving round 1
				MaxApproaches:          3,
				SilenceThreshold:       300 * time.Second,
				PollInterval:           30 * time.Second,
			},
		}
		loop := NewEvaluationLoop(store, opt, cfg)

		// Round 1: no improvement -> streak becomes 1 (< cap 2)
		if _, err := loop.ProposeAndEvaluate(ctx); err != nil {
			t.Fatalf("round 1 unexpected error: %v", err)
		}
		if st := loop.EvolutionStatus(); st.NonImprovingStreak != 1 || st.Halted {
			t.Fatalf("expected streak=1 and unhalted after round 1, got %+v", st)
		}

		// Round 2: genuine improvement on holdout -> accepted and resets streak to 0!
		opt.proposed = SupervisorConfig{
			MaxAttemptsPerApproach: 9,
			MaxApproaches:          3,
			SilenceThreshold:       300 * time.Second,
			PollInterval:           30 * time.Second,
		}
		out2, err := loop.ProposeAndEvaluate(ctx)
		if err != nil {
			t.Fatalf("round 2 unexpected error: %v", err)
		}
		if !out2.Accepted {
			t.Fatalf("expected round 2 improvement accepted, got %+v", out2)
		}
		if st := loop.EvolutionStatus(); st.NonImprovingStreak != 0 || st.Halted || loop.CurrentVersion() != 1 {
			t.Fatalf("expected streak reset to 0 and version=1, got status=%+v version=%d", st, loop.CurrentVersion())
		}

		// Verify Rollback(0) restores initial config cleanly.
		if err := loop.Rollback(0); err != nil {
			t.Fatalf("Rollback(0): %v", err)
		}
		if loop.CurrentConfig().MaxAttemptsPerApproach != 3 || loop.CurrentVersion() != 2 {
			t.Fatalf("expected rolled back MaxAttemptsPerApproach=3 at version=2, got cfg=%+v ver=%d", loop.CurrentConfig(), loop.CurrentVersion())
		}
	})
}
