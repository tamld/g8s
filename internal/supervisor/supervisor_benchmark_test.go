package supervisor

import (
	"context"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/orchestrator"
)

// benchmarkStubWorker is a configurable worker for benchmarking.
type benchmarkStubWorker struct {
	receipts []orchestrator.Receipt
	calls    int
}

func newBenchmarkStubWorker(receipts ...orchestrator.Receipt) *benchmarkStubWorker {
	return &benchmarkStubWorker{receipts: receipts}
}

func (w *benchmarkStubWorker) Name() string { return "bench" }

func (w *benchmarkStubWorker) Available(_ context.Context) error { return nil }

func (w *benchmarkStubWorker) Spawn(_ context.Context, t orchestrator.Task) (orchestrator.Handle, error) {
	idx := w.calls
	if idx >= len(w.receipts) {
		idx = len(w.receipts) - 1
	}
	w.calls++
	receipt := w.receipts[idx]
	if receipt.TaskID == "" {
		receipt.TaskID = t.ID
	}
	if receipt.WorkerName == "" {
		receipt.WorkerName = "bench"
	}
	return &stubHandle{receipt: receipt}, nil
}

// BenchmarkSupervisorIntervention measures the supervisor's intervention effectiveness
// under different failure scenarios.
func BenchmarkSupervisorIntervention(b *testing.B) {
	scenarios := []struct {
		name     string
		receipts []orchestrator.Receipt
		config   SupervisorConfig
	}{
		{
			name: "happy_path_immediate_success",
			receipts: []orchestrator.Receipt{
				goodReceipt(""),
			},
			config: SupervisorConfig{
				MaxAttemptsPerApproach: 3,
				MaxApproaches:          3,
				SilenceThreshold:       30 * time.Second,
			},
		},
		{
			name: "fail_then_recover_1_attempt",
			receipts: []orchestrator.Receipt{
				failingReceipt(""),
				goodReceipt(""),
			},
			config: SupervisorConfig{
				MaxAttemptsPerApproach: 3,
				MaxApproaches:          3,
				SilenceThreshold:       30 * time.Second,
			},
		},
		{
			name: "fail_then_recover_2_attempts",
			receipts: []orchestrator.Receipt{
				failingReceipt(""),
				failingReceipt(""),
				goodReceipt(""),
			},
			config: SupervisorConfig{
				MaxAttemptsPerApproach: 3,
				MaxApproaches:          3,
				SilenceThreshold:       30 * time.Second,
			},
		},
		{
			name: "multiple_approaches_needed",
			receipts: []orchestrator.Receipt{
				failingReceipt(""), failingReceipt(""), failingReceipt(""), // approach 1
				failingReceipt(""), failingReceipt(""), failingReceipt(""), // approach 2
				goodReceipt(""),
			},
			config: SupervisorConfig{
				MaxAttemptsPerApproach: 3,
				MaxApproaches:          3,
				SilenceThreshold:       30 * time.Second,
			},
		},
		{
			name: "high_confidence_rca_escalation",
			receipts: []orchestrator.Receipt{
				failingReceipt(""), failingReceipt(""), failingReceipt(""),
				failingReceipt(""), failingReceipt(""), failingReceipt(""),
				failingReceipt(""), failingReceipt(""), failingReceipt(""),
			},
			config: SupervisorConfig{
				MaxAttemptsPerApproach: 3,
				MaxApproaches:          3,
				SilenceThreshold:       30 * time.Second,
			},
		},
	}

	for _, sc := range scenarios {
		b.Run(sc.name, func(b *testing.B) {
			store := NewStubPersistence()
			worker := newBenchmarkStubWorker(sc.receipts...)
			sup := NewSelfTestSupervisor(store, worker, NewStubReviewer())
			sup.Config = sc.config

			// Inject high-confidence RCA for escalation scenario
			if sc.name == "high_confidence_rca_escalation" {
				sup.RCAFn = func(ctx context.Context, attempts []AttemptRecord) (RCARecord, error) {
					return RCARecord{
						Symptom:    "scope violation persists",
						RootCause:  "worker mutated outside allowed_paths",
						Confidence: 0.9,
					}, nil
				}
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := sup.Run(context.Background(), RunRequest{
					TaskDescription: "benchmark task",
					Role:            "scout",
					Permission:      "read_only",
					Model:           "m1",
					AddDirs:         []string{"./src"},
					SelfTestMode:    true,
				})
				if err != nil {
					b.Fatalf("Run failed: %v", err)
				}
			}
		})
	}
}

// BenchmarkSupervisorFirstAttemptSuccessRate measures the rate of first-attempt success
// across many runs with varying failure patterns.
func BenchmarkSupervisorFirstAttemptSuccessRate(b *testing.B) {
	// Simulate a mixed workload: 50% succeed first attempt, 30% succeed second, 20% need more
	receipts := []orchestrator.Receipt{
		goodReceipt(""),
		goodReceipt(""),
		goodReceipt(""),
		failingReceipt(""), goodReceipt(""),
		failingReceipt(""), failingReceipt(""), goodReceipt(""),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store := NewStubPersistence()
		worker := newBenchmarkStubWorker(receipts...)
		sup := NewSelfTestSupervisor(store, worker, NewStubReviewer())
		sup.Config = SupervisorConfig{
			MaxAttemptsPerApproach: 3,
			MaxApproaches:          3,
			SilenceThreshold:       30 * time.Second,
		}

		_, err := sup.Run(context.Background(), RunRequest{
			TaskDescription: "benchmark task",
			Role:            "scout",
			Permission:      "read_only",
			Model:           "m1",
			AddDirs:         []string{"./src"},
			SelfTestMode:    true,
		})
		if err != nil {
			b.Fatalf("Run failed: %v", err)
		}
	}
}

