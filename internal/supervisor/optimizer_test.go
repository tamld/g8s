// Package supervisor — optimizer_test.go tests the meta-optimizer implementations.
package supervisor

import (
	"testing"
)

func TestStubOptimizer_Propose(t *testing.T) {
	opt := NewStubOptimizer()
	cfg := defaultSupervisorConfig()
	metrics := []Metrics{
		{FirstAttemptSuccess: true, AttemptsToSuccess: 1, ApproachesToSuccess: 1},
		{FirstAttemptSuccess: false, AttemptsToSuccess: 3, ApproachesToSuccess: 2, EscalationCount: 1},
	}

	result := opt.Propose(cfg, metrics)

	// Should return config unchanged
	if result.MaxAttemptsPerApproach != cfg.MaxAttemptsPerApproach {
		t.Error("StubOptimizer should not modify MaxAttemptsPerApproach")
	}
	if result.MaxApproaches != cfg.MaxApproaches {
		t.Error("StubOptimizer should not modify MaxApproaches")
	}
}

func TestHeuristicOptimizer_Propose_NoMetrics(t *testing.T) {
	opt := NewHeuristicOptimizer()
	cfg := defaultSupervisorConfig()
	metrics := []Metrics{}

	result := opt.Propose(cfg, metrics)

	// Should return config unchanged when no metrics
	if result.MaxAttemptsPerApproach != cfg.MaxAttemptsPerApproach {
		t.Error("should not modify config when no metrics")
	}
}

func TestHeuristicOptimizer_Propose_HighAttempts(t *testing.T) {
	opt := NewHeuristicOptimizer()
	cfg := defaultSupervisorConfig()

	// Simulate high avg attempts to success
	metrics := []Metrics{
		{FirstAttemptSuccess: false, AttemptsToSuccess: 5, ApproachesToSuccess: 2, CycleDurationSeconds: 100},
		{FirstAttemptSuccess: false, AttemptsToSuccess: 6, ApproachesToSuccess: 3, CycleDurationSeconds: 120},
		{FirstAttemptSuccess: true, AttemptsToSuccess: 2, ApproachesToSuccess: 1, CycleDurationSeconds: 50},
		{FirstAttemptSuccess: false, AttemptsToSuccess: 4, ApproachesToSuccess: 2, CycleDurationSeconds: 80},
		{FirstAttemptSuccess: false, AttemptsToSuccess: 7, ApproachesToSuccess: 3, CycleDurationSeconds: 150},
	}

	result := opt.Propose(cfg, metrics)

	// Should increase MaxAttemptsPerApproach due to high avg attempts
	if result.MaxAttemptsPerApproach <= cfg.MaxAttemptsPerApproach {
		t.Errorf("expected MaxAttemptsPerApproach to increase from %d to >%d, got %d",
			cfg.MaxAttemptsPerApproach, cfg.MaxAttemptsPerApproach, result.MaxAttemptsPerApproach)
	}
}

func TestHeuristicOptimizer_Propose_HighEscalation(t *testing.T) {
	opt := NewHeuristicOptimizer()
	cfg := defaultSupervisorConfig()

	// Simulate high escalation rate
	metrics := []Metrics{
		{FirstAttemptSuccess: false, AttemptsToSuccess: 3, ApproachesToSuccess: 3, EscalationCount: 1, CycleDurationSeconds: 100},
		{FirstAttemptSuccess: false, AttemptsToSuccess: 3, ApproachesToSuccess: 3, EscalationCount: 1, CycleDurationSeconds: 120},
		{FirstAttemptSuccess: false, AttemptsToSuccess: 3, ApproachesToSuccess: 3, EscalationCount: 1, CycleDurationSeconds: 80},
		{FirstAttemptSuccess: true, AttemptsToSuccess: 1, ApproachesToSuccess: 1, CycleDurationSeconds: 50},
	}

	result := opt.Propose(cfg, metrics)

	// Should increase MaxApproaches due to high escalation rate
	if result.MaxApproaches <= cfg.MaxApproaches {
		t.Errorf("expected MaxApproaches to increase from %d to >%d, got %d",
			cfg.MaxApproaches, cfg.MaxApproaches, result.MaxApproaches)
	}
}

func TestHeuristicOptimizer_Propose_LowFirstAttemptSuccess(t *testing.T) {
	opt := NewHeuristicOptimizer()
	cfg := defaultSupervisorConfig()

	// Simulate low first attempt success rate with enough runs
	metrics := make([]Metrics, 15)
	for i := 0; i < 15; i++ {
		metrics[i] = Metrics{
			FirstAttemptSuccess: false,
			AttemptsToSuccess:   3,
			ApproachesToSuccess: 2,
			CycleDurationSeconds: 100,
		}
	}
	// Make one successful on first attempt
	metrics[0].FirstAttemptSuccess = true
	metrics[0].AttemptsToSuccess = 1
	metrics[0].ApproachesToSuccess = 1

	result := opt.Propose(cfg, metrics)

	// Should increase MaxAttemptsPerApproach due to low first attempt success
	if result.MaxAttemptsPerApproach <= cfg.MaxAttemptsPerApproach {
		t.Errorf("expected MaxAttemptsPerApproach to increase due to low first attempt success")
	}
}

