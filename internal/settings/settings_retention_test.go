package settings

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestEvidenceRetentionSettingsLifecycle(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	mgr, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// 1. Initially unset
	if val, ok := mgr.Get("evidence_retention_days"); ok || val != nil {
		t.Fatalf("expected nil for unset evidence_retention_days, got %v", val)
	}

	// 2. Set valid values (integer-as-string >= 1)
	validCases := []string{"1", "7", "30", "365"}
	for _, tc := range validCases {
		if err := mgr.Set("evidence_retention_days", tc); err != nil {
			t.Fatalf("Set(%q): %v", tc, err)
		}
		val, ok := mgr.Get("evidence_retention_days")
		if !ok || val != tc {
			t.Fatalf("Get: got %v, want %q", val, tc)
		}
	}

	// 3. Set empty value is legal (unlimited retention)
	if err := mgr.Set("evidence_retention_days", ""); err != nil {
		t.Fatalf("Set empty string: %v", err)
	}
	val, ok := mgr.Get("evidence_retention_days")
	if !ok || val != "" {
		t.Fatalf("Get empty string: got %v (ok=%v), want empty string", val, ok)
	}

	// 4. Reload from disk round-trip
	if err := mgr.Set("evidence_retention_days", "14"); err != nil {
		t.Fatalf("Set(14): %v", err)
	}
	mgr2, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("reload NewManager: %v", err)
	}
	val, ok = mgr2.Get("evidence_retention_days")
	if !ok || val != "14" {
		t.Fatalf("reloaded Get: got %v, want 14", val)
	}

	// 5. Unset round-trip
	if err := mgr2.Unset("evidence_retention_days"); err != nil {
		t.Fatalf("Unset: %v", err)
	}
	if _, ok := mgr2.Get("evidence_retention_days"); ok {
		t.Fatalf("expected evidence_retention_days to be unset after Unset")
	}

	// 6. Rejected values: "0", "-3", negative, non-numeric
	invalidCases := []string{"0", "-3", "-1", "abc", "0.5"}
	for _, tc := range invalidCases {
		err := mgr.Set("evidence_retention_days", tc)
		if err == nil {
			t.Fatalf("expected error for Set(%q), got nil", tc)
		}
		if !errors.Is(err, ErrInvalidVal) {
			t.Fatalf("expected ErrInvalidVal for Set(%q), got %v", tc, err)
		}
	}
}
