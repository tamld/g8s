package supervisor

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/cleanup"
	"github.com/tamld/g8s/internal/heartbeat"
	"github.com/tamld/g8s/internal/orchestrator"
)

type mockProcessManager struct {
	killedPIDs []int
}

func (m *mockProcessManager) FindGhostProcesses(ctx context.Context, heartbeatDir string, maxAge time.Duration, clock func() time.Time) ([]cleanup.ProcessInfo, error) {
	return nil, nil
}

func (m *mockProcessManager) KillProcess(pid int, sig syscall.Signal) error {
	m.killedPIDs = append(m.killedPIDs, pid)
	return nil
}

func (m *mockProcessManager) IsProcessAlive(pid int) bool {
	return true
}

func TestPollerFreshnessLevels(t *testing.T) {
	tempDir := t.TempDir()
	hbDir := filepath.Join(tempDir, "heartbeat")

	baseTime := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	currTime := baseTime
	clock := func() time.Time { return currTime }

	hbStore := heartbeat.NewStore(hbDir, clock)

	sessionID := "worker-session-1"
	_, err := hbStore.Record(sessionID, "running", nil,
		heartbeat.WithPID(1234),
		heartbeat.WithLastUpdate(baseTime),
	)
	if err != nil {
		t.Fatalf("record initial heartbeat: %v", err)
	}

	store := NewStubPersistence()
	pm := &mockProcessManager{}
	deadCallbackCalled := false

	cfg := PollerConfig{
		Interval:         10 * time.Millisecond,
		StaleThreshold:   60 * time.Second,
		SilenceThreshold: 300 * time.Second,
		HeartbeatDir:     hbDir,
		Clock:            clock,
		Store:            store,
		ProcessManager:   pm,
		OnWorkerDead: func(taskID string, pid int) {
			deadCallbackCalled = true
		},
	}
	poller := NewHeartbeatPoller(cfg)
	ctx := context.Background()

	// 1. Green (< 60s)
	currTime = baseTime.Add(30 * time.Second)
	color, err := poller.PollOnce(ctx, "task-1", sessionID, 1234)
	if err != nil {
		t.Fatalf("poll green: %v", err)
	}
	if color != ColorGreen {
		t.Errorf("color = %s, want green", color)
	}
	if len(store.decisions["task-1"]) != 0 {
		t.Errorf("expected 0 decisions for green, got %d", len(store.decisions["task-1"]))
	}

	// 2. Yellow (60s <= age < 300s)
	currTime = baseTime.Add(90 * time.Second)
	color, err = poller.PollOnce(ctx, "task-1", sessionID, 1234)
	if err != nil {
		t.Fatalf("poll yellow: %v", err)
	}
	if color != ColorYellow {
		t.Errorf("color = %s, want yellow", color)
	}
	if len(store.decisions["task-1"]) != 1 {
		t.Fatalf("expected 1 decision for yellow, got %d", len(store.decisions["task-1"]))
	}
	if store.decisions["task-1"][0].Kind != "heartbeat_stale" {
		t.Errorf("decision kind = %s, want heartbeat_stale", store.decisions["task-1"][0].Kind)
	}

	// Polling yellow again should not duplicate the decision
	_, _ = poller.PollOnce(ctx, "task-1", sessionID, 1234)
	if len(store.decisions["task-1"]) != 1 {
		t.Errorf("expected 1 decision after duplicate yellow poll, got %d", len(store.decisions["task-1"]))
	}

	// 3. Red (>= 300s)
	currTime = baseTime.Add(350 * time.Second)
	color, err = poller.PollOnce(ctx, "task-1", sessionID, 1234)
	if err != nil {
		t.Fatalf("poll red: %v", err)
	}
	if color != ColorRed {
		t.Errorf("color = %s, want red", color)
	}
	if len(store.decisions["task-1"]) != 2 {
		t.Fatalf("expected 2 decisions after red, got %d", len(store.decisions["task-1"]))
	}
	if store.decisions["task-1"][1].Kind != "worker_dead" {
		t.Errorf("decision kind = %s, want worker_dead", store.decisions["task-1"][1].Kind)
	}
	if !deadCallbackCalled {
		t.Errorf("expected OnWorkerDead callback to be called")
	}
	if len(pm.killedPIDs) != 1 || pm.killedPIDs[0] != 1234 {
		t.Errorf("expected PID 1234 killed, got %v", pm.killedPIDs)
	}
}

