# OpenSpec DELTA-38: Exclusive Device-Lease Concurrency Governor & g8s-mobile-gate Emulator Contract

* **Specification ID**: `DELTA-38`
* **Status**: `APPLIED` (Status: implemented)
* **Implementation Status**: `IMPLEMENTED`
* **Target Packages**: `internal/provider`, `skills/g8s-mobile-gate`
* **Milestone**: `M6 (LLM Consumer & Fleet)`
* **Release**: `v0.20.0`

---

## 1. Problem Statement

1. **Stateful Emulator Collision Under Multi-Worker Fan-Out**: Unlike stateless git worktrees, Android (`emulator-5554` / Google Artemis MCP) and iOS (`xcrun simctl`) emulators are stateful singletons. Two `F1` workers interacting with the same emulator concurrently corrupt each other's UI state.
2. **Stale Leases From Hung Mobile Test Runners**: If a mobile test worker hangs or crashes without releasing its emulator lock, the device slot must automatically expire after its TTL so subsequent tasks can reclaim the emulator and restore a clean AVD snapshot.

---

## 2. Normative Requirements

### R1 — Exclusive Single-Concurrency Device Lease Pool (`internal/provider/device_lease.go`)
- `DeviceLeasePool` MUST enforce `1 device = 1 worker = 1 lease` (`AcquireLease(deviceID, holderTaskID, ttl)`).
- Concurrent acquisition attempts by a different `holderTaskID` before `ExpiresAt` MUST fail closed with `E_DEVICE_LEASE_BUSY`.
- Same-holder calls to `AcquireLease` MUST renew the lease TTL idempotently, and expired leases MUST be automatically reaped on subsequent acquisition or status inspection.
- `ReleaseLease(deviceID, leaseID)` MUST verify `leaseID` and return `E_DEVICE_LEASE_MISMATCH` on mismatch.

### R2 — Vendored `g8s-mobile-gate` Skill (`skills/g8s-mobile-gate/SKILL.md`, `skills/manifest.json`)
- Ship `skills/g8s-mobile-gate/SKILL.md` documenting the 4-step Exclusive Device-Lease & Artemis Perceive-Act-Verify protocol (`AcquireLease` $\to$ `snapshot load clean_boot` $\to$ `uiautomator dump` + `screencap` $\to$ `logcat -b crash` verification $\to$ `ReleaseLease`).

---

## 3. Verification Scenarios

#### Scenario: Exclusive Device Lease Pool Mutual Exclusion and TTL Expiration Reaping
<!-- tests: TestDeviceLeasePoolMutualExclusionAndExpiration, TestDeviceLeasePoolReleaseAndErrors -->
- **GIVEN** a registered mobile emulator (`emulator-5554`) in `DeviceLeasePool`
- **WHEN** `task-worker-1` acquires a lease and `task-worker-2` attempts concurrent acquisition before and after TTL expiration or explicit release
- **THEN** concurrent acquisition by `task-worker-2` fails closed with `E_DEVICE_LEASE_BUSY`, idempotent renewal by `task-worker-1` succeeds, and `task-worker-2` acquires a fresh lease once the prior lease expires or is released.
