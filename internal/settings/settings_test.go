package settings

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigManagerLifecycle(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	mgr, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// 1. Initially empty
	if val, ok := mgr.Get("default_timeout"); ok || val != nil {
		t.Fatalf("expected nil for unset key, got %v", val)
	}

	// 2. Set allowed key
	if err := mgr.Set("default_timeout", "120s"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if val, ok := mgr.Get("default_timeout"); !ok || val != "120s" {
		t.Fatalf("Get default_timeout = %v, want 120s", val)
	}

	// 3. Set unknown key fails with ErrUnknownKey
	err = mgr.Set("invalid_key_xyz", "some_value")
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("expected ErrUnknownKey, got %v", err)
	}

	// 4. Reload manager from disk
	mgr2, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("NewManager reload: %v", err)
	}
	if val, ok := mgr2.Get("default_timeout"); !ok || val != "120s" {
		t.Fatalf("Reloaded default_timeout = %v, want 120s", val)
	}

	// 5. List
	all := mgr2.List()
	if len(all) != 1 || all["default_timeout"] != "120s" {
		t.Fatalf("List = %v, want map with default_timeout:120s", all)
	}

	// 6. Unset
	if err := mgr2.Unset("default_timeout"); err != nil {
		t.Fatalf("Unset: %v", err)
	}
	if _, ok := mgr2.Get("default_timeout"); ok {
		t.Fatalf("expected key to be unset")
	}
}

func TestAutonomyLevel_Table(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	mgr, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// 1. Default is 0
	val, ok := mgr.Get("autonomy_level")
	if !ok {
		t.Fatal("expected autonomy_level to be reported as set by default")
	}
	if n, okInt := val.(int); !okInt || n != 0 {
		t.Fatalf("default autonomy_level = %v (%T), want 0 (int)", val, val)
	}

	// 2. Setting 1 persists
	if err := mgr.Set("autonomy_level", 1); err != nil {
		t.Fatalf("Set autonomy_level 1: %v", err)
	}

	mgr2, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("NewManager reload: %v", err)
	}
	val2, ok := mgr2.Get("autonomy_level")
	if !ok {
		t.Fatal("reloaded autonomy_level missing")
	}
	switch n := val2.(type) {
	case int:
		if n != 1 {
			t.Fatalf("reloaded autonomy_level = %d, want 1", n)
		}
	case float64:
		if n != 1 {
			t.Fatalf("reloaded autonomy_level = %v, want 1", n)
		}
	default:
		t.Fatalf("unexpected type for reloaded autonomy_level: %T", val2)
	}

	// 3. Table of invalid values: set 2 / "high" / -1 refused with the reserved message
	invalidCases := []struct {
		name  string
		value any
	}{
		{"level 2", 2},
		{"string high", "high"},
		{"negative 1", -1},
		{"level 3", 3},
		{"string 2", "2"},
		{"string -1", "-1"},
		{"non-integer float", 1.5},
		{"string random", "unratified"},
		{"nil value", nil},
	}

	for _, tc := range invalidCases {
		t.Run("invalid_"+tc.name, func(t *testing.T) {
			err := mgr.Set("autonomy_level", tc.value)
			if err == nil {
				t.Fatalf("Set(autonomy_level, %v) succeeded, want error", tc.value)
			}
			if !errors.Is(err, ErrInvalidVal) {
				t.Fatalf("Set(autonomy_level, %v) err = %v, want wrapping ErrInvalidVal", tc.value, err)
			}
			expectedMsg := "higher levels are reserved and unratified"
			if !strings.Contains(err.Error(), expectedMsg) {
				t.Fatalf("Set(autonomy_level, %v) err = %q, want containing %q", tc.value, err.Error(), expectedMsg)
			}
		})
	}

	// 4. Valid values table
	validCases := []struct {
		name  string
		value any
	}{
		{"int 0", 0},
		{"int 1", 1},
		{"string 0", "0"},
		{"string 1", "1"},
		{"int64 0", int64(0)},
		{"int64 1", int64(1)},
		{"float64 0", float64(0)},
		{"float64 1", float64(1)},
	}

	for _, tc := range validCases {
		t.Run("valid_"+tc.name, func(t *testing.T) {
			if err := mgr.Set("autonomy_level", tc.value); err != nil {
				t.Fatalf("Set(autonomy_level, %v) failed: %v", tc.value, err)
			}
		})
	}

	// 5. JSON envelope round-trips
	t.Run("JSON_envelope_round_trip", func(t *testing.T) {
		cfg := Config{
			AutonomyLevel: 1,
		}
		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("json.Marshal(Config): %v", err)
		}
		var unmarshaled Config
		if err := json.Unmarshal(data, &unmarshaled); err != nil {
			t.Fatalf("json.Unmarshal(Config): %v", err)
		}
		switch n := unmarshaled.AutonomyLevel.(type) {
		case float64:
			if n != 1 {
				t.Fatalf("unmarshaled AutonomyLevel = %v, want 1", n)
			}
		case int:
			if n != 1 {
				t.Fatalf("unmarshaled AutonomyLevel = %v, want 1", n)
			}
		default:
			t.Fatalf("unmarshaled AutonomyLevel type = %T, want numeric", unmarshaled.AutonomyLevel)
		}
	})
}
