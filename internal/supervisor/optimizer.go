// Package supervisor — optimizer.go implements the read-only query and
// aggregate layer over supervisor_tasks and supervisor_metrics for meta-optimizer
// insights (Concern C). It replaces optimizer_stub.go with pure-Go, read-only
// query capabilities without performing writes or automatic tuning.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

// Optimizer proposes a new SupervisorConfig from a run history. T022 will
// replace the stub with a bayesian- or bandit-style search.
type Optimizer interface {
	Propose(currentConfig SupervisorConfig, metrics []Metrics) SupervisorConfig
}

// StubOptimizer is the deterministic no-op default. It returns currentConfig
// unchanged; tests use it to verify the supervisor pipeline runs without an
// optimizer attached.
type StubOptimizer struct{}

// NewStubOptimizer returns a ready-to-use optimizer.
func NewStubOptimizer() *StubOptimizer { return &StubOptimizer{} }

// Propose returns currentConfig verbatim. The metrics argument is accepted
// to satisfy the Optimizer interface and to make the call site readable.
func (s *StubOptimizer) Propose(currentConfig SupervisorConfig, metrics []Metrics) SupervisorConfig {
	_ = metrics
	return currentConfig
}

// AggregateMetrics holds statistical aggregations across supervisor runs.
type AggregateMetrics struct {
	TotalRuns               int     `json:"total_runs"`
	FirstAttemptSuccessRate float64 `json:"first_attempt_success_rate"`
	AvgAttemptsToSuccess    float64 `json:"avg_attempts_to_success"`
	AvgApproachesToSuccess  float64 `json:"avg_approaches_to_success"`
	EscalationRate          float64 `json:"escalation_rate"`
	AvgCycleSeconds         float64 `json:"avg_cycle_duration_seconds"`
}

// AggregateOptions configures filtering for Aggregate and StreamMetrics queries.
type AggregateOptions struct {
	TimeRange  time.Duration    // e.g. 1*time.Hour, 24*time.Hour (if > 0, relative to Clock)
	Since      time.Time        // explicit lower bound for created_at (inclusive)
	Until      time.Time        // explicit upper bound for created_at (inclusive)
	WorkerName string           // optional worker name filter
	Clock      func() time.Time // injectable clock for deterministic time bounds
}

// TaskMetricsItem represents a single supervisor task with its associated metrics for streaming.
type TaskMetricsItem struct {
	SupervisorTaskID     string    `json:"supervisor_task_id"`
	State                string    `json:"state"`
	CreatedAt            time.Time `json:"created_at"`
	EnvelopeScore        float64   `json:"envelope_score"`
	FirstAttemptSuccess  bool      `json:"first_attempt_success"`
	AttemptsToSuccess    int       `json:"attempts_to_success"`
	ApproachesToSuccess  int       `json:"approaches_to_success"`
	RCAConfidenceAvg     float64   `json:"rca_confidence_avg"`
	CycleDurationSeconds float64   `json:"cycle_duration_seconds"`
	EscalationCount      int       `json:"escalation_count"`
	FalseEscalationRate  float64   `json:"false_escalation_rate"`
}

