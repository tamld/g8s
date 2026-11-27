package worker

// Red-test for #986 part-2 batch 1 (blind audit registry-a.json):
//
//   A-13 (low): the spawn deadline (exec.CommandContext ctx) is armed with
//        a context whose cancel func is discarded, and the default ctx-kill
//        targets the direct child only. The deadline kill must take down the
//        whole process group (mirroring Terminate / the #980 groupKiller
//        contract), and the deadline timer must be released on early exit.

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestSpawnTimeoutKillsProcessGroup: when opts.Timeout fires, the child's
// entire process group must die — the fake child forks a grandchild whose
// pid it records, and the grandchild must not survive the deadline.
func TestSpawnTimeoutKillsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process-group fixture")
	}
	dir := t.TempDir()
	grandPIDFile := filepath.Join(dir, "grandchild.pid")
	child, err := (processRunner{}).Spawn(SpawnOptions{
		Argv:    []string{"/bin/sh", "-c", `sleep 30 & echo $! > "` + grandPIDFile + `"; wait`},
		Dir:     dir,
		RunDir:  dir,
		Timeout: 400 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Cleanup(func() {
		_ = killProcessGroup(child.PID(), syscallSIGKILL)
		<-child.Done()
	})

	var grandPID int
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, rerr := os.ReadFile(grandPIDFile)
		if rerr == nil {
			grandPID, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("grandchild pid file never appeared: %v", rerr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if grandPID <= 0 {
		t.Fatalf("invalid grandchild pid %d", grandPID)
	}

	select {
	case <-child.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("child did not exit after spawn deadline")
	}

	// The grandchild must be dead too: the deadline kill is a group kill.
	// groupAlive is the #980 cross-platform group probe (EPERM counts as
	// alive; Windows returns false — the test skips there anyway).
	deadline = time.Now().Add(3 * time.Second)
	for {
		if !groupAlive(grandPID) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("grandchild pid %d survived the spawn deadline: the ctx kill targets the direct child only (A-13)", grandPID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestSpawnDeadlineReleasedOnEarlyExit: a child that exits before the spawn
// deadline must release the deadline timer via the collect path — the
// discarded `_ = cancel` previously kept the ctx armed for the full
// opts.Timeout past early exit (A-13 timer leak).
func TestSpawnDeadlineReleasedOnEarlyExit(t *testing.T) {
	child, err := (processRunner{}).Spawn(SpawnOptions{
		Argv:    []string{"/bin/sleep", "0"},
		RunDir:  t.TempDir(),
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	select {
	case <-child.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("child did not exit")
	}
	pc, ok := child.(*processChild)
	if !ok {
		t.Fatalf("child is not *processChild")
	}
	select {
	case <-pc.spawnCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("spawn deadline timer not released after early exit (A-13 timer leak)")
	}
}
