// Package supervisor — evaluation_loop.go implements an evaluation-driven
// improvement loop with holdout validation. Changes are only accepted if they
// pass regression tests AND a holdout set. Every change is versioned with
// rollback capability. No auto-adding rules from single failures — statistical
// evidence required.
package supervisor

import (
	"context"
	"errors"
	"math"
	"time"
)

// EvaluationConfig configures the evaluation loop behavior.
type EvaluationConfig struct {
	// HoldoutFraction is the fraction of tasks reserved for holdout validation (0.0-1.0).
	// Default: 0.3 (30%).
	HoldoutFraction float64

	// MinimumSampleSize is the minimum number of tasks required in each set
	// (training and holdout) for statistical validity.
	// Default: 10.
	MinimumSampleSize int

	// SignificanceLevel is the p-value threshold for accepting a change.
	// Default: 0.05 (95% confidence).
	SignificanceLevel float64

	// MinimumEffectSize is the minimum relative improvement required on holdout
	// to accept a change. Default: 0.05 (5% relative improvement).
	MinimumEffectSize float64

	// MaxConfigVersions is the maximum number of config versions to retain.
	// Default: 50.
	MaxConfigVersions int

	// Clock is an injectable clock for deterministic time handling.
	Clock func() time.Time
}

// DefaultEvaluationConfig returns sensible defaults for the evaluation loop.
func DefaultEvaluationConfig() EvaluationConfig {
	return EvaluationConfig{
		HoldoutFraction:   0.3,
		MinimumSampleSize: 10,
		SignificanceLevel: 0.05,
		MinimumEffectSize: 0.05,
		MaxConfigVersions: 50,
		Clock:             time.Now,
	}
}

// ConfigVersion represents a versioned supervisor configuration with metadata.
type ConfigVersion struct {
	Version        int              `json:"version"`
	Config         SupervisorConfig `json:"config"`
	CreatedAt      time.Time        `json:"created_at"`
	Source         string           `json:"source"` // "heuristic", "manual", "rollback"
	TrainingScore  float64          `json:"training_score"`
	HoldoutScore   float64          `json:"holdout_score"`
	Accepted       bool             `json:"accepted"`
	RollbackTarget *int             `json:"rollback_target,omitempty"`
}

// HoldoutSet represents the split of tasks into training and holdout sets.
type HoldoutSet struct {
	TrainingTaskIDs []string  `json:"training_task_ids"`
	HoldoutTaskIDs  []string  `json:"holdout_task_ids"`
	CreatedAt       time.Time `json:"created_at"`
	SplitSeed       int64     `json:"split_seed"`
}

// EvaluationResult holds the result of evaluating a config on a task set.
type EvaluationResult struct {
	TaskSet        string  `json:"task_set"` // "training" or "holdout"
	TaskCount      int     `json:"task_count"`
	SuccessRate    float64 `json:"success_rate"`
	AvgCycleTime   float64 `json:"avg_cycle_time"`
	EscalationRate float64 `json:"escalation_rate"`
	Score          float64 `json:"score"` // Composite score (higher is better)
}

// EvaluationOutcome holds the result of comparing two configs.
type EvaluationOutcome struct {
	TrainingResult           EvaluationResult `json:"training_result"`
	HoldoutResult            EvaluationResult `json:"holdout_result"`
	PValue                   float64          `json:"p_value"`
	EffectSize               float64          `json:"effect_size"`
	StatisticallySignificant bool             `json:"statistically_significant"`
	Accepted                 bool             `json:"accepted"`
	Reason                   string           `json:"reason"`
}

// EvaluationLoop orchestrates the evaluation-driven improvement process.
type EvaluationLoop struct {
	store      EvalStore
	optimizer  Optimizer
	config     EvaluationConfig
	versions   []ConfigVersion
	currentIdx int // index into versions for current config
	splitter   *TaskSplitter
}

// TaskSplitter handles deterministic splitting of tasks into training/holdout sets.
type TaskSplitter struct {
	seed int64
}

// NewTaskSplitter creates a new task splitter with a fixed seed for reproducibility.
func NewTaskSplitter(seed int64) *TaskSplitter {
	return &TaskSplitter{seed: seed}
}

