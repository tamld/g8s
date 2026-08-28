// Package supervisor — supervisor.go is the main loop. It composes planner +
// enforcer + reviewer + rca + escalator + persistence + metrics behind one
// Run(ctx, RunRequest) (RunResult, error) entry point.
package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync/atomic"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/orchestrator"
)

// RunOutcome is the high-level result of a Run call. The supervisor always
// returns a RunResult; Outcome decides whether the loop succeeded, ran out
// of attempts, or paused for human input.
type RunOutcome int

const (
	// RunSucceeded: at least one attempt produced VerdictPass.
	RunSucceeded RunOutcome = iota
	// RunFailed: loop paused for human input (low RCA confidence).
	RunFailed
	// RunEscalated: all approaches + attempts exhausted, escalation emitted.
	RunEscalated
)

// String renders a RunOutcome for logs.
func (o RunOutcome) String() string {
	switch o {
	case RunSucceeded:
		return "SUCCEEDED"
	case RunFailed:
		return "FAILED"
	case RunEscalated:
		return "ESCALATED"
	default:
		return "UNKNOWN"
	}
}

// RunRequest is the brain-supplied input to Run. It is the only surface the
// brain sees; everything else is supervisor-internal.
type RunRequest struct {
	TaskDescription string
	AllowedFiles    []string
	Role            string
	Permission      string
	Model           string
	AddDirs         []string
	EnvelopeHints   map[string]bool
	SelfTestMode    bool
	ParentTaskID    *string
	Timeout         time.Duration
}

// RunResult is what Run returns. Escalation is non-nil only when Escalated=true.
type RunResult struct {
	SupervisorTaskID string
	Escalated        bool
	Verdict          string
	AttemptCount     int
	ApproachCount    int
	Escalation       *Escalation
	Outcome          RunOutcome
}

// SupervisorConfig caps the run-loop. Defaults: 3 attempts per approach,
// 3 approaches, self-test off.
type SupervisorConfig struct {
	MaxAttemptsPerApproach int
	MaxApproaches          int
	SelfTestMode           bool
}

// defaultSupervisorConfig returns the safe defaults called out in the spec.
func defaultSupervisorConfig() SupervisorConfig {
	return SupervisorConfig{
		MaxAttemptsPerApproach: 3,
		MaxApproaches:          3,
		SelfTestMode:           false,
	}
}

// WorktreePool is the subset of orchestrator.Pool the supervisor depends on.
// A nil pool triggers the self-test bypass in Run.
type WorktreePool interface {
	Acquire(ctx context.Context, taskID string) (orchestrator.Worktree, error)
	Release(ctx context.Context, wt orchestrator.Worktree, keep bool) error
}

// StubWorktreePool is a no-op pool for tests; Acquire returns an empty
// Worktree so supervisor logic can still key worktrees by task id.
type StubWorktreePool struct{}

// Acquire returns an empty Worktree keyed by taskID. No git operations.
func (StubWorktreePool) Acquire(_ context.Context, taskID string) (orchestrator.Worktree, error) {
	return orchestrator.Worktree{ID: "stub-" + taskID, Path: "", Branch: "stub/" + taskID, BaseSHA: ""}, nil
}

// Release is a no-op; the stub pool does not own any filesystem state.
func (StubWorktreePool) Release(_ context.Context, _ orchestrator.Worktree, _ bool) error { return nil }

// AttemptRecord is one immutable row in the run history. Persisted as a
// SupervisorDecision payload so the audit trail survives a process restart.
type AttemptRecord struct {
	ApproachIdx    int
	AttemptIdx     int
	StartedAt      time.Time
	FinishedAt     time.Time
	Receipt        orchestrator.Receipt
	ReviewVerdict  Verdict
	ReviewReason   string
}

