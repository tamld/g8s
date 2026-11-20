//go:build darwin

package worker

import (
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// processStartTime returns the process start time as unix seconds, probed
// via `ps -o lstart=` (same inspection style as hasRunMarker on darwin) with
// a forced C locale so the timestamp format is stable. Second granularity:
// a pid reused within the same wall-clock second as the recorded start could
// false-verify — accepted as negligible for a gate whose alternative was an
// unconditional kill of whatever now owns the pid (#980).
func processStartTime(pid int) (int64, bool) {
	if pid <= 0 {
		return 0, false
	}
	cmd := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	// Force the C locale: ps localizes month/day names from the environment.
	cmd.Env = []string{"LC_ALL=C", "LANG=C"}
	out, err := cmd.Output()
	if err != nil {
		return 0, false
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return 0, false
	}
	ts, perr := time.Parse("Mon Jan _2 15:04:05 2006", line)
	if perr != nil {
		return 0, false
	}
	return ts.Unix(), true
}
