//go:build windows

package main

import (
	"golang.org/x/sys/windows"
)

// defaultIsProcessAlive reports whether a PID is alive. Windows: Signal(0)
// is not supported by os.Process (only Kill), so liveness is probed with
// OpenProcess + PROCESS_QUERY_LIMITED_INFORMATION — opening a finished PID
// fails, a live one succeeds (#489).
func defaultIsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	_ = windows.CloseHandle(h)
	return true
}
