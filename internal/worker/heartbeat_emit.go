// Package worker provides worker lifecycle supervision, execution containment,
// and background heartbeat emission shims per DELTA-29.
package worker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/heartbeat"
)

// EmitterOptions configures the worker heartbeat emitter goroutine.
type EmitterOptions struct {
	Binary       string
	CommandLine  string
	Status       string        // running | idle | finished | failed
	PollInterval time.Duration // default 30s
	Metadata     map[string]string

	// Testable and injectable hooks
	PID        int
	BaseDir    string
	Store      *heartbeat.Store
	Clock      func() time.Time
	CPUChecker func(pid int) (float64, error)
	Context    context.Context
}

// StartHeartbeat starts a heartbeat-emitting goroutine for the worker.
// An initial heartbeat record (status=running) is emitted immediately.
// Every PollInterval, CPU usage is inspected to transition between running and idle.
// Returns a stop function the worker calls on shutdown to emit status=finished (or status=failed).
func StartHeartbeat(sessionID string, opts EmitterOptions) func() {
	if strings.TrimSpace(sessionID) == "" {
		return func() {}
	}

	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}

	pollInterval := opts.PollInterval
	if pollInterval <= 0 {
		pollInterval = 30 * time.Second
	}

	pid := opts.PID
	if pid <= 0 {
		pid = os.Getpid()
	}

	binary := opts.Binary
	if binary == "" {
		binary = "agy"
	}

	commandLine := opts.CommandLine
	initialStatus := opts.Status
	if initialStatus == "" {
		initialStatus = heartbeat.StatusRunning
	}

	store := opts.Store
	if store == nil {
		baseDir := opts.BaseDir
		if baseDir == "" {
			baseDir = heartbeat.DefaultHeartbeatDir
		}
		store = heartbeat.NewStore(baseDir, clock)
	}

	cpuChecker := opts.CPUChecker
	if cpuChecker == nil {
		cpuChecker = defaultCPUUsageChecker
	}

	metadataAny := make(map[string]any, len(opts.Metadata))
	for k, v := range opts.Metadata {
		metadataAny[k] = v
	}

	startedAt := clock()

	// Initial write happens immediately (status=running)
	_, _ = store.Record(sessionID, initialStatus, metadataAny,
		heartbeat.WithPID(pid),
		heartbeat.WithBinary(binary),
		heartbeat.WithCommandLine(commandLine),
		heartbeat.WithStartedAt(startedAt),
		heartbeat.WithLastUpdate(startedAt),
	)

	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}

	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	currentStatus := initialStatus
	var statusMu sync.Mutex

	go func() {
		defer close(doneCh)
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if cpuChecker != nil {
					cpu, err := cpuChecker(pid)
					if err == nil {
						statusMu.Lock()
						if cpu < 1.0 {
							currentStatus = heartbeat.StatusIdle
						} else {
							currentStatus = heartbeat.StatusRunning
						}
						statusMu.Unlock()
					}
				}

				statusMu.Lock()
				st := currentStatus
				statusMu.Unlock()

				now := clock()
				_, _ = store.Record(sessionID, st, metadataAny,
					heartbeat.WithPID(pid),
					heartbeat.WithBinary(binary),
					heartbeat.WithCommandLine(commandLine),
					heartbeat.WithLastUpdate(now),
				)
			}
		}
	}()

	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			close(stopCh)
			<-doneCh

			finalStatus := heartbeat.StatusFinished
			if ctx.Err() != nil {
				finalStatus = heartbeat.StatusFailed
			}

			now := clock()
			_, _ = store.Record(sessionID, finalStatus, metadataAny,
				heartbeat.WithPID(pid),
				heartbeat.WithBinary(binary),
				heartbeat.WithCommandLine(commandLine),
				heartbeat.WithLastUpdate(now),
			)
		})
	}

	return stop
}

// defaultCPUUsageChecker inspects process CPU utilization percentage via ps command.
func defaultCPUUsageChecker(pid int) (float64, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("invalid pid: %d", pid)
	}

	if runtime.GOOS == "windows" {
		return 0, nil
	}

	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "%cpu=")
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ps %%cpu for pid %d: %w", pid, err)
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return 0, fmt.Errorf("empty %%cpu output for pid %d", pid)
	}

	val, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %%cpu output %q: %w", trimmed, err)
	}

	return val, nil
}
