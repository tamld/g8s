package provider

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// CodeDeviceBusy is returned when an F1 worker attempts to acquire a device already leased to another task.
	CodeDeviceBusy = "E_DEVICE_LEASE_BUSY"
	// CodeDeviceNotFound is returned when the requested device ID is not registered in the pool.
	CodeDeviceNotFound = "E_DEVICE_NOT_FOUND"
	// CodeDeviceLeaseMismatch is returned when releasing or renewing with an invalid lease ID.
	CodeDeviceLeaseMismatch = "E_DEVICE_LEASE_MISMATCH"
)

// DeviceLease represents an exclusive single-concurrency lock on a mobile emulator or hardware device (DELTA-38).
type DeviceLease struct {
	LeaseID               string    `json:"lease_id"`
	DeviceID              string    `json:"device_id"`
	Platform              string    `json:"platform"`
	HolderTaskID          string    `json:"holder_task_id"`
	AcquiredAt            time.Time `json:"acquired_at"`
	ExpiresAt             time.Time `json:"expires_at"`
	CleanSnapshotRequired bool      `json:"clean_snapshot_required"`
}

// DeviceSlot describes the registration and current lease status of a device in DeviceLeasePool.
type DeviceSlot struct {
	DeviceID    string       `json:"device_id"`
	Platform    string       `json:"platform"`
	ActiveLease *DeviceLease `json:"active_lease,omitempty"`
}

// DeviceLeasePool enforces strict 1-device = 1-worker mutual exclusion across mobile emulators and hardware targets.
type DeviceLeasePool struct {
	mu      sync.Mutex
	slots   map[string]*DeviceSlot
	counter uint64
	nowFunc func() time.Time
}

// NewDeviceLeasePool constructs an empty DeviceLeasePool.
func NewDeviceLeasePool() *DeviceLeasePool {
	return &DeviceLeasePool{
		slots:   make(map[string]*DeviceSlot),
		nowFunc: func() time.Time { return time.Now().UTC() },
	}
}

// RegisterDevice adds or updates a device slot (e.g., "emulator-5554", platform "android").
func (p *DeviceLeasePool) RegisterDevice(deviceID, platform string) error {
	deviceID = strings.TrimSpace(deviceID)
	platform = strings.ToLower(strings.TrimSpace(platform))
	if deviceID == "" {
		return fmt.Errorf("device_id must not be empty")
	}
	if platform == "" {
		platform = "android"
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.slots[deviceID]; ok {
		existing.Platform = platform
		return nil
	}
	p.slots[deviceID] = &DeviceSlot{
		DeviceID: deviceID,
		Platform: platform,
	}
	return nil
}

// AcquireLease grants an exclusive lease on deviceID to holderTaskID for ttl.
// If an existing lease has expired, it is automatically reaped before granting the new lease.
func (p *DeviceLeasePool) AcquireLease(deviceID, holderTaskID string, ttl time.Duration) (*DeviceLease, error) {
	deviceID = strings.TrimSpace(deviceID)
	holderTaskID = strings.TrimSpace(holderTaskID)
	if deviceID == "" || holderTaskID == "" {
		return nil, fmt.Errorf("device_id and holder_task_id are required")
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	slot, ok := p.slots[deviceID]
	if !ok {
		return nil, fmt.Errorf("%s: device %q is not registered", CodeDeviceNotFound, deviceID)
	}

	now := p.nowFunc()
	if slot.ActiveLease != nil {
		if now.After(slot.ActiveLease.ExpiresAt) {
			slot.ActiveLease = nil
		} else if slot.ActiveLease.HolderTaskID == holderTaskID {
			slot.ActiveLease.ExpiresAt = now.Add(ttl)
			cp := *slot.ActiveLease
			return &cp, nil
		} else {
			return nil, fmt.Errorf("%s: device %q is exclusively leased by task %q until %s",
				CodeDeviceBusy, deviceID, slot.ActiveLease.HolderTaskID, slot.ActiveLease.ExpiresAt.Format(time.RFC3339))
		}
	}

	p.counter++
	lease := &DeviceLease{
		LeaseID:               fmt.Sprintf("dlease-%s-%d", deviceID, p.counter),
		DeviceID:              deviceID,
		Platform:              slot.Platform,
		HolderTaskID:          holderTaskID,
		AcquiredAt:            now,
		ExpiresAt:             now.Add(ttl),
		CleanSnapshotRequired: true,
	}
	slot.ActiveLease = lease
	cp := *lease
	return &cp, nil
}

// ReleaseLease releases an active lease on deviceID if leaseID matches.
func (p *DeviceLeasePool) ReleaseLease(deviceID, leaseID string) error {
	deviceID = strings.TrimSpace(deviceID)
	leaseID = strings.TrimSpace(leaseID)

	p.mu.Lock()
	defer p.mu.Unlock()

	slot, ok := p.slots[deviceID]
	if !ok {
		return fmt.Errorf("%s: device %q is not registered", CodeDeviceNotFound, deviceID)
	}
	if slot.ActiveLease == nil {
		return nil
	}
	if slot.ActiveLease.LeaseID != leaseID {
		return fmt.Errorf("%s: active lease on %q is %q, got %q",
			CodeDeviceLeaseMismatch, deviceID, slot.ActiveLease.LeaseID, leaseID)
	}
	slot.ActiveLease = nil
	return nil
}

// SnapshotStatus returns a deterministic sorted slice of all device slots and their active leases.
func (p *DeviceLeasePool) SnapshotStatus() []DeviceSlot {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.nowFunc()
	out := make([]DeviceSlot, 0, len(p.slots))
	for _, slot := range p.slots {
		item := DeviceSlot{
			DeviceID: slot.DeviceID,
			Platform: slot.Platform,
		}
		if slot.ActiveLease != nil && !now.After(slot.ActiveLease.ExpiresAt) {
			cp := *slot.ActiveLease
			item.ActiveLease = &cp
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].DeviceID < out[j].DeviceID
	})
	return out
}
