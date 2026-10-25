---
name: g8s-mobile-gate
description: "Android/iOS mobile emulator perception & action verification gate (Artemis-compatible ADB/xcrun UI hierarchy dump, screenshot capture, exclusive DeviceLeasePool locking, and ANR/crash logcat trapping)."
domain: testing
version: 1.0.0
last_updated: "2026-10-25"
risk_level: medium
evidence_level: runtime_required
args_syntax: "g8s-mobile-gate [verify|snapshot|interact] --device <emulator-id> --app <pkg> [--lease-ttl 5m] [--json]"
---

# g8s Mobile & Emulator Gate Verification Skill (`DELTA-38`)

The `g8s-mobile-gate` skill equips `F1` (`test-runner` / `verifier`) workers with structured visual perception (`screenshot` + `uiautomator` accessibility hierarchy XML) and deterministic interaction (`tap`, `swipe`, `input`, `deeplink`) against Android (`adb` / Google Artemis MCP) and iOS (`xcrun simctl`) emulators under strict single-concurrency hardware lease control (`DeviceLeasePool`).

---

## 1. Purpose & Threats Defeated

### Threats Defeated
- **Concurrent Emulator Collision**: Two `F1` workers tapping or typing on the same `emulator-5554` instance simultaneously, corrupting each other's UI state. Defeated via `internal/provider.DeviceLeasePool` (`1 device = 1 worker = 1 lease`, `E_DEVICE_LEASE_BUSY`).
- **Dirty Emulator State Across Tasks**: Residual login sessions, permissions, or modal dialogs left behind by a previous task. Defeated via mandatory `adb emu avd snapshot load clean_boot` on lease acquisition.
- **Silent Mobile Runtime Crashes (ANR / Fatal Exception)**: UI automation scripts reporting success while background threads crash or trigger Application Not Responding (ANR). Defeated via `adb logcat -b crash` / `xcrun simctl spawn log` assertion traps.
- **Pixel-Coordinate Fragility**: Hardcoded `(x, y)` taps breaking across screen densities. Defeated by combining accessibility tree node bounds (`uiautomator dump`) with multimodal vision fallback (Google Artemis pattern).

---

## 2. Exclusive Device-Lease Protocol (`DELTA-38`)

Before issuing any `adb` or `xcrun` command, the worker MUST hold an active `DeviceLease`:

1. **Acquire Exclusive Lease**:
   - Pool enforces `max_concurrency = 1` per `device_id` (`emulator-5554`, `ios-sim-uuid`).
   - Concurrent workers targeting a leased device receive `E_DEVICE_LEASE_BUSY`.
2. **Restore Clean Snapshot**:
   - Android: `adb -s <device_id> emu avd snapshot load clean_boot`
   - iOS: `xcrun simctl erase <device_id>` or snapshot restore.
3. **Perceive-Act-Verify Loop (Artemis Pattern)**:
   - **Perceive**: Dump accessibility hierarchy (`adb -s <device_id> exec-out uiautomator dump /dev/tty`) and capture frame (`adb -s <device_id> exec-out screencap -p > screen.png`).
   - **Act**: Resolve target element `resource-id` / `content-desc` bounding box `[x1,y1][x2,y2]` and dispatch `adb -s <device_id> shell input tap <cx> <cy>`.
   - **Verify**: Inspect updated UI tree + assert zero fatal crash entries in `adb -s <device_id> logcat -b crash -d`.
4. **Release Lease**:
   - Release `lease_id` back to `DeviceLeasePool` upon task completion.
