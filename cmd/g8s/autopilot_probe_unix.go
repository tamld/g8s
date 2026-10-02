//go:build !windows

package main

import (
	"errors"
	"os"
	"syscall"
)

// defaultIsProcessAlive reports whether a PID is alive. POSIX: signal 0
// probes existence/permission without delivering a signal. On Windows this
// probe is not supported (Signal only supports Kill) — the tagged Windows
// file provides an OpenProcess-based probe instead (#489).
func defaultIsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}
