//go:build !windows

package process

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// PsLister is the Unix implementation of ProcessLister using ps and OS signals.
type PsLister struct{}

func newLister() ProcessLister {
	return &PsLister{}
}

// List inspects all host processes using ps and populates ProcessInfo records.
func (l *PsLister) List() ([]ProcessInfo, error) {
	cmd := exec.Command("ps", "-A", "-o", "pid=,ppid=,user=,command=")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list processes via ps: %w", err)
	}

	return parsePsOutput(out), nil
}

// parsePsOutput parses the stdout lines from ps -A -o pid=,ppid=,user=,command=.
func parsePsOutput(out []byte) []ProcessInfo {
	lines := strings.Split(string(out), "\n")
	var processes []ProcessInfo

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			continue
		}

		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			ppid = 0
		}

		user := fields[2]

		// Extract command line starting after user field to preserve arguments
		var cmdLine string
		userIndex := strings.Index(line, fields[2])
		if userIndex != -1 {
			cmdLine = strings.TrimSpace(line[userIndex+len(fields[2]):])
		} else {
			cmdLine = strings.Join(fields[3:], " ")
		}

		binName := filepath.Base(fields[3])

		// Fast /proc/<pid>/cwd check (Linux)
		var cwd string
		if target, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid)); err == nil {
			cwd = target
		}

		processes = append(processes, ProcessInfo{
			PID:         pid,
			PPID:        ppid,
			User:        user,
			Binary:      binName,
			CommandLine: cmdLine,
			CWD:         cwd,
		})
	}

	return processes
}

// ResolveCWD resolves the current working directory of a specific process.
// On Linux, it reads /proc/<pid>/cwd. On macOS, it falls back to lsof.
func (l *PsLister) ResolveCWD(pid int) string {
	if pid <= 0 {
		return ""
	}

	// 1. Linux /proc/<pid>/cwd
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

// Kill sends SIGTERM to the process.
func (l *PsLister) Kill(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.SIGTERM)
}

// KillForce sends SIGKILL to the process.
func (l *PsLister) KillForce(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.SIGKILL)
}

// IsAlive checks whether the process is currently alive.
func (l *PsLister) IsAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}
