package settings

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestDefaultProviderSettingsLifecycle(t *testing.T) {
	tests := []struct {
		name          string
		providerValue string
	}{
		{
			name:          "codex provider",
			providerValue: "codex",
		},
		{
			name:          "custom provider",
			providerValue: "my-custom-provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "config.json")

			mgr, err := NewManager(configPath)
			if err != nil {
				t.Fatalf("NewManager: %v", err)
			}

			// 1. Initially unset
			if val, ok := mgr.Get("default_provider"); ok || val != nil {
				t.Fatalf("expected nil for unset default_provider, got %v", val)
			}

			// 2. Set default_provider
			if err := mgr.Set("default_provider", tt.providerValue); err != nil {
				t.Fatalf("Set: %v", err)
			}

			// 3. Get default_provider
			if val, ok := mgr.Get("default_provider"); !ok || val != tt.providerValue {
				t.Fatalf("Get default_provider = %v, want %q", val, tt.providerValue)
			}

			// 4. Reload from disk
			mgr2, err := NewManager(configPath)
			if err != nil {
				t.Fatalf("reload NewManager: %v", err)
			}
			if val, ok := mgr2.Get("default_provider"); !ok || val != tt.providerValue {
				t.Fatalf("Reloaded default_provider = %v, want %q", val, tt.providerValue)
			}

			// 5. Unset default_provider
			if err := mgr2.Unset("default_provider"); err != nil {
				t.Fatalf("Unset: %v", err)
			}
			if _, ok := mgr2.Get("default_provider"); ok {
				t.Fatalf("expected default_provider to be unset after Unset")
			}
		})
	}

	t.Run("unknown key still rejected", func(t *testing.T) {
		dir := t.TempDir()
		mgr, err := NewManager(filepath.Join(dir, "config.json"))
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}

		err = mgr.Set("unknown_provider_key_xyz", "value")
		if !errors.Is(err, ErrUnknownKey) {
			t.Fatalf("expected ErrUnknownKey, got %v", err)
		}
	})
}
