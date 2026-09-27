package worker

// #415 PR-1 (POSIX): post-run orphan sweep — a worker that exits while its
// grandchildren keep running (same process group, no setsid escape) leaves
// silent orphans. The sweep verifies the attempt's group is gone after the
// attempt ends and kills survivors. Windows containment is PR-2 (Job
// Objects).

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// A child that exits immediately while leaving a same-group grandchild
// asleep: `sleep 30 &` inside sh stays in the child's process group.
func TestSweepAttemptGroupKillsSameGroupGrandchild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process-group sweep; Windows containment lands in PR-2 (#415)")
	}
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not available")
	}
	dir := t.TempDir()
	runner := processRunner{}
	child, err := runner.Spawn(SpawnOptions{
		Argv:       []string{"sh", "-c", "sleep 30 &\nexit 0"},
		Dir:        dir,
		ResultPath: filepath.Join(dir, "result.json"),
		RunDir:     dir,
		Timeout:    30 * time.Second,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	<-child.Done() // child exited; the grandchild is still asleep

	sup := &Supervisor{clock: time.Now}
	if !groupAlive(child.PID()) {
		t.Skip("grandchild already gone; fixture did not reproduce an orphan")
	}
	sup.sweepAttemptGroup(child, "orphan-sweep-test")

	deadline := time.Now().Add(5 * time.Second)
	for groupAlive(child.PID()) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if groupAlive(child.PID()) {
		t.Fatal("same-group grandchild survived the post-run sweep — silent orphan (#415)")
	}
}

// An exited attempt with a fully-dead group must be a clean no-op: no
// signal storms, no error, no hang.
func TestSweepAttemptGroupCleanExitNoop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process-group sweep; Windows containment lands in PR-2 (#415)")
	}
	dir := t.TempDir()
	runner := processRunner{}
	child, err := runner.Spawn(SpawnOptions{
		Argv:       []string{"sh", "-c", "exit 0"},
		Dir:        dir,
		ResultPath: filepath.Join(dir, "result.json"),
		RunDir:     dir,
		Timeout:    30 * time.Second,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	<-child.Done()
	sup := &Supervisor{clock: time.Now}
	sup.sweepAttemptGroup(child, "noop-test") // must not panic, must not hang
	if groupAlive(child.PID()) {
		t.Fatal("group must stay dead after a clean-exit sweep")
	}
}
