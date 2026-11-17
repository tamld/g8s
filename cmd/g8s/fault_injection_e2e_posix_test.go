//go:build !windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

func isFaultTestProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	if err != nil {
		return false
	}
	if runtime.GOOS == "linux" {
		if raw, rerr := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat")); rerr == nil {
			if _, after, ok := strings.Cut(string(raw), ") "); ok {
				if strings.HasPrefix(after, "Z") {
					return false
				}
			}
		}
	}
	return true
}

func forceKillFaultTestPID(pid int) {
	if pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
