package verifier

import (
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/sandbox"
)

func TestEvolutionGuard_BoundedEconomicsAndOscillationStop(t *testing.T) {
	t.Run("halts_monotonically_on_token_cap", func(t *testing.T) {
		g := NewEvolutionGuard(EvolutionBudget{
			MaxTokensPerCycle:     1000,
			MaxWallClockPerCycle:  time.Hour,
			MaxRetriesPerCycle:    5,
			MaxNonImprovingRounds: 5,
		}, nil)
		if _, err := g.RecordRound(EvolutionRoundRecord{Round: 1, TokensUsed: 600, HoldoutScore: 0.5, HoldoutPassed: true}); err != nil {
			t.Fatalf("round 1 unexpected error: %v", err)
		}
		status, err := g.RecordRound(EvolutionRoundRecord{Round: 2, TokensUsed: 500, HoldoutScore: 0.7, HoldoutPassed: true})
		if err == nil || !strings.Contains(err.Error(), CodeEvolutionTokenCap) {
			t.Fatalf("expected %s, got status=%+v err=%v", CodeEvolutionTokenCap, status, err)
		}
		if !status.Halted || status.HaltCode != CodeEvolutionTokenCap || status.EpistemicClass != sandbox.EpistemicMeasuredMetric {
			t.Fatalf("unexpected halted status: %+v", status)
		}
		if nextErr := g.AllowNextRound(); nextErr == nil || !strings.Contains(nextErr.Error(), CodeEvolutionTokenCap) {
			t.Fatalf("AllowNextRound must stay halted monotonically, got %v", nextErr)
		}
	})

	t.Run("halts_on_wall_clock_cap_with_deterministic_clock", func(t *testing.T) {
		now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
		g := NewEvolutionGuard(EvolutionBudget{
			MaxWallClockPerCycle: 2 * time.Minute,
		}, func() time.Time { return now })
		if err := g.AllowNextRound(); err != nil {
			t.Fatalf("expected round allowed at t=0, got %v", err)
		}
		now = now.Add(3 * time.Minute)
		err := g.AllowNextRound()
		if err == nil || !strings.Contains(err.Error(), CodeEvolutionWallClockCap) {
			t.Fatalf("expected %s after clock advance, got %v", CodeEvolutionWallClockCap, err)
		}
	})

	t.Run("halts_on_retry_cap", func(t *testing.T) {
		g := NewEvolutionGuard(EvolutionBudget{
			MaxRetriesPerCycle: 2,
		}, nil)
		if _, err := g.RecordRound(EvolutionRoundRecord{Round: 1, RetriesUsed: 2, HoldoutScore: 0.6, HoldoutPassed: true}); err != nil {
			t.Fatalf("round 1 unexpected error: %v", err)
		}
		_, err := g.RecordRound(EvolutionRoundRecord{Round: 2, RetriesUsed: 1, HoldoutScore: 0.8, HoldoutPassed: true})
		if err == nil || !strings.Contains(err.Error(), CodeEvolutionRetryCap) {
			t.Fatalf("expected %s, got %v", CodeEvolutionRetryCap, err)
		}
	})

	t.Run("anti_oscillation_stop_and_streak_reset_on_strict_improvement", func(t *testing.T) {
		g := NewEvolutionGuard(EvolutionBudget{
			MaxNonImprovingRounds: 3,
			MinImprovementDelta:   0.01,
		}, nil)

		// Round 1 improves baseline -> streak 0
		st, err := g.RecordRound(EvolutionRoundRecord{Round: 1, HoldoutScore: 0.80, HoldoutPassed: true})
		if err != nil || st.NonImprovingStreak != 0 || !st.HasBest || st.BestScore != 0.80 {
			t.Fatalf("round 1 unexpected: st=%+v err=%v", st, err)
		}
		// Round 2 drops -> streak 1
		st, err = g.RecordRound(EvolutionRoundRecord{Round: 2, HoldoutScore: 0.70, HoldoutPassed: true})
		if err != nil || st.NonImprovingStreak != 1 {
			t.Fatalf("round 2 unexpected: st=%+v err=%v", st, err)
		}
		// Round 3 recovers strictly > 0.80 -> resets streak if > 0.81, wait: 0.85 resets streak to 0!
		st, err = g.RecordRound(EvolutionRoundRecord{Round: 3, HoldoutScore: 0.85, HoldoutPassed: true})
		if err != nil || st.NonImprovingStreak != 0 || st.BestScore != 0.85 {
			t.Fatalf("round 3 expected streak reset, got st=%+v err=%v", st, err)
		}
		// Now oscillate below best 0.85 for K=3 rounds: 0.80 -> 0.85 (no gain) -> 0.82 => HALT!
		if st, err = g.RecordRound(EvolutionRoundRecord{Round: 4, HoldoutScore: 0.80, HoldoutPassed: true}); err != nil || st.NonImprovingStreak != 1 {
			t.Fatalf("round 4 unexpected: st=%+v err=%v", st, err)
		}
		if st, err = g.RecordRound(EvolutionRoundRecord{Round: 5, HoldoutScore: 0.85, HoldoutPassed: true}); err != nil || st.NonImprovingStreak != 2 {
			t.Fatalf("round 5 unexpected: st=%+v err=%v", st, err)
		}
		st, err = g.RecordRound(EvolutionRoundRecord{Round: 6, HoldoutScore: 0.82, HoldoutPassed: true})
		if err == nil || !strings.Contains(err.Error(), CodeEvolutionOscillationStop) {
			t.Fatalf("expected %s on 3rd non-improving round, got st=%+v err=%v", CodeEvolutionOscillationStop, st, err)
		}
		if !st.Halted || g.AllowNextRound() == nil {
			t.Fatalf("expected monotone halt state after oscillation stop: %+v", st)
		}
	})
}