// Supervisor orchestrates the fix loop. All dependencies are fields so tests
// can swap them. SelfTestMode bypasses orchestrator.FanOut and calls the
// Worker directly with an empty Worktree, so the loop is unit-testable
// without a git repo or a real worker binary.
type Supervisor struct {
	Store         Persistence
	Worker        orchestrator.Worker
	Pool          WorktreePool
	Envelope      TaskEnvelope
	Config        SupervisorConfig
	Clock         func() time.Time
	Logger        *log.Logger
	RCAFn         RCA
	Reviewer      Reviewer
	SelfTestMode  bool
	Enforcer      *Enforcer
	MetricsStore  MetricsStore
	AgyClock      func() time.Time
}

// NewSelfTestSupervisor returns a Supervisor wired with stub defaults. Tests
// pass in a Worker (usually a synthetic StubWorker) and a Store (usually a
// StubPersistence).
func NewSelfTestSupervisor(store Persistence, worker orchestrator.Worker, reviewer Reviewer) *Supervisor {
	cfg := defaultSupervisorConfig()
	cfg.SelfTestMode = true
	return &Supervisor{
		Store:        store,
		Worker:       worker,
		Pool:         nil, // self-test bypasses pool
		Envelope:     SelectEnvelope(nil),
		Config:       cfg,
		Clock:        time.Now,
		Logger:       log.New(io.Discard, "", 0),
		RCAFn:        StubRCA,
		Reviewer:     reviewer,
		SelfTestMode: true,
		Enforcer:     NewEnforcer(),
		MetricsStore: NewSQLMetricsStore(time.Now),
	}
}

// Run is the canonical fix-loop entry point. It always returns a RunResult;
// an error is reserved for infrastructure failure (DB write, spawn-after-budget).
func (s *Supervisor) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	if err := ctx.Err(); err != nil {
		return RunResult{}, err
	}
	s.initDefaults()

	if req.SelfTestMode {
		s.SelfTestMode = true
	}
	if s.SelfTestMode {
		s.Config.SelfTestMode = true
	}

	if s.Enforcer != nil {
		if err := s.Enforcer.Validate(s.Envelope, req); err != nil {
			return RunResult{}, err
		}
	}

	supTaskID := newSupervisorTaskID()
	if err := s.persistRunning(ctx, supTaskID, req); err != nil {
		return RunResult{}, fmt.Errorf("supervisor: persist running task: %w", err)
	}

	startWall := s.Clock()

	attempts := make([]AttemptRecord, 0, s.Config.MaxAttemptsPerApproach*s.Config.MaxApproaches)
	confidences := make([]float64, 0, s.Config.MaxApproaches)
	var lastReceipt *orchestrator.Receipt

	maxAttempts := s.Config.MaxAttemptsPerApproach
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	maxApproaches := s.Config.MaxApproaches
	if maxApproaches <= 0 {
		maxApproaches = 3
	}

