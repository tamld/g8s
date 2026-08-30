package sleep

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/pathutil"
)

// SleepState records the operator's sleep/away cycle status per DEBT-50.
type SleepState struct {
	ID           string    `json:"id"`
	Sleeping     bool      `json:"sleeping"`
	SleepStart   time.Time `json:"sleep_start"`
	WakeTime     time.Time `json:"wake_time,omitempty"`
	Until        string    `json:"until,omitempty"` // e.g. "09:00" or ISO-8601
	Operator     string    `json:"operator"`
	CriticalOnly bool      `json:"critical_only"`
	ReportFormat string    `json:"report_format"` // "voice" | "json"
}

// Store provides persistence for SleepState.
type Store interface {
	RecordSleep(ctx context.Context, state *SleepState) error
	RecordWake(ctx context.Context) (*SleepState, error)
	GetSleepState(ctx context.Context) (*SleepState, error)
	IsSleeping(ctx context.Context) bool
}

// FileStore persists SleepState to a JSON file in the g8s state directory.
type FileStore struct {
	mu       sync.RWMutex
	filePath string
}

// NewFileStore constructs a FileStore at the specified path, or default state dir.
func NewFileStore(path string) *FileStore {
	if path == "" {
		path = filepath.Join(pathutil.DefaultStateDir(), "sleep_state.json")
	}
	return &FileStore{filePath: path}
}

// RecordSleep saves the active sleep state.
func (s *FileStore) RecordSleep(_ context.Context, state *SleepState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if state == nil {
		return fmt.Errorf("sleep: state cannot be nil")
	}
	state.Sleeping = true
	if state.SleepStart.IsZero() {
		state.SleepStart = time.Now().UTC()
	}

	if err := os.MkdirAll(filepath.Dir(s.filePath), 0o755); err != nil {
		return fmt.Errorf("sleep: mkdir state dir: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("sleep: marshal state: %w", err)
	}

	tmpFile := s.filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0o644); err != nil {
		return fmt.Errorf("sleep: write temp state: %w", err)
	}
	if err := os.Rename(tmpFile, s.filePath); err != nil {
		return fmt.Errorf("sleep: rename state file: %w", err)
	}
	return nil
}

// RecordWake marks the sleep cycle ended and returns the finalized state.
func (s *FileStore) RecordWake(_ context.Context) (*SleepState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadUnlocked()
	if err != nil {
		// Return empty state if none exists
		return &SleepState{
			Sleeping:   false,
			SleepStart: time.Now().UTC().Add(-time.Hour),
			WakeTime:   time.Now().UTC(),
		}, nil
	}

	state.Sleeping = false
	state.WakeTime = time.Now().UTC()

	data, err := json.MarshalIndent(state, "", "  ")
	if err == nil {
		_ = os.WriteFile(s.filePath, data, 0o644)
	}
	return state, nil
}

// GetSleepState retrieves the current sleep state.
func (s *FileStore) GetSleepState(_ context.Context) (*SleepState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadUnlocked()
}

// IsSleeping returns true if the operator is currently in a sleep cycle.
func (s *FileStore) IsSleeping(ctx context.Context) bool {
	state, err := s.GetSleepState(ctx)
	if err != nil || state == nil {
		return false
	}
	return state.Sleeping
}

func (s *FileStore) loadUnlocked() (*SleepState, error) {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return nil, fmt.Errorf("sleep: read state file: %w", err)
	}
	var state SleepState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("sleep: unmarshal state: %w", err)
	}
	return &state, nil
}
