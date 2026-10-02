// Package settings manages persistent, atomic user and system configurations for g8s.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/tamld/g8s/internal/pathutil"
)

var (
	ErrUnknownKey = errors.New("unknown configuration key")
	ErrInvalidVal = errors.New("invalid configuration value")
)

// AllowedConfigKeys defines the valid configuration keys and their description.
var AllowedConfigKeys = map[string]string{
	"data_dir":                   "Directory for g8s database and persistent storage",
	"scope":                      "Installation and execution scope (user or system)",
	"evidence_dir":               "Centralized directory for exported task execution receipts and logs",
	"evidence_retention_days":    "Evidence directory retention period in days (empty = unlimited)",
	"default_timeout":            "Default maximum execution duration for submitted tasks (e.g. 60s, 5m)",
	"default_model":              "Default target model for dispatch executions",
	"default_role":               "Default worker role profile for submitted tasks",
	"default_provider":           "Default target provider for dispatch executions and queue submissions",
	"log_level":                  "Verbosity level for daemon and CLI operations (debug, info, warn, error)",
	"submit_rate_limit_per_hour": "Maximum tasks submitted per hour per actor (0 = unlimited)",
}

// Config represents the loaded configuration values.
type Config struct {
	DataDir                string `json:"data_dir,omitempty"`
	Scope                  string `json:"scope,omitempty"`
	EvidenceDir            string `json:"evidence_dir,omitempty"`
	EvidenceRetentionDays  string `json:"evidence_retention_days,omitempty"`
	DefaultTimeout         string `json:"default_timeout,omitempty"`
	DefaultModel           string `json:"default_model,omitempty"`
	DefaultRole            string `json:"default_role,omitempty"`
	DefaultProvider        string `json:"default_provider,omitempty"`
	LogLevel               string `json:"log_level,omitempty"`
	SubmitRateLimitPerHour any    `json:"submit_rate_limit_per_hour,omitempty"`
}

// Manager coordinates atomic reads and writes of the configuration store.
type Manager struct {
	configPath string
	mu         sync.RWMutex
	values     map[string]any
}

// NewManager initializes a configuration manager backed by the given configPath.
func NewManager(configPath string) (*Manager, error) {
	if configPath == "" {
		configDir := pathutil.DefaultConfigDir()
		configPath = filepath.Join(configDir, "config.json")
	}

	mgr := &Manager{
		configPath: configPath,
		values:     make(map[string]any),
	}

	if err := mgr.load(); err != nil {
		return nil, err
	}

	return mgr, nil
}

func (m *Manager) load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.configPath)
	if os.IsNotExist(err) {
		// Default empty config
		return nil
	}
	if err != nil {
		return fmt.Errorf("read config file: %w", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("unmarshal config: %w", err)
	}
	m.values = raw
	return nil
}

// Get returns the value associated with key. If unset, canonical defaults for data_dir,
// scope, and evidence_dir are resolved automatically.
func (m *Manager) Get(key string) (any, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	val, ok := m.values[key]
	if ok && val != nil {
		return val, true
	}

	switch key {
	case "data_dir":
		return pathutil.DefaultDataDir(), true
	case "scope":
		return pathutil.ScopeUser, true
	case "evidence_dir":
		return pathutil.DefaultEvidenceDir(), true
	default:
		return nil, false
	}
}

// List returns a copy of all active configuration keys and values.
func (m *Manager) List() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make(map[string]any, len(m.values))
	for k, v := range m.values {
		out[k] = v
	}
	return out
}

// Set updates the value for a key and saves atomically to disk.
func (m *Manager) Set(key string, value any) error {
	if _, allowed := AllowedConfigKeys[key]; !allowed {
		return fmt.Errorf("%w: %q", ErrUnknownKey, key)
	}

	if key == "evidence_retention_days" {
		if err := validateEvidenceRetentionDays(value); err != nil {
			return err
		}
	}
	if key == "submit_rate_limit_per_hour" {
		if err := validateSubmitRateLimitPerHour(value); err != nil {
			return err
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.values[key] = value
	return m.saveLocked()
}

func validateEvidenceRetentionDays(value any) error {
	var s string
	switch v := value.(type) {
	case string:
		s = strings.TrimSpace(v)
		if s == "" {
			return nil
		}
	default:
		return fmt.Errorf("%w: evidence_retention_days must be string, got %T", ErrInvalidVal, value)
	}

	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("%w: %q is not a valid integer", ErrInvalidVal, s)
	}
	if n < 1 {
		return fmt.Errorf("%w: evidence_retention_days must be >= 1, got %d", ErrInvalidVal, n)
	}
	return nil
}

func validateSubmitRateLimitPerHour(value any) error {
	var n int
	switch v := value.(type) {
	case int:
		n = v
	case int64:
		n = int(v)
	case float64:
		if float64(int(v)) != v {
			return fmt.Errorf("%w: submit_rate_limit_per_hour must be an integer, got %v", ErrInvalidVal, v)
		}
		n = int(v)
	case string:
		s := strings.TrimSpace(v)
		var err error
		n, err = strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("%w: %q is not a valid integer", ErrInvalidVal, s)
		}
	default:
		return fmt.Errorf("%w: submit_rate_limit_per_hour must be integer or string integer, got %T", ErrInvalidVal, value)
	}

	if n < 0 {
		return fmt.Errorf("%w: submit_rate_limit_per_hour must be >= 0, got %d", ErrInvalidVal, n)
	}
	return nil
}

// Unset removes a key from configuration.
func (m *Manager) Unset(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.values, key)
	return m.saveLocked()
}

func (m *Manager) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.configPath), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(m.values, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	tmpPath := m.configPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write temporary config: %w", err)
	}

	if err := os.Rename(tmpPath, m.configPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic save config: %w", err)
	}

	return nil
}