outcomeLoop:
	for approachIdx := 0; approachIdx < maxApproaches; approachIdx++ {
		for attemptIdx := 0; attemptIdx < maxAttempts; attemptIdx++ {
			record, err := s.runOneAttempt(ctx, req, approachIdx, attemptIdx, supTaskID)
			if err != nil {
				return RunResult{}, fmt.Errorf("supervisor: attempt infra failure: %w", err)
			}
			attempts = append(attempts, record)
			lastReceipt = &record.Receipt

			switch record.ReviewVerdict {
			case VerdictPass:
				res := s.buildSuccess(ctx, supTaskID, attempts, startWall)
				return res, nil
			case VerdictFail:
				// Failures still consume the attempt budget — RCA may attribute
				// the scope violation to a transient reviewer bug, so we let
				// the loop exhaust naturally and only escalate after the
				// final approach runs out.
				if attemptIdx == maxAttempts-1 {
					break
				}
				continue
			case VerdictRevise:
				// continue retrying within this approach
				if attemptIdx == maxAttempts-1 {
					break // fall out of attempt loop → approach shift
				}
				continue
			}
		}

		// Approach exhausted — run RCA to decide shift vs escalate.
		rcaRec, err := s.RCAFn(ctx, attempts)
		if err != nil {
			return RunResult{}, fmt.Errorf("supervisor: rca: %w", err)
		}
		confidences = append(confidences, rcaRec.Confidence)

		if rcaRec.Confidence < 0.6 {
			// Low confidence: pause (NEEDS_INFO) instead of escalating.
			_ = s.appendDecision(ctx, supTaskID, "needs_info", mustJSON(map[string]any{
				"reason":     "rca_confidence_below_threshold",
				"confidence": rcaRec.Confidence,
				"symptom":    rcaRec.Symptom,
			}))
			if err := s.persistState(ctx, supTaskID, "needs_info"); err != nil {
				return RunResult{}, fmt.Errorf("supervisor: persist needs_info: %w", err)
			}
			break outcomeLoop
		}

		if approachIdx == maxApproaches-1 {
			esc := s.escalateNow(ctx, supTaskID, req, attempts, lastReceipt, "approach_budget_exhausted")
			res := RunResult{
				SupervisorTaskID: supTaskID,
				Escalated:        true,
				Verdict:          VerdictRevise.String(),
				AttemptCount:     len(attempts),
				ApproachCount:    approachIdx + 1,
				Escalation:       &esc,
				Outcome:          RunEscalated,
			}
			return res, nil
		}

		_ = s.appendDecision(ctx, supTaskID, "approach_shift", mustJSON(map[string]any{
			"from_approach": approachIdx,
			"to_approach":   approachIdx + 1,
			"rca":           rcaRec,
		}))
	}

	approachCount := 0
	if len(attempts) > 0 {
		approachCount = attempts[len(attempts)-1].ApproachIdx + 1
	}
	return RunResult{
		SupervisorTaskID: supTaskID,
		Escalated:        false,
		Verdict:          "PAUSED",
		AttemptCount:     len(attempts),
		ApproachCount:    approachCount,
		Escalation:       nil,
		Outcome:          RunFailed,
	}, nil
}

// runOneAttempt executes a single attempt against the worker and returns the
// persisted AttemptRecord. Self-test mode calls Worker.Spawn directly; real
// mode would route through orchestrator.FanOut (TODO when Persistence wires
// the real pool).
func (s *Supervisor) runOneAttempt(
	ctx context.Context,
	req RunRequest,
	approachIdx int,
	attemptIdx int,
	supTaskID string,
) (AttemptRecord, error) {
	startedAt := s.Clock()
	task := orchestrator.Task{
		ID:         fmt.Sprintf("%s-a%d-att%d", supTaskID, approachIdx, attemptIdx),
		Prompt:     req.TaskDescription,
		Model:      req.Model,
		Role:       req.Role,
		Permission: req.Permission,
		AllowedFiles: req.AllowedFiles,
		Iter:       attemptIdx,
	}
	if req.Timeout > 0 {
		task.Timeout = req.Timeout
	}

	_ = s.appendDecision(ctx, supTaskID, "attempt_started", mustJSON(map[string]any{
		"approach_idx": approachIdx,
		"attempt_idx":  attemptIdx,
		"task_id":      task.ID,
		"started_at":   startedAt,
	}))

	receipt, err := s.dispatch(ctx, task)
	finishedAt := s.Clock()

	record := AttemptRecord{
		ApproachIdx: approachIdx,
		AttemptIdx:  attemptIdx,
		StartedAt:   startedAt,
		FinishedAt:  finishedAt,
		Receipt:     receipt,
	}
	if err != nil {
		record.ReviewVerdict = VerdictRevise
		record.ReviewReason = "spawn failed: " + err.Error()
		_ = s.appendDecision(ctx, supTaskID, "review_verdict", mustJSON(map[string]any{
			"approach_idx":  approachIdx,
			"attempt_idx":   attemptIdx,
			"verdict":       record.ReviewVerdict.String(),
			"reason":        record.ReviewReason,
		}))
		return record, nil
	}

	outcome := ReviewReceipt(receipt, s.Envelope, s.Reviewer)
	record.ReviewVerdict = outcome.Verdict
	record.ReviewReason = outcome.Reason

	_ = s.appendDecision(ctx, supTaskID, "review_verdict", mustJSON(map[string]any{
		"approach_idx": approachIdx,
		"attempt_idx":  attemptIdx,
		"verdict":      outcome.Verdict.String(),
		"reason":       outcome.Reason,
		"validated":    outcome.Validated,
	}))

	return record, nil
}