// BenchmarkSupervisorEscalationRate measures the escalation rate under persistent failures.
func BenchmarkSupervisorEscalationRate(b *testing.B) {
	// All failures - should escalate after exhausting budget
	receipts := []orchestrator.Receipt{}
	for i := 0; i < 9; i++ {
		receipts = append(receipts, failingReceipt(""))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store := NewStubPersistence()
		worker := newBenchmarkStubWorker(receipts...)
		sup := NewSelfTestSupervisor(store, worker, NewStubReviewer())
		sup.Config = SupervisorConfig{
			MaxAttemptsPerApproach: 3,
			MaxApproaches:          3,
			SilenceThreshold:       30 * time.Second,
		}
		sup.RCAFn = func(ctx context.Context, attempts []AttemptRecord) (RCARecord, error) {
			return RCARecord{
				Symptom:    "scope violation persists",
				RootCause:  "worker mutated outside allowed_paths",
				Confidence: 0.9,
			}, nil
		}

		res, err := sup.Run(context.Background(), RunRequest{
			TaskDescription: "benchmark task",
			Role:            "scout",
			Permission:      "read_only",
			Model:           "m1",
			AddDirs:         []string{"./src"},
			SelfTestMode:    true,
		})
		if err != nil {
			b.Fatalf("Run failed: %v", err)
		}
		if !res.Escalated {
			b.Fatalf("expected escalation")
		}
	}
}

// BenchmarkSupervisorCycleDuration measures the average cycle duration.
func BenchmarkSupervisorCycleDuration(b *testing.B) {
	receipts := []orchestrator.Receipt{
		failingReceipt(""),
		failingReceipt(""),
		goodReceipt(""),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store := NewStubPersistence()
		worker := newBenchmarkStubWorker(receipts...)
		sup := NewSelfTestSupervisor(store, worker, NewStubReviewer())
		sup.Config = SupervisorConfig{
			MaxAttemptsPerApproach: 3,
			MaxApproaches:          3,
			SilenceThreshold:       30 * time.Second,
		}

		res, err := sup.Run(context.Background(), RunRequest{
			TaskDescription: "benchmark task",
			Role:            "scout",
			Permission:      "read_only",
			Model:           "m1",
			AddDirs:         []string{"./src"},
			SelfTestMode:    true,
		})
		if err != nil {
			b.Fatalf("Run failed: %v", err)
		}
		_ = res
	}
}

// BenchmarkHeuristicOptimizerPropose measures the optimizer's proposal latency.
func BenchmarkHeuristicOptimizerPropose(b *testing.B) {
	optimizer := NewHeuristicOptimizer()
	metrics := []Metrics{}
	for i := 0; i < 100; i++ {
		metrics = append(metrics, Metrics{
			EnvelopeScore:        0.7,
			FirstAttemptSuccess:  i%2 == 0,
			AttemptsToSuccess:    (i % 3) + 1,
			ApproachesToSuccess:  (i % 2) + 1,
			RCAConfidenceAvg:     0.8,
			CycleDurationSeconds: 10.0,
			EscalationCount:      0,
			FalseEscalationRate:  0.0,
		})
	}

	config := SupervisorConfig{
		MaxAttemptsPerApproach: 3,
		MaxApproaches:          3,
		SilenceThreshold:       30 * time.Second,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = optimizer.Propose(config, metrics)
	}
}

// BenchmarkAggregateMetrics measures the aggregate metrics computation latency.
func BenchmarkAggregateMetrics(b *testing.B) {
	// Create a real controlplane store for benchmarking
	dbPath := b.TempDir() + "/test.db"
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		b.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	// Add some test data
	for i := 0; i < 100; i++ {
		st := controlplane.SupervisorTaskRow{
			ID:           "task-" + string(rune(i)),
			State:        "succeeded",
			EnvelopeJSON: "{}",
			ApproachIdx:  0,
			AttemptIdx:   0,
			CreatedAt:    time.Now().Add(-time.Hour),
			UpdatedAt:    time.Now(),
		}
		_ = store.CreateSupervisorTask(context.Background(), st)
		_ = store.SaveMetrics(context.Background(), st.ID, controlplane.MetricsRow{
			SupervisorTaskID:     st.ID,
			EnvelopeScore:        0.7,
			FirstAttemptSuccess:  true,
			AttemptsToSuccess:    1,
			ApproachesToSuccess:  1,
			RCAConfidenceAvg:     0.8,
			CycleDurationSeconds: 10.0,
			EscalationCount:      0,
			FalseEscalationRate:  0.0,
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := Aggregate(store, context.Background(), AggregateOptions{})
		if err != nil {
			b.Fatalf("Aggregate failed: %v", err)
		}
	}
}
