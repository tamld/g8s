# Design and execution contract

Status: D1 reviewed in one round; superseded where noted by the operator's D2 peer-equality, budget and g8s-native transport requirements. See [D2 contract](./peer-budget-contract.md).

## Scope

Same-host interoperation between three equal coordinator sessions; explicit experiment shared-control storage; isolated runtime state per coordinator; one temporary integration-role holder per shared target. Downward, lateral and upward denote capability relationships, not physical hosts or automatic authority levels. Upward includes external model/provider/compute capacity and future larger coordination systems. Remote federation and autonomous authority takeover require later evidence and their own scope.

## Candidate system design

The public g8s core owns coordinator records, typed durable message semantics and evidence references. CLI and MCP expose the same protocol. Thin runtime adapters connect an authorized session to it; they do not own the queue, knowledge, routing or consent state. Existing GUI/Orca exchanges were bootstrap evidence only and are not the operational transport or acceptance oracle for the native MVP. Transport enqueue, attention, started turn, result, and accepted result are distinct observations.

Keep shared cooperation state separate from the worker queue, receipts, process groups and worktrees of each coordinator. Do not move global charter, scorecard or memory databases as a side effect. Make experimental placement explicit. Do not call a per-session default path a pre-existing defect without a failing promised contract.

The shared directory is an explicit opt-in setting outside the checkout and private worker state. SQLite is the durable inbox, not an unbounded notification JSONL file. Define pending-message capacity and terminal retention before implementation; preserve deduplication information for the documented retry horizon when compacting payloads. Exceeding capacity returns an actionable error rather than dropping mail. Notification hints are recoverable from the inbox.

Each integration target has one temporary role holder, eligible from any of the three peers, with identical rules and explicit scope/expiry. Worker changes require narrow write receipts and isolated worktrees; integration uses current scope, diff and expected-head checks. Receiving a baton cannot grant commit/push/merge authority. Message-processing leases do not authorize source mutation. HA is deferred until stale-owner rejection is bound to the shared effect, including its concurrency window. A row recording a lease is insufficient unless the integration path actually enforces it.

## Candidate protocol

Envelope: protocol_version, message_id, campaign_id, sender and recipient node/incarnation, correlation_id, kind, task_id, baseline_head, scope, artifact references/digests, deadline and delivery status. Contract fields do not execute arbitrary supplied commands. Narratives may be carried as bounded context but do not establish authority or consent.

Delivery is at least once. Receiver deduplicates before repeating an effect. Explicit states distinguish queued, attention requested, turn started, acknowledged, result available and accepted/rejected. Receiver state and its durable acknowledgement must survive interruption. A new receiver incarnation cannot silently consume a stale-incarnation assignment.

Only in-store state transitions can be claimed once-only from a transactional deduplication proof. External model calls, app actions and filesystem effects need their own idempotency or reconciliation; no blanket exactly-once execution claim is made. Lifecycle observations need not arrive in order: a result received before its attention observation is not discarded or regressed. Stable node identity and per-session incarnation are separate; the existing state-directory InstanceID is not treated as a per-process generation.

Capability observations bind source, observation time, expiry and status: configured, discovered, probed or admitted. Quota observations separately bind account/pool, window, used/remaining, reset, source and confidence. Unsupported or expired observations remain unknown; do not add percentages across accounts/windows or double-count routes sharing a pool. Health and capacity are separate.

## Provisional responsibility map

| Owner | Responsibility | Ownership boundary |
|---|---|---|
| Codex | Initial contract preparation, independent review and integration when holding that task role | Plan/decision files; temporary role only, no permanent exclusive privilege |
| AGY | Native runtime adapters, capability observations, connected experiment | Four new files: internal/supervisor/mesh_awareness.go, mesh_awareness_test.go, peer_adapter.go, peer_adapter_test.go; no CLI/MCP edits |
| Zcode | Protocol/store state transitions and negative controls | Six new files: internal/mesh/message.go, message_test.go, store.go, store_test.go, fsm.go, fsm_test.go; no CLI/README edits |
| g8s workers | Mechanical implementation, bounded checks, diagnostic extraction | One packet per worker, narrow owned paths, receipts for writes |

Assignments are initial task-fit proposals, not a peer hierarchy or permanent platform roles. A peer must explicitly accept its scope before work is attributed to it. Each coordinator supervises its own g8s worker, supplies a narrow receipt for writes, validates root/scope/result and returns a compact evidence receipt. Shared CLI registration, manifest, feature-map, README and OpenSpec glue belongs to a separate integration packet whose temporary holder may be any peer. Public declarations and spec lineage precede source implementation; do not leave a new package orphaned in the feature map.

Implementation order: freeze domain types and registry/store interfaces; peers supervise disjoint worker packets against those interfaces; core tests and adapter tests run independently; the temporary integration holder applies CLI/declarations in a separate layer-disjoint wave; peers run the connected drill on the resulting built binary. Do not count an adapter-only exchange as a native g8s inbox test.

Repository paths above are relative to this isolated checkout. Every eventual worker brief must expand its owned paths and workdir to the actual isolated deliverable root, not the primary checkout.

## Required negative controls

1. Same message delivered twice: processing and side effects occur once; acknowledgement may replay.
2. Recipient restarts: old-incarnation message cannot grant authority to the new instance.
3. Enqueue succeeds but adapter cannot start a turn: status remains unproven/unavailable, never completed.
4. ACK without result: acceptance remains pending.
5. Result bound to wrong baseline or scope: integration rejects it.
6. Worker reports success with absent checks: packet-specific oracle rejects unverified claims.
7. Notification lost: inbox read after restart still finds pending mail.
8. Retention/capacity reached: old-payload compaction preserves deduplication through the retry horizon and pending mail is not silently lost.
9. Result/ACK observations arrive out of order: terminal acceptance does not regress.

## Deferred scope

No multi-host serving, new public listener, automatic hub election, cross-account quota inference, broad path leases, state-directory takeover, cryptographic identity infrastructure, automatic merge or Aegis rollout. Reopen each only after a concrete consumer outcome demonstrates the need.

## Experiment success criteria

Both actual peer sessions return independently attributable responses without operator paste relaying. A bounded worker runs through g8s and its result is classified honestly. A frozen work package reaches its intended peer, is explicitly acknowledged, and produces a baseline-bound artifact. Record transport actions, owner intervention and recovery rather than claiming quota savings from message counts.

Native MVP acceptance additionally requires real registry/inbox operations and negative controls on the built experimental binary, a successful scoped writer lane, attributable artifact results and no primary-checkout mutation. Bootstrap peer reachability alone cannot satisfy these implementation criteria.
