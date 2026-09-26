---
title: "Spec Kit Integration & SDD Registry Optimization"
description: "Hybrid SDD convergence: adopt GitHub Spec Kit as the feature-delivery workflow engine, keep OpenSpec as the capability SSoT; repair registry drift; add machine-verifiable sync guard (cmd/speccheck); wire CI and ship PR"
status: pending
priority: P1
effort: 8h
tags: [sdd, spec-kit, openspec, governance, registry, pure-go, ci]
blockedBy: []
blocks: []
created: 2026-09-25
---

# Spec Kit Integration & SDD Registry Optimization

## Overview

`spec/constitution.md` declares the "Spec Kit Project Constitution Model", but zero Spec Kit tooling exists (no `.specify/`, no `/speckit.*` commands, no routing rule in AGENTS.md), and the OpenSpec registry has live drift: stale links in `spec/openspec/README.md` (DELTA-08/09/10), DELTA-18/DELTA-20 unregistered there, and a DELTA-11 identity conflict (`README.md` says Orchestration Roadmap; `spec/openspec/README.md` says Knowledge Vault). This plan converges the two frameworks — **Spec Kit = feature-delivery workflow, OpenSpec = living capability registry** — and makes future drift machine-detectable instead of prose-policed.

## Approach Decision (Hybrid, not migration)

| Layer | Owner | Rationale |
|---|---|---|
| Feature delivery (specify→clarify→plan→tasks→implement + checklists) | **Spec Kit** | Rich artifacts, 30+ agent support, GitHub-official momentum |
| Capability SSoT (DELTA registry, lifecycle, archive) | **OpenSpec** | Deeply wired into manifest.json, AGENTS.md, 15 delta files mid-refactor |
| Constitution (invariants) | `spec/constitution.md` | Single editing surface; `.specify` copy is generated + hash-pinned |

## Cross-Plan Dependencies

| Relationship | Plan | Status |
|-------------|------|--------|
| None blocking (advisory: [260923-2230-diff-distiller-verifier](../260923-2230-diff-distiller-verifier/plan.md) touches `internal/` Go code only; this plan touches docs/tooling/CI) | 260923-2230 | in-progress |

## Phases

| Phase | Name | Status |
|-------|------|--------|
| 1 | [Scaffold Spec Kit & Unify Constitution](./phase-01-spec-kit-scaffolding.md) | Pending |
| 2 | [Routing Contract & Registry Repair](./phase-02-routing-contract-registry-repair.md) | Pending |
| 3 | [Pilot: cmd/speccheck via Speckit Chain](./phase-03-pilot-speccheck.md) | Pending |
| 4 | [CI Wiring, Docs & PR](./phase-04-ci-pr.md) | Pending |

## Dependencies

- [github/spec-kit](https://github.com/github/spec-kit) via `uvx specify` — **dev-time only**; all generated artifacts committed so the CLI is optional after init (offline fallback).
- Dual-pass CI (CGO_ENABLED=0 suite + CGO_ENABLED=1 `-race`) must stay green throughout.
- Repo is in detached HEAD today → Phase 4 creates `feat/speckit-integration` from `origin/main` before committing.

## Red-Team Notes (self-review)

1. **"Two frameworks confuse agents"** → AGENTS.md remains the only agent entry point; manifest.json gains a machine-readable `spec_workflow` routing block so tooling never guesses.
2. **"Python/uv dev dependency"** → one-time scaffolding; committed artifacts readable offline; constitution governs the shipped binary, not dev tooling.
3. **"Constitution duplication drift"** → GENERATED header + sha256 equality check enforced by `cmd/speccheck` (Phase 3).
4. **"Scope creep mid-refactor"** → docs + one new `cmd/` tree; zero file overlap with in-progress plans; auto-fix mode deferred (YAGNI).
5. **"Registry renumber risk"** → `git mv` preserves history; full grep sweep; final DELTA numbering is a review-gate decision.
