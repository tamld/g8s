//go:build !linux && !darwin && !windows

package worker

// processStartTime is a stub on platforms without a dedicated probe: the
// recorded start time stays 0 (unavailable) and the layer-1 reap fails
// closed on live pids (#980).
func processStartTime(pid int) (int64, bool) {
	return 0, false
}
