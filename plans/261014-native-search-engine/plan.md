---
title: "Native Cross-Platform Search Engine (internal/search + g8s search)"
description: "Ship ripgrep-semantics as a native CGO_ENABLED=0 feature in g8s with WriteReceipt scope filtering and Offer Bundle integration"
status: in_progress
priority: P1
effort: 2h
issue: 591
branch: main
tags: [search, cli, offer, doctor, cross-platform, security]
blockedBy: []
blocks: []
created: 2026-10-14
---

# Native Cross-Platform Search Engine (`#591`)

**Session type**: T2 (execution)

## Overview

Ship `ripgrep`-semantics as a 100% native feature compiled into the single `CGO_ENABLED=0` `g8s` binary (`darwin`, `linux`, `windows`). Includes a concurrent pure-Go search and file-discovery package (`internal/search`), a first-class CLI command (`g8s search` with `--receipt-id` scope filtering), an advisory `Search Engine` check in `g8s doctor`, and native integration in `tools/ci_spec_code_sync.sh`.

## Cross-Plan Dependencies

| Relationship | Plan | Status |
|-------------|------|--------|
| Extends | [261005-pilot-wave](../261005-pilot-wave/plan.md) | completed |
| Prepares | [261014-v018-agent-video-roadmap](../261014-v018-agent-video-roadmap/plan.md) | pending |

## Phases

| Phase | Name | Status |
|-------|------|--------|
| 1 | [Core Pure-Go Search Package & Doctor Probe](./phase-01-internal-search-engine.md) | Pending |
| 2 | [CLI Subcommand g8s search & Receipt Scope Gate](./phase-02-cli-g8s-search-and-doctor.md) | Pending |
| 3 | [Offer Gate Integration, README Sync & Adversarial Review](./phase-03-offer-gates-and-verification.md) | Pending |

## Dependencies

- Issue `tamld/g8s#591`
- Zero-CGO stdlib (`os`, `io/fs`, `bytes`, `regexp`, `filepath`, `runtime`)
- `internal/receipt` (`VerifyReceipt` + `AllowedPaths` scope matching)
