package supervisor

import (
	"context"
	"errors"
	"testing"
	"time"
)

// stubStoreForEvaluation provides a test store with pre-populated tasks.
type stubStoreForEvaluation struct {
	tasks   map[string]EvalTaskRow
	metrics map[string]Metrics
	clock   func() time.Time
}

func newStubStoreForEvaluation() *stubStoreForEvaluation {
	return &stubStoreForEvaluation{
		tasks:   make(map[string]EvalTaskRow),
		metrics: make(map[string]Metrics),
		clock:   time.Now,
	}
}

func (s *stubStoreForEvaluation) addTask(id, state string, withMetrics bool) {
	s.tasks[id] = EvalTaskRow{ID: id, State: state}

	if withMetrics {
		m := Metrics{
			EnvelopeScore:        0.7,
			FirstAttemptSuccess:  state == "succeeded",
			AttemptsToSuccess:    1,
			ApproachesToSuccess:  1,
			RCAConfidenceAvg:     0.8,
			CycleDurationSeconds: 10.0,
			EscalationCount:      0,
			FalseEscalationRate:  0.0,
		}
		if state == "escalated" {
			m.EscalationCount = 1
			m.FirstAttemptSuccess = false
		}
		if state == "failed" {
			m.FirstAttemptSuccess = false
			m.AttemptsToSuccess = 3
			m.CycleDurationSeconds = 30.0
		}
		s.metrics[id] = m
	}
}

func (s *stubStoreForEvaluation) ListSupervisorTasks(ctx context.Context) ([]EvalTaskRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]EvalTaskRow, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, t)
	}
	return out, nil
}

func (s *stubStoreForEvaluation) GetMetrics(ctx context.Context, id string) (Metrics, error) {
	if err := ctx.Err(); err != nil {
		return Metrics{}, err
	}
	m, ok := s.metrics[id]
	if !ok {
		return Metrics{}, errors.New("metrics not found")
	}
	return m, nil
}

// TestTaskSplitter tests the deterministic task splitting.
func TestTaskSplitter(t *testing.T) {
	splitter := NewTaskSplitter(42)

	taskIDs := []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8", "t9", "t10"}

	// Test 30% holdout
	split := splitter.Split(taskIDs, 0.3)
	if len(split.HoldoutTaskIDs) != 3 {
		t.Errorf("expected 3 holdout tasks, got %d", len(split.HoldoutTaskIDs))
	}
	if len(split.TrainingTaskIDs) != 7 {
		t.Errorf("expected 7 training tasks, got %d", len(split.TrainingTaskIDs))
	}

	// Test reproducibility - same seed should give same split
	splitter2 := NewTaskSplitter(42)
	split2 := splitter2.Split(taskIDs, 0.3)
	for i := range split.HoldoutTaskIDs {
		if split.HoldoutTaskIDs[i] != split2.HoldoutTaskIDs[i] {
			t.Errorf("split not reproducible at index %d: %s vs %s", i, split.HoldoutTaskIDs[i], split2.HoldoutTaskIDs[i])
		}
	}

	// Test different seed gives different split
	splitter3 := NewTaskSplitter(99)
	split3 := splitter3.Split(taskIDs, 0.3)
	different := false
	for i := range split.HoldoutTaskIDs {
		if split.HoldoutTaskIDs[i] != split3.HoldoutTaskIDs[i] {
			different = true
			break
		}
	}
	if !different {
		t.Error("different seed should produce different split")
	}

	// Test edge cases
	emptySplit := splitter.Split([]string{}, 0.3)
	if len(emptySplit.TrainingTaskIDs) != 0 || len(emptySplit.HoldoutTaskIDs) != 0 {
		t.Error("empty input should produce empty split")
	}

	// Test holdoutFraction = 0 (no holdout)
	noHoldout := splitter.Split(taskIDs, 0)
	if len(noHoldout.HoldoutTaskIDs) != 0 {
		t.Error("holdoutFraction=0 should produce no holdout")
	}
	if len(noHoldout.TrainingTaskIDs) != 10 {
		t.Error("holdoutFraction=0 should put all in training")
	}

	// Test holdoutFraction = 1 (all holdout)
	allHoldout := splitter.Split(taskIDs, 1)
	if len(allHoldout.TrainingTaskIDs) != 0 {
		t.Error("holdoutFraction=1 should produce no training")
	}
}

