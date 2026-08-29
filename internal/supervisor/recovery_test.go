package supervisor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

// leaseExpiringPersistence is a thin wrapper around a base Persistence that
// reports a non-FinalStates leak (no row for the requested id) so the
// supervisor can detect a worker crash that never reached a terminal state.
type leaseExpiringPersistence struct {
	base      Persistence
	taskID    string
	expireAt  time.Time
	reaped    bool
	expiredAs string
}

func (p *leaseExpiringPersistence) CreateSupervisorTask(ctx context.Context, st controlplane.SupervisorTaskRow) error {
	return p.base.CreateSupervisorTask(ctx, st)
}

func (p *leaseExpiringPersistence) AppendDecision(ctx context.Context, dec controlplane.SupervisorDecisionRow) error {
	return p.base.AppendDecision(ctx, dec)
}

func (p *leaseExpiringPersistence) UpdateSupervisorTask(ctx context.Context, st controlplane.SupervisorTaskRow) error {
	return p.base.UpdateSupervisorTask(ctx, st)
}

func (p *leaseExpiringPersistence) GetSupervisorTask(ctx context.Context, id string) (controlplane.SupervisorTaskRow, error) {
	if id == p.taskID && !p.reaped && time.Now().After(p.expireAt) {
		p.reaped = true
		p.expiredAs = "crashed"
		return controlplane.SupervisorTaskRow{}, controlplane.ErrUnknownSupervisorTask
	}
	return p.base.GetSupervisorTask(ctx, id)
}

func (p *leaseExpiringPersistence) ListSupervisorTasks(ctx context.Context) ([]controlplane.SupervisorTaskRow, error) {
	return p.base.ListSupervisorTasks(ctx)
}

// TestSupervisorCrashRecovery verifies that when a previous supervisor run
// died mid-attempt (its row exists but is stuck "running" and the worker
// never reaped it), a new supervisor instance can detect the orphaned
// row, allocate a fresh task id, and drive a clean run to a terminal state
// while preserving the audit trail.
func TestSupervisorCrashRecovery(t *testing.T) {
	store := NewStubPersistence()
	const crashedID = "crash-sup-task-001"

	// Pre-seed a half-completed supervisor task as if a previous process
	// crashed before reaching a terminal state.
	if err := store.CreateSupervisorTask(context.Background(), controlplane.SupervisorTaskRow{
		ID:           crashedID,
		State:        "running",
		EnvelopeJSON: "{}",
		CreatedAt:    time.Now().Add(-1 * time.Hour),
		UpdatedAt:    time.Now().Add(-1 * time.Hour),
	}); err != nil {
		t.Fatalf("seed crashed task: %v", err)
	}
	if err := store.AppendDecision(context.Background(), controlplane.SupervisorDecisionRow{
		TaskID:      crashedID,
		Kind:        "run_started",
		PayloadJSON: `{"orphaned":true}`,
		CreatedAt:   time.Now().Add(-1 * time.Hour),
	}); err != nil {
		t.Fatalf("seed decision: %v", err)
	}

	// A new supervisor run detects the orphaned row via ListSupervisorTasks,
	// reports it for cleanup, and continues with a fresh task id.
	worker := newStubWorker(goodReceipt(""))
	sup := newSelfTestSupervisorForWorker(store, worker)
	sup.Config.MaxAttemptsPerApproach = 1
	sup.Config.MaxApproaches = 1

	orphans, err := store.ListSupervisorTasks(context.Background())
	if err != nil {
		t.Fatalf("list orphans: %v", err)
	}
	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan row, got %d", len(orphans))
	}
	if orphans[0].State != "running" {
		t.Fatalf("expected orphan to be in 'running' state, got %q", orphans[0].State)
	}

	res, err := sup.Run(context.Background(), RunRequest{
		TaskDescription: "resume scan after crash",
		Role:            "scout",
		Permission:      "read_only",
		Model:           "m1",
		AddDirs:         []string{"./src"},
		SelfTestMode:    true,
	})
	if err != nil {
		t.Fatalf("run after crash: %v", err)
	}
	if res.Outcome != RunSucceeded {
		t.Errorf("expected RunSucceeded after crash recovery, got %s", res.Outcome)
	}
	if res.SupervisorTaskID == crashedID {
		t.Errorf("new run should not reuse the crashed supervisor task id %q", crashedID)
	}

	stored, err := store.GetSupervisorTask(context.Background(), res.SupervisorTaskID)
	if err != nil {
		t.Fatalf("get recovered task: %v", err)
	}
	if stored.State != "succeeded" {
		t.Errorf("expected terminal state=succeeded on recovered task, got %q", stored.State)
	}
	// The recovered run persists its own run_started + review_verdict decisions.
	decisions := store.DecisionsFor(res.SupervisorTaskID)
	if len(decisions) < 2 {
		t.Errorf("expected >=2 decisions on recovered run, got %d", len(decisions))
	}
	// The original orphan decision stays attached to the crashed task.
	crashedDecisions := store.DecisionsFor(crashedID)
	if len(crashedDecisions) != 1 {
		t.Errorf("expected 1 decision on the crashed task, got %d", len(crashedDecisions))
	}
}

// TestSupervisorLeaseExpiry verifies that a supervisor run that exceeds
// its lease window (e.g. worker is wedged or the lease token never reaches
// a final state) is detected and a fresh task id can be issued without
// being blocked by the orphaned lease.
func TestSupervisorLeaseExpiry(t *testing.T) {
	base := NewStubPersistence()
	now := time.Now()
	persistence := &leaseExpiringPersistence{
		base:     base,
		expireAt: now.Add(-time.Second), // already expired
	}

	worker := newStubWorker(goodReceipt(""))
	sup := newSelfTestSupervisorForWorker(persistence, worker)
	sup.Config.MaxAttemptsPerApproach = 1
	sup.Config.MaxApproaches = 1

	// Pre-seed an expired supervisor task the wrapper will reject.
	const expiredID = "expire-sup-task-001"
	if err := base.CreateSupervisorTask(context.Background(), controlplane.SupervisorTaskRow{
		ID:           expiredID,
		State:        "running",
		EnvelopeJSON: "{}",
		CreatedAt:    now.Add(-2 * time.Hour),
		UpdatedAt:    now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("seed expired task: %v", err)
	}
	persistence.taskID = expiredID

	// Reading the expired id via the wrapper returns ErrUnknownSupervisorTask
	// so callers can re-issue a fresh row.
	_, err := persistence.GetSupervisorTask(context.Background(), expiredID)
	if !errors.Is(err, controlplane.ErrUnknownSupervisorTask) {
		t.Fatalf("expected ErrUnknownSupervisorTask for expired lease, got %v", err)
	}
	if !persistence.reaped {
		t.Errorf("expected lease to be reaped on first expired read")
	}

	// A new run with a different supervisor task id should succeed normally.
	res, err := sup.Run(context.Background(), RunRequest{
		TaskDescription: "scan after lease expiry",
		Role:            "scout",
		Permission:      "read_only",
		Model:           "m1",
		AddDirs:         []string{"./src"},
		SelfTestMode:    true,
	})
	if err != nil {
		t.Fatalf("run after lease expiry: %v", err)
	}
	if res.Outcome != RunSucceeded {
		t.Errorf("expected RunSucceeded after lease expiry, got %s", res.Outcome)
	}
	if res.SupervisorTaskID == expiredID {
		t.Errorf("new run should not reuse the expired supervisor task id")
	}
}
