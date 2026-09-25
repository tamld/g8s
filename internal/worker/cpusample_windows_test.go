//go:build windows

package worker

import (
	"os"
	"testing"
)

func TestWindowsCPUUsageChecker_CurrentProcess(t *testing.T) {
	pid := os.Getpid()
	usage, err := defaultCPUUsageChecker(pid)
	if err != nil {
		t.Fatalf("defaultCPUUsageChecker(%d) failed for current process: %v", pid, err)
	}
	if usage < 0.0 {
		t.Errorf("expected CPU usage >= 0.0, got %f", usage)
	}

	idle, err := sampleCPU(pid)
	if err != nil {
		t.Fatalf("sampleCPU(%d) failed for current process: %v", pid, err)
	}
	t.Logf("current process pid %d: cpu=%.2f%%, idle=%v", pid, usage, idle)
}

func TestWindowsCPUUsageChecker_NonExistentPID(t *testing.T) {
	// A high PID that is guaranteed not to exist
	nonExistentPID := 99999999
	_, err := defaultCPUUsageChecker(nonExistentPID)
	if err == nil {
		t.Errorf("expected error for non-existent pid %d, got nil", nonExistentPID)
	}

	_, err = sampleCPU(nonExistentPID)
	if err == nil {
		t.Errorf("expected error from sampleCPU for non-existent pid %d, got nil", nonExistentPID)
	}
}

func TestWindowsCPUUsageChecker_InvalidPID(t *testing.T) {
	invalidPIDs := []int{-1, 0, -100}
	for _, pid := range invalidPIDs {
		_, err := defaultCPUUsageChecker(pid)
		if err == nil {
			t.Errorf("expected error for invalid pid %d, got nil", pid)
		}
	}
}
