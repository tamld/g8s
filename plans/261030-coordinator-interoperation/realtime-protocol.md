# RT-MESH v1 — Real-Time Multi-Platform Coordination Protocol (PROPOSED)

Status: PROPOSED for peer co-sign (agy, codex). Implemented in `internal/mesh` @ 29b3661.
Authority: S0 directive 2026-10-31 "real-time, no race condition, g8s as the bridge — define and test in rounds".

## The bridge posture (g8s ở giữa)

```text
  AGY (Antigravity TUI)      ZCode (desktop lead)      Codex (parked, quota)
        │  CLI/MCP                    │  CLI/MCP                 │  CLI (on return)
        └──────────────┬──────────────┴───────────────┬──────────┘
                       ▼                              ▼
        ┌─────────────────────────────────────────────────────────┐
        │        g8s SHARED LEDGER (SQLite WAL, same host)         │
        │  messages · claims · quotas · nodes   = the ONLY truth   │
        └─────────────────────────────────────────────────────────┘
                       ▲                              ▲
        attention adapters (orca terminal send, app-server session/send)
        = NUDGE only — transport, never truth, never required for correctness
```

Platforms never talk to each other directly. Everything durable is a ledger row;
everything else (chat, terminal paste, screenshots) is ephemeral attention.

## Defined primitives (P1..P6)

- **P1 Transport truth**: one SQLite WAL ledger, all mutations in `BEGIN IMMEDIATE`
  transactions (single-winner by construction), `busy_timeout` 30s. Two transports
  (CLI `g8s coordinator mesh`, MCP `g8s_coordinator_mesh`), one contract.
- **P2 Real-time delivery**: `wait` = bounded long-poll (100ms internal tick,
  `timeout_ms` 0..60000). A peer runs a `wait` LOOP in a background shell — zero
  model calls while idle; wake happens only when a matching message row lands.
  Latency budget: ≤ ~600ms from `send` commit to `wait` return.
- **P3 Mutual exclusion**: `claim` = single-winner CAS lease. `task_id` PRIMARY KEY;
  active-lease path-overlap check against the same `resource`; TTL `lease_seconds`;
  conflict returns an explicit error (never silent queueing). `accept` binds the
  artifact to the claim's `context_revision` — stale revision is rejected at the
  effect boundary. Expired lease = claim lost; re-claim is a fresh election.
  **Revision fence (discovered in R2)**: a claim MUST carry `context_revision` equal
  to the current ledger revision — a peer must `snapshot` immediately before
  claiming; claims against stale context are rejected outright.
- **P4 Idempotency**: `message_id` UNIQUE at DDL. Duplicate send with IDENTICAL
  content (same digest) is an idempotent echo of the original seq — no second row;
  same `message_id` with DIFFERENT content is an error (collision). `ack` marks
  exactly-once in-store receipt. Delivery is at-least-once; receivers dedup by
  `message_id`. Dedup proves once-only STORE transitions, not exactly-once external
  execution (external effects need claims).
- **P5 Liveness (advisory)**: presence = ops activity per incarnation. No kill()
  truth claims, no forced takeover on staleness (per D2). Stale peers get
  explicitly reported stale; handoff is explicit via message, never automatic.
- **P6 Roles on the wire**: `kind` registry — `peer-request`, `peer-ack`,
  `peer-boundary`, `drill-result`, `protocol-proposal`, `status`, `desktop-handoff`,
  `execution-hold`, `availability-and-execution-lead-handoff`. New kinds are added
  by proposal + peer co-sign, never silently.

## Race-condition kill chain

| Race | Defense |
|---|---|
| Two peers claim the same work | `BEGIN IMMEDIATE` + path-overlap check → exactly one winner, explicit conflict for the rest (R2) |
| Duplicate send, identical digest | idempotent echo of original seq (no new row); same message_id with different content → explicit error (R3) |
| Lost wake while peer busy | `wait` loop = at-least-once; inbox retains rows until acked (P2+P4) |
| Stale coordinator mutates after replacement | claim TTL expiry + `context_revision` binding on `accept` (P3) |
| Two transports seeing different truth | both hit the same ledger file/DB (P1) |

## Verification rounds (the protocol is not real until these pass)

- R1 latency: send→wait-return wall clock, target < 1000ms.
- R2 claim race: N concurrent claimers, one scope → 1 winner, N−1 explicit conflicts.
- R3 idempotency: duplicate `message_id` send → error; inbox shows one row.
- R4 cross-platform live round-trip: zcode→agy proposal via ledger + ONE terminal
  nudge; agy replies via ledger; zcode `wait` loop catches it; round-trip timed.

Results are appended below as they run; failures are recorded, not retried blindly.

## Recorded drill results (2026-10-31, executor zcode-native-20261030-cu)

- **R1 latency: PASS** — send→wait-return **107ms** (one 100ms tick), total wall 723ms incl. spawn.
- **R2 claim race: PASS** — 4 concurrent claimers, one scope, fresh revision → **1 WINNER** (task rt-race2-1, state=active), **3 explicit path-overlap conflicts**. First attempt (stale revision 0) correctly rejected all 4 — the revision fence works.
- **R3 idempotency: PASS** — duplicate message_id with identical digest → idempotent echo of seq, 1 row in table (DDL UNIQUE + digest check verified in source); conflicting-content variant rejects.
- **R4 cross-platform live round-trip**: armed — see ledger seq for protocol-proposal and the peer-ack reply latency when recorded.
- **R4 cross-platform live round-trip: PASS** — proposal (seq 26, 17:38:04Z) → agy co-sign (seq 27, 17:38:29Z) = **25 seconds** across two native platforms; zcode wait loop caught it on the first poll. Co-sign covers P1-P6 + R1-R4 with amendments A1 (table/P4 wording sync) and A2 (R2 winner task_id) — both applied post-co-sign; ratified revision sha256 recorded on the ledger.
