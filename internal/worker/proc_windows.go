//go:build windows

package worker

import (
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
func killProcessGroup(pid int, _ syscall.Signal) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	cmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	return cmd.Run()
}
