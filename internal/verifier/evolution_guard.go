package verifier

import (
	"fmt"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/sandbox"
)

const (
	// CodeEvolutionTokenCap halts an evolution cycle that exceeds its per-cycle token budget.
	CodeEvolutionTokenCap = "E_EVOLUTION_TOKEN_CAP"
	// CodeEvolutionWallClockCap halts an evolution cycle that exceeds its wall-clock deadline.
	CodeEvolutionWallClockCap = "E_EVOLUTION_WALL_CLOCK_CAP"
	// CodeEvolutionRetryCap halts an evolution cycle that exceeds its retry budget.
	CodeEvolutionRetryCap = "E_EVOLUTION_RETRY_CAP"
	// CodeEvolutionOscillationStop halts an evolution cycle after K non-improving rounds.
	CodeEvolutionOscillationStop = "E_EVOLUTION_OSCILLATION_STOP"
)

// EvolutionBudget defines TRUST-G6 hard resource and anti-oscillation caps for self-improvement loops.
type EvolutionBudget struct {
	MaxTokensPerCycle     int           `json:"max_tokens_per_cycle"`
	MaxWallClockPerCycle  time.Duration `json:"max_wall_clock_per_cycle"`
	MaxRetriesPerCycle    int           `json:"max_retries_per_cycle"`
	MaxNonImprovingRounds int           `json:"max_non_improving_rounds"`
	MinImprovementDelta   float64       `json:"min_improvement_delta"`
}

// DefaultEvolutionBudget returns conservative defaults for bounded Tier-A evolution cycles.
func DefaultEvolutionBudget() EvolutionBudget {
	return EvolutionBudget{
		MaxTokensPerCycle:     250000,
		MaxWallClockPerCycle:  30 * time.Minute,
		MaxRetriesPerCycle:    6,
		MaxNonImprovingRounds: 3,
		MinImprovementDelta:   1e-6,
	}
}

// EvolutionRoundRecord captures measured metrics from one candidate evaluation round.
type EvolutionRoundRecord struct {
	Round         int                    `json:"round"`
	TokensUsed    int                    `json:"tokens_used"`
	RetriesUsed   int                    `json:"retries_used"`
	HoldoutScore  float64                `json:"holdout_score"`
	HoldoutPassed bool                   `json:"holdout_passed"`
	ScoreClass    sandbox.EpistemicClass `json:"score_class"`
	RecordedAt    time.Time              `json:"recorded_at"`
}

// EvolutionGuardStatus summarizes the bounded evolution state and halt condition.
type EvolutionGuardStatus struct {
	Halted             bool                   `json:"halted"`
	HaltCode           string                 `json:"halt_code,omitempty"`
	HaltReason         string                 `json:"halt_reason,omitempty"`
	TotalTokens        int                    `json:"total_tokens"`
	TotalRetries       int                    `json:"total_retries"`
	Elapsed            time.Duration          `json:"elapsed"`
	BestScore          float64                `json:"best_score"`
	HasBest            bool                   `json:"has_best"`
	NonImprovingStreak int                    `json:"non_improving_streak"`
	RoundsRecorded     int                    `json:"rounds_recorded"`
	EpistemicClass     sandbox.EpistemicClass `json:"epistemic_class"`
}

// EvolutionGuard enforces TRUST-G6 bounded evolution economics and monotone anti-oscillation stopping.
type EvolutionGuard struct {
	mu                 sync.Mutex
	budget             EvolutionBudget
	clock              func() time.Time
	startedAt          time.Time
	totalTokens        int
	totalRetries       int
	bestScore          float64
	hasBest            bool
	nonImprovingStreak int
	roundsRecorded     int
	halted             bool
	haltCode           string
	haltReason         string
}

// NewEvolutionGuard initializes a cycle guard using clock (defaulting to time.Now if nil).
func NewEvolutionGuard(budget EvolutionBudget, clock func() time.Time) *EvolutionGuard {
	if clock == nil {
		clock = time.Now
	}
	return &EvolutionGuard{
		budget:    budget,
		clock:     clock,
		startedAt: clock(),
	}
}

func (g *EvolutionGuard) haltLocked(code, reason string) {
	if g.halted {
		return
	}
	g.halted = true
	g.haltCode = code
	g.haltReason = reason
}

