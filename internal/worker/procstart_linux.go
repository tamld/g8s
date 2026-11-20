//go:build linux

package worker

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// processStartTime returns the kernel start time of pid — /proc/<pid>/stat
// field 22 (starttime, in clock ticks since boot). The raw tick value is
// only ever compared against values recorded by this same helper on the same
// machine/boot (#980), so no HZ conversion is needed for identity matching.
func processStartTime(pid int) (int64, bool) {
	if pid <= 0 {
		return 0, false
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, false
	}
	// Field 2 (comm) may contain spaces and parentheses — parse after the
	// last ')' so the remaining fields start at field 3 (state).
	idx := bytes.LastIndexByte(data, ')')
	if idx < 0 {
		return 0, false
	}
	fields := strings.Fields(string(data[idx+1:]))
	// fields[0] is field 3; starttime (field 22) is therefore index 19.
	if len(fields) < 20 {
		return 0, false
	}
	ticks, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return 0, false
	}
	return ticks, true
}
