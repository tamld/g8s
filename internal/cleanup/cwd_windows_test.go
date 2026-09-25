//go:build windows

package cleanup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveProcessCWD_Windows(t *testing.T) {
	// 1. Current process should resolve to current working directory
	pid := os.Getpid()
	expectedDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd failed: %v", err)
	}

	cwd := resolveProcessCWD(pid)
	if cwd == "" {
		t.Fatalf("expected non-empty CWD for current PID %d", pid)
	}

	// Normalize case for Windows path comparison
	if !strings.EqualFold(filepath.Clean(cwd), filepath.Clean(expectedDir)) {
		t.Errorf("resolveProcessCWD(%d) = %q; want %q", pid, cwd, expectedDir)
	}

	// 2. Invalid PID <= 0 returns empty string
	if res := resolveProcessCWD(0); res != "" {
		t.Errorf("resolveProcessCWD(0) = %q; want empty string", res)
	}
	if res := resolveProcessCWD(-123); res != "" {
		t.Errorf("resolveProcessCWD(-123) = %q; want empty string", res)
	}

	// 3. Non-existent PID returns empty string
	if res := resolveProcessCWD(9999999); res != "" {
		t.Errorf("resolveProcessCWD(9999999) = %q; want empty string", res)
	}
}