// TestEvaluationLoopBasic tests basic evaluation loop functionality.
func TestEvaluationLoopBasic(t *testing.T) {
	store := newStubStoreForEvaluation()

	// Add 20 completed tasks with metrics (10 succeeded, 5 escalated, 5 failed)
	for i := 0; i < 10; i++ {
		store.addTask("task-succ-"+string(rune(i)), "succeeded", true)
	}
	for i := 0; i < 5; i++ {
		store.addTask("task-esc-"+string(rune(i)), "escalated", true)
	}
	for i := 0; i < 5; i++ {
		store.addTask("task-fail-"+string(rune(i)), "failed", true)
	}

	config := DefaultEvaluationConfig()
	config.HoldoutFraction = 0.3
	config.MinimumSampleSize = 5

	optimizer := NewHeuristicOptimizer()
	loop := NewEvaluationLoop(store, optimizer, config)

	// Check initial config
	currentConfig := loop.CurrentConfig()
	if currentConfig.MaxAttemptsPerApproach != 3 {
		t.Errorf("initial MaxAttemptsPerApproach should be 3, got %d", currentConfig.MaxAttemptsPerApproach)
	}

	// Run evaluation with a proposed config
	proposedConfig := SupervisorConfig{
		MaxAttemptsPerApproach: 5,
		MaxApproaches:          4,
		SilenceThreshold:       300 * time.Second,
		PollInterval:           30 * time.Second,
	}

	outcome, err := loop.RunEvaluation(context.Background(), proposedConfig)
	if err != nil {
		t.Fatalf("RunEvaluation failed: %v", err)
	}

	t.Logf("Outcome: Accepted=%v, Reason=%s, PValue=%.4f, EffectSize=%.4f",
		outcome.Accepted, outcome.Reason, outcome.PValue, outcome.EffectSize)
	t.Logf("Training: Score=%.4f, Holdout: Score=%.4f",
		outcome.TrainingResult.Score, outcome.HoldoutResult.Score)

	// Should have evaluated both sets
	if outcome.TrainingResult.TaskCount == 0 {
		t.Error("training result should have tasks")
	}
	if outcome.HoldoutResult.TaskCount == 0 {
		t.Error("holdout result should have tasks")
	}
}

// TestProposeAndEvaluate tests the full propose-and-evaluate cycle.
func TestProposeAndEvaluate(t *testing.T) {
	store := newStubStoreForEvaluation()

	// Add enough tasks for meaningful evaluation
	for i := 0; i < 15; i++ {
		store.addTask("task-"+string(rune(i)), "succeeded", true)
	}
	for i := 0; i < 5; i++ {
		store.addTask("task-esc-"+string(rune(i)), "escalated", true)
	}

	config := DefaultEvaluationConfig()
	config.HoldoutFraction = 0.2
	config.MinimumSampleSize = 5

	optimizer := NewHeuristicOptimizer()
	loop := NewEvaluationLoop(store, optimizer, config)

	// Run propose and evaluate
	outcome, err := loop.ProposeAndEvaluate(context.Background())
	if err != nil {
		t.Fatalf("ProposeAndEvaluate failed: %v", err)
	}

	t.Logf("ProposeAndEvaluate: Accepted=%v, Reason=%s", outcome.Accepted, outcome.Reason)

	// Should have at least initial version
	if len(loop.VersionHistory()) < 1 {
		t.Error("should have at least initial version")
	}

	// Check version history
	history := loop.VersionHistory()
	for _, v := range history {
		t.Logf("Version %d: Source=%s, Accepted=%v, TrainingScore=%.4f, HoldoutScore=%.4f",
			v.Version, v.Source, v.Accepted, v.TrainingScore, v.HoldoutScore)
	}
}

