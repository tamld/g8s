package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSandboxMarkerLifecycleAndValidation(t *testing.T) {
	stateDir := t.TempDir()

	// Missing directory returns empty nil error
	markers, err := ListSandboxMarkers(stateDir)
	if err != nil || len(markers) != 0 {
		t.Fatalf("empty ListSandboxMarkers = (%v, %v), want nil, nil", markers, err)
	}

	// Reject traversal or empty IDs
	for _, bad := range []string{"", ".", "..", "../escape", "sub/dir", `sub\dir`} {
		if err := WriteSandboxMarker(stateDir, SandboxMarker{SandboxID: bad, WorkerPID: 123}); err == nil {
			t.Errorf("WriteSandboxMarker(%q) succeeded, want error", bad)
		}
	}

	m := SandboxMarker{
		SandboxID:   "sbx-registry-test-1",
		WorkerPID:   4242,
		WorktreeDir: "/tmp/wt",
		RunDir:      "/tmp/run",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := WriteSandboxMarker(stateDir, m); err != nil {
		t.Fatalf("WriteSandboxMarker: %v", err)
	}

	// Non-JSON / dotfiles should be ignored cleanly
	_ = os.WriteFile(filepath.Join(SandboxMarkersDir(stateDir), ".tmp-ignore.json"), []byte("{bad"), 0o600)
	_ = os.WriteFile(filepath.Join(SandboxMarkersDir(stateDir), "corrupt.json"), []byte("{bad"), 0o600)

	listed, err := ListSandboxMarkers(stateDir)
	if err != nil {
		t.Fatalf("ListSandboxMarkers: %v", err)
	}
	if len(listed) != 1 || listed[0].SandboxID != m.SandboxID || listed[0].WorkerPID != 4242 {
		t.Fatalf("unexpected listed markers: %+v", listed)
	}

	RemoveSandboxMarker(stateDir, m.SandboxID)
	listedAfter, err := ListSandboxMarkers(stateDir)
	if err != nil || len(listedAfter) != 0 {
		t.Fatalf("expected 0 markers after removal, got (%+v, %v)", listedAfter, err)
	}
}

func TestPreparePersistsAndCleansUpSandboxMarker(t *testing.T) {
	stateDir := t.TempDir()
	workDir := t.TempDir()
	runDir := t.TempDir()

	prep, err := Prepare(Config{
		Argv:         []string{"echo", "ok"},
		WorktreeDir:  workDir,
		RunDir:       runDir,
		StateDir:     stateDir,
		Permission:   "read_only",
		ProviderName: "codex",
		NoSandbox:    true,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	markers, err := ListSandboxMarkers(stateDir)
	if err != nil || len(markers) != 1 {
		t.Fatalf("expected 1 persisted marker before Cleanup, got (%+v, %v)", markers, err)
	}
	if markers[0].SandboxID != prep.SandboxID || markers[0].WorkerPID != os.Getpid() {
		t.Fatalf("marker mismatch: %+v (want id=%s pid=%d)", markers[0], prep.SandboxID, os.Getpid())
	}

	prep.Cleanup()

	markersAfter, err := ListSandboxMarkers(stateDir)
	if err != nil || len(markersAfter) != 0 {
		t.Fatalf("expected 0 markers after Cleanup, got (%+v, %v)", markersAfter, err)
	}
}

func TestFindSandboxProcesses_POSIX(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FindSandboxProcesses env scan is POSIX-specific")
	}
	const sbxID = "sbx-find-test-posix"
	exe := selfHelperArgv(t)
	cmd := exec.Command(exe, "-test.run=^TestSandboxHelperProcess$")
	cmd.Env = append(os.Environ(),
		sandboxHelperEnv+"=1",
		"HELPER_ACTION=sleep_detached",
		"G8S_SANDBOX_ID="+sbxID,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper sleep: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	found := false
	var pids []int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pids = FindSandboxProcesses(sbxID)
		for _, p := range pids {
			if p == cmd.Process.Pid {
				found = true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !found {
		t.Fatalf("FindSandboxProcesses(%q) = %v, want pid %d", sbxID, pids, cmd.Process.Pid)
	}

	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	pidsAfter := FindSandboxProcesses(sbxID)
	for _, p := range pidsAfter {
		if p == cmd.Process.Pid {
			t.Fatalf("pid %d still returned after exit", p)
		}
	}
}
