//go:build !windows

package worker

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// sampleCPU inspects process CPU utilization on Unix-like platforms.
// Returns idle = true if CPU% < 1.0.
func sampleCPU(pid int) (idle bool, err error) {
	cpu, err := defaultCPUUsageChecker(pid)
	if err != nil {
		return false, err
	}
	return cpu < 1.0, nil
}

// defaultCPUUsageChecker inspects process CPU utilization percentage via ps command.
func defaultCPUUsageChecker(pid int) (float64, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("invalid pid: %d", pid)
	}

	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "%cpu=")
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ps %%cpu for pid %d: %w", pid, err)
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return 0, fmt.Errorf("empty %%cpu output for pid %d", pid)
	}

	val, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %%cpu output %q: %w", trimmed, err)
	}

	return val, nil
}
