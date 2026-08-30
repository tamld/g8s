// Package heartbeat implements per-session worker heartbeat tracking and freshness
// observability per DEBT-29.
//
// Workers write atomic JSON heartbeat records into .heartbeat/agy/<sessionID>.json.
// The supervisor and CLI read heartbeats to determine live, stale, or dead workers
// with zero-trust validation and injectable deterministic clocks.
package heartbeat

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/state"
)

// Standard heartbeat execution status values.
const (
	StatusRunning  = string(state.HeartbeatStateRunning)
	StatusIdle     = string(state.HeartbeatStateIdle)
	StatusFinished = string(state.HeartbeatStateFinished)
	StatusFailed   = string(state.HeartbeatStateFailed)
)

// Freshness thresholds.
const (
	ThresholdActive = 60 * time.Second
	ThresholdStale  = 300 * time.Second
)

// Freshness classification levels.
const (
	FreshnessActive = "active" // < 60s
	FreshnessStale  = "stale"  // 60s - 300s
	FreshnessDead   = "dead"   // > 300s
)

// DefaultHeartbeatDir is the default directory where heartbeat files are recorded.
const DefaultHeartbeatDir = ".heartbeat/agy"

// ErrSessionNotFound is returned when no heartbeat file matches the session ID.
var ErrSessionNotFound = errors.New("heartbeat session not found")

