//go:build !windows

package orchestrator

import (
	"os/exec"
	"syscall"
)

// configureSysProcAttr places each agy child in its own process group so
// Cancel can signal the entire subtree with kill(-pgid), mirroring
// internal/worker's supervisor-owned containment (#986 A-14).
func configureSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree SIGKILLs the whole process group led by pid (the child's
// own group — Setpgid makes the child its group leader). Callers fall back
// to the direct child when group signaling reports an error.
func killProcessTree(pid int) error {
	if pid <= 0 {
		return syscall.ESRCH
	}
	return syscall.Kill(-pid, syscall.SIGKILL)
}
