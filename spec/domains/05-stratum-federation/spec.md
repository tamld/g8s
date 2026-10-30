# Living Domain Spec 05 — Orthogonal Stratum Federation & Fleet Hygiene

- **Domain ID**: `05-stratum-federation`
- **Collision Domains**: `federation`, `platform-hygiene`
- **Feature IDs**: `FEAT-CHARTER-FEDERATION-01`, `FEAT-DOCTOR-DIAG-01`, `FEAT-COORDINATOR-MESH-01`
- **Constitutional Laws**: Law 1 (Zero-Trust Delegation), Law 3 (Blast-Radius Containment)

---

### R1 — Stratum Adjacency Law & Cross-Repository Fleet Hygiene {#R1-stratum-and-fleet-hygiene}

<!-- inherited-from: DELTA-07, DELTA-14, DELTA-16, DELTA-32, DELTA-33 -->
<!-- feature_id: FEAT-CHARTER-FEDERATION-01 -->

Multi-repository delegation MUST obey Orthogonal Control Strata (`S0..S3 + O-Plane`) via `g8s charter` and cross-repository workspace cleanliness via `g8s fleet status`.

#### Scenario: Federation and fleet hygiene commands are registered in CLI surface
<!-- tests: TestCanonicalCommandsAndAliases -->
- **GIVEN** the canonical CLI surface contract
- **WHEN** `IsRegisteredCommand` checks `charter` and `fleet`
- **THEN** both commands are registered and exposed in `g8s --help`

---

### R2 — Equal-Peer Coordinator Ledger & Scoped Task Claims {#R2-peer-coordinator-ledger}

<!-- inherited-from: DELTA-46 -->
<!-- feature_id: FEAT-COORDINATOR-MESH-01 -->

Multiple equal-peer F0 coordinators sharing an explicit local SQLite WAL ledger MUST register node incarnations, publish self-observed quota with provenance, acquire non-overlapping path-scoped task claims under immediate transactions, and reject worker (`F1`/`F2`) or stale-incarnation mutations (`PROPOSED DELTA-46`).

#### Scenario: Equal-peer coordinator mesh claims, incarnation fencing, and CLI/MCP parity
<!-- tests: TestMeshOverlappingClaimsSingleWinner, TestMeshDisjointClaims, TestMeshClaimRejectsTraversalAndChangedReplay, TestMeshAllPeersSameRights, TestMeshLeaseExpiry, TestMeshStaleIncarnation, TestCoordinatorMeshNativeRegistrationAndParity, TestCoordinatorMeshRejectsWorkerAndUnknownArgs -->
- **GIVEN** an explicit SQLite WAL coordinator mesh ledger directory
- **WHEN** equal-peer F0 nodes execute `register`, `quota-publish`, `claim`, `release`, `snapshot`, and `wait` via `g8s coordinator mesh` or `g8s_coordinator_mesh`
- **THEN** overlapping path claims admit a single winner, disjoint claims succeed concurrently, stale incarnations and F1/F2 worker contexts are rejected, and CLI/MCP surfaces produce identical state transitions
