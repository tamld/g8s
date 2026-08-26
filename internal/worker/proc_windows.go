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

// killProcessGroup terminates the entire process tree on Windows.
// It executes taskkill /T /F /PID <pid> to forcefully terminate the process
// and all child processes spawned by it, preventing orphaned background workers.
// If the process has already exited (taskkill exit code 128 or 1), nil is returned.
func killProcessGroup(pid int, _ syscall.Signal) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
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
