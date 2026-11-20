package worker

// #980 red-tests: identity-verify before every orphan kill, and retire
// child.pid/child.run.json when an attempt ends. A stale pid file must never
// SIGKILL an unrelated process group (pgid reuse) or a concurrently running
// worker's live attempt.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"
)

// killSpy records every killGroup decision made through the Supervisor seam
// and delegates to the real killProcessGroup so genuine kills still land.
type killSpy struct {
	mu   sync.Mutex
	pids []int
}

func newKillSpy(sup *Supervisor) *killSpy {
	spy := &killSpy{}
	sup.killGroup = func(pid int, sig syscall.Signal) error {
		spy.mu.Lock()
		spy.pids = append(spy.pids, pid)
		spy.mu.Unlock()
		return killProcessGroup(pid, sig)
	}
	return spy
}

func (k *killSpy) called(pid int) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, p := range k.pids {
		if p == pid {
			return true
		}
	}
	return false
}

func (k *killSpy) count() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.pids)
}

// reapSup builds a minimal Supervisor whose runRoot is an isolated runs dir.
func reapSup(t *testing.T) (*Supervisor, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("reapOrphans is POSIX-only (Windows containment is Job Objects, #415 PR-2)")
	}
	runsDir := filepath.Join(t.TempDir(), "runs")
	sup := &Supervisor{clock: time.Now, runRoot: runsDir}
	return sup, runsDir
}

