package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const sandboxMarkersSubdir = "sandboxes"

// SandboxMarker records an active worker sandbox launch so crash-recovery
// sweeps (`g8s cleanup --target ghost-process`) can identify and reap orphaned
// G8S_SANDBOX_ID children/grandchildren if the worker process is SIGKILLed (#942).
type SandboxMarker struct {
	SandboxID   string `json:"sandbox_id"`
	WorkerPID   int    `json:"worker_pid"`
	WorktreeDir string `json:"worktree_dir,omitempty"`
	RunDir      string `json:"run_dir,omitempty"`
	CreatedAt   string `json:"created_at"`
}

// SandboxMarkersDir returns the marker directory inside stateDir, or "" if empty.
func SandboxMarkersDir(stateDir string) string {
	trimmed := strings.TrimSpace(stateDir)
	if trimmed == "" {
		return ""
	}
	return filepath.Join(trimmed, sandboxMarkersSubdir)
}

func sanitizeSandboxMarkerID(sandboxID string) string {
	id := strings.TrimSpace(sandboxID)
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\") || strings.ContainsRune(id, '\x00') {
		return ""
	}
	return id
}

// WriteSandboxMarker atomically persists a sandbox marker JSON file under stateDir.
func WriteSandboxMarker(stateDir string, m SandboxMarker) error {
	dir := SandboxMarkersDir(stateDir)
	if dir == "" {
		return errors.New("sandbox: empty state_dir for sandbox marker")
	}
	id := sanitizeSandboxMarkerID(m.SandboxID)
	if id == "" {
		return fmt.Errorf("sandbox: invalid sandbox_id %q", m.SandboxID)
	}
	m.SandboxID = id

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("sandbox: create markers dir %s: %w", dir, err)
	}
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("sandbox: encode marker %s: %w", id, err)
	}

	tmp, err := os.CreateTemp(dir, "."+id+".tmp-*")
	if err != nil {
		return fmt.Errorf("sandbox: create temp marker for %s: %w", id, err)
	}
	tmpPath := tmp.Name()
	if _, werr := tmp.Write(data); werr != nil {
		if cerr := tmp.Close(); cerr != nil {
			werr = fmt.Errorf("%w (close: %v)", werr, cerr)
		}
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sandbox: write marker %s: %w", id, werr)
	}
	if cerr := tmp.Close(); cerr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sandbox: close temp marker %s: %w", id, cerr)
	}
	_ = os.Chmod(tmpPath, 0o600)

	dest := filepath.Join(dir, id+".json")
	if rerr := os.Rename(tmpPath, dest); rerr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sandbox: rename marker %s: %w", id, rerr)
	}
	return nil
}

// RemoveSandboxMarker removes the marker file for sandboxID best-effort.
func RemoveSandboxMarker(stateDir, sandboxID string) {
	dir := SandboxMarkersDir(stateDir)
	id := sanitizeSandboxMarkerID(sandboxID)
	if dir == "" || id == "" {
		return
	}
	if err := os.Remove(filepath.Join(dir, id+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "[warn] sandbox: remove marker %s: %v\n", id, err)
	}
}

// ListSandboxMarkers returns all valid persisted sandbox markers under stateDir.
func ListSandboxMarkers(stateDir string) ([]SandboxMarker, error) {
	dir := SandboxMarkersDir(stateDir)
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("sandbox: read markers dir %s: %w", dir, err)
	}

	var markers []SandboxMarker
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") || strings.HasPrefix(ent.Name(), ".") {
			continue
		}
		p := filepath.Join(dir, ent.Name())
		// #nosec G304 -- path derived exclusively from directory listing under stateDir/sandboxes
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			continue
		}
		var m SandboxMarker
		if jerr := json.Unmarshal(raw, &m); jerr != nil {
			continue
		}
		if sanitizeSandboxMarkerID(m.SandboxID) == "" {
			continue
		}
		markers = append(markers, m)
	}
	return markers, nil
}

// FindSandboxProcesses returns PIDs of live processes tagged with G8S_SANDBOX_ID=sandboxID.
func FindSandboxProcesses(sandboxID string) []int {
	id := sanitizeSandboxMarkerID(sandboxID)
	if id == "" {
		return nil
	}
	return findSandboxProcessesOS(id)
}
