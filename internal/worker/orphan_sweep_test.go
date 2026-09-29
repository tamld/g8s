package worker

// #415 PR-1 (POSIX): post-run orphan sweep — a worker that exits while its
// grandchildren keep running (same process group, no setsid escape) leaves
// silent orphans. The sweep verifies the attempt's group is gone after the
// attempt ends and kills survivors. Windows containment is PR-2 (Job
// Objects).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
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

func findPython() string {
	for _, p := range []string{"/usr/bin/python3", "python3", "python"} {
		if path, err := exec.LookPath(p); err == nil {
			return path
		}
	}
	return ""
}

func writeSetsidScript(t *testing.T, dir, pidFile string) string {
	t.Helper()
	scriptPath := filepath.Join(dir, "escape.py")
	pyCode := fmt.Sprintf(`import os, sys, time
if os.fork() > 0:
    sys.exit(0)
os.setsid()
if os.fork() > 0:
    sys.exit(0)
devnull = os.open("/dev/null", os.O_RDWR)
os.dup2(devnull, 0)
os.dup2(devnull, 1)
os.dup2(devnull, 2)
with open(%q, "w") as f:
    f.write(str(os.getpid()))
time.sleep(30)
`, pidFile)
	if err := os.WriteFile(scriptPath, []byte(pyCode), 0o755); err != nil {
		t.Fatalf("write escape script: %v", err)
	}
	return scriptPath
}

