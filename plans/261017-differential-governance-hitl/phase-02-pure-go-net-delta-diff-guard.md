# Phase 2 (WF2): Pure-Go Net-Delta Diff Guard (`#595` — Hypothesis H2)

## 1. Objective
Implement a 100% Pure-Go **Net-Delta Diff Guard (`CheckTypeDiffGuard = "diff-guard"`)** in `internal/verifier` that runs only at the handoff boundary (`g8s verify`), inspects only added lines (`+`) in the Net-Delta ($\Delta = \text{After} - \text{Before}$) across `AllowedPaths`, separates `Hard Invariants` from `Advisory Signals`, and detects dormant (`Cold`) rule re-awakening for **Hypothesis `H2`**.

## 2. Target Files
- `internal/verifier/verifier.go`
- `internal/verifier/diff_guard.go` (new)
- `internal/verifier/diff_guard_test.go` (new)
- `cmd/g8s/verify.go`

## 3. Implementation Details
1. **CheckType Registration**:
   - Add `CheckTypeDiffGuard CheckType = "diff-guard"` in `internal/verifier/verifier.go` and permit it in `ParseRegistry`.
2. **Net-Delta Parser & Guard (`internal/verifier/diff_guard.go`)**:
   - `ParseUnifiedDiffAddedLines(diffText string) map[string][]AddedLine`: extracts only added lines (`+` excluding `+++`) with file path and line number from `git diff` output.
   - Evaluates added lines in `AllowedPaths` against two tiers:
     - **Hard Invariants (`Severity: "hard"`)**: High-confidence secret literals in added code (e.g., `sk-[a-zA-Z0-9]{20,}`, `ghp_[a-zA-Z0-9]{36}`, `AKIA[0-9A-Z]{16}`, `-----BEGIN (RSA|OPENSSH|EC|PRIVATE) KEY-----`). Hard invariant violations fail the check unconditionally (`OK: false`).
     - **Advisory Signals (`Severity: "advisory"`)**:
       1. `SpatialAnchor.DeniedFragments` from `Vault` records matching the file/package (both `APPLIED` [`Hot`] and `DEPRECATED`/`DORMANT` [`Cold`] records; when a `Cold` record matches, flag `DormantReawakened: true` for Hypothesis `H2`).
       2. Tier-0 silent failure patterns in non-test `.go` files (e.g., `_ =` discarding an `err` variable or `catch {}` stub comments).
   - **Override Receipt Support (`TaskRef.OverrideReceiptID` / `--override-reason`)**:
     - When `DiffGuard` finds only `Advisory Signals` and `target.OverrideReason != ""` (or class status is `StatusAdvisory`), the check records the advisories in `Verdict.Advisories` and passes (`OK: true`) with an audit trail.
