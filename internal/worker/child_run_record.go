package worker

// Spawn-time identity records for attempt children (#980). Every spawned
// child leaves TWO files in its attempt dir under the shared runRoot:
//
//	child.pid       — the process-group leader pid (unchanged format)
//	child.run.json  — the identity record binding that pid to this attempt:
//	                  the spawning worker's pid + start time, and the child's
//	                  own kernel start time.
//
// Multiple worker processes share <state-dir>/runs (multi-coordinator
// topology), so reapOrphans refuses to kill any recorded group whose pid file
// lacks a parseable identity record (legacy or corrupt files are only
// cleaned up) or whose recorded identity does not match the live process.
// This is the same fail-closed invariant as the Layer 2 hasRunMarker gate
// ("NEVER kill by ... heuristics alone"), applied to the layer-1 pid-file
// path.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tamld/g8s/internal/process"
)

// childRunMarkerName is the identity record written beside child.pid at
// spawn (#980).
const childRunMarkerName = "child.run.json"

// childRunRecord is the spawn-time identity manifest of one attempt child.
// StartTime/PPIDStart values are platform-native process start times (see
// processStartTime) and are only ever compared against values recorded by
// the same platform on the same machine.
type childRunRecord struct {
	Schema        int    `json:"schema"`                   // 1
	PID           int    `json:"pid"`                      // process-group leader pid (== child.pid)
	PPID          int    `json:"ppid"`                     // pid of the worker process that spawned this child
	PPIDStart     int64  `json:"ppid_start_time"`          // start time of the spawning worker (0 = unavailable)
	StartTime     int64  `json:"start_time"`               // kernel start time of the child (0 = unavailable)
	StartedUnix   int64  `json:"started_unix"`             // wall-clock spawn time (diagnostics)
	AttemptMarker string `json:"attempt_marker,omitempty"` // G8S_RUN_MARKER value (Layer 2 sweep key, #439)
}

// writeChildRunRecord persists the identity record beside child.pid right
// after a successful spawn. Best-effort: a missing record disables the
// layer-1 reap for that attempt (fail closed), never the run itself.
func writeChildRunRecord(runDir string, pid int, env []string) {
	if strings.TrimSpace(runDir) == "" {
		return
	}
	rec := childRunRecord{
		Schema:      1,
		PID:         pid,
		PPID:        os.Getpid(),
		StartedUnix: time.Now().Unix(),
	}
	if start, ok := processStartTime(rec.PPID); ok {
		rec.PPIDStart = start
	}
	if start, ok := processStartTime(pid); ok {
		rec.StartTime = start
	}
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, runMarkerEnv+"="); ok && v != "" {
			rec.AttemptMarker = v
			break
		}
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return
	}
	markerPath := filepath.Join(runDir, childRunMarkerName)
	if werr := os.WriteFile(markerPath, data, 0o600); werr != nil {
		fmt.Fprintf(os.Stderr, "[warn] worker: write %s: %v\n", markerPath, werr)
	}
}

// readChildRunRecord loads the identity record from an attempt dir. It
// returns nil for missing, corrupt, unknown-schema, or pid-less records —
// every nil result makes reapOrphans fail closed (cleanup without kill).
func readChildRunRecord(attemptDir string) *childRunRecord {
	data, err := os.ReadFile(filepath.Join(attemptDir, childRunMarkerName))
	if err != nil {
		return nil
	}
	var rec childRunRecord
	if json.Unmarshal(data, &rec) != nil || rec.Schema != 1 || rec.PID <= 0 {
		return nil
	}
	return &rec
}

// pidAlive reports whether the OS currently has a process with this pid,
// delegating to the platform lister in internal/process (DEBT-36).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return process.NewLister().IsAlive(pid)
}

// groupKiller is the injectable process-group kill seam used by the layer-1
// orphan reap and the post-run sweep (#980 tests).
type groupKiller func(pid int, sig syscall.Signal) error

// killGroupFn signals a process group through the injected seam, falling
// back to the platform killProcessGroup for struct-literal Supervisors
// (tests build &Supervisor{...} without NewSupervisor).
func (s *Supervisor) killGroupFn(pid int, sig syscall.Signal) error {
	if s != nil && s.killGroup != nil {
		return s.killGroup(pid, sig)
	}
	return killProcessGroup(pid, sig)
}
