# OpenSpec DELTA-23: Native Cross-Platform Agent I/O & Search Engine (`#591`, `#593`)

* **Specification ID**: `DELTA-23`
* **Title**: Native Cross-Platform Agent I/O (`search`, `read`, `edit`, `--select`) & F0/F1/F2 Containment Enforcement
* **Status**: `APPLIED`
* **Milestone**: `M6` (Agent Ergonomics & Zero-Dependency I/O)
* **Target Packages**: `internal/search`, `internal/fsops`, `internal/cli`, `internal/worker`, `internal/harness`, `internal/mcp`, `internal/server`, `cmd/g8s`
* **Foundational Axioms**: Axiom 1 (Zero-CGO Pure-Go Binary), Axiom 2 (Capability-Scoped Write Receipts), ADR-0032 (F0/F1/F2 Containment Tiers)

---

## 1. Context & Motivation

Prior to `DELTA-23`, agents and CI verification gates relied on external POSIX/BSD/GNU shell utilities (`rg`, `grep`, `find`, `sed`, `awk`, `head`, `tail`, `jq`) that behave inconsistently across macOS, Linux, Windows, and minimal container images, and bypass `g8s` capability receipts when invoked via raw shell redirection.

`DELTA-23` embeds a 100% pure-Go, zero-CGO Agent I/O suite directly into the `g8s` binary (`g8s search`, `g8s read`, `g8s edit`, `g8s select` / `--select`) and exposes identical capabilities over the MCP stdio server (`g8s_search`, `g8s_read`, `g8s_edit`) and HTTP daemon API (`/api/v1/fs/search`, `/api/v1/fs/read`, `/api/v1/fs/edit`). Every operation enforces `WriteReceipt.AllowedPaths` and F0/F1/F2 containment boundaries in-process.

---

## 2. ADDED Requirements

### Requirement: Pure-Go concurrent repository search and file enumeration (`internal/search`)

`search.Execute` provides deterministic, bounded-concurrency (`runtime.NumCPU()`) regex and literal content search (`-F`, `-i`, `-g`, `-m`) as well as fast file enumeration (`--files`), automatically honoring `.gitignore` rules, skipping `.git` and binary files (NUL byte detection in first 512 bytes), and enforcing `AllowedPaths` when a Write Receipt or scope root is active.

#### Scenario: literal and regex search with gitignore and binary filtering
<!-- tests: TestSearchExecute, TestSearchAdversarialRedTeam -->
- `search.Execute` matches regex or fixed-string patterns across worker goroutines, skips `.git/` and `.gitignore`-matched paths, ignores NUL-containing binary files, and returns deterministic file/line/column results.

#### Scenario: receipt-scoped search blocks out-of-scope paths
<!-- tests: TestSearchAdversarialRedTeam -->
- When `AllowedPaths` is populated from a Write Receipt, `search.Execute` restricts results and explicit file targets to paths matching the receipt's glob patterns.

---

### Requirement: Bounded line-range file reader with SHA-256 precondition digest (`internal/fsops.Read`)

`fsops.Read` reads UTF-8 text files with 1-indexed `[StartLine, EndLine]` slicing, enforces a binary NUL-byte guard and a maximum byte budget (`DefaultMaxReadBytes = 64 KiB`), computes the full-file `sha256:<hex>` digest for optimistic concurrency control, and validates `ScopeRoot` and `AllowedPaths` after `filepath.EvalSymlinks`.

#### Scenario: line-range slicing and binary file guard
<!-- tests: TestRead -->
- `fsops.Read` returns the requested 1-indexed line slice along with `TotalLines` and `SHA256`, and rejects binary files containing NUL bytes in the first 512 bytes.

---

### Requirement: Atomic compare-and-swap file editor with receipt scope enforcement (`internal/fsops.Edit`)

`fsops.Edit` performs exact-match string replacement (`OldString -> NewString`), rejects ambiguous multi-matches unless `ReplaceAll: true` is set, verifies optional `IfSHA256` preconditions before mutating, preserves original CRLF vs LF line endings and file permissions (`os.FileMode`), writes via a sibling temporary file + `os.Rename` for crash-safe atomicity, and enforces `ScopeRoot` and `AllowedPaths` with symlink traversal protection.

#### Scenario: atomic CAS edit and CRLF preservation
<!-- tests: TestEdit -->
- `fsops.Edit` succeeds when `IfSHA256` matches the on-disk digest, preserves CRLF line endings when editing CRLF files, and rejects stale `IfSHA256` digests or ambiguous multi-occurrence targets when `ReplaceAll` is false.

#### Scenario: symlink escape and out-of-scope paths are blocked
<!-- tests: TestVerifyScope -->
- `fsops.VerifyScope` resolves symlinks on existing files and parent directories and blocks any path that escapes `ScopeRoot` or fails to match `AllowedPaths`.

---

### Requirement: Zero-dependency JSON dot-path selector (`internal/cli.SelectPath`)

`cli.SelectPath` and the global `--select <dot.path>` flag extract scalar or structured values from JSON envelopes (`v1`) or arbitrary JSON inputs (`g8s select`) without `jq` or `python3 -c`.

#### Scenario: dot-path and array-index extraction
<!-- tests: TestSelectPath, TestDefaultSelectorIntegration -->
- `cli.SelectPath` resolves nested object keys and `[i]` array indices (e.g. `.data.receipt_id`, `.data.matches[0].line`) and returns a descriptive error when a key or index is missing.

---

### Requirement: F0/F1/F2 cognitive and delegation boundary propagation (`internal/worker`, `internal/harness`)

When F0 spawns an F1 worker via `internal/worker`, the worker process environment is populated with `G8S_TIER=F1`, `G8S_TASK_ID`, `G8S_PERMISSION`, `G8S_RECEIPT_ID`, and `G8S_SCOPE_ROOT`, and `harness.BuildContractPrompt` injects the `Native I/O & Containment Policy (F1/F2)` directive. When `G8S_TIER=F1`, `fsops.ResolveDelegationContext` automatically binds `G8S_RECEIPT_ID` and `G8S_SCOPE_ROOT` and rejects mutations that lack a Write Receipt.

#### Scenario: F1 tier requires receipt for mutation and inherits env defaults
<!-- tests: TestResolveDelegationContext, TestWorkerChildEnvDelegation, TestNativeIOAndContainmentPolicyRendered -->
- Under `G8S_TIER=F1`, `ResolveDelegationContext` fails closed on mutation when no receipt is present, automatically inherits `G8S_RECEIPT_ID` and `G8S_SCOPE_ROOT`, and worker/harness tests confirm environment and prompt injection.

---

### Requirement: Tri-surface parity across CLI, MCP (`g8s_search`, `g8s_read`, `g8s_edit`), and HTTP (`/api/v1/fs/*`)

All three surfaces expose `search`, `read`, and `edit` backed by the same `internal/search` and `internal/fsops` domain packages and enforce Write Receipt validation on mutation.

#### Scenario: MCP and HTTP filesystem endpoints enforce receipts and execute search/read/edit
<!-- tests: TestMCPSearchReadEditTools, TestMCPEditRequiresReceiptAndEnforcesScope, TestFSEndpoints -->
- `g8s_search`, `g8s_read`, `g8s_edit` and `POST /api/v1/fs/{search,read,edit}` execute in-process and reject unreceipted or out-of-scope edits.
