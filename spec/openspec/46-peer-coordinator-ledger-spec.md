# OpenSpec DELTA-46: Peer Coordinator Ledger

* **Specification ID**: `DELTA-46`
* **Status**: `PROPOSED` — experimental branch, not released or admitted.
* **Implementation Status**: `PROPOSED`
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

- register: node_id, incarnation, capabilities (optional JSON object). Idempotent for the same identity. Stores observation time.
- quota-publish: node_id, incarnation, observation with source, observed_at (RFC3339), windows array. Each window has name, window_minutes, used_percent and/or remaining_percent (nullable), resets_at (nullable RFC3339), status observed|unknown. Percentages are finite 0..100 and complementary when both given. Unknown contains no numeric assertion. No tokens or credentials accepted. Records are node-owned, with provenance; never inferred from quality scores.
- send: node_id, incarnation, message_id, to_node, to_incarnation, kind, payload (JSON object). Recipient must currently match. Same id plus identical semantic content is idempotent; same id plus different content fails ID_CONFLICT. Payload capped at 16 KiB; pending inbox capacity is bounded. No input command is executed.
- inbox: node_id, incarnation, limit (1..50). Returns pending recipient-bound messages in stable sequence order. Reading does not acknowledge.
- ack: node_id, incarnation, message_id. Only its recipient can ACK; repeat ACK is idempotent. ACK is not work acceptance.
- claim: node_id, incarnation, task_id, resource, paths (nonempty normalized relative literal file/directory paths, no wildcards/traversal), context_revision, lease_seconds (1..300). Uses one immediate SQLite transaction to check current context revision and overlapping unexpired claims on the same resource. Same owner/task/content may retry idempotently; conflicting scope/owner fails. All peers follow identical rules.
- release: node_id, incarnation, task_id. Only the unexpired current owner may release its claim; no forced reassignment.
- accept: node_id, incarnation, task_id, context_revision, artifact_ref, artifact_sha256. Atomic in-store acceptance checks current claim owner/incarnation, unexpired lease and current ledger revision, binds artifact digest, increments revision once. Repeated identical acceptance is idempotent; a changed artifact or stale owner/revision fails. This is ledger acceptance only, not permission to mutate a Git ref.
- snapshot: returns current revision, registered nodes, their quota observations and task/artifact records. No raw credentials or host topology.
- wait: node_id, incarnation, timeout_ms (0..60000). Bounded context-cancellable machine wait for a pending inbox message; returns pending messages or an empty timed-out result. No model call inside the wait.

## Durability and limits

SQLite WAL, busy timeout, immediate write transactions and 0600 database/0700 directory. Validate JSON before opening a write transaction. Bound payload, pending capacity and list result sizes. Retain message IDs/content digests and accepted task results for the experiment's lifetime to preserve deduplication. Explicit future retention must not erase deduplication inside the retry horizon. Crash/reopen must retain messages and quota.

## Verification scenarios

- Real SQLite tests: register/incarnation replacement; quota range/unknown/source/ownership; duplicate identical/different messages; wrong-recipient ACK; inbox restart recovery; concurrent overlapping claims (exactly one winner); disjoint claims; stale revision; fake-clock lease expiry; accepted-artifact idempotence; old-incarnation rejection; bounded wait/cancellation.
- CLI/MCP parity tests execute the same operations against the same ledger.
- Three real sessions each detect their own quota and publish it, read the same revision/snapshot, claim disjoint contributions to one problem, send/ACK results and record accepted artifact references. Zero steady-state GUI actions and zero human message relays.

#### Scenario: Peer coordinator ledger operations, claims, inbox durability, and CLI/MCP parity
<!-- tests: TestMeshOverlappingClaimsSingleWinner, TestMeshDisjointClaims, TestMeshAcceptRevisionAndIdempotence, TestMeshLeaseExpiry, TestMeshClaimRejectsTraversalAndChangedReplay, TestMeshAllPeersSameRights, TestMeshMessageRoundTripAndDedup, TestMeshQuotaOwnershipAndValidation, TestMeshInboxDurableReopen, TestMeshStaleIncarnation, TestMeshWaitCancellation, TestCoordinatorMeshNativeRegistrationAndParity, TestCoordinatorMeshRejectsWorkerAndUnknownArgs -->
- **GIVEN** an explicit SQLite WAL peer coordinator ledger directory
- **WHEN** equal-peer coordinators execute `register`, `quota-publish`, `send`, `inbox`, `ack`, `claim`, `release`, `accept`, `snapshot`, and `wait` across CLI and MCP surfaces
- **THEN** all invariants for single-winner overlapping claims, disjoint claims, revisioned artifact acceptance, message deduplication, recipient-only ACK, quota ownership, durable reopen, and worker-tier rejection hold

Unit tests cannot certify platform quota availability, native wakeup, model quality, Git fencing or reduction in consumed account quota. Those require separate current evidence.
