# Plan: S3a — Memory Promotion Gate (#395, ADR-0023 v1 scope)

**Session type**: T2 (execution)

```yaml
issue: 395
session_type: T2 (execution)
slice: S3a (Lane B, after S2/#394 closed)
ratified_by: ADR-0023 (Proposed) — operator 3-round T1 brainstorm 2026-09-26
red_contract: ADR-0023 Red Test Proof C1–C9 (C7 live-red captured 2026-09-26)
```

## Objective

Vault writes stop being ungated: promotion requires the gate (deterministic
checks + tombstone lookup + naked-payload Jev), revocation tombstones by
payload hash, injection carries a hard ≤4096-char budget, and workers never
call Propose. Storage/retrieval engines untouched.

## v1 scope (per ADR-0023 §7)

- `internal/memory`: MemoryEntry v1 (labels: kind/lifecycle/trust/salience/
  scope/provenance), additive migration (`memory_entries`, `memory_tombstones`),
  FSM `Transition` (C1), `Propose` promotion gate (C2, G1 tombstones, G3
  naked-payload, G4 meta-entry rejection), `RevokeBySession` + tombstones
  (C9), salience counters with in-sample no-op (C4 v1-deterministic subset).
- Gate sensor injectable: default reflex (Jev), tests use a stub; sensor down
  → fail-open with `trust=unverified` + telemetry event (ADR DEGRADED state).
- `internal/telemetry`: `InjectPreflightContext` ranked truncation with hard
  4096-char cap (C7); read path stays Jev-free (C8).
- `cmd/g8s`: `memory list` / `memory revoke --session X` (C9 CLI surface).
- v1.1 deferred: G2 out-of-sample counters; doctrine flow stays PR-process (C10).

## Verification contract

RED C1–C9 first (command + exit code in ledger), dual-pass CI, pre-push 12/12,
independent adversarial review, PR, close #395.
