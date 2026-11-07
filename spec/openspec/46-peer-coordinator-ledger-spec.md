# OpenSpec DELTA-46: Peer Coordinator Ledger

* **Specification ID**: `DELTA-46`
* **Status**: `APPLIED` — merged in PR #700 and AM2 retention PR #760; blind spec-vs-code review (`ae3a8020`, issue #751) mapped 37 normative requirements; non-blocking verification/doc deltas tracked in issue #758.
* **Implementation Status**: `APPLIED` (RT-MESH v1 core PR #700 + AM2 retention PR #760; v1.1 A1–A5 implemented 2026-11-07 for #754; verification/doc deltas tracked in issue #758)
* **Target Packages**: `internal/mesh`, `internal/mcp`, `cmd/g8s`
* **Milestone**: `M6 (LLM Consumer & Fleet)`
* **domain_href**: `spec/domains/05-stratum-federation/spec.md#R2-peer-coordinator-ledger`
* **domain_href_secondary**: `spec/domains/06-knowledge-and-memory/spec.md#R3-revision-bound-mesh-synchronization`
* **feature_id**: `FEAT-COORDINATOR-MESH-01`
* **Domain**: 05-stratum-federation and 06-knowledge-and-memory.

## Contract

Three equal coordinator clients share an explicitly selected local SQLite ledger while retaining isolated worker queues. The ledger owns node incarnations, quota observations, typed messages, scoped task claims and accepted knowledge revision. It does not grant OS write authority, launch arbitrary commands or automatically take over an unavailable peer.

The pure-Go core lives in internal/mesh. Its public seam is Open(dir string) (*Store, error), Close() error, and Execute(ctx context.Context, operation string, input json.RawMessage) (json.RawMessage, error). CLI and MCP call this same seam. A missing explicit ledger directory fails closed. No GUI or provider-specific protocol is required to use the core.

## Operations

All requests and results are JSON objects. Unknown operations and malformed/unknown fields fail. Actor-bound operations carry node_id and incarnation; they must match the current registered node. Registration of a fresh incarnation invalidates old-incarnation requests without automatically transferring its tasks.

- register: node_id, incarnation, capabilities (optional JSON object), transport (optional policy object; see A2–A4), surface_state (optional declaration; see A5). Idempotent for the same identity and explicit policy. Stores observation time.
- quota-publish: node_id, incarnation, observation with source, observed_at (RFC3339), windows array. Each window has name, window_minutes, used_percent and/or remaining_percent (nullable), resets_at (nullable RFC3339), status observed|unknown. Percentages are finite 0..100 and complementary when both given. Unknown contains no numeric assertion. No tokens or credentials accepted. Records are node-owned, with provenance; never inferred from quality scores.
- send: node_id, incarnation, message_id, to_node, to_incarnation, kind, payload (JSON object). Recipient must currently match. Same id plus identical semantic content is idempotent; same id plus different content fails ID_CONFLICT. Payload capped at 16 KiB; pending inbox capacity is bounded. No input command is executed.
- inbox: node_id, incarnation, limit (1..50). Returns pending surfaced recipient-bound messages in stable sequence order (A5); drives idle probes (A4). Reading does not acknowledge.
- ack: node_id, incarnation, message_id (legacy per-message) or seq (A1 cumulative watermark; optional message_id must match its anchor). Only its recipient can ACK; repeat ACK is idempotent. ACK is not work acceptance.
- claim: node_id, incarnation, task_id, resource, paths (nonempty normalized relative literal file/directory paths, no wildcards/traversal), context_revision, lease_seconds (1..300). Uses one immediate SQLite transaction to check current context revision and overlapping unexpired claims on the same resource. Same owner/task/content may retry idempotently; conflicting scope/owner fails. All peers follow identical rules.
- release: node_id, incarnation, task_id. Only the unexpired current owner may release its claim; no forced reassignment.
- accept: node_id, incarnation, task_id, context_revision, artifact_ref, artifact_sha256. Atomic in-store acceptance checks current claim owner/incarnation, unexpired lease and current ledger revision, binds artifact digest, increments revision once. Repeated identical acceptance is idempotent; a changed artifact or stale owner/revision fails. This is ledger acceptance only, not permission to mutate a Git ref.
- snapshot: returns current revision, registered nodes (including A4 stale and A5 surface state), message delivery states (A5), their quota observations and task/artifact records. No raw credentials or host topology.
- wait: node_id, incarnation, timeout_ms (0..60000). Bounded context-cancellable machine wait for a pending inbox message; returns pending messages or an empty timed-out result. No model call inside the wait.
- prune: older_than (Go duration string, default `168h`, minimum `1h`), dry_run (boolean, default false). In one transaction, deletes only ACKed messages whose `created_at` (no separate ack timestamp column) is before `now - older_than`; before delete, pruned messages leave a dedup tombstone (`message_id`, digest, seq) that is never removed by prune. Retried sends of a pruned id with the same semantic content stay idempotent (same seq, no redelivery); reuse with a different digest still fails ID_CONFLICT. Tombstones do not count toward message capacity limits. Dry run creates no tombstones. Never deletes pending (unACKed) messages, claims, nodes, or quota rows. Returns `pruned`, `dry_run`, and `cutoff` (RFC3339). Invalid or sub-hour `older_than` fails with a typed error.

## Durability and limits

SQLite WAL, busy timeout, immediate write transactions and 0600 database/0700 directory. Validate JSON before opening a write transaction. Bound payload, pending capacity and list result sizes. `messages.created_at` is always written as UTC RFC3339 so retention cutoff comparisons remain correct under lexical string order. Retain message IDs/content digests and accepted task results for the experiment's lifetime to preserve deduplication. Explicit future retention must not erase deduplication inside the retry horizon. Message tombstones (`message_id`, digest, seq) are never removed by `prune` and are retained for the ledger lifetime (small rows); explicit tombstone compaction is a future AM item. Crash/reopen must retain messages and quota.

## Resource limits (implementation-verified)

These are implementation bounds for the current ledger, not changes to the normative operation requirements above.

| Surface | Current bound |
| --- | --- |
| IDs validated by `validID` | 1..128 ASCII bytes from `[A-Za-z0-9_.-]` (node/incarnation and message IDs); claim/accept identifiers retain their operation-specific validation. |
| Registered nodes | 64; retrying or replacing an existing node does not allocate another slot. |
| Quota observation | 1..16 windows. |
| Request and payload size | Core `Execute` input and CLI input each cap the entire JSON request at 16,384 bytes. The send handler independently caps the payload object at 16,384 bytes; the public request envelope consumes part of the available input budget. |
| Message capacity | 10,000 stored message rows total, including ACKed rows until pruning; 1,000 pending messages per recipient node/incarnation. Dedup tombstones do not consume either capacity. |
| List results | Inbox and wait return at most 50 messages; snapshot returns the first 1,000 claims ordered by task ID. Node/quota lists are bounded by the 64-node registry. Snapshot does not delete omitted claims. |
| Claim lease | Omitted or zero `lease_seconds` defaults to 60 seconds; explicit nonzero values must be 1..300 seconds. |
| Accepted artifact | Nonempty `artifact_ref` of at most 2,048 bytes; `artifact_sha256` exactly 64 ASCII hexadecimal characters (upper or lower case). |
| Wait | `timeout_ms` 0..60,000; pending messages are checked immediately, then polled every 100 ms until a message, timeout, or cancellation. |
| Filesystem modes | Newly created ledger directory 0700; database 0600. Mode assertions are skipped on Windows, where ACL semantics differ. Existing directories are not chmod-normalized by `Open`. |

Boundary and durability evidence: `TestMeshPayloadAndInputSizeLimits`, `TestMeshPendingInboxCapacity`, `TestMeshTotalMessageCapacity`, `TestMeshSnapshotClaimListBound`, `TestMeshPrivateDirectoryAndDatabaseModes`, and `TestMeshInboxDurableReopen`. CLI admission and transport evidence: `TestCoordinatorMeshCLIF0Gate`, `TestCoordinatorMeshCLIMeshDirFailClosed`, and `TestCoordinatorMeshCLIAndMCPAllOperationsParity` (the original ten operations plus AM2 `prune`). Reopen checks preserve quota ownership, provenance, timestamps, observed percentages and unknown/null values alongside the inbox.

## Verification scenarios

- Real SQLite tests: register/incarnation replacement; quota range/unknown/source/ownership; duplicate identical/different messages; wrong-recipient ACK; inbox restart recovery; concurrent overlapping claims (exactly one winner); disjoint claims; stale revision; fake-clock lease expiry; accepted-artifact idempotence; old-incarnation rejection; bounded wait/cancellation.
- CLI/MCP parity tests execute the same operations against the same ledger.
- Three real sessions each detect their own quota and publish it, read the same revision/snapshot, claim disjoint contributions to one problem, send/ACK results and record accepted artifact references. Zero steady-state GUI actions and zero human message relays.

#### Scenario: Peer coordinator ledger operations, claims, inbox durability, and CLI/MCP parity
<!-- tests: TestMeshOverlappingClaimsSingleWinner, TestMeshDisjointClaims, TestMeshAcceptRevisionAndIdempotence, TestMeshLeaseExpiry, TestMeshClaimRejectsTraversalAndChangedReplay, TestMeshAllPeersSameRights, TestMeshMessageRoundTripAndDedup, TestMeshQuotaOwnershipAndValidation, TestMeshInboxDurableReopen, TestMeshStaleIncarnation, TestMeshWaitCancellation, TestMeshRetentionPrune, TestMeshRetentionPruneDefaultOlderThan, TestMeshRetentionPruneTombstoneDedup, TestMeshRetentionPruneTombstoneDurability, TestMeshMessageCreatedAtUTCFormat, TestCoordinatorMeshNativeRegistrationAndParity, TestCoordinatorMeshMCPEnumMatchesMeshHandlers, TestCoordinatorMeshRejectsWorkerAndUnknownArgs -->
- **GIVEN** an explicit SQLite WAL peer coordinator ledger directory
- **WHEN** equal-peer coordinators execute `register`, `quota-publish`, `send`, `inbox`, `ack`, `claim`, `release`, `accept`, `snapshot`, `wait`, and `prune` across CLI and MCP surfaces
- **THEN** all invariants for single-winner overlapping claims, disjoint claims, revisioned artifact acceptance, message deduplication, recipient-only ACK, quota ownership, durable reopen, and worker-tier rejection hold

Unit tests cannot certify platform quota availability, native wakeup, model quality, Git fencing or reduction in consumed account quota. Those require separate current evidence.

## RT-MESH v1.1 amendments (#754)

### A1 — cumulative acknowledgement

Status: implemented 2026-11-07. Red tests: `TestMeshCumulativeAckPrefixAndReplay`,
`TestMeshCumulativeAckPeerIsolation`.

The send envelope remains node_id/incarnation/message_id/to_node/to_incarnation/kind/payload.
An ACK may carry a positive `seq` watermark instead of `message_id`. The sequence
must identify a message addressed to the current recipient incarnation. It ACKs
all messages through that sequence from the anchor's sender incarnation to that
recipient incarnation, including an intermediate message omitted by the consumer.
It never acknowledges another peer's stream. An optional message_id with seq must
identify the same anchor. Legacy message_id-only ACKs retain their per-message
meaning. Repeated or lower watermarks cannot undo progress. ACKs and replayed sends
have no duplicate delivery effects; existing digests/tombstones retain their meaning.
An unknown sequence is refused (including a pruned anchor); replaying its send still
returns the tombstoned sequence without redelivery. ACK is not work acceptance.

### A2 — per-peer flow window

Status: implemented 2026-11-07. Red test: `TestMeshFlowWindowSlides`.

`register` accepts optional `transport: {window_size: N}` (1..1000; omitted/zero
uses default 1000). This recipient-owned policy limits each directed sender and
recipient incarnation pair independently. Explicit registration policy updates
are idempotent; omitted policy preserves it. Policies survive reopen and do not
transfer across recipient incarnations. New sends at N pending messages fail
with typed `*FlowWindowError` (`FLOW_WINDOW_FULL`); no message or queue is added.
Existing global/recipient caps remain. Identical send replays bypass admission
and return the original seq even with a full window. ACKs free pending slots;
A1 cumulative ACKs slide the window by the whole acknowledged prefix. Admission
and insertion share one immediate transaction, including across Store handles.

### A3 — per-peer retry budget

Status: implemented 2026-11-07. Red tests: `TestMeshRetryBudgetAckRecovery`,
`TestMeshRetryCoolOffAndDurability`.

Recipient registration transport policy additionally accepts `retry_budget`
(default 3, 1..100) and `cool_off_seconds` (default 30, 1..3600). Zero/omitted
fields use defaults. Each sender/recipient incarnation pair has a durable failure
counter. The shared-ledger transport has no network delivery attempt: a failed
new-send admission due to a flow, recipient, or total capacity limit consumes
one attempt. Invalid envelopes, wrong identities, digest conflicts, and exact
idempotent replays do not consume attempts. Successful enqueue alone does not
reset failures. On the budget-th failure the pair is degraded/suspended until
cool-off expiry, returning typed `*PeerDegradedError` (`PEER_DEGRADED`, deadline).
Further new sends during suspension fail without growing counters or inboxes.
After expiry a fresh budget is available on the next new send; if capacity is
still exhausted, that retry consumes the first attempt of the new budget.

ACKs remain allowed while suspended. A successful ACK that changes at least one
pending row to ACKed resets that stream's failures and suspension immediately;
repeated/old ACKs with no progress do not. Legacy and cumulative ACKs both obey
this rule. Each failure update commits even though send returns an error; budget
state survives reopen. Policies and counters never leak between peers or
incarnations. Cool-off uses the Store clock (fake-clock tests), never test sleeps.

### A4 — ping/pong keepalive

Status: implemented 2026-11-07. Red tests: `TestMeshKeepaliveStaleAndPong`,
`TestMeshKeepaliveBypassesWindowAndBudget`.

Registration transport policy adds `keepalive_seconds` (default 30, 1..3600)
and `keepalive_threshold` (default 3, 1..100); zero uses defaults. Inbox/wait
polls drive idle checks with the Store clock, without a background goroutine.
After a recipient's observed_at is idle for its interval, a polling peer sends
one outstanding `ping` per directed current-incarnation pair. It uses the same
message envelope with an empty object payload. A `pong` is an explicit send
whose payload contains `ping_id` identifying the reverse-direction pending ping.
It acknowledges that ping; pong rows are immediately ACKed. New outbound messages
and registration refresh the actor's observed_at and retire probes addressed to
that incarnation. Exact send replays do not prove fresh liveness.

Snapshot nodes expose `stale: true` when an unanswered current-incarnation ping
is at least threshold intervals old. This reuses registered node incarnations,
observed_at and pending messages: it does not replace identities, transfer claims,
or grant takeover. A fresh incarnation ignores old probes, as other mesh identity
checks already do. Probe age, policies and responses survive reopen.
Ping/pong bypass delivery flow windows, recipient pending delivery counts, retry
suspension and retry attempts. They never reset retry budgets, including via ACK.
The 10000-row storage cap still applies: explicit probes fail without retry
charges and automatic polling skips generation when storage is full.
Automatic outstanding probes are bounded by the 64-node registry; duplicate
automatic polls cannot enqueue repeated pending probes. Operators must continue
polling for idle probes to be issued; snapshot observes without generating probes.

### A5 — screen-state gate

Status: implemented 2026-11-07. Red tests: `TestMeshScreenStateGate`,
`TestMeshScreenStateAckAndDurability`.

Registration additionally accepts optional `surface_state`: `ready` (default),
`modal`, or `busy`. Empty explicitly clears to ready; omission preserves the
current incarnation's declaration. A new incarnation defaults to ready. The
declaration is durable and owned by the registered recipient incarnation.
Modal and busy gate delivery: sends are accepted into the same message ledger,
consume ordinary pending/window capacity, and return `gated: true` and
`surfaced: false`. Inbox/wait omit gated deliveries; probes remain visible and
exempt from this gate. A ready/empty declaration surfaces all pending messages;
recipient ACK also clears gating for its acknowledged messages without declaring
the whole surface ready. ACK retains A1 stream scope and A3 recovery semantics.

Snapshot nodes include `surface_state`, and snapshot `messages` exposes the first
1000 ledger messages in sequence order with `gated` and `surfaced` booleans.
ACKed rows are surfaced; pruning may remove them under existing retention rules.
Send replays report current gating without duplicating a delivery. Surfaced means
the ledger permits the recipient surface to consume the message, not proof that
a model turn started or work was accepted. Existing accepted artifact claims and
revision semantics remain unchanged. No CLI subcommand or flag is added.

### Live interop drill — v1.1 (2026-11-07, recorded)

Two-platform live drill on a fresh drill ledger (`zcode` direct CLI × `agy` F0
peer coordinator; drill-ledger sha256
`8909208903d80565049624c7d8fd719dd6cb6614fa9295e7f134ab8e0ea982ff`):

- **A2 flow window**: registered transport `window_size:3` — sends 1-3 accepted,
  send 4 refused typed `FLOW_WINDOW_FULL: peer agy max 3 pending messages reached`.
- **A1 cumulative ack**: recipient-side `ack {seq:3}` watermark; the previously
  refused send then succeeded as `seq 4` (window slid by the acked prefix).
- **A3 retry budget**: window-refusals consumed the budget (3) — subsequent sends
  returned `PEER_DEGRADED: peer agy retry at <fixed deadline>` with no counter
  growth while suspended; after the 10s cool-off the suspension lifted and the
  next send hit the ordinary `FLOW_WINDOW_FULL` capacity check (fresh budget,
  per the A3 spec).
- **A5 screen-state gate**: peer registered `surface_state:modal` — snapshot
  showed the new messages `gated:true` and the inbox hid them; after the peer
  cleared to `ready`, the same rows surfaced (`gated:false`).
- **A4 keepalive**: red-tested in-process (fake clocks, `keepalive_test.go`);
  live idle-interval drill deferred — requires a long-idle two-platform window.
- **Co-sign**: `agy` F0 peer coordinator sent a `peer-response` co-sign
  (`cosign-v11-261108`, durable ledger seq 3,
  `~/.local/state/g8s-mesh-ledger/ledger.db`, verdict `accepted`). The same
  operation attempted from an F1 worker context was refused
  (`E_DENIED: coordinator protocol is F0-only`) — the stratum firewall holds on
  the mesh surface itself.
