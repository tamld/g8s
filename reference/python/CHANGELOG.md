# Changelog

## 2026-08-22

- Implemented receipt-based write delegation gate:
  - Brain issues time-limited, scope-restricted receipts via `ControlPlane.issue_write_receipt()`.
  - `validate_dispatch()` enforces receipt gate: `workspace_write` without valid receipt is blocked.
  - Receipts are one-time use, auto-expire (max 3600s), and track issuer + consumer_task_id.
  - `revoke_write_receipt()` allows Brain to cancel issued receipts before consumption.
  - `list_active_receipts()` provides audit visibility of outstanding receipts.
- Enhanced `build_contract_prompt()`:
  - Injects `allowed_paths`, `receipt_id`, and `issuer` into delegated worker prompts.
  - Injects wiki engine policy for read-only workers: ALLOWED (query/search/read/classify), FORBIDDEN (write/reflect/orient/claim/bypass).
- Added `--receipt-id` CLI flag to `agy_dispatch.py` for receipt consumption.
- System design documentation: `docs/DESIGN-receipt-delegation.md`.
- 70 new tests across 2 test suites (38 receipt delegation + 32 safety coordination), total 82.

## 2026-07-07

- Added a dependency-light stdio MCP minimum surface:
  - `agy_list_roles`;
  - `agy_list_permissions`;
  - `agy_self_awareness`;
  - `agy_dispatch_task`.
- Kept `workspace_write` blocked at the MCP layer until a separate human-approved plan defines rollback, observability, and tests.
- Added focused MCP server tests with fake dispatch and contract-violation coverage.
- Added local source traceability for the AGY dispatch plugin.
- Preserved the read-only automation hard guard:
  - sandbox stays enabled by default even when permission prompts are skipped;
  - read-only mutation side effects are reported as harness failures.
- Added cross-OS AGY executable resolution:
  - explicit `--agy-bin`;
  - `AGY_BIN`;
  - `PATH` lookup;
  - conservative macOS and Windows home fallbacks;
  - Windows `.exe`, `.cmd`, and `.bat` suffix handling.
- Added focused unit coverage for dispatcher command construction, read-only contract violation detection, and executable resolution.
