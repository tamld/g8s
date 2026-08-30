//go:build !windows

package provider

import (
	"os/exec"
	"syscall"
)

func configureSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(pid int) error {
	if pid <= 0 {
		return syscall.ESRCH
	}
	return syscall.Kill(-pid, syscall.SIGKILL)
}
