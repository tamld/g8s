package controlplane

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestControlPlaneSegregatedInterfaces(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "interfaces.db")
	store, err := NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()

	var queue TaskQueueStore = store
	var lease TaskLeaseStore = store
	var lineage TaskLineageReader = store
	var briefs BriefRepository = store
	var events EventLogRepository = store
	var sessions SessionRepository = store
	var repo ControlPlaneRepository = store
	_ = repo

	if _, err := lease.ReconcileExpired(ctx); err != nil {
		t.Fatalf("ReconcileExpired via TaskLeaseStore: %v", err)
	}

	task, err := queue.SubmitTask(ctx, SubmitTaskRequest{
		IdempotencyKey: "idem-iface-1",
		Payload:        json.RawMessage(`{"prompt":"verify domain interface segregation"}`),
		Role:           "collector",
		Permission:     "read_only",
		Model:          "claude-sonnet-4-6",
		AddDirs:        []string{t.TempDir()},
	})
	if err != nil {
		t.Fatalf("SubmitTask via TaskQueueStore: %v", err)
	}

	chain, err := lineage.GetTaskLineage(ctx, task.TaskID)
	if err != nil || len(chain) != 1 {
		t.Fatalf("GetTaskLineage via TaskLineageReader: len=%d err=%v", len(chain), err)
	}

	evList, err := events.Events(ctx, task.TaskID)
	if err != nil || len(evList) == 0 {
		t.Fatalf("Events via EventLogRepository: len=%d err=%v", len(evList), err)
	}

	now := time.Now().UTC()
	err = briefs.CreateBrief(ctx, BriefRow{
		ID:        "brief-iface-1",
		Title:     "Interface Segregation Brief",
		PayloadMD: "Payload",
		DodMD:     "DoD",
		IssuedBy:  "supervisor",
		IssuedAt:  now,
		ExpiresAt: now.Add(time.Hour),
		Status:    "active",
	})
	if err != nil {
		t.Fatalf("CreateBrief via BriefRepository: %v", err)
	}

	activeBriefs, err := briefs.ListActiveBriefs(ctx)
	if err != nil || len(activeBriefs) != 1 {
		t.Fatalf("ListActiveBriefs via BriefRepository: len=%d err=%v", len(activeBriefs), err)
	}

	if err := sessions.RegisterSession(ctx, Session{
		ID:   "sess-iface-1",
		Mode: SessionModeInPlace,
	}); err != nil {
		t.Fatalf("RegisterSession via SessionRepository: %v", err)
	}
}