// TestRollback tests config rollback functionality.
func TestRollback(t *testing.T) {
	store := newStubStoreForEvaluation()

	for i := 0; i < 10; i++ {
		store.addTask("task-"+string(rune(i)), "succeeded", true)
	}

	config := DefaultEvaluationConfig()
	config.HoldoutFraction = 0.2

	optimizer := NewHeuristicOptimizer()
	loop := NewEvaluationLoop(store, optimizer, config)

	initialVersion := loop.CurrentVersion()

	// Manually add a few versions
	loop.acceptConfig(SupervisorConfig{MaxAttemptsPerApproach: 4}, EvaluationOutcome{Accepted: true})
	loop.acceptConfig(SupervisorConfig{MaxAttemptsPerApproach: 5}, EvaluationOutcome{Accepted: true})

	if loop.CurrentVersion() != 2 {
		t.Errorf("expected version 2, got %d", loop.CurrentVersion())
	}

	// Rollback to version 0
	err := loop.Rollback(0)
	if err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	if loop.CurrentVersion() != 3 { // rollback creates new version entry
		t.Errorf("expected version 3 after rollback, got %d", loop.CurrentVersion())
	}

	// Config should match version 0
	rolledBackConfig := loop.CurrentConfig()
	if rolledBackConfig.MaxAttemptsPerApproach != 3 {
		t.Errorf("rollback config should match version 0 (3), got %d", rolledBackConfig.MaxAttemptsPerApproach)
	}

	// Check rollback version metadata
	history := loop.VersionHistory()
	lastVersion := history[len(history)-1]
	if lastVersion.Source != "rollback" {
		t.Errorf("last version source should be 'rollback', got %s", lastVersion.Source)
	}
	if lastVersion.RollbackTarget == nil || *lastVersion.RollbackTarget != 0 {
		t.Errorf("rollback target should be 0")
	}
	_ = initialVersion
}

// TestInsufficientData tests behavior with insufficient task history.
func TestInsufficientData(t *testing.T) {
	store := newStubStoreForEvaluation()

	// Only add 3 tasks (less than MinimumSampleSize*2 = 10)
	for i := 0; i < 3; i++ {
		store.addTask("task-"+string(rune(i)), "succeeded", true)
	}

	config := DefaultEvaluationConfig()
	config.MinimumSampleSize = 5

	optimizer := NewHeuristicOptimizer()
	loop := NewEvaluationLoop(store, optimizer, config)

	proposedConfig := SupervisorConfig{MaxAttemptsPerApproach: 5}
	outcome, err := loop.RunEvaluation(context.Background(), proposedConfig)
	if err != nil {
		t.Fatalf("RunEvaluation failed: %v", err)
	}

	if outcome.Accepted {
		t.Error("should not accept with insufficient data")
	}
	if outcome.Reason != "insufficient task history for evaluation" {
		t.Errorf("expected 'insufficient task history' reason, got: %s", outcome.Reason)
	}
}

// TestOptimizerNoChange tests when optimizer proposes no change.
func TestOptimizerNoChange(t *testing.T) {
	store := newStubStoreForEvaluation()

	for i := 0; i < 10; i++ {
		store.addTask("task-"+string(rune(i)), "succeeded", true)
	}

	config := DefaultEvaluationConfig()
	optimizer := NewStubOptimizer() // Returns current config unchanged
	loop := NewEvaluationLoop(store, optimizer, config)

	outcome, err := loop.ProposeAndEvaluate(context.Background())
	if err != nil {
		t.Fatalf("ProposeAndEvaluate failed: %v", err)
	}

	if outcome.Accepted {
		t.Error("should not accept when optimizer proposes no change")
	}
	if outcome.Reason != "optimizer proposed no change" {
		t.Errorf("expected 'optimizer proposed no change' reason, got: %s", outcome.Reason)
	}
}

