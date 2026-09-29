package memory

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"modernc.org/sqlite"
)

func TestIsBusyErr(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "database is locked string",
			err:      errors.New("database is locked"),
			expected: true,
		},
		{
			name:     "database table is locked string",
			err:      errors.New("database table is locked"),
			expected: true,
		},
		{
			name:     "constraint failed string",
			err:      errors.New("constraint failed"),
			expected: false,
		},
		{
			name:     "wrapped database is locked",
			err:      fmt.Errorf("context: %w", errors.New("database is locked")),
			expected: true,
		},
		{
			name:     "sqlite error zero code",
			err:      &sqlite.Error{},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isBusyErr(tc.err)
			if got != tc.expected {
				t.Errorf("isBusyErr(%v) = %v; want %v", tc.err, got, tc.expected)
			}
		})
	}
}

func TestWithBusyRetry_SuccessAfterRetries(t *testing.T) {
	origAttempts := busyRetryAttempts
	origBackoff := busyBackoff
	t.Cleanup(func() {
		busyRetryAttempts = origAttempts
		busyBackoff = origBackoff
	})
	busyRetryAttempts = 3
	busyBackoff = func(int) time.Duration { return 0 }

	calls := 0
	busyErr := errors.New("database is locked")
	err := withBusyRetry(func() error {
		calls++
		if calls < 3 {
			return busyErr
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got: %d", calls)
	}
}

func TestWithBusyRetry_Exhaustion(t *testing.T) {
	origAttempts := busyRetryAttempts
	origBackoff := busyBackoff
	t.Cleanup(func() {
		busyRetryAttempts = origAttempts
		busyBackoff = origBackoff
	})
	busyRetryAttempts = 3
	busyBackoff = func(int) time.Duration { return 0 }

	calls := 0
	sentinelBusyErr := errors.New("database is locked: exhaustion test")
	err := withBusyRetry(func() error {
		calls++
		return sentinelBusyErr
	})

	if !errors.Is(err, sentinelBusyErr) {
		t.Fatalf("expected sentinel error via errors.Is, got: %v", err)
	}
	if err.Error() != sentinelBusyErr.Error() {
		t.Fatalf("expected error text %q, got: %q", sentinelBusyErr.Error(), err.Error())
	}
	if calls != 3 {
		t.Fatalf("expected exactly %d calls, got: %d", busyRetryAttempts, calls)
	}
}

func TestWithBusyRetry_NonBusyError(t *testing.T) {
	origAttempts := busyRetryAttempts
	origBackoff := busyBackoff
	t.Cleanup(func() {
		busyRetryAttempts = origAttempts
		busyBackoff = origBackoff
	})
	busyRetryAttempts = 5
	busyBackoff = func(int) time.Duration { return 0 }

	calls := 0
	nonBusyErr := errors.New("constraint failed: unique index violation")
	err := withBusyRetry(func() error {
		calls++
		return nonBusyErr
	})

	if !errors.Is(err, nonBusyErr) {
		t.Fatalf("expected identical error via errors.Is, got: %v", err)
	}
	if err.Error() != nonBusyErr.Error() {
		t.Fatalf("expected error text %q, got: %q", nonBusyErr.Error(), err.Error())
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 call, got: %d", calls)
	}
}

func TestWithBusyRetry_BackoffInjection(t *testing.T) {
	origAttempts := busyRetryAttempts
	origBackoff := busyBackoff
	t.Cleanup(func() {
		busyRetryAttempts = origAttempts
		busyBackoff = origBackoff
	})

	var recordedAttempts []int
	busyRetryAttempts = 3
	busyBackoff = func(attempt int) time.Duration {
		recordedAttempts = append(recordedAttempts, attempt)
		return 0 // zero duration honored (no sleep)
	}

	start := time.Now()
	calls := 0
	busyErr := errors.New("database is locked")
	err := withBusyRetry(func() error {
		calls++
		return busyErr
	})

	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		t.Fatalf("zero duration not honored, took %v", elapsed)
	}
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got: %d", calls)
	}

	expectedIndexes := []int{0, 1}
	if len(recordedAttempts) != len(expectedIndexes) {
		t.Fatalf("expected %d backoff calls, got %d: %v", len(expectedIndexes), len(recordedAttempts), recordedAttempts)
	}
	for i, idx := range recordedAttempts {
		if idx != expectedIndexes[i] {
			t.Fatalf("expected backoff call %d to have attempt index %d, got %d", i, expectedIndexes[i], idx)
		}
	}
}

func TestBusyBackoff_DefaultRange(t *testing.T) {
	for i := 0; i < 50; i++ {
		d := busyBackoff(i)
		if d < 50*time.Millisecond || d > 200*time.Millisecond {
			t.Fatalf("backoff duration out of [50ms, 200ms] range: %v", d)
		}
	}
}

func TestWorkingContext_StoreLoad_WithBusyRetry(t *testing.T) {
	adapter, _ := setupTestAdapter(t)
	ctx := context.Background()

	wCtx := &WorkingContext{
		TaskID:       "task-busy-test",
		Prompt:       "test prompt",
		Role:         "worker",
		AllowedPaths: []string{"*"},
		Status:       StatusActive,
	}

	err := adapter.StoreWorkingContext(ctx, "task-busy-test", wCtx)
	if err != nil {
		t.Fatalf("StoreWorkingContext failed: %v", err)
	}

	loaded, err := adapter.LoadWorkingContext(ctx, "task-busy-test")
	if err != nil {
		t.Fatalf("LoadWorkingContext failed: %v", err)
	}

	if loaded.TaskID != "task-busy-test" || loaded.Prompt != "test prompt" {
		t.Fatalf("loaded context mismatch: %+v", loaded)
	}
}
