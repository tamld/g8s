package provider

import (
	"strings"
	"testing"
	"time"
)

func TestDeviceLeasePoolMutualExclusionAndExpiration(t *testing.T) {
	pool := NewDeviceLeasePool()
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	pool.nowFunc = func() time.Time { return now }

	if err := pool.RegisterDevice("emulator-5554", "android"); err != nil {
		t.Fatalf("RegisterDevice failed: %v", err)
	}

	lease1, err := pool.AcquireLease("emulator-5554", "task-worker-1", 2*time.Minute)
	if err != nil {
		t.Fatalf("AcquireLease for worker 1 failed: %v", err)
	}
	if !lease1.CleanSnapshotRequired || lease1.DeviceID != "emulator-5554" {
		t.Fatalf("unexpected lease1: %+v", lease1)
	}

	// Second worker attempting to acquire the same emulator concurrently must be rejected with E_DEVICE_LEASE_BUSY
	_, errBusy := pool.AcquireLease("emulator-5554", "task-worker-2", 2*time.Minute)
	if errBusy == nil || !strings.Contains(errBusy.Error(), CodeDeviceBusy) {
		t.Fatalf("expected %s when second worker requests busy emulator, got: %v", CodeDeviceBusy, errBusy)
	}

	// Same worker renewing its own lease succeeds idempotently
	renewed, errRenew := pool.AcquireLease("emulator-5554", "task-worker-1", 5*time.Minute)
	if errRenew != nil || renewed.LeaseID != lease1.LeaseID {
		t.Fatalf("expected idempotent lease renewal for same holder, got err=%v lease=%+v", errRenew, renewed)
	}

	// Advance clock past TTL expiration; worker 2 can now acquire the reaped slot
	now = now.Add(6 * time.Minute)
	lease2, errAfterExpiry := pool.AcquireLease("emulator-5554", "task-worker-2", 2*time.Minute)
	if errAfterExpiry != nil {
		t.Fatalf("expected expired lease to be reaped, got error: %v", errAfterExpiry)
	}
	if lease2.HolderTaskID != "task-worker-2" || lease2.LeaseID == lease1.LeaseID {
		t.Fatalf("expected fresh lease for task-worker-2, got: %+v", lease2)
	}
}

func TestDeviceLeasePoolReleaseAndErrors(t *testing.T) {
	pool := NewDeviceLeasePool()
	if err := pool.RegisterDevice("", "android"); err == nil {
		t.Fatalf("expected error registering empty device ID")
	}
	if _, err := pool.AcquireLease("unknown-emu", "task-1", time.Minute); err == nil || !strings.Contains(err.Error(), CodeDeviceNotFound) {
		t.Fatalf("expected %s for unregistered device, got: %v", CodeDeviceNotFound, err)
	}

	_ = pool.RegisterDevice("ios-sim-1", "ios")
	lease, err := pool.AcquireLease("ios-sim-1", "task-ios-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	snap := pool.SnapshotStatus()
	if len(snap) != 1 || snap[0].ActiveLease == nil || snap[0].ActiveLease.HolderTaskID != "task-ios-1" {
		t.Fatalf("unexpected snapshot status: %+v", snap)
	}

	if err := pool.ReleaseLease("ios-sim-1", "wrong-lease-id"); err == nil || !strings.Contains(err.Error(), CodeDeviceLeaseMismatch) {
		t.Fatalf("expected %s on mismatched lease ID, got: %v", CodeDeviceLeaseMismatch, err)
	}

	if err := pool.ReleaseLease("ios-sim-1", lease.LeaseID); err != nil {
		t.Fatalf("expected clean release, got: %v", err)
	}

	// Second worker can immediately acquire after release
	if _, err := pool.AcquireLease("ios-sim-1", "task-ios-2", time.Minute); err != nil {
		t.Fatalf("expected immediate acquisition after release, got: %v", err)
	}
}
