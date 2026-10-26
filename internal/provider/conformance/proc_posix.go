//go:build !windows

package conformance

import (
	"errors"
	"os/exec"
	"syscall"
)

// pgidSupported reports whether process-group probes (P4) can run here.
const pgidSupported = true

func configurePgid(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killGroup(pid int) error {
	if pid <= 0 {
		return syscall.ESRCH
	}
	return syscall.Kill(-pid, syscall.SIGKILL)
}

// groupAlive reports whether any process remains in the process group led by pid.
func groupAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(-pid, syscall.Signal(0))
	if err == nil {
		return true
	}
	// EPERM means the group exists but belongs to another user.
	return errors.Is(err, syscall.EPERM)
}
