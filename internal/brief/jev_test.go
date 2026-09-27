package brief

// DELTA-22 requirement 3: the flag alone must not invoke scoring (no hook
// in v1); flag + wired hook runs and surfaces the verdict; flag off never
// calls the hook.

import (
	"errors"
	"testing"
)

func TestApplyJevQualityFlagOffNeverCallsHook(t *testing.T) {
	t.Setenv("G8S_BRIEF_JEV_QUALITY", "")
	called := false
	JevQualityHook = func(title, payload string) (string, error) {
		called = true
		return "ok", nil
	}
	defer func() { JevQualityHook = nil }()
	if _, err := ApplyJevQuality("t", "p"); err != nil {
		t.Fatalf("flag off must be a silent no-op, got %v", err)
	}
	if called {
		t.Fatal("hook must not run with the flag off")
	}
}

func TestApplyJevQualityFlagOnWithoutHookDegrades(t *testing.T) {
	t.Setenv("G8S_BRIEF_JEV_QUALITY", "1")
	JevQualityHook = nil
	if _, err := ApplyJevQuality("t", "p"); err == nil {
		t.Fatal("flag on without a hook must return an explicit unavailability error")
	}
}

func TestApplyJevQualityFlagOnWithHookRuns(t *testing.T) {
	t.Setenv("G8S_BRIEF_JEV_QUALITY", "1")
	JevQualityHook = func(title, payload string) (string, error) { return "well-formed", nil }
	defer func() { JevQualityHook = nil }()
	v, err := ApplyJevQuality("t", "p")
	if err != nil || v != "well-formed" {
		t.Fatalf("wired hook must run: %q %v", v, err)
	}
	JevQualityHook = func(title, payload string) (string, error) { return "", errors.New("sensor down") }
	if _, err := ApplyJevQuality("t", "p"); err == nil {
		t.Fatal("hook errors must surface as degradation notices, not panics")
	}
}
