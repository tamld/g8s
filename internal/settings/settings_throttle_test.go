package settings

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestSubmitRateLimitSettingsLifecycle(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	mgr, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// 1. Initially unset
	if val, ok := mgr.Get("submit_rate_limit_per_hour"); ok || val != nil {
		t.Fatalf("expected nil for unset submit_rate_limit_per_hour, got %v", val)
	}

	// 2. Set valid values (0 = unlimited, integers >= 0)
	validCases := []struct {
		input any
		want  any
	}{
		{"0", "0"},
		{"3", "3"},
		{"100", "100"},
		{5, 5},
	}
	for _, tc := range validCases {
		if err := mgr.Set("submit_rate_limit_per_hour", tc.input); err != nil {
			t.Fatalf("Set(%v): %v", tc.input, err)
		}
		val, ok := mgr.Get("submit_rate_limit_per_hour")
		if !ok || val != tc.want {
			t.Fatalf("Get: got %v, want %v", val, tc.want)
		}
	}

	// 3. Reload from disk round-trip
	if err := mgr.Set("submit_rate_limit_per_hour", "42"); err != nil {
		t.Fatalf("Set(42): %v", err)
	}
	mgr2, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("reload NewManager: %v", err)
	}
	val, ok := mgr2.Get("submit_rate_limit_per_hour")
	if !ok || val != "42" {
		t.Fatalf("reloaded Get: got %v, want 42", val)
	}

	// 4. Unset round-trip
	if err := mgr2.Unset("submit_rate_limit_per_hour"); err != nil {
		t.Fatalf("Unset: %v", err)
	}
	if _, ok := mgr2.Get("submit_rate_limit_per_hour"); ok {
		t.Fatalf("expected submit_rate_limit_per_hour to be unset after Unset")
	}

	// 5. Rejected values: "-5" rejected, negative ints, non-numeric strings
	invalidCases := []any{"-5", "-1", -5, -1, "abc", "0.5"}
	for _, tc := range invalidCases {
		err := mgr.Set("submit_rate_limit_per_hour", tc)
		if err == nil {
			t.Fatalf("expected error for Set(%v), got nil", tc)
		}
		if !errors.Is(err, ErrInvalidVal) {
			t.Fatalf("expected ErrInvalidVal for Set(%v), got %v", tc, err)
		}
	}
}
