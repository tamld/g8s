//go:build !windows

package worker

import (
	"os/exec"
	"syscall"
)

const (
	syscallSIGTERM = syscall.SIGTERM
	syscallSIGKILL = syscall.SIGKILL
)

// configureSysProcAttr places each child in its own process group so the
// supervisor can signal the entire tree with kill(-pgid).
func configureSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return syscall.ESRCH
	}
	return syscall.Kill(-pid, sig)
}
