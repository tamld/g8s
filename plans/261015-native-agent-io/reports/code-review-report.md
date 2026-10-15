# Code Review Report — Native Cross-Platform Agent I/O (`tamld/g8s#593`)

- **Date**: 2026-10-15
- **Issue**: `tamld/g8s#593` (`feat(fsops,cli,mcp,server): native cross-platform Agent I/O (read, edit, --select) across CLI, MCP, and HTTP API`)
- **Spec Delta**: [`spec/openspec/23-native-agent-io-spec.md`](../../../spec/openspec/23-native-agent-io-spec.md) (`DELTA-23`)
- **Workers Dispatched (`F0 → F1`)**:
  - Wave 1 (`WF1`): `4f923a8b-5077-4ab2-a41c-b51cf3287fc2` (Receipt `5967778b-f960-491e-b483-f622907edae1`, commit `7d82577`)
  - Wave 2 (`WF2`): `9393d89a-872d-4fca-ba9b-6226d2efcdca` (Receipt `b75dec75-343c-47d3-8d75-1f9536d0d24f`)

---

## Stage 1 — Spec Compliance Audit (`DELTA-23`)

| Requirement | Implementation | Verification | Status |
| :--- | :--- | :--- | :---: |
| **Cat 2: Bounded File Reader (`fsops.Read`)** | `internal/fsops/fsops.go` (`Read`, 1-indexed line slicing, binary NUL guard, `DefaultMaxReadBytes = 64 KiB`, `sha256:<hex>` digest) | `TestRead` (`83.0%` coverage) | PASS |
| **Cat 3: Atomic CAS File Editor (`fsops.Edit`)** | `internal/fsops/fsops.go` (`Edit`, exact string match, ambiguity guard when `ReplaceAll=false`, `IfSHA256` precondition check, CRLF/LF preservation, sibling temp-file + `os.Rename`) | `TestEdit`, `TestVerifyScope` (`83.0%` coverage) | PASS |
| **Cat 4: JSON Dot-Path Selector (`cli.SelectPath`)** | `internal/cli/select.go` & `internal/cli/envelope.go` (`SelectPath`, `SetDefaultSelector`, global `--select` interceptor + `g8s select` subcommand) | `TestSelectPath`, `TestDefaultSelectorIntegration` (`89.0%` coverage) | PASS |
| **F0/F1/F2 Containment Enforcement** | `internal/worker/worker.go` (`G8S_TIER=F1`, `G8S_TASK_ID`, `G8S_PERMISSION`, `G8S_RECEIPT_ID`, `G8S_SCOPE_ROOT` env propagation), `internal/harness/harness.go` (`Native I/O & Containment Policy (F1/F2)` prompt block), `internal/fsops/fsops.go` (`ResolveDelegationContext`) | `TestResolveDelegationContext`, `TestWorkerChildEnvDelegation`, `TestNativeIOAndContainmentPolicyRendered` | PASS |
| **Tri-Surface Parity (CLI, MCP, HTTP)** | `cmd/g8s/fsops.go` (`g8s read`, `g8s edit`, `g8s select`), `internal/mcp/tools_ext.go` (`g8s_search`, `g8s_read`, `g8s_edit`), `internal/server/server.go` (`POST /api/v1/fs/{search,read,edit}`) | `TestRunReadEditSelect_CLI`, `TestMCPSearchReadEditTools`, `TestMCPEditRequiresReceiptAndEnforcesScope`, `TestFSEndpoints` | PASS |
| **CI Gate & Docs Migration** | `tools/ai_lint.sh`, `tools/brief_lint.sh`, `README.md`, `AGENTS.md`, `offer/SEARCH_GUIDE.md`, `skills/g8s-supervisor/SKILL.md`, `manifest.json`, `spec/openspec/README.md` | `ci_spec_code_sync.sh`, `ci_structure_sync.sh`, `ci_link_integrity.sh`, `ai_lint_test.sh`, `brief_lint_test.sh` | PASS |

---

## Stage 2 — Code Quality & Static Analysis Audit

1. **Remediation Applied During Review**:
   - **Finding `CR-593-1` (`errcheck` in `internal/cli/select.go`)**: Replaced `atomic.Value` type assertion in `DefaultSelector()` with `sync.RWMutex` + `string` (`defaultSelectorMu` / `defaultSelectorVal`) to eliminate unchecked type assertion warnings in `internal/` library code.
   - **Finding `CR-593-2` (`unused` helper in `cmd/g8s/fsops.go`)**: Removed dead `openStore()` helper left by F1 worker in `cmd/g8s/fsops.go` (`resolveReceiptAllowedPaths` opens `receipt.NewReceiptManager` directly).
   - **Finding `CR-593-3` (Layer Ownership Check `tools/ci_layer_check.sh`)**: Split commits along architectural boundaries (`W=1 C=0` in Wave 1 commit `7d82577`, `W=0 C=1` in Wave 2/3 commit) to honor `DEBT-34` layer isolation.
2. **Static Analysis & Linter Gates**:
   - `GOTOOLCHAIN=go1.26.9 golangci-lint run`: **0 issues**.
   - `bash tools/ai_lint.sh && bash tools/ai_lint_test.sh`: **8/8 checks clean**, all self-tests passed.
   - `bash tools/brief_lint.sh && bash tools/brief_lint_test.sh`: **3/3 checks clean**, all self-tests passed.

---

## Stage 3 — Adversarial Security & Boundary Review

1. **Symlink Traversal & Scope Escape (`fsops.VerifyScope`)**:
   - Evaluated via `filepath.EvalSymlinks` on both the scope root and the target path (or its nearest existing ancestor directory when creating/replacing files). Any symlink pointing outside `ScopeRoot` or failing `AllowedPaths` glob matching is rejected before I/O occurs.
2. **Unreceipted Mutation Prevention Across Surfaces**:
   - **CLI (`g8s edit`)**: When `G8S_TIER=F1`, `ResolveDelegationContext` fails closed if neither `--receipt-id` nor `G8S_RECEIPT_ID` is present, and verifies `!rec.Consumed` in SQLite.
   - **MCP (`g8s_edit`)**: Requires an explicit `receipt_id` (or `G8S_RECEIPT_ID`) and verifies the receipt via `ReceiptVerifier` before calling `fsops.Edit`.
   - **HTTP (`POST /api/v1/fs/edit`)**: Protected by bearer-token `authMiddleware`, `http.MaxBytesReader` (`1 MiB` DoS cap), and mandatory `ReceiptVerifier` check (`403 E_FORBIDDEN` on missing/consumed/out-of-scope receipts).
3. **Crash-Safe Atomic File Replacement**:
   - `fsops.Edit` writes to a sibling `.g8s-edit-*` temporary file in the same directory, syncs and closes the descriptor, applies original `os.FileMode` permissions, and executes an atomic `os.Rename` only after `IfSHA256` precondition validation succeeds.

---

## Final Verdict: **APPROVED FOR MERGE & PUSH**