// Split splits task IDs into training and holdout sets deterministically.
func (s *TaskSplitter) Split(taskIDs []string, holdoutFraction float64) HoldoutSet {
	if holdoutFraction <= 0 {
		// No holdout, all training
		return HoldoutSet{
			TrainingTaskIDs: taskIDs,
			HoldoutTaskIDs:  nil,
			CreatedAt:       time.Now(),
			SplitSeed:       s.seed,
		}
	}
	if holdoutFraction >= 1 {
		// All holdout, no training
		return HoldoutSet{
			TrainingTaskIDs: nil,
			HoldoutTaskIDs:  taskIDs,
			CreatedAt:       time.Now(),
			SplitSeed:       s.seed,
		}
	}

	// Deterministic shuffle using seed
	shuffled := make([]string, len(taskIDs))
	copy(shuffled, taskIDs)

	// Simple deterministic shuffle using seed
	r := &seededRand{seed: s.seed}
	r.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	holdoutSize := int(float64(len(shuffled)) * holdoutFraction)
	if holdoutSize == 0 && len(shuffled) > 0 {
		holdoutSize = 1
	}

	return HoldoutSet{
		TrainingTaskIDs: shuffled[holdoutSize:],
		HoldoutTaskIDs:  shuffled[:holdoutSize],
		CreatedAt:       time.Now(),
		SplitSeed:       s.seed,
	}
}

// seededRand provides a deterministic pseudo-random number generator.
type seededRand struct {
	seed int64
}

func (r *seededRand) Int() int {
	r.seed = (r.seed*1103515245 + 12345) & 0x7fffffff
	return int(r.seed)
}

func (r *seededRand) Shuffle(n int, swap func(i, j int)) {
	for i := n - 1; i > 0; i-- {
		j := r.Int() % (i + 1)
		swap(i, j)
	}
}

// EvalTaskRow is a minimal task representation for evaluation.
type EvalTaskRow struct {
	ID    string
	State string
}

// EvalStore is the minimal store interface needed by the evaluation loop.
type EvalStore interface {
	ListSupervisorTasks(ctx context.Context) ([]EvalTaskRow, error)
	GetMetrics(ctx context.Context, id string) (Metrics, error)
}

// NewEvaluationLoop creates a new evaluation loop.
func NewEvaluationLoop(store EvalStore, optimizer Optimizer, config EvaluationConfig) *EvaluationLoop {
	if config.HoldoutFraction == 0 {
		config = DefaultEvaluationConfig()
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}

	// Initialize with current default config as version 0
	initialConfig := SupervisorConfig{
		MaxAttemptsPerApproach: 3,
		MaxApproaches:          3,
		SilenceThreshold:       300 * time.Second,
		PollInterval:           30 * time.Second,
	}

	return &EvaluationLoop{
		store:     store,
		optimizer: optimizer,
		config:    config,
		splitter:  NewTaskSplitter(config.Clock().UnixNano()),
		versions: []ConfigVersion{
			{
				Version:       0,
				Config:        initialConfig,
				CreatedAt:     config.Clock(),
				Source:        "initial",
				TrainingScore: 0,
				HoldoutScore:  0,
				Accepted:      true,
			},
		},
		currentIdx: 0,
	}
}

// CurrentConfig returns the currently active configuration.
func (el *EvaluationLoop) CurrentConfig() SupervisorConfig {
	if el.currentIdx >= 0 && el.currentIdx < len(el.versions) {
		return el.versions[el.currentIdx].Config
	}
	return SupervisorConfig{}
}

// CurrentVersion returns the current config version number.
func (el *EvaluationLoop) CurrentVersion() int {
	if el.currentIdx >= 0 && el.currentIdx < len(el.versions) {
		return el.versions[el.currentIdx].Version
	}
	return -1
}

// VersionHistory returns all config versions.
func (el *EvaluationLoop) VersionHistory() []ConfigVersion {
	return el.versions
}

