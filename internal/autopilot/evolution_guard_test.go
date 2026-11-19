package autopilot

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/verifier"
)

func TestScheduler_EvolutionGuardBounds(t *testing.T) {
	t.Run("halts_runaway_retry_and_oscillation_without_requeueing", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Cron = ""
		cfg.MaxItemsPerTick = 20
		cfg.EvolutionBudget = &verifier.EvolutionBudget{
			MaxTokensPerCycle:     100000,
			MaxWallClockPerCycle:  time.Hour,
			MaxRetriesPerCycle:    2,
			MaxNonImprovingRounds: 5,
		}

		calls := 0
		s, err := NewScheduler(cfg, func(ctx context.Context, item *WorkItem) error {
			calls++
			return errors.New("persistent failing item")
		})
		if err != nil {
			t.Fatalf("NewScheduler: %v", err)
		}
		s.running = true
		s.queue.Enqueue(&WorkItem{ID: "runaway-1", Score: 1.0})

		s.processQueue(context.Background())

		// Round 1 (retry 1), Round 2 (retry 2), Round 3 (retry 3 > cap 2 -> halts & drops requeue).
		if calls != 3 {
			t.Fatalf("expected exactly 3 handler attempts before RetryCap halt, got %d", calls)
		}
		st := s.EvolutionStatus()
		if !st.Halted || st.HaltCode != verifier.CodeEvolutionRetryCap {
			t.Fatalf("expected %s halt, got %+v", verifier.CodeEvolutionRetryCap, st)
		}
		if s.queue.Len() != 0 {
			t.Fatalf("expected halted item NOT to be re-queued, queue len=%d", s.queue.Len())
		}

		// Subsequent processQueue call must stay halted monotonically with 0 additional calls.
		s.queue.Enqueue(&WorkItem{ID: "runaway-2", Score: 1.0})
		s.processQueue(context.Background())
		if calls != 3 {
			t.Fatalf("expected halted scheduler to refuse additional items, calls=%d", calls)
		}
	})

	t.Run("halts_on_wall_clock_cap_with_deterministic_clock", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Cron = ""
		cfg.MaxItemsPerTick = 10

		now := time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC)
		guard := verifier.NewEvolutionGuard(verifier.EvolutionBudget{
			MaxWallClockPerCycle:  time.Minute,
			MaxNonImprovingRounds: 10,
			MaxRetriesPerCycle:    10,
		}, func() time.Time { return now })

		calls := 0
		s, err := NewScheduler(cfg, func(ctx context.Context, item *WorkItem) error {
			calls++
			now = now.Add(2 * time.Minute)
			return nil
		})
		if err != nil {
			t.Fatalf("NewScheduler: %v", err)
		}
		s.SetEvolutionGuard(guard)
		s.running = true
		s.queue.Enqueue(&WorkItem{ID: "slow-1", Score: 1.0})
		s.queue.Enqueue(&WorkItem{ID: "slow-2", Score: 0.9})

		s.processQueue(context.Background())

		if calls != 1 {
			t.Fatalf("expected wall clock cap to halt after 1 slow item, got calls=%d", calls)
		}
		st := s.EvolutionStatus()
		if !st.Halted || st.HaltCode != verifier.CodeEvolutionWallClockCap {
			t.Fatalf("expected %s halt, got %+v", verifier.CodeEvolutionWallClockCap, st)
		}
	})

	t.Run("descending_priority_succeeding_items_never_trigger_false_oscillation_stop", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Cron = ""
		cfg.MaxItemsPerTick = 10
		cfg.EvolutionBudget = &verifier.EvolutionBudget{
			MaxNonImprovingRounds: 3,
			MaxRetriesPerCycle:    6,
			MaxWallClockPerCycle:  time.Hour,
			MaxTokensPerCycle:     100000,
		}

		calls := 0
		s, err := NewScheduler(cfg, func(ctx context.Context, item *WorkItem) error {
			calls++
			return nil
		})
		if err != nil {
			t.Fatalf("NewScheduler: %v", err)
		}
		s.running = true
		for i, score := range []float64{0.9, 0.8, 0.7, 0.6, 0.5} {
			s.queue.Enqueue(&WorkItem{ID: fmt.Sprintf("ok-%d", i), Score: score})
		}

		s.processQueue(context.Background())

		if calls != 5 {
			t.Fatalf("expected all 5 succeeding items to run, got calls=%d", calls)
		}
		st := s.EvolutionStatus()
		if st.Halted || st.NonImprovingStreak != 0 || st.BestScore != 5 {
			t.Fatalf("expected unhalted streak=0 BestScore=5, got %+v", st)
		}
	})
}
