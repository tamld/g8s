# Native inbox and recovery drill

Status: pending phase 2. Initial drill preparation: Codex. Runtime/adapter supervisor: AGY. Counterexample supervisor: Zcode. Role rotation is part of the test; these are not permanent privileges.

## Observable experiment

Use the built experimental binary and explicit shared coordinator storage. Start from three actual session registrations and isolated worker state directories. Send one bounded task to each peer, request attention through its admitted adapter, receive ACK/result, then independently accept or reject the baseline-bound artifacts. No operator message relaying and no direct edits to the primary checkout.

## Checkoffs

- [ ] Three sessions see the same registered peers and their own three-direction capability view.
- [ ] All three peers can claim, hand off and accept scoped work under identical protocol rights.
- [ ] Overlapping integration claims and stale context revisions cannot both pass the shared-effect gate.
- [ ] CLI and MCP clients share the same native state and protocol; zero GUI/Orca actions in the steady-state drill.
- [ ] Unknown quota remains null and quality fields stay separate.
- [ ] Actual native inbox records prove delivery, ACK, result and acceptance separately.
- [ ] Duplicate send/notification does not repeat an in-store effect.
- [ ] Lost notification is recovered by inbox read after restart.
- [ ] A stale-incarnation message does not authorize a new session.
- [ ] A wrong baseline/scope or unavailable adapter produces a clear rejection/unproven state.
- [ ] Capacity and retention controls preserve pending work and deduplication for the declared horizon.
- [ ] Compacted/restarted state retains the accepted task's evidence references.
- [ ] Human message-relay count is zero; coordinator and adapter interventions are counted separately.
- [ ] Model calls, retries and context loaded are recorded; shared quota pools are not counted twice and no unmeasured savings claim is made.

## Exit and failure behavior

Accept the native MVP only when meaningful checks and actual connected records cover the above cases. Bootstrap UI/Orca exchange does not satisfy these checkoffs. Preserve failures, stop speculative retries, and leave a bounded continuation artifact when a lane or adapter is unavailable. No auto-HA, remote service, automatic merge or rollout to another repository is added by this drill.