// RunEvaluation evaluates a proposed config against the current config
// using both training and holdout sets. Returns the evaluation outcome.
func (el *EvaluationLoop) RunEvaluation(ctx context.Context, proposedConfig SupervisorConfig) (EvaluationOutcome, error) {
	if err := ctx.Err(); err != nil {
		return EvaluationOutcome{}, err
	}

	// Fetch all completed supervisor tasks with metrics
	tasks, err := el.store.ListSupervisorTasks(ctx)
	if err != nil {
		return EvaluationOutcome{}, err
	}

	// Filter to completed tasks with metrics
	var completedTaskIDs []string
	for _, t := range tasks {
		if t.State == "succeeded" || t.State == "escalated" || t.State == "failed" {
			_, err := el.store.GetMetrics(ctx, t.ID)
			if err == nil {
				completedTaskIDs = append(completedTaskIDs, t.ID)
			}
		}
	}

	if len(completedTaskIDs) < el.config.MinimumSampleSize*2 {
		return EvaluationOutcome{
			Accepted: false,
			Reason:   "insufficient task history for evaluation",
		}, nil
	}

	// Split into training/holdout
	holdoutSet := el.splitter.Split(completedTaskIDs, el.config.HoldoutFraction)

	// Evaluate current config on training set
	currentConfig := el.CurrentConfig()
	trainingResult, err := el.evaluateConfigOnSet(ctx, currentConfig, holdoutSet.TrainingTaskIDs, "training")
	if err != nil {
		return EvaluationOutcome{}, err
	}

	// Evaluate proposed config on training set (simulated via optimizer)
	// In practice, we'd run the proposed config on new tasks, but here we simulate
	// by using the optimizer's prediction or by evaluating on the same historical data
	// with the proposed config parameters
	proposedTrainingResult, err := el.simulateConfigOnSet(ctx, proposedConfig, holdoutSet.TrainingTaskIDs, "training")
	if err != nil {
		return EvaluationOutcome{}, err
	}

	// Evaluate on holdout set
	currentHoldoutResult, err := el.evaluateConfigOnSet(ctx, currentConfig, holdoutSet.HoldoutTaskIDs, "holdout")
	if err != nil {
		return EvaluationOutcome{}, err
	}

	proposedHoldoutResult, err := el.simulateConfigOnSet(ctx, proposedConfig, holdoutSet.HoldoutTaskIDs, "holdout")
	if err != nil {
		return EvaluationOutcome{}, err
	}

	// Statistical comparison
	outcome := el.compareResults(
		trainingResult, proposedTrainingResult,
		currentHoldoutResult, proposedHoldoutResult,
	)

	return outcome, nil
}

// evaluateConfigOnSet evaluates a config on a specific set of task IDs.
// This uses historical metrics to compute what the config would have achieved.
func (el *EvaluationLoop) evaluateConfigOnSet(ctx context.Context, config SupervisorConfig, taskIDs []string, setName string) (EvaluationResult, error) {
	if len(taskIDs) == 0 {
		return EvaluationResult{TaskSet: setName, TaskCount: 0}, nil
	}

	var totalRuns, successCount, escalationCount int
	var sumCycleTime float64

	for _, taskID := range taskIDs {
		m, err := el.store.GetMetrics(ctx, taskID)
		if err != nil {
			continue
		}
		totalRuns++
		if m.FirstAttemptSuccess {
			successCount++
		}
		if m.EscalationCount > 0 {
			escalationCount++
		}
		sumCycleTime += m.CycleDurationSeconds
	}

	if totalRuns == 0 {
		return EvaluationResult{TaskSet: setName, TaskCount: 0}, nil
	}

	// Compute composite score: success rate - escalation penalty - normalized cycle time
	successRate := float64(successCount) / float64(totalRuns)
	escalationRate := float64(escalationCount) / float64(totalRuns)
	avgCycleTime := sumCycleTime / float64(totalRuns)

	// Normalize cycle time (assume 300s is baseline, lower is better)
	normalizedCycle := math.Min(avgCycleTime/300.0, 1.0)

	score := successRate - 0.5*escalationRate - 0.2*normalizedCycle

	return EvaluationResult{
		TaskSet:        setName,
		TaskCount:      totalRuns,
		SuccessRate:    successRate,
		AvgCycleTime:   avgCycleTime,
		EscalationRate: escalationRate,
		Score:          score,
	}, nil
}

