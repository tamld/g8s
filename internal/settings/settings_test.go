package settings

import (
	"errors"
	"path/filepath"
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
