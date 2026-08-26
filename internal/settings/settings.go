// Package settings manages persistent, atomic user and system configurations for g8s.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	ErrUnknownKey = errors.New("unknown configuration key")
	ErrInvalidVal = errors.New("invalid configuration value")
)

// AllowedConfigKeys defines the valid configuration keys and their description.
var AllowedConfigKeys = map[string]string{
	"evidence_dir":    "Centralized directory for exported task execution receipts and logs",
	"default_timeout": "Default maximum execution duration for submitted tasks (e.g. 60s, 5m)",
	"default_model":   "Default target model for dispatch executions",
	"default_role":    "Default worker role profile for submitted tasks",
	"log_level":       "Verbosity level for daemon and CLI operations (debug, info, warn, error)",
}

// Config represents the loaded configuration values.
type Config struct {
	EvidenceDir    string `json:"evidence_dir,omitempty"`
	DefaultTimeout string `json:"default_timeout,omitempty"`
	DefaultModel   string `json:"default_model,omitempty"`
	DefaultRole    string `json:"default_role,omitempty"`
	LogLevel       string `json:"log_level,omitempty"`
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
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve user home: %w", err)
		}
		configDir := filepath.Join(home, ".config", "g8s")
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			configDir = filepath.Join(xdg, "g8s")
		}
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

// Get returns the value associated with key.
func (m *Manager) Get(key string) (any, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	val, ok := m.values[key]
	return val, ok
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

	m.mu.Lock()
	defer m.mu.Unlock()

	m.values[key] = value
	return m.saveLocked()
}

// Unset removes a key from configuration.
func (m *Manager) Unset(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.values, key)
	return m.saveLocked()
}

func (m *Manager) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.configPath), 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(m.values, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	tmpPath := m.configPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("write temporary config: %w", err)
	}

	if err := os.Rename(tmpPath, m.configPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic save config: %w", err)
	}

	return nil
}