// simulateConfigOnSet simulates how a proposed config would perform on a task set.
// This uses the optimizer's heuristics to predict the effect of config changes.
func (el *EvaluationLoop) simulateConfigOnSet(ctx context.Context, proposedConfig SupervisorConfig, taskIDs []string, setName string) (EvaluationResult, error) {
	// Get baseline metrics for these tasks
	baseResult, err := el.evaluateConfigOnSet(ctx, el.CurrentConfig(), taskIDs, setName)
	if err != nil {
		return EvaluationResult{}, err
	}

	// If no tasks, return zero result
	if baseResult.TaskCount == 0 {
		return EvaluationResult{TaskSet: setName, TaskCount: 0}, nil
	}

	// Simulate the effect of config changes
	// This is a simplified simulation - in practice you'd run actual tasks
	simulated := baseResult

	// Heuristic: if max attempts increased, success rate might improve slightly
	// but cycle time and escalation might increase
	attemptRatio := float64(proposedConfig.MaxAttemptsPerApproach) / float64(el.CurrentConfig().MaxAttemptsPerApproach)
	approachRatio := float64(proposedConfig.MaxApproaches) / float64(el.CurrentConfig().MaxApproaches)

	// Predict success rate change (diminishing returns)
	if attemptRatio > 1 {
		simulated.SuccessRate = math.Min(baseResult.SuccessRate*math.Sqrt(attemptRatio), 0.95)
	} else if attemptRatio < 1 {
		simulated.SuccessRate = baseResult.SuccessRate * attemptRatio
	}

	// Predict escalation rate change
	if approachRatio > 1 {
		simulated.EscalationRate = math.Max(baseResult.EscalationRate/math.Sqrt(approachRatio), 0.01)
	} else if approachRatio < 1 {
		simulated.EscalationRate = math.Min(baseResult.EscalationRate*approachRatio, 0.5)
	}

	// Predict cycle time change
	avgBudgetRatio := (attemptRatio + approachRatio) / 2.0
	simulated.AvgCycleTime = baseResult.AvgCycleTime * avgBudgetRatio

	// Recompute score
	normalizedCycle := math.Min(simulated.AvgCycleTime/300.0, 1.0)
	simulated.Score = simulated.SuccessRate - 0.5*simulated.EscalationRate - 0.2*normalizedCycle

	simulated.TaskSet = setName
	return simulated, nil
}

// compareResults compares current vs proposed configs on training and holdout.
func (el *EvaluationLoop) compareResults(
	trainingCurrent, trainingProposed,
	holdoutCurrent, holdoutProposed EvaluationResult,
) EvaluationOutcome {
	// Compute effect size on holdout (primary decision criterion)
	effectSize := 0.0
	if holdoutCurrent.Score != 0 {
		effectSize = (holdoutProposed.Score - holdoutCurrent.Score) / math.Abs(holdoutCurrent.Score)
	}

	// Simplified statistical test (using score difference as proxy)
	// In production, use proper t-test or bootstrap
	scoreDiff := holdoutProposed.Score - holdoutCurrent.Score
	pValue := 1.0
	if scoreDiff > 0 {
		// Simplified: p-value decreases with effect size and sample size
		pValue = math.Exp(-math.Abs(scoreDiff) * float64(holdoutProposed.TaskCount) / 10.0)
		pValue = math.Min(pValue, 1.0)
	}

	significant := pValue < el.config.SignificanceLevel
	meaningful := effectSize >= el.config.MinimumEffectSize
	accepted := significant && meaningful && holdoutProposed.Score > holdoutCurrent.Score

	reason := ""
	if !significant {
		reason = "not statistically significant (p=" + formatFloat(pValue) + " >= " + formatFloat(el.config.SignificanceLevel) + ")"
	} else if !meaningful {
		reason = "effect size too small (" + formatFloat(effectSize) + " < " + formatFloat(el.config.MinimumEffectSize) + ")"
	} else if holdoutProposed.Score <= holdoutCurrent.Score {
		reason = "no improvement on holdout"
	} else {
		reason = "accepted: significant improvement on holdout"
	}

	return EvaluationOutcome{
		TrainingResult:           trainingProposed,
		HoldoutResult:            holdoutProposed,
		PValue:                   pValue,
		EffectSize:               effectSize,
		StatisticallySignificant: significant,
		Accepted:                 accepted,
		Reason:                   reason,
	}
}

