---
name: Technical Debt / Refactor
about: Track engineering debt, quality gaps, performance bottlenecks, or test parity
title: "debt: "
labels: ["debt"]
assignees: ""
---

## Debt Summary
Clear summary of the technical debt, architectural smell, or code quality gap.

## Rationale & Impact
- What is the friction or risk caused by this debt?
- Why pay this down now?

## Layer & Ownership
- **Affected Layer**:
  - [ ] `internal/orchestrator/` (supervisor)
  - [ ] `internal/worker/` (worker)
  - [ ] `internal/controlplane/` (shared state / SQLite)
  - [ ] `cmd/g8s/` (CLI wiring & envelope)
  - [ ] `.github/workflows/` (CI / CD / Quality gates)
  - [ ] `docs/` (documentation / specs)

## Proposed Refactoring Plan
Step-by-step approach to resolve this debt without regressions.

## Acceptance Criteria / DoD
- [ ] Code refactored to pure Go idiomatic standards
- [ ] No regression in aggregate test coverage (>= 80%)
- [ ] All linters (`gofumpt`, `staticcheck`, `errcheck`, `gosec`, `ai_lint.sh`) green
- [ ] `CGO_ENABLED=0 go test ./...` and `go test -race ./...` pass
