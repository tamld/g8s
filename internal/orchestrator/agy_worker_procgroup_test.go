//go:build !windows

package orchestrator

// POSIX process-group fixtures for #986 batch-1 orchestrator slice #2
// (A-14): agy Cancel must kill the spawned process tree (group leader +
// children), falling back to a direct child kill for handles whose cmd was
// started without Setpgid.

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestAgyWorkerCancelKillsProcessGroup: Cancel must terminate the entire
// process group (Setpgid at spawn + group kill), not just the direct child.
// The fake agy binary forks a grandchild whose pid it records; after Cancel
// the grandchild must be gone too.
func TestAgyWorkerCancelKillsProcessGroup(t *testing.T) {
	requireCommand(t, "sh")
	dir := t.TempDir()
	grandPIDFile := filepath.Join(dir, "grandchild.pid")
	scriptPath := filepath.Join(dir, "fake-agy")
	script := "#!/bin/sh\nsleep 60 &\necho $! > \"" + grandPIDFile + "\"\nwait\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agy script: %v", err)
	}

	w := &AgyWorker{binary: scriptPath, clock: fixedClock, mounts: DefaultMountRegistry()}
	handle, err := w.Spawn(context.Background(), Task{})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	h, ok := handle.(*agyHandle)
	if !ok {
		t.Fatalf("Spawn handle is not *agyHandle")
	}

	// Wait until the grandchild has registered its pid, then remember it for
	// best-effort cleanup so a failed red run does not leak `sleep 60`.
	var grandPID int
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, rerr := os.ReadFile(grandPIDFile)
		if rerr == nil {
			// The shell redirection creates the file BEFORE writing the pid,
			// so an early read sees an empty file (CI-oracle race: parse of ""
			// yields 0). Keep polling until a positive pid actually lands.
			if parsed, perr := strconv.Atoi(strings.TrimSpace(string(data))); perr == nil && parsed > 0 {
				grandPID = parsed
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("grandchild pid never materialized (file present but empty until the shell writes $!): last read err=%v", rerr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if grandPID <= 0 {
		t.Fatalf("invalid grandchild pid %d", grandPID)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(grandPID, syscall.SIGKILL)
		_ = handle.Cancel(context.Background())
		_, _ = h.Wait(context.Background())
	})

	if err := handle.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_, _ = h.Wait(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return within 5s after Cancel")
	}

	// The whole group must be dead, not just the direct child (A-14).
	deadline = time.Now().Add(3 * time.Second)
	for {
		if err := syscall.Kill(grandPID, 0); err == syscall.ESRCH {
			return // fully reaped — process group killed
		}
		if time.Now().After(deadline) {
			t.Fatalf("grandchild pid %d still alive after Cancel: Cancel kills the direct child only (A-14)", grandPID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestAgyHandleCancelDirectKillFallback: a struct-literal handle whose cmd
// was started WITHOUT Setpgid (e.g. legacy test handles) is not a process
// group leader, so group signaling reports ESRCH and Cancel must fall back
// to killing the direct child instead of erroring out or killing the wrong
// group.
func TestAgyHandleCancelDirectKillFallback(t *testing.T) {
	requireCommand(t, "sleep")
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start: %v", err)
	}
	h := &agyHandle{
		cmd:       cmd,
		stdout:    &bytes.Buffer{},
		stderr:    &bytes.Buffer{},
		startedAt: fixedClock(),
		clock:     fixedClock,
	}
	// No Setpgid was applied: the child shares our process group, so it is
	// NOT a group leader and kill(-pid) cannot address a group. Cancel must
	// still terminate it via the direct-kill fallback.
	if err := h.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel (direct-kill fallback): %v", err)
	}
	waitDone := make(chan error, 1)
	go func() {
		waitErr := cmd.Wait() // reaps the killed child
		waitDone <- waitErr
	}()
	select {
	case waitErr := <-waitDone:
		if waitErr == nil {
			t.Fatal("child exited successfully after Cancel: expected killed")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("child still running 3s after Cancel: direct-kill fallback did not fire")
	}
}
