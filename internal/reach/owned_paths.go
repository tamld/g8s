package reach

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tamld/g8s/internal/pathutil"
)

// ErrPathNotRegistered is returned when deleting a path absent from the ledger.
var ErrPathNotRegistered = errors.New("reach: path not registered")

// ErrSymlinkRefused is returned when a registered delete target is a symlink.
var ErrSymlinkRefused = errors.New("reach: symlink refused")

type ownedPathsSnapshot struct {
	Paths []string `json:"paths"`
}

// OwnedPaths tracks filesystem paths the reach layer may delete during cleanup.
type OwnedPaths struct {
	mu    sync.Mutex
	path  string
	paths map[string]struct{}
}

// NewOwnedPaths returns a ledger bound to the default owned-paths snapshot.
func NewOwnedPaths() *OwnedPaths {
	return &OwnedPaths{
		path:  OwnedPathsPath(""),
		paths: make(map[string]struct{}),
	}
}

// OwnedPathsPath resolves the JSON snapshot path for reach owned paths.
func OwnedPathsPath(stateDir string) string {
	if override := strings.TrimSpace(os.Getenv("G8S_REACH_OWNED_PATHS_PATH")); override != "" {
		return override
	}
	if strings.TrimSpace(stateDir) == "" {
		stateDir = pathutil.DefaultStateDir()
	}
	return filepath.Join(stateDir, "reach_owned_paths.json")
}

// Load reads the snapshot; a missing file leaves an empty ledger.
func (o *OwnedPaths) Load() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	data, err := os.ReadFile(o.path) // #nosec G304 -- operator state path
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			o.paths = make(map[string]struct{})
			return nil
		}
		return fmt.Errorf("reach: load owned paths %s: %w", o.path, err)
	}
	var snap ownedPathsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("reach: parse owned paths %s: %w", o.path, err)
	}
	o.paths = make(map[string]struct{}, len(snap.Paths))
	for _, p := range snap.Paths {
		if p == "" {
			continue
		}
		o.paths[p] = struct{}{}
	}
	return nil
}

// Save atomically replaces the snapshot on disk.
func (o *OwnedPaths) Save() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.saveLocked()
}

func (o *OwnedPaths) saveLocked() error {
	list := make([]string, 0, len(o.paths))
	for p := range o.paths {
		list = append(list, p)
	}
	snap := ownedPathsSnapshot{Paths: list}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("reach: marshal owned paths: %w", err)
	}
	return writeFileAtomic(o.path, data)
}

func normalizeOwnedPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("reach: empty path")
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("reach: abs path: %w", err)
	}
	return abs, nil
}

// RegisterOwnedPath records an absolute cleaned path eligible for later deletion.
func (o *OwnedPaths) RegisterOwnedPath(path string) error {
	abs, err := normalizeOwnedPath(path)
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.paths[abs] = struct{}{}
	return o.saveLocked()
}

// RemoveRegistered deletes a registered path from disk and drops it from the ledger.
func (o *OwnedPaths) RemoveRegistered(path string) error {
	abs, err := normalizeOwnedPath(path)
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.paths[abs]; !ok {
		return ErrPathNotRegistered
	}
	fi, err := os.Lstat(abs)
	if err != nil {
		return fmt.Errorf("reach: lstat %s: %w", abs, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return ErrSymlinkRefused
	}
	if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reach: remove %s: %w", abs, err)
	}
	delete(o.paths, abs)
	return o.saveLocked()
}
