# Plan: S4 — Context Broker (#396, ADR-0021 §8.2)

**Session type**: T2 (execution)

```yaml
issue: 396
session_type: T2 (execution)
slice: S4 (Lane B, after S3a/#395 merged — hard precondition)
ratified_by: ADR-0021 §8.2 + issue #396 ratified design
adr: docs/decisions/0021-standard-operating-model.md §8.2
```

## Objective

Jev triage stops being context-starved: every deployment point can carry a
`ContextPacket` assembled from Vault (post-gate, #395) + Telemetry (recent
outcomes, failure signatures) + SOM state (phase, DoR status), with hard
token/latency budgets and fail-open degradation.

## Design (from ratified issue + ADR sketch)

- New `internal/context` package: `Broker` + `ContextPacket`.
  - Queries (each independently failing): vault = ranked label query over
    `memory_entries` (active entries, top-N by kind×salience×trust×scope);
    telemetry = recent trace events + top negative_patterns; SOM = current
    phase/DoR status source (injectable; v1 reads latest supervisor task
    state from the controlplane).
  - Budget: packet ≤ 4096 chars total (mirrors G6), top-N per source ≤ 5.
  - Fail-open per source: source error → skip that section +
    `broker_failure` telemetry event; total failure → minimal request (v1
    behavior byte-for-byte).
- `TriageRequest v2`: additive `ContextPacket *ContextPacket` (omitempty) —
  old JSON/flags byte-compatible (Constitution Axiom 5).
- Pilot deployment point: `g8s reflex triage` (L1 pre-mutation) assembles the
  packet and attaches it before `TriageMutation`. One command, end-to-end;
  `--no-enrich` forces legacy behavior.

## Red tests (before implementation)

- Packet assembly: empty vault → empty vault section, no error; empty
  telemetry → same; both empty → minimal packet (degraded paths).
- Fail-open: broken vault source → minimal request + `broker_failure` event
  recorded on the sink.
- Budget: oversized vault hits → packet ≤ 4096 chars, top-ranked kept.
- Backward-compat: `TriageRequest` v1 JSON parses as v2 with nil packet.
- Latency: unit-level determinism only; live 2x check deferred to the
  operator-invoked run (recorded in ledger).

## Non-goals

- L3/L6 wiring (only L1 pilot); broker-judged quality as blocking (stays
  advisory — L2 deterministic floor is #398's slice).
