//go:build windows

package worker

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"syscall"
)

const (
	syscallSIGTERM = syscall.SIGTERM
	syscallSIGKILL = syscall.SIGKILL
)

// configureSysProcAttr configures process creation attributes on Windows.
func configureSysProcAttr(cmd *exec.Cmd) {
	// CREATE_NEW_PROCESS_GROUP = 0x00000200
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x00000200,
	}
}

// killProcessGroup terminates the entire process tree on Windows,
// mirroring the POSIX two-stage contract (#336):
//
//   - SIGTERM stage: taskkill /T without /F requests graceful termination —
//     GUI/windowed children receive WM_CLOSE and console children attached
//     to a control handler get a cleanup window before being terminated.
//   - SIGKILL stage (or any explicit force path): taskkill /T /F hard-terminates.
//
// If the process has already exited, taskkill exit codes 128 (not found)
// and 1 (already terminated) are treated as benign.
func killProcessGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	if sig == syscallSIGKILL {
		return forceKillProcessTree(pid)
	}
	// Graceful stage: taskkill without /F posts WM_CLOSE to top-level
	// windows of the tree and signals console process groups. Processes
	// that ignore the request are cleaned up by the SIGKILL stage after
	// the supervisor's grace period (processChild.Terminate).
	graceful := exec.Command("taskkill", "/T", "/PID", strconv.Itoa(pid))
	_ = graceful.Run() // best effort; survivors are force-killed later
	return nil
}

// forceKillProcessTree hard-terminates the process tree (taskkill /T /F).
func forceKillProcessTree(pid int) error {
	cmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	err := cmd.Run()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// Exit code 128 (process not found) or 1 (already terminated) is benign.
		if exitErr.ExitCode() == 128 || exitErr.ExitCode() == 1 {
			return nil
		}
	}
	return err
}
