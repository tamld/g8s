package cleanup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/sandbox"
)

const cleanupOrphanHelperEnv = "GO_WANT_CLEANUP_ORPHAN_HELPER"

func TestCleanupSandboxOrphanHelper(t *testing.T) {
	if os.Getenv(cleanupOrphanHelperEnv) != "1" {
		return
	}
	if os.Getenv("HELPER_MODE") == "exit_now" {
		os.Exit(0)
	}
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

func spawnCleanupHelperProcess(t *testing.T, mode, sandboxID string) *exec.Cmd {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cmd := exec.Command(exe, "-test.run=^TestCleanupSandboxOrphanHelper$")
	env := append(os.Environ(), cleanupOrphanHelperEnv+"=1", "HELPER_MODE="+mode)
	if sandboxID != "" {
		env = append(env, "G8S_SANDBOX_ID="+sandboxID)
	}
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper (%s): %v", mode, err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})
	return cmd
}

func TestSweepGhostProcesses_LiveWorkerSandboxSkipped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX G8S_SANDBOX_ID orphan env scan does not apply on Windows JobObject")
	}

	stateDir := t.TempDir()
	const sbxID = "sbx-live-worker-skip"
	child := spawnCleanupHelperProcess(t, "sleep", sbxID)

	if err := sandbox.WriteSandboxMarker(stateDir, sandbox.SandboxMarker{
		SandboxID: sbxID,
		WorkerPID: os.Getpid(),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("WriteSandboxMarker: %v", err)
	}

	report, err := RunCleanupSweep(context.Background(), CleanupConfig{
		RepoDir:        t.TempDir(),
		HeartbeatDir:   t.TempDir(),
		StateDir:       stateDir,
		Targets:        []string{TargetGhostProcess},
		DryRun:         false,
		GracePeriod:    500 * time.Millisecond,
		ProcessManager: &DefaultProcessManager{},
	})
	if err != nil {
		t.Fatalf("RunCleanupSweep: %v", err)
	}
	childStr := strconv.Itoa(child.Process.Pid)
	for _, it := range report.Items {
		if it.ID == childStr {
			t.Fatalf("live worker sandbox child pid %d must not be touched: %+v", child.Process.Pid, it)
		}
	}
	pm := &DefaultProcessManager{}
	if !pm.IsProcessAlive(child.Process.Pid) {
		t.Fatalf("live worker child pid %d died unexpectedly", child.Process.Pid)
	}
}

func TestSweepGhostProcesses_DeadWorkerSandboxOrphanReaped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX G8S_SANDBOX_ID orphan env scan does not apply on Windows JobObject")
	}

	stateDir := t.TempDir()
	repoDir := t.TempDir()
	const sbxID = "sbx-dead-orphan-reap"

	// Create a guaranteed dead worker PID.
	deadCmd := spawnCleanupHelperProcess(t, "exit_now", "")
	deadWorkerPID := deadCmd.Process.Pid
	_ = deadCmd.Wait()

	orphan := spawnCleanupHelperProcess(t, "sleep", sbxID)
	orphanStr := strconv.Itoa(orphan.Process.Pid)

	if err := sandbox.WriteSandboxMarker(stateDir, sandbox.SandboxMarker{
		SandboxID: sbxID,
		WorkerPID: deadWorkerPID,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("WriteSandboxMarker: %v", err)
	}

	// Wait briefly until child env becomes visible on Darwin/Linux process table.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(sandbox.FindSandboxProcesses(sbxID)) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 1. Dry-run reports would_kill without killing process or removing marker.
	dryRep, err := RunCleanupSweep(context.Background(), CleanupConfig{
		RepoDir:        repoDir,
		HeartbeatDir:   filepath.Join(repoDir, ".heartbeat"),
		StateDir:       stateDir,
		Targets:        []string{TargetGhostProcess},
		DryRun:         true,
		GracePeriod:    1 * time.Second,
		ProcessManager: &DefaultProcessManager{RepoDir: repoDir},
	})
	if err != nil {
		t.Fatalf("dry-run RunCleanupSweep: %v", err)
	}
	sawDry := false
	for _, it := range dryRep.Items {
		if it.Target == TargetGhostProcess && it.ID == orphanStr && it.Action == "would_kill" {
			sawDry = true
		}
	}
	if !sawDry {
		t.Fatalf("expected would_kill item for orphan pid %s, got %+v", orphanStr, dryRep.Items)
	}
	pm := &DefaultProcessManager{}
	if !pm.IsProcessAlive(orphan.Process.Pid) {
		t.Fatalf("orphan pid %d died during dry-run", orphan.Process.Pid)
	}

	// 2. Force sweep terminates the orphan and removes its marker file.
	applyRep, err := RunCleanupSweep(context.Background(), CleanupConfig{
		RepoDir:        repoDir,
		HeartbeatDir:   filepath.Join(repoDir, ".heartbeat"),
		DBPath:         filepath.Join(stateDir, "g8s.db"),
		Targets:        []string{TargetGhostProcess},
		DryRun:         false,
		GracePeriod:    1 * time.Second,
		ProcessManager: &DefaultProcessManager{RepoDir: repoDir},
	})
	if err != nil {
		t.Fatalf("apply RunCleanupSweep: %v", err)
	}
	sawKill := false
	for _, it := range applyRep.Items {
		if it.Target == TargetGhostProcess && it.ID == orphanStr && it.Action == "killed" {
			sawKill = true
		}
	}
	if !sawKill {
		t.Fatalf("expected killed item for orphan pid %s, got %+v", orphanStr, applyRep.Items)
	}
	_ = orphan.Wait()
	if pm.IsProcessAlive(orphan.Process.Pid) {
		t.Fatalf("orphan pid %d survived ghost-process sweep", orphan.Process.Pid)
	}

	markers, err := sandbox.ListSandboxMarkers(stateDir)
	if err != nil || len(markers) != 0 {
		t.Fatalf("expected marker pruned after reap, got (%+v, %v)", markers, err)
	}
}

func TestSweepGhostProcesses_PrunesStaleMarkerWithNoSurvivors(t *testing.T) {
	stateDir := t.TempDir()
	deadCmd := spawnCleanupHelperProcess(t, "exit_now", "")
	deadPID := deadCmd.Process.Pid
	_ = deadCmd.Wait()

	if err := sandbox.WriteSandboxMarker(stateDir, sandbox.SandboxMarker{
		SandboxID: "sbx-stale-no-survivors",
		WorkerPID: deadPID,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("WriteSandboxMarker: %v", err)
	}

	if _, err := RunCleanupSweep(context.Background(), CleanupConfig{
		RepoDir:        t.TempDir(),
		HeartbeatDir:   t.TempDir(),
		StateDir:       stateDir,
		Targets:        []string{TargetGhostProcess},
		DryRun:         false,
		GracePeriod:    500 * time.Millisecond,
		ProcessManager: &DefaultProcessManager{},
	}); err != nil {
		t.Fatalf("RunCleanupSweep: %v", err)
	}

	markers, err := sandbox.ListSandboxMarkers(stateDir)
	if err != nil || len(markers) != 0 {
		t.Fatalf("expected stale marker pruned, got (%+v, %v)", markers, err)
	}
}
