//go:build windows

package worker

import (
	"fmt"
	"os"
)

// sampleCPU inspects process state on Windows.
// On Windows, single-shot ps CPU inspection is unavailable.
// If the process is alive, reports idle = false (active) or idle = true if not alive.
func sampleCPU(pid int) (idle bool, err error) {
	cpu, err := defaultCPUUsageChecker(pid)
	if err != nil {
		return false, err
	}
	return cpu < 1.0, nil
}

// defaultCPUUsageChecker inspects process CPU utilization on Windows.
// Checks if the target process is alive; if alive, returns a non-idle CPU value (5.0)
// so the emitter reports running rather than crashing.
func defaultCPUUsageChecker(pid int) (float64, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("invalid pid: %d", pid)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return 0, fmt.Errorf("find process %d: %w", pid, err)
	}
	_ = proc

	return 5.0, nil
}