// Heartbeat represents the structured JSON payload emitted by a worker.
type Heartbeat struct {
	SessionID   string         `json:"session_id"`
	PID         int            `json:"pid"`
	Binary      string         `json:"binary"`
	CommandLine string         `json:"command_line"`
	StartedAt   time.Time      `json:"started_at"`
	LastUpdate  time.Time      `json:"last_update"`
	Status      string         `json:"status"`
	CurrentStep string         `json:"current_step,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// RecordOption configures optional fields when recording a heartbeat.
type RecordOption func(*Heartbeat)

// WithPID sets the operating system process ID.
func WithPID(pid int) RecordOption {
	return func(h *Heartbeat) {
		h.PID = pid
	}
}

// WithBinary sets the worker executable binary name.
func WithBinary(binary string) RecordOption {
	return func(h *Heartbeat) {
		h.Binary = binary
	}
}

// WithCommandLine sets the worker execution command line.
func WithCommandLine(cmd string) RecordOption {
	return func(h *Heartbeat) {
		h.CommandLine = cmd
	}
}

// WithStartedAt sets the worker process start timestamp.
func WithStartedAt(t time.Time) RecordOption {
	return func(h *Heartbeat) {
		h.StartedAt = t
	}
}

// WithLastUpdate sets an explicit last update timestamp.
func WithLastUpdate(t time.Time) RecordOption {
	return func(h *Heartbeat) {
		h.LastUpdate = t
	}
}

// WithCurrentStep sets the worker's current step description.
func WithCurrentStep(step string) RecordOption {
	return func(h *Heartbeat) {
		h.CurrentStep = step
	}
}

// Store provides operations to read and write worker heartbeat files.
type Store struct {
	baseDir string
	clock   func() time.Time
	mu      sync.RWMutex
}

// NewStore initializes a heartbeat Store targeting baseDir with an optional clock.
func NewStore(baseDir string, clock func() time.Time) *Store {
	if baseDir == "" {
		baseDir = os.Getenv("G8S_HEARTBEAT_DIR")
		if baseDir == "" {
			baseDir = DefaultHeartbeatDir
		}
	}
	if clock == nil {
		clock = time.Now
	}
	return &Store{
		baseDir: baseDir,
		clock:   clock,
	}
}

// BaseDir returns the configured storage directory.
func (s *Store) BaseDir() string {
	return s.baseDir
}

// Record atomically writes the heartbeat for sessionID.
// It writes to a temporary file in the same directory and performs an atomic rename.
func (s *Store) Record(sessionID, status string, metadata map[string]any, opts ...RecordOption) (*Heartbeat, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("session_id must not be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock()

	// Load existing heartbeat if present to preserve StartedAt and unchanged fields
	existing, _ := s.readInternal(sessionID)

	hb := Heartbeat{
		SessionID:  sessionID,
		PID:        os.Getpid(),
		Binary:     "agy",
		StartedAt:  now,
		LastUpdate: now,
		Status:     status,
		Metadata:   metadata,
	}

	if existing != nil {
		if !existing.StartedAt.IsZero() {
			hb.StartedAt = existing.StartedAt
		}
		if existing.Binary != "" {
			hb.Binary = existing.Binary
		}
		if existing.CommandLine != "" {
			hb.CommandLine = existing.CommandLine
		}
		if existing.PID > 0 {
			hb.PID = existing.PID
		}
	}

	for _, opt := range opts {
		opt(&hb)
	}

	if hb.Metadata == nil {
		hb.Metadata = make(map[string]any)
	}

	if err := os.MkdirAll(s.baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("create heartbeat dir: %w", err)
	}

	data, err := json.MarshalIndent(hb, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal heartbeat json: %w", err)
	}

	targetPath := filepath.Join(s.baseDir, sessionID+".json")

	// Generate random suffix for temporary file in the same filesystem directory
	var randBytes [4]byte
	_, _ = rand.Read(randBytes[:])
	tmpPath := filepath.Join(s.baseDir, fmt.Sprintf("%s.tmp.%s", sessionID, hex.EncodeToString(randBytes[:])))

	tmpFile, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("create temp heartbeat file: %w", err)
	}

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("write temp heartbeat file: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("sync temp heartbeat file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("close temp heartbeat file: %w", err)
	}

	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("atomic rename heartbeat file: %w", err)
	}

	return &hb, nil
}

// Status reads and parses the heartbeat for sessionID.
func (s *Store) Status(sessionID string) (*Heartbeat, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readInternal(sessionID)
}

func (s *Store) readInternal(sessionID string) (*Heartbeat, error) {
	targetPath := filepath.Join(s.baseDir, sessionID+".json")
	data, err := os.ReadFile(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
		}
		return nil, fmt.Errorf("read heartbeat file %s: %w", targetPath, err)
	}

	var hb Heartbeat
	if err := json.Unmarshal(data, &hb); err != nil {
		return nil, fmt.Errorf("unmarshal heartbeat json %s: %w", targetPath, err)
	}

	return &hb, nil
}

// IsStale evaluates whether a heartbeat's last_update exceeds maxAge.
func (s *Store) IsStale(sessionID string, maxAge time.Duration) (bool, error) {
	hb, err := s.Status(sessionID)
	if err != nil {
		return true, err
	}

	now := s.clock()
	return now.Sub(hb.LastUpdate) > maxAge, nil
}

// Freshness returns the freshness status level for a session: active, stale, or dead.
func (s *Store) Freshness(sessionID string) (string, error) {
	hb, err := s.Status(sessionID)
	if err != nil {
		return FreshnessDead, err
	}
	return CalculateFreshness(hb.LastUpdate, s.clock()), nil
}

// CalculateFreshness computes freshness level given a lastUpdate timestamp and current time.
func CalculateFreshness(lastUpdate, now time.Time) string {
	if lastUpdate.IsZero() {
		return FreshnessDead
	}
	age := now.Sub(lastUpdate)
	if age < 0 {
		return FreshnessActive
	}
	if age < ThresholdActive {
		return FreshnessActive
	}
	if age <= ThresholdStale {
		return FreshnessStale
	}
	return FreshnessDead
}

// List returns all heartbeat records in the storage directory, sorted by last update descending.
func (s *Store) List() ([]*Heartbeat, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Heartbeat{}, nil
		}
		return nil, fmt.Errorf("read heartbeat directory: %w", err)
	}

	var results []*Heartbeat
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		sessionID := strings.TrimSuffix(entry.Name(), ".json")
		hb, err := s.readInternal(sessionID)
		if err != nil {
			continue
		}
		results = append(results, hb)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].LastUpdate.After(results[j].LastUpdate)
	})

	return results, nil
}

// Package-level convenience functions using default store.

var defaultStore = NewStore(DefaultHeartbeatDir, time.Now)

// Record writes a heartbeat using default settings.
func Record(sessionID, status string, metadata map[string]any, opts ...RecordOption) (*Heartbeat, error) {
	return defaultStore.Record(sessionID, status, metadata, opts...)
}

// Status retrieves a heartbeat using default settings.
func Status(sessionID string) (*Heartbeat, error) {
	return defaultStore.Status(sessionID)
}

// IsStale checks staleness using default settings.
func IsStale(sessionID string, maxAge time.Duration) (bool, error) {
	return defaultStore.IsStale(sessionID, maxAge)
}

// List returns all heartbeats using default settings.
func List() ([]*Heartbeat, error) {
	return defaultStore.List()
}
