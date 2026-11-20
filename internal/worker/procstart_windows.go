//go:build windows

package worker

// processStartTime is a Windows stub: reapOrphans never runs on Windows
// (Job Objects own containment, #415 PR-2) and the recorded start times are
// only compared by the layer-1 reap (#980).
func processStartTime(pid int) (int64, bool) {
	return 0, false
}
