# ADR-0027: Availability / Downtime Measurement Is "Not Measured, By Design"

- **Status**: Accepted (2026-09-30)
- **Issue**: #441 (enterprise transparency split: access-audit / SLA / ledger)
- **Supersedes**: none. **Depends on**: ADR-0025-class single-node control
  plane posture (CAP trade-off, single writer).

## Context

The system-design-primer availability cross-check (#441) asks for downtime
in numbers: 99.9% = 52min/year of unavailability. g8s publishes zero
downtime numbers today. The control plane is deliberately single-node
SQLite WAL (ADR-0021 posture: one writer, one host, one state dir), and
the product's containment guarantees are per-host, not cluster-grade.

Publishing an SLA number we do not measure would be an UNPROVEN claim in
the Enterprise Evidence Ledger (`docs/ENTERPRISE_LEDGER.md`) — the exact
thing the ledger exists to prevent.

## Decision

1. g8s does **not** publish availability/downtime figures in v0.x. The
   honest entry in the ledger is `UNPROVEN (by design)`, not a fabricated
   number.
2. The measurement contract for WHEN this changes is written down now:
   - **Trigger A** — multi-node control plane (leader/follower or shared
     queue across hosts) becomes a ratified roadmap item → availability
     measurement becomes mandatory for that release.
   - **Trigger B** — a signed enterprise SLA requires a figure → measure
     before signing, not after (heartbeat-based liveness + task-state
     transitions already exist as raw material).
3. If asked for a number meanwhile, the documented answer is: "single-node
   control plane; availability equals the host's availability; g8s adds no
   HA layer by design" (see ADR-0021 CAP trade-off).

## Consequences

- Enterprise conversations get a truthful, specific answer instead of a
  made-up SLA.
- The ledger row stays UNPROVEN until a trigger fires; a future slice then
  implements liveness/downtime measurement (candidate: control-plane
  heartbeat continuity + QUEUED-starvation detection) and flips the row to
  PROVEN with a methodology note.
- No code in this decision — it is a documented product posture.

## Related

- docs/designs/access-audit-design.md (the other #441 half: who-did-what)
- docs/ENTERPRISE_LEDGER.md (the ledger this decision feeds)
- ADR-0021 (single-node CAP posture), #465 (multi-session ownership on
  one host — tenancy, not HA)
