## Summary
<!-- Provide a brief explanation of the changes introduced by this PR. -->

## Related Issues / DELTAs
- Closes #
- Spec Delta: `spec/openspec/`

## "Gears" Layer-Ownership Checklist (DEBT-34)
<!-- Verify that this PR adheres to single-layer ownership without disjoint layer collisions -->
- [ ] **Designated Layer Boundary**:
  - `internal/orchestrator/` (supervisor loop, meta-optimizer, mounts)
  - `internal/worker/` (worker supervisor, execution pipe, process isolation)
  - `internal/controlplane/` (shared SQLite WAL schema, state models)
  - `cmd/g8s/` (CLI wiring, envelope serialization)
  - `.github/` / `tools/` / `docs/` (CI/CD, scripts, documentation)
- [ ] **No Disjoint Cross-Layer Bleed**: PR does not simultaneously mutate disjoint layers (e.g. orchestrator + worker in the same PR).

## Quality & Verification Checklist
- [ ] **Zero-CGO Axiom**: `CGO_ENABLED=0 go test ./...` passes.
- [ ] **Race Detector**: `CGO_ENABLED=1 go test -race ./internal/...` passes.
- [ ] **Code Formatting & Linters**: `gofumpt`, `staticcheck`, `errcheck`, `gosec` clean.
- [ ] **AI Anti-Pattern Gate**: `bash tools/ai_lint.sh` passes without violations.
- [ ] **Coverage Invariant**: Aggregate package coverage remains >= 80%.
- [ ] **Language Purity**: 100% professional English, zero non-ASCII diacritics in code/comments.
- [ ] **Receipt / Sandbox Invariants**: No secret leaks, no sensitive path access.

## Manual Verification / Evidence
<!-- Paste test logs, CLI envelope output, or dogfooding evidence here -->
```text

```