// dispatch routes to either self-test Worker.Spawn or a stub FanOut call.
// Self-test mode is the only one we exercise today; the FanOut path is here
// to keep the seam alive for WU3.
func (s *Supervisor) dispatch(ctx context.Context, task orchestrator.Task) (orchestrator.Receipt, error) {
	if s.Worker == nil {
		return orchestrator.Receipt{}, errors.New("supervisor: Worker is nil")
	}
	handle, err := s.Worker.Spawn(ctx, task)
	if err != nil {
		return orchestrator.Receipt{}, err
	}
	receipt, err := handle.Wait(ctx)
	if err != nil && receipt.TaskID == "" {
		// Synthesize a minimal receipt so reviewer can still grade.
		receipt.TaskID = task.ID
	}
	return receipt, nil
}

// buildSuccess materializes the success RunResult and persists the terminal
// state + metrics row.
func (s *Supervisor) buildSuccess(ctx context.Context, supTaskID string, attempts []AttemptRecord, startWall time.Time) RunResult {
	_ = s.persistState(ctx, supTaskID, "succeeded")
	s.persistMetrics(ctx, supTaskID, attempts, startWall, false)
	successIdx := len(attempts) - 1
	return RunResult{
		SupervisorTaskID: supTaskID,
		Escalated:        false,
		Verdict:          VerdictPass.String(),
		AttemptCount:     len(attempts),
		ApproachCount:    attempts[successIdx].ApproachIdx + 1,
		Escalation:       nil,
		Outcome:          RunSucceeded,
	}
}

// escalateNow persists the escalated state, builds the Escalation record, and
// returns it. Caller wraps it into a RunResult.
func (s *Supervisor) escalateNow(
	ctx context.Context,
	supTaskID string,
	req RunRequest,
	attempts []AttemptRecord,
	lastReceipt *orchestrator.Receipt,
	trigger string,
) Escalation {
	rcaRec, _ := s.RCAFn(ctx, attempts)
	esc := BuildEscalation(supTaskID, supTaskID, trigger, attempts, lastReceipt, rcaRec)
	_ = s.appendDecision(ctx, supTaskID, "escalated", mustJSON(map[string]any{
		"trigger":    trigger,
		"rca":        rcaRec,
		"escalation": esc,
		"request":    req,
	}))
	_ = s.persistState(ctx, supTaskID, "escalated")
	return esc
}

// persistRunning writes the initial supervisor_tasks row.
func (s *Supervisor) persistRunning(ctx context.Context, id string, req RunRequest) error {
	envJSON, _ := json.Marshal(s.Envelope)
	st := controlplane.SupervisorTaskRow{
		ID:           id,
		State:        "running",
		EnvelopeJSON: string(envJSON),
		CreatedAt:    s.Clock(),
		UpdatedAt:    s.Clock(),
		ParentTaskID: req.ParentTaskID,
	}
	if err := s.Store.CreateSupervisorTask(ctx, st); err != nil {
		return err
	}
	return s.appendDecision(ctx, id, "run_started", mustJSON(map[string]any{
		"request": req,
		"config":  s.Config,
	}))
}