// Aggregate queries supervisor_tasks and supervisor_metrics from store and computes
// aggregate telemetry metrics according to opts. It is strictly read-only.
func Aggregate(store *controlplane.Store, ctx context.Context, opts AggregateOptions) (AggregateMetrics, error) {
	if store == nil {
		return AggregateMetrics{}, errors.New("supervisor: store is required")
	}
	if err := ctx.Err(); err != nil {
		return AggregateMetrics{}, err
	}

	tasks, err := store.ListSupervisorTasks(ctx)
	if err != nil {
		return AggregateMetrics{}, fmt.Errorf("supervisor: list supervisor tasks: %w", err)
	}

	since, until := resolveTimeBounds(opts)

	agg := AggregateMetrics{}
	var firstAttemptSuccessCount int
	var escalationCount int
	var sumAttempts float64
	var sumApproaches float64
	var sumCycleSeconds float64

	for _, t := range tasks {
		if err := ctx.Err(); err != nil {
			return AggregateMetrics{}, err
		}

		if !since.IsZero() && t.CreatedAt.Before(since) {
			continue
		}
		if !until.IsZero() && t.CreatedAt.After(until) {
			continue
		}

		if opts.WorkerName != "" {
			worker, err := store.GetSupervisorTaskWorker(ctx, t.ID)
			if err != nil || !strings.EqualFold(worker, opts.WorkerName) {
				continue
			}
		}

		m, err := store.GetMetrics(ctx, t.ID)
		if err != nil {
			// Skip tasks without recorded metrics
			continue
		}

		agg.TotalRuns++
		if m.FirstAttemptSuccess {
			firstAttemptSuccessCount++
		}
		sumAttempts += float64(m.AttemptsToSuccess)
		sumApproaches += float64(m.ApproachesToSuccess)
		if m.EscalationCount > 0 {
			escalationCount++
		}
		sumCycleSeconds += m.CycleDurationSeconds
	}

	if agg.TotalRuns == 0 {
		return agg, nil
	}

	denom := float64(agg.TotalRuns)
	agg.FirstAttemptSuccessRate = float64(firstAttemptSuccessCount) / denom
	agg.AvgAttemptsToSuccess = sumAttempts / denom
	agg.AvgApproachesToSuccess = sumApproaches / denom
	agg.EscalationRate = float64(escalationCount) / denom
	agg.AvgCycleSeconds = sumCycleSeconds / denom

	return agg, nil
}

// StreamMetrics scans supervisor tasks and emits each task's telemetry item to fn.
func StreamMetrics(store *controlplane.Store, ctx context.Context, opts AggregateOptions, fn func(item TaskMetricsItem) error) error {
	if store == nil {
		return errors.New("supervisor: store is required")
	}
	if fn == nil {
		return errors.New("supervisor: fn is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	tasks, err := store.ListSupervisorTasks(ctx)
	if err != nil {
		return fmt.Errorf("supervisor: list supervisor tasks: %w", err)
	}

	since, until := resolveTimeBounds(opts)

	for _, t := range tasks {
		if err := ctx.Err(); err != nil {
			return err
		}

		if !since.IsZero() && t.CreatedAt.Before(since) {
			continue
		}
		if !until.IsZero() && t.CreatedAt.After(until) {
			continue
		}

		if opts.WorkerName != "" {
			worker, err := store.GetSupervisorTaskWorker(ctx, t.ID)
			if err != nil || !strings.EqualFold(worker, opts.WorkerName) {
				continue
			}
		}

		m, err := store.GetMetrics(ctx, t.ID)
		if err != nil {
			continue
		}

		item := TaskMetricsItem{
			SupervisorTaskID:     t.ID,
			State:                t.State,
			CreatedAt:            t.CreatedAt,
			EnvelopeScore:        m.EnvelopeScore,
			FirstAttemptSuccess:  m.FirstAttemptSuccess,
			AttemptsToSuccess:    m.AttemptsToSuccess,
			ApproachesToSuccess:  m.ApproachesToSuccess,
			RCAConfidenceAvg:     m.RCAConfidenceAvg,
			CycleDurationSeconds: m.CycleDurationSeconds,
			EscalationCount:      m.EscalationCount,
			FalseEscalationRate:  m.FalseEscalationRate,
		}

		if err := fn(item); err != nil {
			return err
		}
	}

	return nil
}

func resolveTimeBounds(opts AggregateOptions) (time.Time, time.Time) {
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}

	since := opts.Since
	if opts.TimeRange > 0 {
		cutoff := clock().Add(-opts.TimeRange)
		if since.IsZero() || cutoff.After(since) {
			since = cutoff
		}
	}
	until := opts.Until
	return since, until
}
