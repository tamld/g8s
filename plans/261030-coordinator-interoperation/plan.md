---
title: "Coordinator interoperation with three-dimensional capability awareness"
description: "Connect existing coordinator sessions without human message relaying, while retaining g8s worker containment and evidence-based acceptance."
status: in-progress
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
| 1 | [Protocol contract and negative controls](./phase-01-protocol-contract.md) | Ready under D2 |
| 2 | [Coordinator core, adapters and CLI integration](./phase-02-implementation.md) | Pending phase 1 |
| 3 | [Native inbox and recovery drill](./phase-03-native-drill.md) | Pending phase 2 |

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

## Acceptance boundaries

Delivery, attention, turn start, response and supervisor acceptance are separate facts. A quality score is not remaining quota. A text label is not a cryptographic signature. A lease or checksum is not automatic write authority. Missing evidence remains unknown. Runtime receipts remain outside tracked project history; only sanitized conclusions enter this plan.

## Verification route

Planning artifacts: source-bound peer review, recorded decision dispositions, local-link validation and `git diff --check` including inspection of untracked additions. Worker evidence: task-bound get/receipt records plus packet-specific oracles, never exit status alone. Implementation: meaningful negative controls, appropriate Go tests and declaration/CLI/spec gates before integration. Experimental source code is not represented as implemented by this planning artifact.

Execution handoff: the next packet is phase 1 under the [D2 peer and budget contract](./peer-budget-contract.md). Codex is preparing that packet, not holding permanent integration authority. No new debate round is required. Runtime workers still need the packet-specific write receipt, correct deliverable root and verification evidence before source changes are accepted. D1 peer ACKs prove historical scope acceptance; D2 peer consumption and a native CLI/MCP exchange remain to be demonstrated.
