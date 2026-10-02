package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/pathutil"
	"modernc.org/sqlite"
)

// TestRenewHeartbeat_BusyRetrySuccess verifies that transient SQLITE_BUSY errors
// during RenewHeartbeat are retried and eventually succeed (#465).
func TestRenewHeartbeat_BusyRetrySuccess(t *testing.T) {
	origAttempts := busyRetryAttempts
	origBackoff := busyBackoff
	t.Cleanup(func() {
		busyRetryAttempts = origAttempts
		busyBackoff = origBackoff
	})
	busyRetryAttempts = 3
	busyBackoff = func(int) time.Duration { return 0 }

	s, _ := newTestStore(t)
	ctx := context.Background()
	task := mustSubmit(t, s, submitReq("busy-retry-success"))
	claimed, err := s.ClaimTask(ctx, "worker-busy", 10)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimTask failed: %v", err)
	}

	calls := 0
	s.leaseExecOverride = func(ctx context.Context, query string, args ...any) (sql.Result, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("database is locked (transient)")
		}
		return s.db.ExecContext(ctx, query, args...)
	}

	if err := s.RenewHeartbeat(ctx, task.TaskID, "worker-busy", 15); err != nil {
		t.Fatalf("RenewHeartbeat want nil after retry, got: %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 exec attempts, got %d", calls)
	}
}

// TestRenewHeartbeat_BusyExhaustion verifies that when busy retries are exhausted,
// the returned error wraps ErrBusy and is distinguishable from ErrLeaseLost (#465).
func TestRenewHeartbeat_BusyExhaustion(t *testing.T) {
	origAttempts := busyRetryAttempts
	origBackoff := busyBackoff
	t.Cleanup(func() {
		busyRetryAttempts = origAttempts
		busyBackoff = origBackoff
	})
	busyRetryAttempts = 3
	busyBackoff = func(int) time.Duration { return 0 }

	s, _ := newTestStore(t)
	ctx := context.Background()
	task := mustSubmit(t, s, submitReq("busy-exhaustion"))
	claimed, err := s.ClaimTask(ctx, "worker-busy", 10)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimTask failed: %v", err)
	}

	calls := 0
	s.leaseExecOverride = func(ctx context.Context, query string, args ...any) (sql.Result, error) {
		calls++
		return nil, errors.New("sqlite_busy: database table is locked")
	}

	err = s.RenewHeartbeat(ctx, task.TaskID, "worker-busy", 15)
	if err == nil {
		t.Fatal("expected error on busy exhaustion, got nil")
	}
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("expected errors.Is(err, ErrBusy) = true, got err: %v", err)
	}
	if errors.Is(err, ErrLeaseLost) {
		t.Fatalf("busy error must NOT wrap ErrLeaseLost, got err: %v", err)
	}

	instID := pathutil.InstanceID()
	if !strings.Contains(err.Error(), instID) {
		t.Fatalf("heartbeat error %q should include instance ID %q", err.Error(), instID)
	}
	if calls != 3 {
		t.Fatalf("expected 3 attempts before exhaustion, got %d", calls)
	}
}

// TestRenewHeartbeat_GenuineExpiry verifies that an expired lease returns ErrLeaseLost,
// which is distinguishable from ErrBusy via errors.Is (#465).
func TestRenewHeartbeat_GenuineExpiry(t *testing.T) {
	fc := newFakeClock()
	s, _ := newTestStoreWithClock(t, fc)
	ctx := context.Background()

	task := mustSubmit(t, s, submitReq("genuine-expiry"))
	claimed, err := s.ClaimTask(ctx, "worker-exp", 10)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimTask failed: %v", err)
	}

	// Advance clock past the 10s lease duration
	fc.Advance(15 * time.Second)

	err = s.RenewHeartbeat(ctx, task.TaskID, "worker-exp", 10)
	if err == nil {
		t.Fatal("expected error on genuine lease expiry, got nil")
	}
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expected errors.Is(err, ErrLeaseLost) = true, got err: %v", err)
	}
	if errors.Is(err, ErrBusy) {
		t.Fatalf("genuine expiry error must NOT be ErrBusy, got err: %v", err)
	}

	instID := pathutil.InstanceID()
	if !strings.Contains(err.Error(), instID) {
		t.Fatalf("lease lost error %q should include instance ID %q", err.Error(), instID)
	}
}

// TestHeartbeat_BusyRetryAndTokenDistinction verifies Heartbeat token checking
// and busy-retry integration (#465).
func TestHeartbeat_BusyRetryAndTokenDistinction(t *testing.T) {
	origAttempts := busyRetryAttempts
	origBackoff := busyBackoff
	t.Cleanup(func() {
		busyRetryAttempts = origAttempts
		busyBackoff = origBackoff
	})
	busyRetryAttempts = 3
	busyBackoff = func(int) time.Duration { return 0 }

	s, _ := newTestStore(t)
	ctx := context.Background()
	task := mustSubmit(t, s, submitReq("hb-token-busy"))
	claimed, err := s.ClaimTask(ctx, "worker-hb", 10)
	if err != nil || claimed == nil || claimed.LeaseToken == nil {
		t.Fatalf("ClaimTask failed: %v", err)
	}

	calls := 0
	s.leaseExecOverride = func(ctx context.Context, query string, args ...any) (sql.Result, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("database is locked")
		}
		return s.db.ExecContext(ctx, query, args...)
	}

	// Valid token with transient busy retry
	if err := s.Heartbeat(ctx, task.TaskID, "worker-hb", *claimed.LeaseToken, 10); err != nil {
		t.Fatalf("Heartbeat with valid token failed after retry: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls for Heartbeat, got %d", calls)
	}
}

// TestWithBusyRetry_InIsolation tests the local copy of the busy-retry helper (#465).
func TestWithBusyRetry_InIsolation(t *testing.T) {
	origAttempts := busyRetryAttempts
	origBackoff := busyBackoff
	t.Cleanup(func() {
		busyRetryAttempts = origAttempts
		busyBackoff = origBackoff
	})
	busyRetryAttempts = 3
	busyBackoff = func(int) time.Duration { return 0 }

	// Non-busy error returns immediately without retry
	nonBusyCalls := 0
	nonBusyErr := errors.New("syntax error")
	err := withBusyRetry(func() error {
		nonBusyCalls++
		return nonBusyErr
	})
	if !errors.Is(err, nonBusyErr) {
		t.Fatalf("expected nonBusyErr, got %v", err)
	}
	if nonBusyCalls != 1 {
		t.Fatalf("expected 1 call for non-busy error, got %d", nonBusyCalls)
	}

	// Busy error retried and succeeds
	busyCalls := 0
	err = withBusyRetry(func() error {
		busyCalls++
		if busyCalls < 2 {
			return errors.New("database is locked")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error after retry, got %v", err)
	}
	if busyCalls != 2 {
		t.Fatalf("expected 2 calls, got %d", busyCalls)
	}

	// isBusyErr edge cases
	if isBusyErr(nil) {
		t.Fatal("nil error should not be busy")
	}
	if !isBusyErr(ErrBusy) {
		t.Fatal("ErrBusy should be recognized as busy")
	}
	if isBusyErr(&sqlite.Error{}) {
		t.Fatal("zero code sqlite.Error should not be busy")
	}
}
