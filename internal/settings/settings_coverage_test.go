package settings

// settings_coverage_test.go — closes the Linux-CI coverage holes in the
// settings package (validators for the auto-retry keys were at 0%, the
// Manager load/save round-trip and Get path under-tested). Found via
// the CI Linux profile after the Wave F keys landed.

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestValidateAutoRetryEnabled_Table(t *testing.T) {
	good := []any{true, false, "true", "false", "1", "0", "  True  "}
	for _, v := range good {
		if err := validateAutoRetryEnabled(v); err != nil {
			t.Errorf("validateAutoRetryEnabled(%v) = %v, want nil", v, err)
		}
	}
	bad := []any{"yes", "on", 1, 0, -1.5, []bool{true}, nil}
	for _, v := range bad {
		if err := validateAutoRetryEnabled(v); err == nil {
			t.Errorf("validateAutoRetryEnabled(%v) = nil, want error", v)
		} else if !errors.Is(err, ErrInvalidVal) {
			t.Errorf("validateAutoRetryEnabled(%v) error must wrap ErrInvalidVal, got %v", v, err)
		}
	}
}

func TestValidateAutoRetryMaxPerTask_Table(t *testing.T) {
	good := []any{0, 1, 2, 10, "5"}
	for _, v := range good {
		if err := validateAutoRetryMaxPerTask(v); err != nil {
			t.Errorf("validateAutoRetryMaxPerTask(%v) = %v, want nil", v, err)
		}
	}
	bad := []any{-1, 11, 100, "many", 2.5, nil}
	for _, v := range bad {
		if err := validateAutoRetryMaxPerTask(v); err == nil {
			t.Errorf("validateAutoRetryMaxPerTask(%v) = nil, want error", v)
		}
	}
}

func TestValidateAutoRetryMaxPerHour_Table(t *testing.T) {
	good := []any{0, 1, 10, 1000, "25"}
	for _, v := range good {
		if err := validateAutoRetryMaxPerHour(v); err != nil {
			t.Errorf("validateAutoRetryMaxPerHour(%v) = %v, want nil", v, err)
		}
	}
	bad := []any{-1, 1001, 5000, "hourly", nil}
	for _, v := range bad {
		if err := validateAutoRetryMaxPerHour(v); err == nil {
			t.Errorf("validateAutoRetryMaxPerHour(%v) = nil, want error", v)
		}
	}
}

func TestValidateSubmitRateLimitPerHour_NumericForms(t *testing.T) {
	good := []any{0, 1, 500, int64(250), 12.0, "42", " 7 "}
	for _, v := range good {
		if err := validateSubmitRateLimitPerHour(v); err != nil {
			t.Errorf("validateSubmitRateLimitPerHour(%v) = %v, want nil", v, err)
		}
	}
	bad := []any{-1, 12.5, "abc", true, nil}
	for _, v := range bad {
		if err := validateSubmitRateLimitPerHour(v); err == nil {
			t.Errorf("validateSubmitRateLimitPerHour(%v) = nil, want error", v)
		}
	}
}

func TestManager_SetGetRoundTrip_AndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	mgr, err := NewManager(path)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	// Get on a fresh manager: unknown key and default handling.
	if _, ok := mgr.Get("submit_rate_limit_per_hour"); ok {
		t.Error("fresh manager must not report an unset key as present")
	}
	// Set + Get round-trip through the save path.
	if err := mgr.Set("submit_rate_limit_per_hour", 25); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok := mgr.Get("submit_rate_limit_per_hour")
	if !ok {
		t.Fatal("Get after Set: key missing")
	}
	if n, okNum := got.(int); !okNum || n != 25 {
		t.Fatalf("in-memory round-trip value = %v (%T), want 25 (int)", got, got)
	}
	// Unknown keys are rejected at Set.
	if err := mgr.Set("not_a_real_key", 1); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("Set(unknown key) = %v, want ErrUnknownKey", err)
	}
	// Persistence: a second manager over the same file sees the value.
	mgr2, err := NewManager(path)
	if err != nil {
		t.Fatalf("NewManager reload: %v", err)
	}
	// Reloaded values arrive via JSON, so numbers come back as float64.
	v, ok := mgr2.Get("submit_rate_limit_per_hour")
	if !ok {
		t.Fatal("reloaded key missing")
	}
	switch n := v.(type) {
	case float64:
		if n != 25 {
			t.Fatalf("reloaded value = %v, want 25", n)
		}
	case int:
		if n != 25 {
			t.Fatalf("reloaded value = %d, want 25", n)
		}
	default:
		t.Fatalf("reloaded value type %T, want numeric", v)
	}
	// Invalid value persists nothing — a defaulted key keeps its default.
	if err := mgr2.Set("auto_retry_max_per_task", -5); err == nil {
		t.Fatal("Set(invalid) must fail")
	}
	if v, ok := mgr2.Get("auto_retry_max_per_task"); ok {
		if n, okNum := v.(int); !okNum || n != 2 {
			t.Fatalf("failed Set must leave the default (2) untouched, got %v (%T)", v, v)
		}
	}
}