// persistState transitions the supervisor_tasks row to a terminal/intermediate
// state.
func (s *Supervisor) persistState(ctx context.Context, id string, state string) error {
	st, err := s.Store.GetSupervisorTask(ctx, id)
	if err != nil {
		return err
	}
	st.State = state
	return s.Store.UpdateSupervisorTask(ctx, st)
}

// appendDecision writes a single audit-trail row.
func (s *Supervisor) appendDecision(ctx context.Context, taskID, kind, payload string) error {
	return s.Store.AppendDecision(ctx, controlplane.SupervisorDecisionRow{
		TaskID:      taskID,
		Kind:        kind,
		PayloadJSON: payload,
		CreatedAt:   s.Clock(),
	})
}

// persistMetrics writes the post-run telemetry bundle.
func (s *Supervisor) persistMetrics(ctx context.Context, supTaskID string, attempts []AttemptRecord, startWall time.Time, escalated bool) {
	if s.MetricsStore == nil {
		return
	}
	dur := s.Clock().Sub(startWall).Seconds()
	envelopeScore := s.Envelope.Score
	attemptsToSuccess := 0
	approachesToSuccess := 0
	if !escalated && len(attempts) > 0 {
		for i, a := range attempts {
			if a.ReviewVerdict == VerdictPass {
				attemptsToSuccess = i + 1
				approachesToSuccess = a.ApproachIdx + 1
				break
			}
		}
	}
	m := Metrics{
		EnvelopeScore:        envelopeScore,
		FirstAttemptSuccess:  len(attempts) >= 1 && attempts[0].ReviewVerdict == VerdictPass,
		AttemptsToSuccess:    attemptsToSuccess,
		ApproachesToSuccess:  approachesToSuccess,
		RCAConfidenceAvg:     avgConfidence(attempts),
		CycleDurationSeconds: dur,
		EscalationCount:      boolToInt(escalated),
	}
	_ = s.MetricsStore.SaveMetrics(ctx, supTaskID, m)
}

// initDefaults fills in optional fields so callers can construct a half-baked
// Supervisor struct (typical for tests).
func (s *Supervisor) initDefaults() {
	if s.Clock == nil {
		s.Clock = time.Now
	}
	if s.Logger == nil {
		s.Logger = log.New(io.Discard, "", 0)
	}
	if s.RCAFn == nil {
		s.RCAFn = StubRCA
	}
	if s.Reviewer == nil {
		s.Reviewer = NewStubReviewer()
	}
	if s.Enforcer == nil {
		s.Enforcer = NewEnforcer()
	}
	if s.MetricsStore == nil {
		s.MetricsStore = NewSQLMetricsStore(s.Clock)
	}
	if s.Config.MaxAttemptsPerApproach == 0 {
		s.Config.MaxAttemptsPerApproach = 3
	}
	if s.Config.MaxApproaches == 0 {
		s.Config.MaxApproaches = 3
	}
	if s.Envelope.SelectedFields == nil {
		s.Envelope = SelectEnvelope(nil)
	}
}

// newSupervisorTaskID is a deterministic-friendly id generator. Uses the
// injected clock's nanosecond + an atomic counter so concurrent calls do
// not collide.
var supTaskCounter atomic.Int64

// supTaskClockFunc is package-level so newSupervisorTaskID can stay
// parameter-free for callers. Tests reset it via init().
var supTaskClockFunc = time.Now

func newSupervisorTaskID() string {
	n := supTaskCounter.Add(1)
	return fmt.Sprintf("sup-%d-%d", supTaskClockFunc().UnixNano(), n)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func avgConfidence(attempts []AttemptRecord) float64 {
	if len(attempts) == 0 {
		return 0
	}
	total := 0.0
	for _, a := range attempts {
		// ponytail: confidence per attempt is 1.0 for pass, 0.0 otherwise;
		// RCA-time confidence is captured at escalateNow, not per attempt.
		if a.ReviewVerdict == VerdictPass {
			total += 1.0
		}
	}
	return total / float64(len(attempts))
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