// TestEvaluationConfigDefaults tests default configuration values.
func TestEvaluationConfigDefaults(t *testing.T) {
	config := DefaultEvaluationConfig()

	if config.HoldoutFraction != 0.3 {
		t.Errorf("default HoldoutFraction should be 0.3, got %f", config.HoldoutFraction)
	}
	if config.MinimumSampleSize != 10 {
		t.Errorf("default MinimumSampleSize should be 10, got %d", config.MinimumSampleSize)
	}
	if config.SignificanceLevel != 0.05 {
		t.Errorf("default SignificanceLevel should be 0.05, got %f", config.SignificanceLevel)
	}
	if config.MinimumEffectSize != 0.05 {
		t.Errorf("default MinimumEffectSize should be 0.05, got %f", config.MinimumEffectSize)
	}
	if config.MaxConfigVersions != 50 {
		t.Errorf("default MaxConfigVersions should be 50, got %d", config.MaxConfigVersions)
	}
}

// TestSimulateConfigOnSet tests the simulation logic.
func TestSimulateConfigOnSet(t *testing.T) {
	store := newStubStoreForEvaluation()

	for i := 0; i < 10; i++ {
		store.addTask("task-"+string(rune(i)), "succeeded", true)
	}

	config := DefaultEvaluationConfig()
	optimizer := NewHeuristicOptimizer()
	loop := NewEvaluationLoop(store, optimizer, config)

	taskIDs := []string{"task-0", "task-1", "task-2"}

	// Test with increased attempts
	proposedConfig := SupervisorConfig{
		MaxAttemptsPerApproach: 6,
		MaxApproaches:          4,
	}

	simulated, err := loop.simulateConfigOnSet(context.Background(), proposedConfig, taskIDs, "training")
	if err != nil {
		t.Fatalf("simulateConfigOnSet failed: %v", err)
	}

	t.Logf("Base config: MaxAttempts=3, Proposed: MaxAttempts=6")
	t.Logf("Simulated SuccessRate: %.4f (base was 1.0)", simulated.SuccessRate)
	t.Logf("Simulated EscalationRate: %.4f", simulated.EscalationRate)
	t.Logf("Simulated AvgCycleTime: %.2f", simulated.AvgCycleTime)
}

// TestEvaluateConfigOnSet tests the evaluation logic on task sets.
func TestEvaluateConfigOnSet(t *testing.T) {
	store := newStubStoreForEvaluation()

	// Add mixed results
	store.addTask("task-1", "succeeded", true)
	store.addTask("task-2", "succeeded", true)
	store.addTask("task-3", "escalated", true)
	store.addTask("task-4", "failed", true)

	config := DefaultEvaluationConfig()
	optimizer := NewHeuristicOptimizer()
	loop := NewEvaluationLoop(store, optimizer, config)

	taskIDs := []string{"task-1", "task-2", "task-3", "task-4"}
	result, err := loop.evaluateConfigOnSet(context.Background(), loop.CurrentConfig(), taskIDs, "training")
	if err != nil {
		t.Fatalf("evaluateConfigOnSet failed: %v", err)
	}

	if result.TaskCount != 4 {
		t.Errorf("expected 4 tasks, got %d", result.TaskCount)
	}
	if result.SuccessRate != 0.5 {
		t.Errorf("expected success rate 0.5 (2/4), got %f", result.SuccessRate)
	}
	if result.EscalationRate != 0.25 {
		t.Errorf("expected escalation rate 0.25 (1/4), got %f", result.EscalationRate)
	}

	t.Logf("Result: SuccessRate=%.2f, EscalationRate=%.2f, AvgCycleTime=%.2f, Score=%.4f",
		result.SuccessRate, result.EscalationRate, result.AvgCycleTime, result.Score)
}