func TestPollerNoPollDisabled(t *testing.T) {
	cfg := PollerConfig{
		NoPoll: true,
	}
	poller := NewHeartbeatPoller(cfg)
	color, err := poller.PollOnce(context.Background(), "task-1", "session-1", 100)
	if err != nil {
		t.Fatalf("poll disabled: %v", err)
	}
	if color != ColorGreen {
		t.Errorf("expected green when NoPoll is true, got %s", color)
	}
}

// Mock worker for testing aging heartbeat triggering retry in Supervisor
type mockHangingWorker struct {
	hbDir   string
	hbStore *heartbeat.Store
	clock   func() time.Time
	spawned int
	hungPID int
}

type hangingHandle struct {
	pid    int
	task   orchestrator.Task
	worker *mockHangingWorker
}

func (h *hangingHandle) PID() int                         { return h.pid }
func (h *hangingHandle) Cancel(ctx context.Context) error { return nil }
func (h *hangingHandle) StdoutStream() interface {
	Read(p []byte) (int, error)
	Close() error
} {
	return nil
}

func (h *hangingHandle) Wait(ctx context.Context) (orchestrator.Receipt, error) {
	if h.worker.spawned == 1 {
		// First attempt hangs until context cancelled
		<-ctx.Done()
		return orchestrator.Receipt{TaskID: h.task.ID, OK: false}, ctx.Err()
	}
	// Second attempt succeeds immediately
	return orchestrator.Receipt{
		OK:         true,
		TaskID:     h.task.ID,
		WorkerName: "hanging-worker",
		CommitSHA:  "commit-abc-123",
		ReturnCode: 0,
	}, nil
}

func (w *mockHangingWorker) Name() string                        { return "hanging-worker" }
func (w *mockHangingWorker) Available(ctx context.Context) error { return nil }
func (w *mockHangingWorker) Spawn(ctx context.Context, t orchestrator.Task) (orchestrator.Handle, error) {
	w.spawned++
	pid := 4000 + w.spawned
	w.hungPID = pid

	// Write an initial heartbeat that is already 400s old for attempt 1
	var lastUpdate time.Time
	if w.spawned == 1 {
		lastUpdate = w.clock().Add(-400 * time.Second)
	} else {
		lastUpdate = w.clock()
	}

	_, _ = w.hbStore.Record(t.ID, "running", nil,
		heartbeat.WithPID(pid),
		heartbeat.WithLastUpdate(lastUpdate),
	)

	return &hangingHandle{
		pid:    pid,
		task:   t,
		worker: w,
	}, nil
}

func TestPollerAgingHeartbeatTriggersRetry(t *testing.T) {
	tempDir := t.TempDir()
	hbDir := filepath.Join(tempDir, "heartbeat")

	currTime := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return currTime }

	hbStore := heartbeat.NewStore(hbDir, clock)

	worker := &mockHangingWorker{
		hbDir:   hbDir,
		hbStore: hbStore,
		clock:   clock,
	}

	store := NewStubPersistence()
	pm := &mockProcessManager{}

	reviewer := NewStubReviewer()
	sup := NewSelfTestSupervisor(store, worker, reviewer)
	sup.Config.SelfTestMode = false
	sup.Config.NoPoll = false
	sup.Config.PollInterval = 5 * time.Millisecond
	sup.Config.SilenceThreshold = 300 * time.Second
	sup.Config.HeartbeatDir = hbDir
	sup.Config.ProcessManager = pm
	sup.Clock = clock

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := RunRequest{
		TaskDescription: "test task that hangs on attempt 1",
		Role:            "collector",
		Permission:      "read_only",
		Model:           "gemini-3.7-flash-high",
		AddDirs:         []string{tempDir},
	}

	res, err := sup.Run(ctx, req)
	if err != nil {
		t.Fatalf("supervisor Run failed: %v", err)
	}

	if res.Outcome != RunSucceeded {
		t.Errorf("outcome = %v, want RunSucceeded", res.Outcome)
	}

	if worker.spawned < 2 {
		t.Errorf("expected at least 2 attempts spawned due to retry, got %d", worker.spawned)
	}

	// Verify worker_dead decision was emitted
	foundWorkerDead := false
	for _, decList := range store.decisions {
		for _, d := range decList {
			if d.Kind == "worker_dead" {
				foundWorkerDead = true
				break
			}
		}
	}
	if !foundWorkerDead {
		t.Errorf("expected worker_dead decision in store, got %v", store.decisions)
	}
}
