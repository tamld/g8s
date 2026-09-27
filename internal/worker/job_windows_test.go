//go:build windows

package worker

// #415 PR-2: Windows Job Object containment — kernel-guaranteed tree kill.
// These tests require a real Windows host (windows-latest CI or the
// operator's Windows machine); they are skipped on every other OS.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A child that exits while a detached grandchild keeps running must NOT
// survive the Job Object close: taskkill /T cannot always reach detached
// grandchildren, KILL_ON_JOB_CLOSE can.
func TestJobObjectKillsDetachedGrandchild(t *testing.T) {
	dir := t.TempDir()
	runner := processRunner{}
	child, err := runner.Spawn(SpawnOptions{
		// start /b detaches ping from the cmd job semantics as seen by
		// taskkill /T on some trees — exactly the escape class under test.
		Argv:       []string{"cmd", "/c", "start /b ping -n 60 127.0.0.1 > nul & exit 0"},
		Dir:        dir,
		ResultPath: filepath.Join(dir, "result.json"),
		RunDir:     dir,
		Timeout:    2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	<-child.Done() // child exited; the detached ping is still running
	time.Sleep(500 * time.Millisecond)

	if !pingRunning() {
		t.Skip("could not observe the detached ping (fixture did not reproduce); re-run to confirm")
	}

	pc, ok := child.(*processChild)
	if !ok {
		t.Fatalf("child is %T, want *processChild", child)
	}
	pc.closeJob() // KILL_ON_JOB_CLOSE: kernel kills the whole tree

	deadline := time.Now().Add(10 * time.Second)
	for pingRunning() && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	if pingRunning() {
		t.Fatal("detached grandchild survived the Job Object close — containment gap (#415)")
	}
}

func pingRunning() bool {
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq ping.exe", "/FO", "CSV").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), "ping.exe")
}

// The job handle must survive the child's normal runtime (assignment while
// running) and close idempotently.
func TestJobObjectCloseIdempotent(t *testing.T) {
	dir := t.TempDir()
	runner := processRunner{}
	child, err := runner.Spawn(SpawnOptions{
		Argv:       []string{"cmd", "/c", "exit 0"},
		Dir:        dir,
		ResultPath: filepath.Join(dir, "result.json"),
		RunDir:     dir,
		Timeout:    time.Minute,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	<-child.Done()
	pc, ok := child.(*processChild)
	if !ok {
		t.Fatalf("child is %T, want *processChild", child)
	}
	pc.closeJob()
	pc.closeJob() // idempotent — must not panic or crash
	if _, err := os.Stat(filepath.Join(dir, "result.json")); os.IsNotExist(err) {
		t.Log("result file absent (wrap-exec not exercised in raw spawn) — informational")
	}
}
