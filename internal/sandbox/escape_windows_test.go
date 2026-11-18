//go:build windows

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func configureDetachedHelper(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}
}

func isTestProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() {
		if cerr := windows.CloseHandle(h); cerr != nil {
			fmt.Fprintf(os.Stderr, "[warn] sandbox test: close handle %d: %v\n", pid, cerr)
		}
	}()
	const windowsStillActive uint32 = 259
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == windowsStillActive
}

func forceKillTestPID(pid int) {
	if pid <= 0 {
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return
	}
	defer func() {
		if cerr := windows.CloseHandle(h); cerr != nil {
			fmt.Fprintf(os.Stderr, "[warn] sandbox test: close terminate handle %d: %v\n", pid, cerr)
		}
	}()
	if terr := windows.TerminateProcess(h, 1); terr != nil {
		fmt.Fprintf(os.Stderr, "[warn] sandbox test: terminate pid %d: %v\n", pid, terr)
	}
}

func TestWindowsSuspendedStartAttachesJobBeforeFirstInstruction(t *testing.T) {
	if err := ResumeProcessThreads(-1); err == nil {
		t.Fatal("expected ResumeProcessThreads(-1) to fail")
	}

	cmd := exec.Command("cmd.exe", "/c", "ping -n 30 127.0.0.1 > nul")
	configureContainedProcAttr(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start suspended child: %v", err)
	}
	release, err := attachContainedHandle(cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("attachContainedHandle: %v", err)
	}
	defer func() {
		release()
	}()

	// Closing the JobObject handle terminates the resumed child tree immediately.
	release()
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case waitErr := <-done:
		t.Logf("JobObject release exited pid %d: %v", cmd.Process.Pid, waitErr)
	case <-time.After(5 * time.Second):
		forceKillTestPID(cmd.Process.Pid)
		t.Fatalf("pid %d did not terminate after JobObject release", cmd.Process.Pid)
	}
	if isTestProcessAlive(cmd.Process.Pid) {
		forceKillTestPID(cmd.Process.Pid)
		t.Fatalf("pid %d still alive after JobObject release", cmd.Process.Pid)
	}
}
