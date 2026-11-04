package cleanup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/process"
)

// heartbeatPIDMappingFixture matches TestFindGhostProcesses_HeartbeatPIDMapping setup.
func heartbeatPIDMappingFixture(t *testing.T) (tempDir string, clock func() time.Time, lister *mockCleanupProcessLister) {
	t.Helper()
	tempDir = t.TempDir()
	agyDir := filepath.Join(tempDir, "agy")
	if err := os.MkdirAll(agyDir, 0o755); err != nil {
		t.Fatalf("mkdir agy: %v", err)
	}

	now := time.Date(2026, 8, 30, 1, 0, 0, 0, time.UTC)
	clock = func() time.Time { return now }

	hbActive := HeartbeatData{
		SessionID:   "sess-active-uuid",
		PID:         5001,
		Binary:      "agy",
		CommandLine: "agy worker --task=1",
		StartedAt:   now.Add(-10 * time.Minute).Format(time.RFC3339),
		LastUpdate:  now.Add(-10 * time.Second).Format(time.RFC3339),
		Status:      "running",
	}
	activeData, err := json.Marshal(hbActive)
	if err != nil {
		t.Fatalf("marshal active hb: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agyDir, "sess-active-uuid.json"), activeData, 0o644); err != nil {
		t.Fatalf("write active hb: %v", err)
	}

	hbStale := HeartbeatData{
		SessionID:   "sess-stale-uuid",
		PID:         5002,
		Binary:      "agy",
		CommandLine: "agy worker --task=2",
		StartedAt:   now.Add(-20 * time.Minute).Format(time.RFC3339),
		LastUpdate:  now.Add(-10 * time.Minute).Format(time.RFC3339),
		Status:      "running",
	}
	staleData, err := json.Marshal(hbStale)
	if err != nil {
		t.Fatalf("marshal stale hb: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agyDir, "sess-stale-uuid.json"), staleData, 0o644); err != nil {
		t.Fatalf("write stale hb: %v", err)
	}

	lister = &mockCleanupProcessLister{
		processes: []process.ProcessInfo{
			{PID: 5001, Binary: "agy", CommandLine: "agy worker --task=1", CWD: tempDir},
			{PID: 5002, Binary: "agy.exe", CommandLine: "agy.exe worker --task=2", CWD: tempDir},
			{PID: 5003, Binary: "agy", CommandLine: "agy worker --task=3", CWD: tempDir},
			{PID: 5004, Binary: "vim", CommandLine: "vim file.txt", CWD: tempDir},
		},
	}
	return tempDir, clock, lister
}

func TestFindGhostProcesses_AncestorSelfPIDCollision(t *testing.T) {
	tempDir, clock, lister := heartbeatPIDMappingFixture(t)

	// Simulate Linux CI flake: test process PID equals mocked stale worker 5002.
	pmCollide := &DefaultProcessManager{
		RepoDir:         tempDir,
		Lister:          lister,
		AncestorSelfPID: 5002,
	}
	ghosts, err := pmCollide.FindGhostProcesses(context.Background(), tempDir, 5*time.Minute, clock)
	if err != nil {
		t.Fatalf("FindGhostProcesses (collision): %v", err)
	}
	if len(ghosts) != 1 {
		t.Fatalf("collision: expected 1 ghost (5003 only), got %d: %+v", len(ghosts), ghosts)
	}
	if ghosts[0].PID != 5003 {
		t.Fatalf("collision: expected ghost PID 5003, got %d", ghosts[0].PID)
	}

	pmFixed := &DefaultProcessManager{
		RepoDir:           tempDir,
		Lister:            lister,
		AncestorSelfPID:   900_001,
		AncestorParentPID: 900_002,
	}
	ghosts, err = pmFixed.FindGhostProcesses(context.Background(), tempDir, 5*time.Minute, clock)
	if err != nil {
		t.Fatalf("FindGhostProcesses (fixed): %v", err)
	}
	if len(ghosts) != 2 {
		t.Fatalf("fixed: expected 2 ghosts, got %d: %+v", len(ghosts), ghosts)
	}
}

func TestFindGhostProcesses_AncestorExcludesRealSelfWithoutOverride(t *testing.T) {
	tempDir := t.TempDir()
	realSelf := os.Getpid()

	lister := &mockCleanupProcessLister{
		processes: []process.ProcessInfo{
			{PID: realSelf, Binary: "agy", CommandLine: "agy worker", CWD: tempDir},
		},
	}
	pm := &DefaultProcessManager{
		RepoDir: tempDir,
		Lister:  lister,
	}
	ghosts, err := pm.FindGhostProcesses(context.Background(), tempDir, time.Minute, time.Now)
	if err != nil {
		t.Fatalf("FindGhostProcesses: %v", err)
	}
	if len(ghosts) != 0 {
		t.Fatalf("expected real self PID %d excluded from ghosts, got: %+v", realSelf, ghosts)
	}
}
