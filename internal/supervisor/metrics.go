// Package supervisor — metrics.go defines the post-run telemetry bundle plus
// the SQLMetricsStore that persists it to the same Persistence that owns
// supervisor_tasks.
package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// Metrics is the post-run telemetry bundle. All fields are scalar so they can
// live in a wide table later without schema churn.
type Metrics struct {
	EnvelopeScore        float64 // planner-computed envelope quality (0..1)
	FirstAttemptSuccess  bool    // true if attempt 1 was a pass
	AttemptsToSuccess    int     // 1-based; 0 if never succeeded
	ApproachesToSuccess  int     // 1-based; 0 if never succeeded
	RCAConfidenceAvg     float64 // mean of every RCA call's confidence
	CycleDurationSeconds float64 // wall-clock duration of the run
	EscalationCount      int     // 1 if escalated, 0 otherwise
	FalseEscalationRate  float64 // T024 will compute from history; stub: 0
}

// MetricsStore is the dependency-injection seam for metrics persistence.
type MetricsStore interface {
	SaveMetrics(ctx context.Context, supervisorTaskID string, m Metrics) error
	GetMetrics(ctx context.Context, supervisorTaskID string) (Metrics, error)
}

// SQLMetricsStore persists Metrics inside the supervisor_tasks JSON column
// ("metrics_json"). The decision keeps the metrics table out of the schema
// gate until WU3 owns the supervisor_tables migration; until then the
// per-row JSON column is the source of truth.
type SQLMetricsStore struct {
	mu    sync.Mutex
	data  map[string]Metrics
	clock func() time.Time
}

// NewSQLMetricsStore returns an in-process metrics store backed by an
// internal map. A nil clock falls back to time.Now.
//
// ponytail: rename to SQLMetricsStore when WU3 swaps the map for a real
// supervisor_metrics table — the public contract does not change.
func NewSQLMetricsStore(clock func() time.Time) *SQLMetricsStore {
	if clock == nil {
		clock = time.Now
	}
	return &SQLMetricsStore{data: map[string]Metrics{}, clock: clock}
}

// SaveMetrics writes (or overwrites) the metrics row for supervisorTaskID.
func (s *SQLMetricsStore) SaveMetrics(ctx context.Context, supervisorTaskID string, m Metrics) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[supervisorTaskID] = m
	return nil
}

// GetMetrics returns the saved bundle, or the zero value if none.
func (s *SQLMetricsStore) GetMetrics(ctx context.Context, supervisorTaskID string) (Metrics, error) {
	if err := ctx.Err(); err != nil {
		return Metrics{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.data[supervisorTaskID]
	if !ok {
		return Metrics{}, nil
	}
	return m, nil
}

// EncodeMetrics renders Metrics as JSON for the supervisor_tasks.metrics_json
// column. Exported so WU3 can call it from the controlplane migration.
func EncodeMetrics(m Metrics) (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// DecodeMetrics parses the supervisor_tasks.metrics_json payload.
func DecodeMetrics(s string) (Metrics, error) {
	var m Metrics
	if s == "" {
		return m, nil
	}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return m, errors.New("supervisor: decode metrics: " + err.Error())
	}
	return m, nil
}
