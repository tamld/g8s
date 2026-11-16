//go:build linux

package main

import (
	"os/exec"
	"runtime"
	"syscall"
)

func configureWrapExecChild(cmd *exec.Cmd) func() {
	runtime.LockOSThread()
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}
	return runtime.UnlockOSThread
}