// spawnAttemptChild spawns a real attempt child whose child.pid and identity
// marker land in attemptDir exactly like a production spawn (#980).
func spawnAttemptChild(t *testing.T, attemptDir string, argv ...string) Child {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process-group fixtures")
	}
	if err := os.MkdirAll(attemptDir, 0o700); err != nil {
		t.Fatalf("mkdir attempt dir: %v", err)
	}
	child, err := (processRunner{}).Spawn(SpawnOptions{
		Argv:       argv,
		Dir:        attemptDir,
		RunDir:     attemptDir,
		ResultPath: filepath.Join(attemptDir, "result.json"),
		Timeout:    60 * time.Second,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Cleanup(func() {
		select {
		case <-child.Done():
		default:
			_ = killProcessGroup(child.PID(), syscallSIGKILL)
			<-child.Done()
		}
	})
	return child
}

// deadPID returns the pid of a process that has fully exited and been reaped —
// a stand-in for a crashed predecessor's worker pid. It is spawned OUTSIDE the
// run root so its own pid/identity files never pollute reap scope.
func deadPID(t *testing.T) int {
	t.Helper()
	dir := t.TempDir()
	child, err := (processRunner{}).Spawn(SpawnOptions{
		Argv:       []string{"sh", "-c", "exit 0"},
		Dir:        dir,
		RunDir:     dir,
		ResultPath: filepath.Join(dir, "result.json"),
		Timeout:    30 * time.Second,
	})
	if err != nil {
		t.Fatalf("spawn throwaway: %v", err)
	}
	<-child.Done()
	return child.PID()
}

// rewriteRunRecord loads the spawn-written identity marker, lets the test
// mutate it, and persists it back (simulating the stale state under test).
func rewriteRunRecord(t *testing.T, attemptDir string, mutate func(*childRunRecord)) {
	t.Helper()
	rec := readChildRunRecord(attemptDir)
	if rec == nil {
		t.Fatalf("child.run.json missing or unreadable in %s — spawn must write the identity marker", attemptDir)
	}
	mutate(rec)
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	if err := os.WriteFile(filepath.Join(attemptDir, childRunMarkerName), data, 0o600); err != nil {
		t.Fatalf("write record: %v", err)
	}
}

func attemptFileExists(attemptDir, name string) bool {
	_, err := os.Stat(filepath.Join(attemptDir, name))
	return err == nil
}

func waitPIDDead(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pid %d still alive after 5s", pid)
}

// (a) A child.pid whose recorded start time does not match the live process
// (the pgid-reuse simulation) must never be killed (#980).
func TestReapOrphansNoKillOnStartIdentityMismatch(t *testing.T) {
	sup, runsDir := reapSup(t)
	attemptDir := filepath.Join(runsDir, "task-980-mismatch", "attempt-1")
	child := spawnAttemptChild(t, attemptDir, "sleep", "30")
	if !pidAlive(child.PID()) {
		t.Skip("sleep child already gone; fixture did not reproduce a live pid")
	}
	rewriteRunRecord(t, attemptDir, func(rec *childRunRecord) {
		rec.PPID = deadPID(t)                     // predecessor worker is gone
		rec.PPIDStart = 0                         // owner liveness no longer verifiable
		rec.StartTime = rec.StartTime + 987654321 // recorded start no longer matches the live process
	})
	spy := newKillSpy(sup)
	sup.reapOrphans()
	if spy.count() != 0 {
		t.Fatalf("reapOrphans killed %v despite a mismatched recorded start time (pgid reuse)", spy.pids)
	}
	if !pidAlive(child.PID()) {
		t.Fatal("live process was killed via the reused-pgid simulation — identity gate failed (#980)")
	}
	if attemptFileExists(attemptDir, "child.pid") {
		t.Fatal("stale child.pid must be removed when identity verification fails")
	}
}

// (a2) A legacy plain child.pid with no identity marker is cleaned up but
// never killed — fail closed on unverifiable identity (#980).
func TestReapOrphansNoKillOnMissingMarker(t *testing.T) {
	sup, runsDir := reapSup(t)
	attemptDir := filepath.Join(runsDir, "task-980-legacy", "attempt-1")
	child := spawnAttemptChild(t, attemptDir, "sleep", "30")
	if !pidAlive(child.PID()) {
		t.Skip("sleep child already gone; fixture did not reproduce a live pid")
	}
	if err := os.Remove(filepath.Join(attemptDir, childRunMarkerName)); err != nil {
		t.Fatalf("remove marker: %v", err)
	}
	spy := newKillSpy(sup)
	sup.reapOrphans()
	if spy.count() != 0 {
		t.Fatalf("reapOrphans killed %v despite a missing identity marker", spy.pids)
	}
	if !pidAlive(child.PID()) {
		t.Fatal("live process was killed through a legacy marker-less pid file")
	}
	if attemptFileExists(attemptDir, "child.pid") {
		t.Fatal("legacy child.pid must still be removed (cleanup without kill)")
	}
}

// (a3) A marker whose recorded pid disagrees with child.pid is inconsistent
// state — cleanup only, never kill (#980).
func TestReapOrphansNoKillOnMarkerPidMismatch(t *testing.T) {
	sup, runsDir := reapSup(t)
	attemptDir := filepath.Join(runsDir, "task-980-pidmismatch", "attempt-1")
	child := spawnAttemptChild(t, attemptDir, "sleep", "30")
	if !pidAlive(child.PID()) {
		t.Skip("sleep child already gone; fixture did not reproduce a live pid")
	}
	rewriteRunRecord(t, attemptDir, func(rec *childRunRecord) {
		rec.PID = child.PID() + 1
		rec.PPID = deadPID(t)
		rec.PPIDStart = 0
	})
	spy := newKillSpy(sup)
	sup.reapOrphans()
	if spy.count() != 0 {
		t.Fatalf("reapOrphans killed %v despite an inconsistent pid/identity record", spy.pids)
	}
	if !pidAlive(child.PID()) {
		t.Fatal("live process was killed through an inconsistent identity record")
	}
	if attemptFileExists(attemptDir, "child.pid") {
		t.Fatal("inconsistent child.pid must be removed (cleanup without kill)")
	}
}

// A live attempt owned by a still-running worker is left completely
// untouched: no kill, and both identity files stay for that worker's own
// post-run sweep (#980 — shared runRoot, multi-coordinator topology).
func TestReapOrphansSkipsLiveAttemptOfRunningWorker(t *testing.T) {
	sup, runsDir := reapSup(t)
	attemptDir := filepath.Join(runsDir, "task-980-live", "attempt-1")
	child := spawnAttemptChild(t, attemptDir, "sleep", "30")
	if !pidAlive(child.PID()) {
		t.Skip("sleep child already gone; fixture did not reproduce a live pid")
	}
	// The marker as written by Spawn records PPID = this test process (alive,
	// start time matches) — exactly the concurrently-running-worker case.
	spy := newKillSpy(sup)
	sup.reapOrphans()
	if spy.count() != 0 {
		t.Fatalf("reapOrphans killed %v while the owning worker is still running", spy.pids)
	}
	if !pidAlive(child.PID()) {
		t.Fatal("a concurrently running worker's live attempt was killed (#980)")
	}
	if !attemptFileExists(attemptDir, "child.pid") || !attemptFileExists(attemptDir, childRunMarkerName) {
		t.Fatal("identity files of a live attempt must be kept for the owner's post-run sweep")
	}
}

// (c) Valid identity marker + matching recorded start time + dead owner
// (crashed predecessor) -> the verified orphan is still killed; existing
// behavior is preserved (#980).
func TestReapOrphansKillsVerifiedOrphan(t *testing.T) {
	sup, runsDir := reapSup(t)
	attemptDir := filepath.Join(runsDir, "task-980-verified", "attempt-1")
	child := spawnAttemptChild(t, attemptDir, "sleep", "30")
	if !pidAlive(child.PID()) {
		t.Skip("sleep child already gone; fixture did not reproduce a live pid")
	}
	rewriteRunRecord(t, attemptDir, func(rec *childRunRecord) {
		rec.PPID = deadPID(t) // crashed predecessor; PID/StartTime stay as spawned
		rec.PPIDStart = 0
	})
	spy := newKillSpy(sup)
	sup.reapOrphans()
	if !spy.called(child.PID()) {
		t.Fatalf("verified orphan (pid %d) was not killed — kills: %v", child.PID(), spy.pids)
	}
	waitPIDDead(t, child.PID())
	if attemptFileExists(attemptDir, "child.pid") || attemptFileExists(attemptDir, childRunMarkerName) {
		t.Fatal("identity files must be retired after a verified kill")
	}
}

// The leader pid may already be dead while same-group grandchildren survive
// (#415 crash shape). With no live leader there can be no unrelated group
// leader (a reused leader would need pid == pgid), so the recorded group is
// still reaped (#980).
func TestReapOrphansKillsGroupSurvivorsOfDeadLeader(t *testing.T) {
	sup, runsDir := reapSup(t)
	attemptDir := filepath.Join(runsDir, "task-980-survivors", "attempt-1")
	child := spawnAttemptChild(t, attemptDir, "sh", "-c", "sleep 30 &\nexit 0")
	<-child.Done() // leader exited; the same-group grandchild is still asleep
	if !groupAlive(child.PID()) {
		t.Skip("grandchild already gone; fixture did not reproduce group survivors")
	}
	// Simulate the crashed predecessor: the recorded owner must be dead so
	// the ownership probe does not classify this as a live attempt.
	rewriteRunRecord(t, attemptDir, func(rec *childRunRecord) {
		rec.PPID = deadPID(t)
		rec.PPIDStart = 0
	})
	spy := newKillSpy(sup)
	sup.reapOrphans()
	if !spy.called(child.PID()) {
		t.Fatalf("same-group survivors (pgid %d) were not reaped — kills: %v", child.PID(), spy.pids)
	}
	deadline := time.Now().Add(5 * time.Second)
	for groupAlive(child.PID()) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if groupAlive(child.PID()) {
		t.Fatal("same-group survivors outlived the verified reap (#415/#980)")
	}
	if attemptFileExists(attemptDir, "child.pid") {
		t.Fatal("child.pid must be removed after the group-survivor reap")
	}
}

// (b) After the attempt ends, sweepAttemptGroup retires child.pid and the
// identity marker — matching its documented contract (#980).
func TestSweepAttemptGroupRetiresPidFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process-group fixtures")
	}
	attemptDir := t.TempDir()
	child := spawnAttemptChild(t, attemptDir, "sh", "-c", "exit 0")
	<-child.Done()
	if !attemptFileExists(attemptDir, "child.pid") {
		t.Fatal("spawn must write child.pid")
	}
	if !attemptFileExists(attemptDir, childRunMarkerName) {
		t.Fatal("spawn must write the identity marker")
	}
	sup := &Supervisor{clock: time.Now}
	sup.sweepAttemptGroup(child, "980-retire")
	if attemptFileExists(attemptDir, "child.pid") {
		t.Fatal("child.pid must be removed after attempt end (sweepAttemptGroup doc contract)")
	}
	if attemptFileExists(attemptDir, childRunMarkerName) {
		t.Fatal("identity marker must be removed after attempt end")
	}
	if groupAlive(child.PID()) {
		t.Fatal("group must stay dead after a clean-exit sweep")
	}
}
