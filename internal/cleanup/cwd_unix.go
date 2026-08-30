//go:build !windows

package cleanup

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// resolveProcessCWD resolves the current working directory of a process by PID.
// On Linux, it reads /proc/<pid>/cwd symlink. On macOS/BSD, it falls back to lsof.
func resolveProcessCWD(pid int) string {
	if pid <= 0 {
		return ""
	}

	// 1. Linux /proc/<pid>/cwd symlink
	procCwd := fmt.Sprintf("/proc/%d/cwd", pid)
	if target, err := os.Readlink(procCwd); err == nil && target != "" {
		return target
	}

	// 2. macOS / BSD fallback via lsof
	if _, err := os.Stat("/proc"); os.IsNotExist(err) {
		cmd := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn")
		out, err := cmd.Output()
		if err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "n/") {
					return strings.TrimPrefix(line, "n")
				}
			}
		}
	}

	return ""
}