func TestHeuristicOptimizer_Propose_LongCycles(t *testing.T) {
	opt := NewHeuristicOptimizer()
	cfg := defaultSupervisorConfig()

	// Simulate long cycle durations
	metrics := []Metrics{
		{FirstAttemptSuccess: true, AttemptsToSuccess: 1, ApproachesToSuccess: 1, CycleDurationSeconds: 200},
		{FirstAttemptSuccess: false, AttemptsToSuccess: 3, ApproachesToSuccess: 2, CycleDurationSeconds: 250},
		{FirstAttemptSuccess: false, AttemptsToSuccess: 2, ApproachesToSuccess: 2, CycleDurationSeconds: 300},
	}

	result := opt.Propose(cfg, metrics)

	// Should increase SilenceThreshold due to long cycles
	if result.SilenceThreshold <= cfg.SilenceThreshold {
		t.Errorf("expected SilenceThreshold to increase from %v to >%v, got %v",
			cfg.SilenceThreshold, cfg.SilenceThreshold, result.SilenceThreshold)
	}
}

func TestComputeAggregateFromSlice(t *testing.T) {
	metrics := []Metrics{
		{FirstAttemptSuccess: true, AttemptsToSuccess: 1, ApproachesToSuccess: 1, EscalationCount: 0, CycleDurationSeconds: 50},
		{FirstAttemptSuccess: false, AttemptsToSuccess: 3, ApproachesToSuccess: 2, EscalationCount: 0, CycleDurationSeconds: 100},
		{FirstAttemptSuccess: false, AttemptsToSuccess: 2, ApproachesToSuccess: 3, EscalationCount: 1, CycleDurationSeconds: 150},
		{FirstAttemptSuccess: true, AttemptsToSuccess: 1, ApproachesToSuccess: 1, EscalationCount: 0, CycleDurationSeconds: 40},
	}

	agg := computeAggregateFromSlice(metrics)

	if agg.TotalRuns != 4 {
		t.Errorf("TotalRuns = %d, want 4", agg.TotalRuns)
	}
	if agg.FirstAttemptSuccessRate != 0.5 {
		t.Errorf("FirstAttemptSuccessRate = %f, want 0.5", agg.FirstAttemptSuccessRate)
	}
	expectedAvgAttempts := (1 + 3 + 2 + 1) / 4.0
	if agg.AvgAttemptsToSuccess != expectedAvgAttempts {
		t.Errorf("AvgAttemptsToSuccess = %f, want %f", agg.AvgAttemptsToSuccess, expectedAvgAttempts)
	}
	expectedAvgApproaches := (1 + 2 + 3 + 1) / 4.0
	if agg.AvgApproachesToSuccess != expectedAvgApproaches {
		t.Errorf("AvgApproachesToSuccess = %f, want %f", agg.AvgApproachesToSuccess, expectedAvgApproaches)
	}
	if agg.EscalationRate != 0.25 {
		t.Errorf("EscalationRate = %f, want 0.25", agg.EscalationRate)
	}
	expectedAvgCycle := (50 + 100 + 150 + 40) / 4.0
	if agg.AvgCycleSeconds != expectedAvgCycle {
		t.Errorf("AvgCycleSeconds = %f, want %f", agg.AvgCycleSeconds, expectedAvgCycle)
	}
}

func TestHeuristicOptimizer_RespectsBounds(t *testing.T) {
	opt := NewHeuristicOptimizer()
	cfg := defaultSupervisorConfig()

	// Simulate extremely high attempts that would exceed max bound
	metrics := []Metrics{
		{FirstAttemptSuccess: false, AttemptsToSuccess: 100, ApproachesToSuccess: 50, CycleDurationSeconds: 1000},
		{FirstAttemptSuccess: false, AttemptsToSuccess: 100, ApproachesToSuccess: 50, CycleDurationSeconds: 1000},
	}

	result := opt.Propose(cfg, metrics)

	// Should respect max bounds
	if result.MaxAttemptsPerApproach > opt.MaxAttemptsPerApproach {
		t.Errorf("MaxAttemptsPerApproach %d exceeds max bound %d", result.MaxAttemptsPerApproach, opt.MaxAttemptsPerApproach)
	}
	if result.MaxApproaches > opt.MaxApproaches {
		t.Errorf("MaxApproaches %d exceeds max bound %d", result.MaxApproaches, opt.MaxApproaches)
	}
	if result.MaxAttemptsPerApproach < opt.MinAttemptsPerApproach {
		t.Errorf("MaxAttemptsPerApproach %d below min bound %d", result.MaxAttemptsPerApproach, opt.MinAttemptsPerApproach)
	}
	if result.MaxApproaches < opt.MinApproaches {
		t.Errorf("MaxApproaches %d below min bound %d", result.MaxApproaches, opt.MinApproaches)
	}
}

func TestHeuristicOptimizer_CustomBounds(t *testing.T) {
	opt := &HeuristicOptimizer{
		MinAttemptsPerApproach: 2,
		MaxAttemptsPerApproach: 5,
		MinApproaches:          2,
		MaxApproaches:          5,
	}

	cfg := defaultSupervisorConfig()
	metrics := []Metrics{
		{FirstAttemptSuccess: false, AttemptsToSuccess: 10, ApproachesToSuccess: 10, CycleDurationSeconds: 100},
	}

	result := opt.Propose(cfg, metrics)

	// Should respect custom bounds
	if result.MaxAttemptsPerApproach > 5 || result.MaxAttemptsPerApproach < 2 {
		t.Errorf("MaxAttemptsPerApproach %d not in custom bounds [2,5]", result.MaxAttemptsPerApproach)
	}
	if result.MaxApproaches > 5 || result.MaxApproaches < 2 {
		t.Errorf("MaxApproaches %d not in custom bounds [2,5]", result.MaxApproaches)
	}
}