package worker

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/heartbeat"
)

func TestStartHeartbeat_ImmediateInitialWrite(t *testing.T) {
	tempDir := t.TempDir()
	now := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	opts := EmitterOptions{
		Binary:       "agy-test",
		CommandLine:  "agy --task 'inspect repo'",
		Status:       "running",
		PollInterval: 100 * time.Millisecond,
		Metadata:     map[string]string{"env": "test", "role": "builder"},
		PID:          12345,
		BaseDir:      tempDir,
		Clock:        clock,
		CPUChecker:   func(pid int) (float64, error) { return 10.0, nil },
	}

	stop := StartHeartbeat("sess-1", opts)
	defer stop()

	store := heartbeat.NewStore(tempDir, clock)
	hb, err := store.Status("sess-1")
	if err != nil {
		t.Fatalf("expected heartbeat file to exist immediately: %v", err)
	}

	if hb.SessionID != "sess-1" {
		t.Errorf("expected session_id sess-1, got %s", hb.SessionID)
	}
	if hb.PID != 12345 {
		t.Errorf("expected pid 12345, got %d", hb.PID)
	}
	if hb.Binary != "agy-test" {
		t.Errorf("expected binary agy-test, got %s", hb.Binary)
	}
	if hb.CommandLine != "agy --task 'inspect repo'" {
		t.Errorf("expected command_line 'agy --task 'inspect repo'', got %s", hb.CommandLine)
	}
	if hb.Status != heartbeat.StatusRunning {
		t.Errorf("expected status running, got %s", hb.Status)
	}
	if hb.Metadata["env"] != "test" || hb.Metadata["role"] != "builder" {
		t.Errorf("unexpected metadata: %+v", hb.Metadata)
	}
}

func TestStartHeartbeat_StopWritesFinished(t *testing.T) {
	tempDir := t.TempDir()
	now := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	opts := EmitterOptions{
		Binary:       "agy",
		PollInterval: 50 * time.Millisecond,
		BaseDir:      tempDir,
		Clock:        clock,
	}

	stop := StartHeartbeat("sess-finished", opts)
	stop()

	store := heartbeat.NewStore(tempDir, clock)
	hb, err := store.Status("sess-finished")
	if err != nil {
		t.Fatalf("read heartbeat: %v", err)
	}
	if hb.Status != heartbeat.StatusFinished {
		t.Errorf("expected status %s on stop, got %s", heartbeat.StatusFinished, hb.Status)
	}
}

func TestStartHeartbeat_StopWithContextErrorWritesFailed(t *testing.T) {
	tempDir := t.TempDir()
	now := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	opts := EmitterOptions{
		Binary:       "agy",
		PollInterval: 50 * time.Millisecond,
		BaseDir:      tempDir,
		Clock:        clock,
		Context:      ctx,
	}

	stop := StartHeartbeat("sess-failed", opts)
	stop()

	store := heartbeat.NewStore(tempDir, clock)
	hb, err := store.Status("sess-failed")
	if err != nil {
		t.Fatalf("read heartbeat: %v", err)
	}
	if hb.Status != heartbeat.StatusFailed {
		t.Errorf("expected status %s on context cancellation, got %s", heartbeat.StatusFailed, hb.Status)
	}
}

func TestStartHeartbeat_CPUTransitionsRunningToIdleAndBack(t *testing.T) {
	tempDir := t.TempDir()
	now := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	var mu sync.Mutex
	currentCPU := 0.2 // < 1% CPU -> idle

	opts := EmitterOptions{
		Binary:       "agy",
		PollInterval: 20 * time.Millisecond,
		BaseDir:      tempDir,
		Clock:        clock,
		CPUChecker: func(pid int) (float64, error) {
			mu.Lock()
			defer mu.Unlock()
			return currentCPU, nil
		},
	}

	stop := StartHeartbeat("sess-cpu", opts)
	defer stop()

	store := heartbeat.NewStore(tempDir, clock)

	// Wait for at least one tick
	time.Sleep(60 * time.Millisecond)

	hb, err := store.Status("sess-cpu")
	if err != nil {
		t.Fatalf("read heartbeat: %v", err)
	}
	if hb.Status != heartbeat.StatusIdle {
		t.Errorf("expected status %s for CPU < 1%%, got %s", heartbeat.StatusIdle, hb.Status)
	}

	// Now set CPU to 50% (> 1%) -> should transition to running
	mu.Lock()
	currentCPU = 50.0
	mu.Unlock()

	time.Sleep(60 * time.Millisecond)

	hb, err = store.Status("sess-cpu")
	if err != nil {
		t.Fatalf("read heartbeat: %v", err)
	}
	if hb.Status != heartbeat.StatusRunning {
		t.Errorf("expected status %s for CPU >= 1%%, got %s", heartbeat.StatusRunning, hb.Status)
	}
}

func TestStartHeartbeat_EmptySessionID(t *testing.T) {
	tempDir := t.TempDir()
	opts := EmitterOptions{
		BaseDir: tempDir,
	}

	stop := StartHeartbeat("", opts)
	if stop == nil {
		t.Fatal("expected non-nil stop func")
	}
	stop()

	entries, _ := os.ReadDir(tempDir)
	if len(entries) != 0 {
		t.Errorf("expected no files created for empty session ID, found %d", len(entries))
	}
}

func TestStartHeartbeat_MultipleStopCalls(t *testing.T) {
	tempDir := t.TempDir()
	opts := EmitterOptions{
		BaseDir:      tempDir,
		PollInterval: 10 * time.Millisecond,
	}

	stop := StartHeartbeat("sess-multi-stop", opts)
	stop()
	stop() // second call should be no-op and not panic
}

func TestStartHeartbeat_IntegrationWithHeartbeatStore(t *testing.T) {
	tempDir := t.TempDir()
	now := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	opts := EmitterOptions{
		Binary:       "agy",
		CommandLine:  "agy --model gemini",
		BaseDir:      filepath.Join(tempDir, "agy"),
		Clock:        clock,
		PollInterval: 50 * time.Millisecond,
	}

	stop := StartHeartbeat("sess-int", opts)
	defer stop()

	store := heartbeat.NewStore(filepath.Join(tempDir, "agy"), clock)
	freshness, err := store.Freshness("sess-int")
	if err != nil {
		t.Fatalf("freshness error: %v", err)
	}
	if freshness != heartbeat.FreshnessActive {
		t.Errorf("expected freshness %s, got %s", heartbeat.FreshnessActive, freshness)
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if len(list) != 1 || list[0].SessionID != "sess-int" {
		t.Errorf("expected 1 session in store, got %d", len(list))
	}
}

func TestDefaultCPUUsageChecker_CurrentProcess(t *testing.T) {
	cpu, err := defaultCPUUsageChecker(os.Getpid())
	if err != nil {
		t.Logf("defaultCPUUsageChecker returned err (expected on some CI/platforms): %v", err)
	} else {
		if cpu < 0 {
			t.Errorf("expected non-negative cpu percentage, got %f", cpu)
		}
	}

	// Invalid PID should error
	_, err = defaultCPUUsageChecker(-1)
	if err == nil {
		t.Error("expected error for invalid pid -1")
	}
}
