//go:build windows

package worker

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// stillActive is the Win32 STILL_ACTIVE status code (259).
const stillActive = 259

// sampleCPU inspects process state on Windows.
// Returns idle = true if CPU% < 1.0 or if the process cannot be sampled.
func sampleCPU(pid int) (idle bool, err error) {
	cpu, err := defaultCPUUsageChecker(pid)
	if err != nil {
		return false, err
	}
	return cpu < 1.0, nil
}

// defaultCPUUsageChecker inspects process CPU utilization on Windows using native Win32 APIs.
// Verifies whether the process is alive via OpenProcess and GetExitCodeProcess.
// Calculates CPU utilization percentage from process kernel/user time over elapsed wall-clock time.
func defaultCPUUsageChecker(pid int) (float64, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("invalid pid: %d", pid)
	}

	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, fmt.Errorf("open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)

	var exitCode uint32
	if err := windows.GetExitCodeProcess(h, &exitCode); err != nil {
		return 0, fmt.Errorf("get exit code for pid %d: %w", pid, err)
	}
	if exitCode != stillActive {
		return 0, fmt.Errorf("process %d is not running (exit code %d)", pid, exitCode)
	}

	var creationTime, exitTime, kernelTime, userTime windows.Filetime
	if err := windows.GetProcessTimes(h, &creationTime, &exitTime, &kernelTime, &userTime); err != nil {
		return 0, fmt.Errorf("get process times for pid %d: %w", pid, err)
	}

	var nowFt windows.Filetime
	windows.GetSystemTimeAsFileTime(&nowFt)

	cpuNs := kernelTime.Nanoseconds() + userTime.Nanoseconds()
	elapsedNs := nowFt.Nanoseconds() - creationTime.Nanoseconds()
	if elapsedNs <= 0 {
		elapsedNs = 1
	}

	pct := (float64(cpuNs) / float64(elapsedNs)) * 100.0
	if pct < 0 {
		pct = 0
	}

	return pct, nil
}
