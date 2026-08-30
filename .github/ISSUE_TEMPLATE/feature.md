---
name: Feature Request
about: Suggest an idea or capability enhancement for g8s
title: "feat: "
labels: ["enhancement"]
assignees: ""
---

## Problem Statement / Motivation
A clear description of the problem or limitation being addressed. Why is this needed?

## Proposed Solution / Design
Detailed description of the proposed feature, CLI interface, MCP tool, or internal engine.

## OpenSpec Reference / Constitution Alignment
- **Target Spec / DELTA**: `spec/openspec/XX-*.md`
- **Constitution Axioms**: Does this strictly adhere to Zero-CGO, 2-Tier governance, and Receipt Delegation?
- **Layer Affected**: `internal/orchestrator/`, `internal/worker/`, `internal/controlplane/`, `cmd/g8s/`, etc.

## Acceptance Criteria / DoD
- [ ] Spec delta authored/updated in `spec/openspec/`
- [ ] Unit & integration tests written before/alongside implementation
- [ ] Zero-CGO build and `-race` test suite passing
- [ ] Clean CLI envelope / JSON schema integration

## Alternatives Considered
Any alternative designs, CLI flags, or architectural trade-offs considered.
