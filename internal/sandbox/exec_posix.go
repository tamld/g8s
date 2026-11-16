//go:build !windows

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func configureContainedProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func attachContainedHandle(_ *exec.Cmd) (func(), error) {
	return nil, nil
}

func terminateContainedProc(cmd *exec.Cmd, _ func()) {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return
	}
	pid := cmd.Process.Pid
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		if kerr := cmd.Process.Kill(); kerr != nil && !errors.Is(kerr, os.ErrProcessDone) {
			fmt.Fprintf(os.Stderr, "[warn] sandbox: terminate pid %d: %v\n", pid, kerr)
		}
	}
}