func readPID(t *testing.T, pidFile string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil && len(data) > 0 {
			pid, perr := strconv.Atoi(strings.TrimSpace(string(data)))
			if perr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("failed to read PID from %s within deadline", pidFile)
	return 0
}

func isPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// TestSweepEscapeKillsSetsidGrandchild verifies that a grandchild that escapes
// the child's process group via setsid (double-fork) and carries the attempt's
// G8S_RUN_MARKER is tracked down and killed by the Layer 2 escape sweep (#439).
func TestSweepEscapeKillsSetsidGrandchild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX setsid sweep; Windows containment uses Job Objects (#415 PR-2)")
	}
	py := findPython()
	if py == "" {
		t.Skip("python not available")
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "grandchild.pid")
	scriptPath := writeSetsidScript(t, dir, pidFile)

	marker := "attempt-439-escape-test"
	runner := processRunner{}
	child, err := runner.Spawn(SpawnOptions{
		Argv:       []string{"sh", "-c", fmt.Sprintf("%s %q\nexit 0", py, scriptPath)},
		Dir:        dir,
		Env:        []string{"G8S_RUN_MARKER=" + marker},
		ResultPath: filepath.Join(dir, "result.json"),
		RunDir:     dir,
		Timeout:    30 * time.Second,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	<-child.Done() // shell and intermediate child exited; grandchild is detached

	grandchildPID := readPID(t, pidFile)
	t.Cleanup(func() {
		if isPIDAlive(grandchildPID) {
			if proc, err := os.FindProcess(grandchildPID); err == nil {
				_ = proc.Kill()
			}
		}
	})

	if !isPIDAlive(grandchildPID) {
		t.Fatal("setsid grandchild should be alive before sweep")
	}

	sup := &Supervisor{clock: time.Now}
	sup.sweepAttemptGroup(child, "escape-test", marker)

	deadline := time.Now().Add(5 * time.Second)
	for isPIDAlive(grandchildPID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if isPIDAlive(grandchildPID) {
		t.Fatal("setsid grandchild survived layer-2 marker sweep — silent orphan escape (#439)")
	}
}

// TestSweepEscapeNoFalseKill verifies that an unrelated process (carrying a different
// marker or no marker) is NOT killed by the Layer 2 sweep (#439).
func TestSweepEscapeNoFalseKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX setsid sweep; Windows containment uses Job Objects (#415 PR-2)")
	}
	py := findPython()
	if py == "" {
		t.Skip("python not available")
	}
	dir := t.TempDir()
	targetPIDFile := filepath.Join(dir, "target.pid")
	unrelatedPIDFile := filepath.Join(dir, "unrelated.pid")
	targetScript := writeSetsidScript(t, dir, targetPIDFile)

	unrelatedScriptPath := filepath.Join(dir, "unrelated.py")
	pyCodeUnrelated := fmt.Sprintf(`import os, sys, time
if os.fork() > 0:
    sys.exit(0)
os.setsid()
if os.fork() > 0:
    sys.exit(0)
devnull = os.open("/dev/null", os.O_RDWR)
os.dup2(devnull, 0)
os.dup2(devnull, 1)
os.dup2(devnull, 2)
with open(%q, "w") as f:
    f.write(str(os.getpid()))
time.sleep(30)
`, unrelatedPIDFile)
	if err := os.WriteFile(unrelatedScriptPath, []byte(pyCodeUnrelated), 0o755); err != nil {
		t.Fatalf("write unrelated script: %v", err)
	}

	targetMarker := "attempt-439-target"
	unrelatedMarker := "attempt-439-unrelated"
	runner := processRunner{}

	childTarget, err := runner.Spawn(SpawnOptions{
		Argv:       []string{"sh", "-c", fmt.Sprintf("%s %q\nexit 0", py, targetScript)},
		Dir:        dir,
		Env:        []string{"G8S_RUN_MARKER=" + targetMarker},
		ResultPath: filepath.Join(dir, "result_target.json"),
		RunDir:     dir,
		Timeout:    30 * time.Second,
	})
	if err != nil {
		t.Fatalf("spawn target: %v", err)
	}
	<-childTarget.Done()

	childUnrelated, err := runner.Spawn(SpawnOptions{
		Argv:       []string{"sh", "-c", fmt.Sprintf("%s %q\nexit 0", py, unrelatedScriptPath)},
		Dir:        dir,
		Env:        []string{"G8S_RUN_MARKER=" + unrelatedMarker},
		ResultPath: filepath.Join(dir, "result_unrelated.json"),
		RunDir:     dir,
		Timeout:    30 * time.Second,
	})
	if err != nil {
		t.Fatalf("spawn unrelated: %v", err)
	}
	<-childUnrelated.Done()

	targetPID := readPID(t, targetPIDFile)
	unrelatedPID := readPID(t, unrelatedPIDFile)

	t.Cleanup(func() {
		if isPIDAlive(targetPID) {
			if p, err := os.FindProcess(targetPID); err == nil {
				_ = p.Kill()
			}
		}
		if isPIDAlive(unrelatedPID) {
			if p, err := os.FindProcess(unrelatedPID); err == nil {
				_ = p.Kill()
			}
		}
	})

	if !isPIDAlive(targetPID) || !isPIDAlive(unrelatedPID) {
		t.Fatal("both target and unrelated processes must be running before sweep")
	}

	sup := &Supervisor{clock: time.Now}
	// Run sweep targeted ONLY to targetMarker
	sup.sweepAttemptGroup(childTarget, "target-task", targetMarker)

	// Target must be killed
	deadline := time.Now().Add(5 * time.Second)
	for isPIDAlive(targetPID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if isPIDAlive(targetPID) {
		t.Fatal("target process survived sweep")
	}

	// Unrelated process must survive (no false kill!)
	if !isPIDAlive(unrelatedPID) {
		t.Fatal("unrelated process was falsely killed by sweep — marker matching invariant violated")
	}
}

// TestSweepEscapeBoundedCap verifies that candidate PID walking is capped at maxPIDs
// and bounded by time budget (#439).
func TestSweepEscapeBoundedCap(t *testing.T) {
	const syntheticCount = 1000
	const maxCap = 512
	candidates := make([]int, syntheticCount)
	for i := 0; i < syntheticCount; i++ {
		candidates[i] = 100000 + i
	}

	checkedCount := 0
	mockCheck := func(pid int, marker string) bool {
		checkedCount++
		return false
	}

	sup := &Supervisor{clock: time.Now}
	killed, examined := sup.sweepCandidatePIDs(candidates, "budget-task", "budget-marker", mockCheck, maxCap, 5*time.Second)

	if killed != 0 {
		t.Fatalf("expected 0 killed, got %d", killed)
	}
	if examined != maxCap {
		t.Fatalf("expected examined to equal cap %d, got %d", maxCap, examined)
	}
	if checkedCount != maxCap {
		t.Fatalf("expected checkedCount to equal cap %d, got %d", maxCap, checkedCount)
	}

	// Also verify time budget bounds execution
	slowChecked := 0
	slowCheck := func(pid int, marker string) bool {
		slowChecked++
		time.Sleep(5 * time.Millisecond)
		return false
	}
	// 50ms budget with 5ms sleep per check allows ~10 checks, far below 512
	_, examinedBudget := sup.sweepCandidatePIDs(candidates, "budget-task", "budget-marker", slowCheck, maxCap, 50*time.Millisecond)
	if examinedBudget >= maxCap {
		t.Fatalf("time budget did not stop sweep early: examined %d", examinedBudget)
	}
}
