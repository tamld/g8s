//go:build windows

package worker

// #415 PR-2: Windows Job Object containment — kernel-guaranteed tree kill.
// These tests require a real Windows host (windows-latest CI or the
// operator's Windows machine); they are skipped on every other OS.

import (
	"encoding/csv"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A child that exits while a detached grandchild keeps running must NOT
// survive the Job Object close: taskkill /T cannot always reach detached
// grandchildren, KILL_ON_JOB_CLOSE can.
func TestJobObjectKillsDetachedGrandchild(t *testing.T) {
	baseline := pingPIDs(t)
	settle, shutdown := 500*time.Millisecond, 10*time.Second
	if jobTestRaceEnabled {
		settle, shutdown = time.Second, 20*time.Second
	}
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
	time.Sleep(settle)

	observed := pingPIDs(t)
	for pid := range baseline {
		delete(observed, pid)
	}
	if len(observed) == 0 {
		t.Skip("could not observe the detached ping (fixture did not reproduce); re-run to confirm")
	}

	pc, ok := child.(*processChild)
	if !ok {
		t.Fatalf("child is %T, want *processChild", child)
	}
	pc.closeJob() // KILL_ON_JOB_CLOSE: kernel kills the whole tree

	deadline := time.Now().Add(shutdown)
	for {
		running := pingPIDs(t)
		// Once an observed PID disappears, forget it permanently. New ping
		// processes (including later reuse of that PID) are not our tree.
		for pid := range observed {
			if _, ok := running[pid]; !ok {
				delete(observed, pid)
			}
		}
		if len(observed) == 0 {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("detached grandchild survived the Job Object close — containment gap (#415): PIDs %v", observed)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func pingPIDs(t *testing.T) map[int]struct{} {
	t.Helper()
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq ping.exe", "/FO", "CSV").Output()
	if err != nil {
		t.Fatalf("enumerate ping PIDs: %v", err)
	}
	pids := make(map[int]struct{})
	reader := csv.NewReader(strings.NewReader(string(out)))
	// tasklist may emit a localized one-column message when no process
	// matches. Headers and messages are ignored by image-name column.
	reader.FieldsPerRecord = -1
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("parse tasklist CSV: %v", err)
		}
		if len(record) == 0 || !strings.EqualFold(record[0], "ping.exe") {
			continue
		}
		if len(record) < 2 {
			t.Fatal("tasklist ping row has no PID column")
		}
		pid, err := strconv.Atoi(strings.TrimSpace(record[1]))
		if err != nil || pid <= 0 {
			t.Fatalf("invalid tasklist ping PID %q", record[1])
		}
		pids[pid] = struct{}{}
	}
	return pids
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
