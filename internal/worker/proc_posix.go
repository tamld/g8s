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

// groupAlive reports whether any process remains in the process group led
// by pid (the child's group — Setpgid makes the child its own leader).
// EPERM counts as alive: the group exists but is owned by another user.
func groupAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(-pid, 0) == nil
}

// spawnJobHandle is a POSIX stub: attempt containment uses process groups
// (Setpgid + killProcessGroup); Job Objects are Windows-only (#415 PR-2).
func spawnJobHandle(cmd *exec.Cmd) (uintptr, error) {
	return 0, nil
}

// closeJobHandle is a POSIX stub — nothing to close.
func closeJobHandle(h uintptr) {}

// closeJob is a POSIX no-op on the handle (nothing to close — process-
// group containment applies), kept in parity with the Windows path so the
// shared Terminate/collect call sites stay branch-free.
func (c *processChild) closeJob() {
	c.jobClose.Do(func() { closeJobHandle(0) })
}
