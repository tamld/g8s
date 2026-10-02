# ADR-0028: Multi-Project Tenancy on One Host (#465)

- **Status**: Proposed (2026-10-02) — decision points D1–D4 await
  operator ratification. Landlogged behind the P0/P1 fixes that made
  the interim posture safe (#468, #491).
- **Issue**: #465 (2+ concurrent sessions on one host killed each
  other's workers and reaped each other's worktrees). RCA red-teamed in
  `plans/260929-465-multi-tenant/plan.md`.
- **Supersedes**: none. **Depends on**: #491 per-state-dir instance
  identity + lease resilience, #468 identity-scoped ghost kill +
  state-dir worktree root, ADR-0021 single-node posture.

## Context

One project / one supervisor / N workers is proven stable (12+ tasks,
parallel drains, zero cross-contamination this campaign). Two projects
sharing g8s on one host was a design gap, discovered live when the
aegis session ran g8s while the g8s debt-zero session was draining:
host-global defaults meant one session's `g8s cleanup` SIGKILLed the
other's live agy workers and RemoveAll'd its preserved worktrees.

Landed containment (P0/P1, 2026-09-29→10-02):
- Ghost-kill is identity-scoped: stale/invalid heartbeat + no CWD/cmdline
  corroboration to the repo → NOT a ghost; PID-recycle rule (#468).
- Pool worktree root moved under THIS instance's state dir (#468).
- Lease resilience: transient DB errors no longer kill workers;
  awaitOutcome re-checks the authoritative lease (#491).
- Interim doctrine, proven in the field: concurrent sessions use
  DISTINCT `G8S_STATE_DIR` (the state dir is the ownership boundary).

## Decision points (awaiting operator)

### D1 — Default state-dir policy (recommendation: keep host-global default + doctrine)

Keep `~/.local/state/g8s` as the default and make distinct
`G8S_STATE_DIR` per concurrent session the DOCUMENTED requirement for
multi-project hosts. Deriving per-project state dirs from the repo root
(rejected in the RCA) changes on-disk layout for every existing user
and breaks the deliberate shared-queue use case; a silent layout change
is the #461 manifest-drift class of surprise.

### D2 — Sweep/kill jurisdiction (recommendation: identity-scoped forever, no silent admin)

Kills, reaps, and sweeps only ever touch resources the invoking
instance's state dir owns — as shipped in #468/#491. Cross-instance
administration stays possible ONLY through an explicit flag
(`--force-foreign` class) that logs attribution BEFORE acting
(resource, owner if known, action, why). Never restore silent
host-wide sweeps.

Falsification citations: the aegis incident (5 workers killed /
worktrees reaped across 4 dispatch rounds); sa-002 hostile-lifecycle
probe caught the real Windows 8.3-path destruction chain in CI; the
sweep-registry comparison lesson (#477 collision-refusal).

### D3 — Deliberate shared-queue mode (recommendation: explicit config, identity-scoped primitives)

Some operators WANT one queue across repos (plan option 1 rejected for
this reason). Keep it possible but explicit: a shared state dir is a
deliberate configuration, and every destructive primitive stays
identity-scoped even then (a shared QUEUE does not imply a shared TRIGGER
finger). Document that shared-queue tenants inherit each other's lease
reconciliation but never each other's cleanup.

### D4 — Reopen trigger (recommendation: write it down now)

Revisit defaults when multi-project-on-one-host becomes ROUTINE
(≥3 projects with recurring concurrent drains in one month, or a second
live contention incident DESPITE the doctrine). Analogous to ADR-0027's
trigger contract: the posture changes when evidence, not ambition,
arrives.

## Consequences

- No code in this decision — it documents the ratified-interim posture
  and the conditions to change it. Implementation work beyond #468/#491
  is NOT scheduled until a trigger fires.
- The docs/user-guide tenancy section (concurrent sessions =
  distinct state dirs) becomes the normative reference once ratified.
- #465 closes with ratification + the normative docs (or stays open as
  the tracker until then).

## Related

- #465, #468, #491, #477 (worktree collision-refusal), plans/
  260929-465-multi-tenant/plan.md (RCA + red team), ADR-0021
  (single-node posture), ADR-0027 (trigger-contract pattern).
