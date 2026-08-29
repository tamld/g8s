//go:build !windows

package process

import (
	"os"
	"testing"
)

func TestParsePsOutput(t *testing.T) {
	mockOutput := []byte(`
    1     0 root             /sbin/launchd
  100     1 root             /usr/libexec/logd --debug
 5420  1000 tamld            /usr/local/bin/agy subagent run --task=abc
 9999  5420 tamld            claude --print "hello world"
`)

	procs := parsePsOutput(mockOutput)
	if len(procs) != 4 {
		t.Fatalf("expected 4 processes, got %d", len(procs))
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

func TestResolveCWD_Self(t *testing.T) {
	lister := NewLister()
	selfPID := os.Getpid()
	cwd := lister.ResolveCWD(selfPID)
	expectedCwd, _ := os.Getwd()

	if cwd != "" && expectedCwd != "" {
		t.Logf("Resolved self CWD: %s (expected: %s)", cwd, expectedCwd)
	}
}
