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
    `memory_entries` (active only, trust ≥ unverified, top-N by
    kind×salience×trust×scope); telemetry = recent trace events + top
    negative_patterns; SOM = current phase/DoR status source (injectable,
    v1: read from controlplane supervisor_tasks latest state).
  - Budget: packet ≤ 4096 chars total (mirrors G6), top-N per source ≤ 5.
  - Fail-open per source: source error → skip + `broker_failure` telemetry;
    total failure → minimal request (v1 behavior byte-for-byte).
- `TriageRequest v2`: additive `ContextPacket *ContextPacket` (omitempty) —
  old JSON/flags byte-compatible (Constitution Axiom 5); new CLI flag
  `--enrich` (default on where wired; `--no-enrich` to force legacy).
- Pilot deployment point: `g8s reflex triage` (L1 pre-mutation) assembles the
  packet and attaches it before `TriageMutation`. One command, end-to-end.

## Red tests (before implementation)

- Packet assembly: empty vault → packet with empty vault section, no error;
  empty telemetry → same; both empty → minimal packet (degraded paths).
- Fail-open: broken vault source → minimal request + `broker_failure` event
  recorded on the sink.
- Budget: oversized vault hits → packet ≤ 4096 chars, top-ranked kept.
- Backward-compat: `TriageRequest` v1 JSON parses as v2 with nil ContextPacket;
  old CLI flags produce identical argv behavior.
- Latency: unit-level (no live model in tests); live 2x check deferred to the
  operator-invoked run (recorded in ledger).

## Non-goals

- L3/L6 wiring (only L1 pilot); token-latency tuning beyond budget cap;
  broker-judged quality as blocking (stays advisory per L2 deterministic
  floor — #398 owns L2/L4).
