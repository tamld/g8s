//go:build windows

package process

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// TasklistLister is the Windows implementation of ProcessLister using tasklist and taskkill.
type TasklistLister struct{}

func newLister() ProcessLister {
	return &TasklistLister{}
}

// List inspects host processes using tasklist /FO CSV /V /NH and populates ProcessInfo records.
func (l *TasklistLister) List() ([]ProcessInfo, error) {
	cmd := exec.Command("tasklist", "/FO", "CSV", "/V", "/NH")
	out, err := cmd.Output()
	if err != nil {
		// Fallback to basic tasklist if /V is unsupported
		cmd = exec.Command("tasklist", "/FO", "CSV", "/NH")
		out, err = cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("list processes via tasklist: %w", err)
		}
	}

	return parseTasklistCSV(out), nil
}

// parseTasklistCSV parses tasklist CSV output.
// Expected /V columns: "Image Name","PID","Session Name","Session#","Mem Usage","Status","User Name","CPU Time","Window Title"
// Expected basic columns: "Image Name","PID","Session Name","Session#","Mem Usage"
func parseTasklistCSV(data []byte) []ProcessInfo {
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil
	}

	var processes []ProcessInfo
	for _, row := range records {
		if len(row) < 2 {
			continue
		}

		binName := strings.TrimSpace(row[0])
		pidStr := strings.TrimSpace(row[1])
		pid, err := strconv.Atoi(pidStr)
		if err != nil || pid <= 0 {
			continue
		}

		user := ""
		if len(row) >= 7 {
			user = strings.TrimSpace(row[6])
		}

		cleanBin := filepath.Base(binName)
		cleanBin = strings.TrimSuffix(cleanBin, ".exe")

		processes = append(processes, ProcessInfo{
			PID:         pid,
			Binary:      cleanBin,
			CommandLine: binName,
			User:        user,
		})
	}

	return processes
}

// ResolveCWD on Windows returns empty or resolves via PowerShell/WMIC if available.
func (l *TasklistLister) ResolveCWD(pid int) string {
	return ""
}

// Kill terminates a Windows process using taskkill.
func (l *TasklistLister) Kill(pid int) error {
	cmd := exec.Command("taskkill", "/PID", strconv.Itoa(pid))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("taskkill pid %d: %w (%s)", pid, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// KillForce forcefully terminates a Windows process using taskkill /F.
func (l *TasklistLister) KillForce(pid int) error {
	cmd := exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("taskkill /F pid %d: %w (%s)", pid, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// IsAlive checks whether the process is alive on Windows.
func (l *TasklistLister) IsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), strconv.Itoa(pid))
}
