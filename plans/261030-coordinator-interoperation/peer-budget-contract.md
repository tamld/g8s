# D2: Equal peers, synchronized knowledge, bounded cost, native transport

Source: the operator's direct instruction after D1. This is a scoped experiment contract; it does not silently amend global stratum rules or claim implementation exists.

## Goal

Codex, AGY and Zcode solve one shared problem as equal coordinators. The system preserves their shared knowledge, prevents overlapping accepted changes, and minimizes operator relay and expensive model/context work. No coordinator is a permanent leader, integrator or mandatory communication hub.

## Symmetric coordination

- All three have the same coordinator protocol rights under the operator's campaign authority. Actual usable tools/providers remain separately observed capabilities.
- A task has one temporary owner; an integration target has one temporary integrator. Either role is available to any eligible peer under the same scoped claim, release and expiry rules.
- Disjoint work can proceed concurrently in separate receipt-bound worker worktrees. Shared hotspots and common target refs are serialized. Integration must verify actual role ownership, receipt scope and expected head at the effect boundary; merely issuing a lease does not prove safety.
- Silence, stale telemetry or quota pressure does not trigger forced takeover. Preserve in-flight work; coordinate explicit handoff or stop at the role deadline. Automatic HA remains deferred.
- Initial file packages from D1 remain useful proposals, but no model/platform receives a permanent specialty or veto from its name.

## Knowledge synchronization

Git-tracked specifications, decisions and accepted artifacts are the durable semantic source. A shared local SQLite control plane records active tasks, role ownership, message observations, artifact digests and the current accepted context revision.

Each task binds a baseline commit, context revision, acceptance oracle, owned scope and evidence references. A result carries those bindings back. A stale or conflicting result is rebased/reviewed before acceptance, not silently applied. Coordinators exchange concise deltas and references, not complete conversation histories or three mutable copies of one plan. Independent private worker queues are not merged.

## g8s-native protocol and activation

CLI and MCP are two transports for one g8s-owned contract and durable state. Implement operations for node/session registration, task claim/release, message send/read/ack, revisioned artifact references and bounded inbox wait. Extend the existing coordinator surface; new commands/tools remain proposed until registered and tested.

Machine waiting must not repeatedly call a model. A connected session can block on a bounded wait and receive only a targeted new event or actionable error. Pending work survives a missed notification or receiver restart; duplicate messages do not repeat in-store effects.

Every recipient runtime needs one admitted CLI/MCP connection or an equivalent native callback. A database event alone cannot start an arbitrary closed app. A session lacking that connection is unavailable for autonomous dispatch; no GUI fallback is silently counted as g8s activation. Platform glue may be thin and replaceable, but transport, knowledge and task ownership stay in g8s. No GUI coordinates, screenshots, UI polling or mandatory Orca dependency in the normal loop.

## Budget strategy

- Assign one owner to routine work; involve another peer only for a declared independent check, conflict or risk gate. Do not broadcast every microtask to all three.
- Mechanical inventory, implementation and test extraction use bounded g8s workers with explicit input/output budgets. Coordinators spend context on decomposition, counterexamples and acceptance.
- Source context is shared by digest/revision and loaded just in time. Reuse an accepted artifact when its input bindings match; do not repeat consumed model calls to repair a local parser.
- Coalesce non-actionable observations and notifications. Deterministic routing uses fresh supported capability/budget evidence; unknown quota is never invented.
- Model/account/window quota and host compute are separate resources. Moving work to another device or route does not imply a fresh account quota pool.
- Codex avoids computer use in steady-state operation. Measure its actual context/turn consumption rather than asserting it is always the most expensive peer.
- Set packet-level call/attempt/deadline ceilings before dispatch. On budget exhaustion, retain the task/context/evidence and request a bounded handoff through the same protocol rather than launching speculative retries.

## Acceptance additions

1. Each peer can own and hand off a task under identical rules; no fixed Codex-only integration gate.
2. Two peers claiming overlapping work cannot both obtain accepted integration authority; disjoint work remains possible.
3. A stale context revision or integration role is rejected at the shared effect.
4. One native CLI client and one MCP client interoperate against the same inbox and state semantics without GUI or Orca.
5. An inactive, disconnected or unsupported runtime remains visibly unavailable; no false wakeup/completion claims.
6. Accepted knowledge references converge across the peers after interruption without replaying whole chats.
7. Operator message relays and steady-state GUI actions are zero in the controlled native drill. Model calls, retries and context loaded are recorded; quota savings require comparison to a baseline.

## Current evidence boundary

D1 proved an agent-operated GUI/Orca bootstrap and supplied-text worker smokes. It did not prove native peer activation, symmetric integration leases, synchronized knowledge revisions or quota reduction. D2 is authoritative scope from the operator; the historical peer ACKs bind D1, not independent acknowledgement or successful implementation of D2. Native peer consumption and the above controls must be demonstrated next.