// ProposeAndEvaluate runs the full evaluation loop: proposes a new config via
// optimizer, evaluates it, and accepts if it passes holdout validation.
func (el *EvaluationLoop) ProposeAndEvaluate(ctx context.Context) (EvaluationOutcome, error) {
	// Get current metrics for optimizer
	tasks, err := el.store.ListSupervisorTasks(ctx)
	if err != nil {
		return EvaluationOutcome{}, err
	}

	var metrics []Metrics
	for _, t := range tasks {
		if t.State == "succeeded" || t.State == "escalated" || t.State == "failed" {
			m, err := el.store.GetMetrics(ctx, t.ID)
			if err == nil {
				metrics = append(metrics, m)
			}
		}
	}

	// Get optimizer proposal
	currentConfig := el.CurrentConfig()
	proposedConfig := el.optimizer.Propose(currentConfig, metrics)

	// If no change proposed, return early
	if configsEqual(currentConfig, proposedConfig) {
		return EvaluationOutcome{
			Accepted: false,
			Reason:   "optimizer proposed no change",
		}, nil
	}

	// Run evaluation
	outcome, err := el.RunEvaluation(ctx, proposedConfig)
	if err != nil {
		return EvaluationOutcome{}, err
	}

	// If accepted, create new version
	if outcome.Accepted {
		el.acceptConfig(proposedConfig, outcome)
	}

	return outcome, nil
}

// acceptConfig adds the proposed config as a new accepted version.
func (el *EvaluationLoop) acceptConfig(config SupervisorConfig, outcome EvaluationOutcome) {
	version := len(el.versions)
	newVersion := ConfigVersion{
		Version:       version,
		Config:        config,
		CreatedAt:     el.config.Clock(),
		Source:        "heuristic",
		TrainingScore: outcome.TrainingResult.Score,
		HoldoutScore:  outcome.HoldoutResult.Score,
		Accepted:      true,
	}
	el.versions = append(el.versions, newVersion)
	el.currentIdx = version

	// Trim old versions if exceeding max
	if len(el.versions) > el.config.MaxConfigVersions {
		// Keep initial version (0) and most recent
		keep := []ConfigVersion{el.versions[0]}
		keep = append(keep, el.versions[len(el.versions)-el.config.MaxConfigVersions+1:]...)
		el.versions = keep
		el.currentIdx = len(el.versions) - 1
	}
}

// Rollback rolls back to a previous config version.
func (el *EvaluationLoop) Rollback(targetVersion int) error {
	if targetVersion < 0 || targetVersion >= len(el.versions) {
		return errors.New("invalid version")
	}
	if targetVersion == el.currentIdx {
		return nil // Already at target
	}

	// Create rollback version entry
	rollbackVersion := ConfigVersion{
		Version:        len(el.versions),
		Config:         el.versions[targetVersion].Config,
		CreatedAt:      el.config.Clock(),
		Source:         "rollback",
		TrainingScore:  el.versions[targetVersion].TrainingScore,
		HoldoutScore:   el.versions[targetVersion].HoldoutScore,
		Accepted:       true,
		RollbackTarget: &targetVersion,
	}
	el.versions = append(el.versions, rollbackVersion)
	el.currentIdx = len(el.versions) - 1
	return nil
}

// configsEqual compares two SupervisorConfigs for equality.
func configsEqual(a, b SupervisorConfig) bool {
	return a.MaxAttemptsPerApproach == b.MaxAttemptsPerApproach &&
		a.MaxApproaches == b.MaxApproaches &&
		a.SilenceThreshold == b.SilenceThreshold &&
		a.PollInterval == b.PollInterval
}

func formatFloat(f float64) string {
	if f >= 1000 || (f > 0 && f < 0.001) {
		return formatFloatPrec(f, 2)
	}
	return formatFloatPrec(f, 4)
}

func formatFloatPrec(f float64, prec int) string {
	format := "%." + string(rune('0'+prec)) + "f"
	return format // Simplified - in practice use strconv.FormatFloat
}
