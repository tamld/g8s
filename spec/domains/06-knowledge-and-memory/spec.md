# Living Domain Spec 06 — Decoupled Knowledge Vault & Memory Promotion

- **Domain ID**: `06-knowledge-and-memory`
- **Collision Domain**: `knowledge-memory`
- **Feature IDs**: `FEAT-VAULT-MEMORY-01`, `FEAT-COORDINATOR-MESH-01`
- **Constitutional Laws**: Law 2 (Deterministic State), Law 6 (Evidence over Verdicts)

---

### R1 — Pure-Go SQLite FTS5 Knowledge Vault & Multi-Project Partitioning {#R1-vault-fts5-partitioning}

<!-- inherited-from: DELTA-11, DELTA-21 -->
<!-- feature_id: FEAT-VAULT-MEMORY-01 -->

Operational lessons and Tri-Anchor knowledge records MUST persist in a pure-Go SQLite FTS5 vault partitioned by `repo_id` and `scope_tier`.

#### Scenario: Knowledge vault and memory CLI commands are registered
<!-- tests: TestCanonicalCommandsAndAliases -->
- **GIVEN** the canonical CLI surface registry
- **WHEN** `CanonicalCommands()` is inspected
- **THEN** `vault`, `memory`, and `lesson` are registered

---

### R2 — Capability-Scoped F1/F2 Shared Skills & MCP Tool Hydration {#R2-capability-skill-hydration}

<!-- inherited-from: DELTA-01, DELTA-19, DELTA-33 -->
<!-- feature_id: FEAT-VAULT-MEMORY-01 -->

Every F1 worker and F2 subagent execution MUST inherit deterministic capability-hydration environment variables (`G8S_ROLE`, `G8S_STRATUM`, `G8S_RECOMMENDED_SKILLS`, `G8S_MCP_TOOLS_ALLOWED`) strictly bounded by its `permission` profile (`read_only` $\to$ `S3-A` without `g8s_edit`; `workspace_write` $\to$ `S3-M` with `g8s_edit`).

#### Scenario: Permission and role boundary enforcement on capability hydration
<!-- tests: TestResolveCapabilityHydrationEnforcesPermissionAndRoleBoundaries -->
- **GIVEN** `read_only` (`scout`) and `workspace_write` (`implementer`) worker dispatches
- **WHEN** `ResolveCapabilityHydration` resolves the worker environment
- **THEN** `read_only` resolves to `S3-A` and excludes `g8s_edit`, while `workspace_write` resolves to `S3-M` and includes `g8s_edit` alongside role-specific `G8S_RECOMMENDED_SKILLS`

---

### R3 — Revision-Bound Peer Inbox & Accepted Artifact Synchronization {#R3-revision-bound-mesh-synchronization}

<!-- inherited-from: DELTA-46 -->
<!-- feature_id: FEAT-COORDINATOR-MESH-01 -->

Peer coordinators MUST synchronize accepted task knowledge via monotonic `context_revision` counters, SHA-256 artifact digests, durable recipient-bound inbox messages, and idempotent deduplication (`PROPOSED DELTA-46`).

#### Scenario: Durable peer inbox deduplication, quota provenance, and revision-bound acceptance
<!-- tests: TestMeshAcceptRevisionAndIdempotence, TestMeshMessageRoundTripAndDedup, TestMeshQuotaOwnershipAndValidation, TestMeshInboxDurableReopen, TestMeshWaitCancellation -->
- **GIVEN** registered peer coordinator nodes on a shared SQLite WAL ledger
- **WHEN** peers exchange `send`/`inbox`/`ack` messages, publish self-quota observations, and `accept` claimed task artifacts
- **THEN** duplicate message IDs with identical payloads are deduplicated, only the recipient can ACK, accepted artifacts atomically advance `context_revision`, and state survives store reopen

### R4 — Reach Layer Read-Only Platform Evidence Acquisition {#R4-reach-evidence-acquisition}

<!-- inherited-from: DELTA-47 -->
<!-- feature_id: FEAT-REACH-01 -->

The platform MUST provide a read-only data-acquisition layer (`internal/reach`, ADR-0037) with an in-code channel→action→backend registry, ordered fallback dispatch classifying transient failures as fallback and bad requests as terminal, provenance-labeled responses, and a single agent-facing CLI (`g8s reach`) emitting the versioned envelope. Credentials must never enter g8s state, receipts, logs, or ContextPacket, and no operation may mutate any upstream platform.

#### Scenario: Reach registry ordering, fallback-vs-terminal classification, and envelope parity
<!-- tests: TestLookupDeclaredOrder, TestLookupUnknownChannelAction, TestChainFallbackOnTransient, TestChainStopsOnTerminal, TestChainRecorderPerAttempt, TestReachReadJSONEnvelope, TestHealthLedgerLoadSaveRoundTrip, TestHealthLedgerDemoteOnFallbackCooldown, TestOwnedPathsRegisterDeleteSuccess, TestOwnedPathsRejectSymlink, TestReachReadUsesHealthLedger -->
- **GIVEN** the in-code reach registry with injected fake backends
- **WHEN** lookups and chained fetches execute against known/unknown channels with transient, terminal, and reordered backends
- **THEN** declared order holds, transient failures advance the chain, terminal failures stop it, attempts are recorded with rank/class, and the CLI envelope carries provenance and the attempt trail
