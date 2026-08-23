# Task: Design comprehensive test suite for AGY Dispatch Receipt Delegation System

You are analyzing the receipt-based write delegation system in `/Users/tamld/plugins/agy-dispatch/scripts/`.

## Files to read
1. `/Users/tamld/plugins/agy-dispatch/scripts/agy_control_plane.py` — focus on `write_receipts` table schema (search for `write_receipts`), `issue_write_receipt()` method, `validate_write_receipt()` method
2. `/Users/tamld/plugins/agy-dispatch/scripts/agy_harness.py` — focus on `validate_dispatch()` receipt gate, `PermissionProfile` dataclass, `PERMISSIONS` dict
3. `/Users/tamld/plugins/agy-dispatch/scripts/agy_dispatch.py` — focus on `--receipt-id` CLI arg, how it's passed to `validate_dispatch()`
4. `/Users/tamld/plugins/agy-dispatch/scripts/test_agy_dispatch.py` — existing test patterns

## System Architecture

Brain (Opus) calls `issue_write_receipt(issuer, allowed_paths, ttl_seconds)` → stores receipt in SQLite → returns receipt_id UUID.
Worker (Flash) calls `agy_dispatch.py --permission workspace_write --receipt-id <UUID>` → harness calls `validate_write_receipt()` → if valid, consumes receipt (one-time) and allows mutation.

## Your Output

Generate a COMPLETE Python test file `test_receipt_delegation.py` with pytest. Cover ALL these categories:

### Category 1: Happy Path (5+ tests)
- Issue + validate flow
- Multiple receipts, each consumed independently
- Receipt with various allowed_paths patterns (single file, glob, multiple paths)
- Receipt with different TTL values (boundary: 1s, 3600s)
- validate returns correct issuer and allowed_paths

### Category 2: Security Boundary (8+ tests)
- Re-use consumed receipt → ValueError
- Expired receipt → ValueError (test with real sleep AND with mocked clock)
- Non-existent receipt_id → ValueError
- Empty string receipt_id → ValueError
- SQL injection in receipt_id → must not crash or bypass
- SQL injection in issuer field → must not corrupt DB
- SQL injection in allowed_paths → must not corrupt DB
- workspace_write without receipt_id → harness blocks
- workspace_write with empty string receipt_id → harness blocks
- Concurrent receipt consumption (two threads, only one wins)

### Category 3: Input Validation (5+ tests)
- Issue with empty allowed_paths → ValueError
- Issue with ttl_seconds=0 → ValueError
- Issue with ttl_seconds=-1 → ValueError
- Issue with ttl_seconds=3601 → ValueError
- Issue with ttl_seconds=3600 (boundary) → OK
- Issue with ttl_seconds=1 (boundary) → OK

### Category 4: Integration with Harness (5+ tests)
- validate_dispatch with read_only permission + no receipt → OK (receipt not needed)
- validate_dispatch with automation_read + no receipt → OK
- validate_dispatch with workspace_write + valid receipt → OK, gate dict includes receipt info
- validate_dispatch with workspace_write + expired receipt → ValueError
- validate_dispatch with workspace_write + consumed receipt → ValueError
- Harness error message includes actionable guidance

### Category 5: Worst Case / Edge Cases (5+ tests)
- DB file deleted mid-session → graceful error
- Issue 1000 receipts → no performance degradation (< 1s)
- Receipt with unicode characters in issuer → OK
- Receipt with very long allowed_paths list (100 entries) → OK
- Receipt just expired (0.001s ago) → still rejected
- Two ControlPlane instances pointing to same DB → concurrent safety
- Schema migration: fresh DB creates write_receipts table

### Category 6: Out of Scope / Abuse (3+ tests)
- Worker tries to call issue_write_receipt directly (should work — enforcement is policy, not code)
- Passing receipt_id to read_only permission (should be ignored, not error)
- Manipulating DB directly to set consumed=0 (raw SQL bypass — document as known limitation)

Output the COMPLETE test file. Use `tempfile.TemporaryDirectory` for DB isolation. Use `unittest.mock.patch` for clock mocking. Use `threading` for concurrency tests.
