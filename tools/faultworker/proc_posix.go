//go:build !windows

package main

import (
	"os/exec"
	"syscall"
	"time"
)

func spawnDetachedSleepChild(exe string, d time.Duration) (int, error) {
	cmd := exec.Command(exe, "--sleep-child", d.String())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	go func() {
		_ = cmd.Wait()
	}()
	return pid, nil
}
