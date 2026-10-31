---
title: "Coordinator interoperation with three-dimensional capability awareness"
description: "Connect existing coordinator sessions without human message relaying, while retaining g8s worker containment and evidence-based acceptance."
status: completed
priority: P1
branch: codex/coordinator-mesh-experiment
tags: [feature, backend, experimental]
blockedBy: []
blocks: []
created: 2026-10-30
---

# Coordinator interoperation experiment

## Outcome and authority

The operator authorized one debate round, a frozen execution plan, bounded g8s workers, and a connected local experiment. The subsequent D2 instruction makes Codex, AGY and Zcode equal peers solving one shared problem. Task ownership and integration are temporary roles acquired through a scoped protocol, never permanent Codex privileges. No human paste relay should be needed for the experiment.

The baseline is `f3ced6add2f48951fe669031d826fe9b5570d5c8`. Work happens on this isolated branch. Authority is local and reversible; publishing, merge, release, credential access, and changes to other repositories are outside this packet.

## Phases

| Phase | Name | Status |
|---|---|---|
| 0 | [Native peer connection and one-round debate](./phase-00-peer-bootstrap.md) | Completed |
| 1 | [Protocol contract and negative controls](./phase-01-protocol-contract.md) | Completed |
| 2 | [Coordinator core, adapters and CLI integration](./phase-02-implementation.md) | Completed |
| 3 | [Native inbox and recovery drill](./phase-03-native-drill.md) | Completed |
| 4 | [RT-MESH v1 real-time protocol and R1–R4 drills](./realtime-protocol.md) | Completed |

## Dependencies and prior work

The existing stratum-federation and v0.20 worker campaigns supply reusable mechanisms, not unproven mesh guarantees. No unfinished matching mesh plan was found in the initial scoped scan. Reuse the current worker queue, receipts, worktrees, CLI envelopes and explicit charter verification where their semantics fit. Detailed final scope and ownership live in [Design and execution contract](./design.md).

## Deterministic campaign checklist

- [x] Identify clean baseline and current executable.
- [x] Create isolated experiment branch/worktree.
- [x] Prove a private runtime can pass doctor.
- [x] Locate the existing AGY and Zcode sessions.
- [x] Send one independent debate request to each peer.
- [x] Validate both peer responses and close R1 without another debate round.
- [x] Record the selected design, rejected alternatives and acceptance tests.
- [x] Confirm each execution owner accepted its exact work package.
- [x] Complete g8s worker preflights and evaluate their actual results.
- [x] Run a bootstrap connected exchange without human message relaying.
- [x] Separate demonstrated capabilities from remaining implementation work.
- [x] Run the artifact review and final zero-change check on the primary checkout.
- [x] Ratify RT-MESH v1 (`realtime-protocol.md`) and pass R1–R4 live interop drills plus `internal/mesh/drills_test.go` regression suite.
- [x] Complete K5 programmatic quota self-observation (`status=observed` across 3 Antigravity windows) and parallel slice convergence.

## Acceptance boundaries

Delivery, attention, turn start, response and supervisor acceptance are separate facts. A quality score is not remaining quota. A text label is not a cryptographic signature. A lease or checksum is not automatic write authority. Missing evidence remains unknown. Runtime receipts remain outside tracked project history; only sanitized conclusions enter this plan.

## Verification route

Planning artifacts: source-bound peer review, recorded decision dispositions, local-link validation and `git diff --check` including inspection of untracked additions. Worker evidence: task-bound get/receipt records plus packet-specific oracles, never exit status alone. Implementation: meaningful negative controls, appropriate Go tests and declaration/CLI/spec gates before integration.

## Finalized Phase 3 & 4 Convergence Summary (2026-10-31)

- **Core Implementation (`internal/mesh/*`, `cmd/g8s/coordinator_mesh.go`, `internal/mcp/mesh.go`, `DELTA-46`)**: SQLite WAL peer ledger with 10 deterministic operations (`register`, `quota-publish`, `send`, `inbox`, `ack`, `claim`, `release`, `accept`, `snapshot`, `wait`), CLI/MCP parity (`g8s coordinator mesh` and `g8s_coordinator_mesh`), and 16 passing unit/regression tests (`claims_test.go`, `messages_test.go`, `drills_test.go`, `internal/mcp/mesh_test.go`).
- **RT-MESH v1 Real-Time Protocol (`realtime-protocol.md`)**: Ratified across peers with verified R1 long-poll wake latency (`107ms`), R2 4-claimer single-winner CAS race + `context_revision` fence, R3 `message_id` idempotent echo vs. digest-collision rejection, and R4 cross-platform live round-trip (`25s`).
- **Three-Platform Ledger Convergence (`seq1..seq34`) & K5 Programmatic Quota Self-Check**: Codex, AGY, and ZCode coordinated handoffs, claims, and artifact acceptances through the shared SQLite ledger; AGY verified programmatic self-observation of all three quota windows (`gemini_weekly`, `gemini_five_hour`, `claude_gpt_weekly`) via `orca account list --json` (`antigravity-cli`) and `orca terminal read`.