func (g *EvolutionGuard) statusLocked(now time.Time) EvolutionGuardStatus {
	elapsed := now.Sub(g.startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	return EvolutionGuardStatus{
		Halted:             g.halted,
		HaltCode:           g.haltCode,
		HaltReason:         g.haltReason,
		TotalTokens:        g.totalTokens,
		TotalRetries:       g.totalRetries,
		Elapsed:            elapsed,
		BestScore:          g.bestScore,
		HasBest:            g.hasBest,
		NonImprovingStreak: g.nonImprovingStreak,
		RoundsRecorded:     g.roundsRecorded,
		EpistemicClass:     sandbox.EpistemicMeasuredMetric,
	}
}

// AllowNextRound checks monotone halt state and wall-clock budget before dispatching another experiment.
func (g *EvolutionGuard) AllowNextRound() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock()
	if !g.halted && g.budget.MaxWallClockPerCycle > 0 && now.Sub(g.startedAt) > g.budget.MaxWallClockPerCycle {
		g.haltLocked(
			CodeEvolutionWallClockCap,
			fmt.Sprintf("elapsed %s exceeded max_wall_clock_per_cycle %s", now.Sub(g.startedAt), g.budget.MaxWallClockPerCycle),
		)
	}
	if g.halted {
		return fmt.Errorf("%s: %s", g.haltCode, g.haltReason)
	}
	return nil
}

// RecordRound updates cycle usage and holdout scores, halting monotonically if any cap is crossed.
func (g *EvolutionGuard) RecordRound(rec EvolutionRoundRecord) (EvolutionGuardStatus, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.clock()
	if !rec.RecordedAt.IsZero() && rec.RecordedAt.After(now) {
		now = rec.RecordedAt
	}
	if g.halted {
		return g.statusLocked(now), fmt.Errorf("%s: %s", g.haltCode, g.haltReason)
	}

	g.roundsRecorded++
	if rec.TokensUsed > 0 {
		g.totalTokens += rec.TokensUsed
	}
	if rec.RetriesUsed > 0 {
		g.totalRetries += rec.RetriesUsed
	}

	if g.budget.MaxWallClockPerCycle > 0 && now.Sub(g.startedAt) > g.budget.MaxWallClockPerCycle {
		g.haltLocked(
			CodeEvolutionWallClockCap,
			fmt.Sprintf("elapsed %s exceeded max_wall_clock_per_cycle %s", now.Sub(g.startedAt), g.budget.MaxWallClockPerCycle),
		)
	}
	if !g.halted && g.budget.MaxTokensPerCycle > 0 && g.totalTokens > g.budget.MaxTokensPerCycle {
		g.haltLocked(
			CodeEvolutionTokenCap,
			fmt.Sprintf("total tokens %d exceeded max_tokens_per_cycle %d", g.totalTokens, g.budget.MaxTokensPerCycle),
		)
	}
	if !g.halted && g.budget.MaxRetriesPerCycle > 0 && g.totalRetries > g.budget.MaxRetriesPerCycle {
		g.haltLocked(
			CodeEvolutionRetryCap,
			fmt.Sprintf("total retries %d exceeded max_retries_per_cycle %d", g.totalRetries, g.budget.MaxRetriesPerCycle),
		)
	}

	improved := rec.HoldoutPassed && (!g.hasBest || rec.HoldoutScore > g.bestScore+g.budget.MinImprovementDelta)
	if improved {
		g.bestScore = rec.HoldoutScore
		g.hasBest = true
		g.nonImprovingStreak = 0
	} else {
		g.nonImprovingStreak++
	}

	if !g.halted && g.budget.MaxNonImprovingRounds > 0 && g.nonImprovingStreak >= g.budget.MaxNonImprovingRounds {
		g.haltLocked(
			CodeEvolutionOscillationStop,
			fmt.Sprintf("reached %d consecutive non-improving rounds (cap=%d)", g.nonImprovingStreak, g.budget.MaxNonImprovingRounds),
		)
	}

	status := g.statusLocked(now)
	if g.halted {
		return status, fmt.Errorf("%s: %s", g.haltCode, g.haltReason)
	}
	return status, nil
}

// Status returns a snapshot of the guard's measured telemetry and halt state.
func (g *EvolutionGuard) Status() EvolutionGuardStatus {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.statusLocked(g.clock())
}
