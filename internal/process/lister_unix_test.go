//go:build !windows

package process

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestParsePsOutput(t *testing.T) {
	mockOutput := []byte(`
    1     0 root             /sbin/launchd
  100     1 root             /usr/libexec/logd --debug
 5420  1000 tamld            /usr/local/bin/agy subagent run --task=abc
 9999  5420 tamld            claude --print "hello world"
   invalid line
   10   20
   abc  def root /bin/sh
   30   xyz root /bin/sh
`)

	procs := parsePsOutput(mockOutput)
	if len(procs) != 5 {
		t.Fatalf("expected 5 processes, got %d", len(procs))
	}

	// Process 0
	if procs[0].PID != 1 || procs[0].PPID != 0 || procs[0].User != "root" || procs[0].Binary != "launchd" {
		t.Errorf("unexpected proc 0: %+v", procs[0])
	}

	// Process 2: agy
	if procs[2].PID != 5420 || procs[2].PPID != 1000 || procs[2].User != "tamld" || procs[2].Binary != "agy" {
		t.Errorf("unexpected proc 2: %+v", procs[2])
	}
	if procs[2].CommandLine != "/usr/local/bin/agy subagent run --task=abc" {
		t.Errorf("unexpected command line: %q", procs[2].CommandLine)
	}

	// Process 3: claude
	if procs[3].PID != 9999 || procs[3].Binary != "claude" {
		t.Errorf("unexpected proc 3: %+v", procs[3])
	}
}

func TestResolveCWD_EdgeCases(t *testing.T) {
	lister := NewLister()
	if cwd := lister.ResolveCWD(0); cwd != "" {
		t.Errorf("expected empty string for PID 0, got %s", cwd)
	}
	if cwd := lister.ResolveCWD(-1); cwd != "" {
		t.Errorf("expected empty string for PID -1, got %s", cwd)
	}

	selfPID := os.Getpid()
	cwd := lister.ResolveCWD(selfPID)
	expectedCwd, _ := os.Getwd()

	if cwd != "" && expectedCwd != "" {
		t.Logf("Resolved self CWD: %s (expected: %s)", cwd, expectedCwd)
	}
}

func TestPsLister_KillAndKillForce(t *testing.T) {
	lister := NewLister()

	// Spawn a short-lived process for testing Kill (SIGTERM)
	cmd1 := exec.Command("sleep", "30")
	if err := cmd1.Start(); err != nil {
		t.Fatalf("start sleep process: %v", err)
	}
	pid1 := cmd1.Process.Pid

	if !lister.IsAlive(pid1) {
		t.Fatalf("expected spawned process %d to be alive", pid1)
	}

	if err := lister.Kill(pid1); err != nil {
		t.Errorf("Kill() failed: %v", err)
	}

	_ = cmd1.Wait()
	time.Sleep(20 * time.Millisecond)

	if lister.IsAlive(pid1) {
		t.Errorf("expected process %d to be terminated after Kill", pid1)
	}

	// Spawn another process for testing KillForce (SIGKILL)
	cmd2 := exec.Command("sleep", "30")
	if err := cmd2.Start(); err != nil {
		t.Fatalf("start sleep process: %v", err)
	}
	pid2 := cmd2.Process.Pid

	if err := lister.KillForce(pid2); err != nil {
		t.Errorf("KillForce() failed: %v", err)
	}

	_ = cmd2.Wait()
	time.Sleep(20 * time.Millisecond)

	if lister.IsAlive(pid2) {
		t.Errorf("expected process %d to be terminated after KillForce", pid2)
	}
}
