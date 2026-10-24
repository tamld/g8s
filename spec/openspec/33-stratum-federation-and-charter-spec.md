# OpenSpec DELTA-33: Orthogonal Stratum Federation (`S0..S3` + `O-Plane`) and Epoch-Fenced Charter Engine

* **Specification ID**: `DELTA-33`
* **Status**: `APPLIED` (Status: implemented)
* **Implementation Status**: `IMPLEMENTED`
* **Target Packages**: `internal/charter`, `internal/fsops`, `internal/mcp`, `cmd/g8s`
* **Milestone**: `M6 (LLM Consumer & Fleet)`
* **Release**: `v0.20.0`

---

## 1. Problem Statement

1. **Conflation of Containment Rings and Control Strata**: `ADR-0032` defines `F0 / F1 / F2` as physical OS/worktree containment rings, but multi-repository portfolio orchestration requires a distinct control-plane hierarchy (`Principal -> FederationCoordinator -> DomainGovernor -> BoundedMutator / BlindAdjudicator`) without introducing `F(-1)` or `F3+`.
2. **S1 Micromanagement & Direct Mutation Hazard**: Without kernel-level stratum guards in `internal/fsops` and `cmd/g8s/submit.go`, a portfolio coordinator (`S1`) can bypass the repository governor (`S2`) to edit files or spawn `S3` workers directly, corrupting domain worktrees and context budgets.
3. **Unfenced Cross-Repo Delegation**: Delegating repository objectives across sessions lacked an epoch-fenced, digest-verified SQLite WAL charter ledger (`charters.db`) capable of atomic revocation and O-Plane portfolio projection.

---

## 2. Normative Requirements

### R1 — Orthogonal Stratum Taxonomy & Anti-Spoofing Resolution (`internal/charter`)
- `internal/charter` MUST define `Ring` (`F0`, `F1`, `F2`) and `Stratum` (`S0`, `S1`, `S2`, `S3-M`, `S3-A`, `O-Plane`).
- `CurrentStratum()` MUST clamp any process with `G8S_TIER` in `{F1, F2}` or non-empty `G8S_RECEIPT_ID` to `S3-M` (or `S3-A` when `G8S_STRATUM=S3-A`), preventing `F1`/`F2` workers from spoofing `S0`/`S1`/`S2`. When `G8S_STRATUM` is unset on `F0`, `CurrentStratum()` defaults to `S2` (`DomainGovernor`).

### R2 — Strict Stratum Adjacency & Cross-Stratum Mutation Firewall (`internal/charter`, `internal/fsops`, `cmd/g8s`)
- `ValidateAdjacency(caller, target)` MUST allow only `S0 -> {S1, S2, O-Plane}`, `S1 -> {S2, O-Plane}`, and `S2 -> {S3-M, S3-A, O-Plane}`, rejecting all other edges (`S1 -> S3-M`, `S3-A -> S3-M`, etc.) with `E_STRATUM_ADJACENCY_VIOLATION`.
- `cmd/g8s/submit.go` MUST reject `g8s submit` when `CurrentStratum()` is `S1`, `S3-A`, or `O-Plane`.
- `VerifyMutationAllowed(stratum, targetPath)` MUST block file mutations when `stratum` is in `{S1, S3-A, O-Plane}` with `E_S1_DIRECT_MUTATION_FORBIDDEN`, enforced inside `fsops.ResolveDelegationContext` (when `isMutation == true`) and `fsops.VerifyWriteScope`.

### R3 — Epoch-Fenced SQLite WAL Charter Ledger (`internal/charter`)
- `Store` (`$G8S_STATE_DIR/charters.db`) MUST persist charters in SQLite WAL mode with `CharterID`, `IssuerStratum`, `TargetStratum`, `RepoID`, `ScopeRoot`, deduplicated `AllowedPaths`, `OutcomeMandate`, `BudgetTokens`, `Epoch`, `Fence`, `CharterDigest`, `State`, `IssuedAt`, `ExpiresAt`, `RevokeReason`, and `SealedReceiptHash`.
- `ComputeDigest` MUST compute a canonical SHA-256 over immutable charter fields; `Get` MUST verify `ComputeDigest(c) == c.CharterDigest` on every read.
- `RevokeAndReclaim(charterID, reason)` MUST atomically increment `Epoch`, rotate `Fence` (`ComputeFence`), and set `State = REVOKED`, causing `Verify` to reject stale epochs or fence tokens (`E_CHARTER_FENCE_STALE` / `E_CHARTER_INACTIVE`).

### R4 — CLI & MCP Surface (`cmd/g8s/charter.go`, `internal/mcp`)
- CLI command `g8s charter <issue|verify|revoke|complete|list|portfolio> [--json]` MUST expose the full charter lifecycle and O-Plane portfolio projection.
- MCP stdio server (`g8s mcp`) MUST expose `g8s_charter_issue`, `g8s_charter_verify`, and `g8s_portfolio_status` (19 total tools).

---

## 3. Verification Scenarios

#### Scenario: Charter Lifecycle and Canonical Digest Integrity
<!-- tests: TestCharterLifecycleAndCanonicalDigest -->
- **GIVEN** an initialized SQLite WAL charter store in `$G8S_STATE_DIR/charters.db`
- **WHEN** `S1` issues a charter to `S2` for `tamld/g8s`, verifies its `(epoch, fence)`, checks tamper detection on `OutcomeMandate`, and completes it with a sealed receipt hash
- **THEN** `Issue`, `Verify`, and `Complete` succeed and any mutation to immutable fields alters `ComputeDigest`.

#### Scenario: Stratum Adjacency Matrix and S1/S3-A/O-Plane Mutation Firewall
<!-- tests: TestStratumAdjacencyAndMutationFirewall, TestFSOPSEnforcesS1MutationFirewall -->
- **GIVEN** processes operating under strata `S1`, `S3-A`, and `O-Plane`
- **WHEN** `ValidateAdjacency` is evaluated on `S1 -> S3-M` or `fsops.VerifyWriteScope` / `fsops.ResolveDelegationContext(..., true)` is invoked
- **THEN** `S1 -> S3-M` is rejected with `E_STRATUM_ADJACENCY_VIOLATION` and direct file writes from `S1`, `S3-A`, and `O-Plane` are rejected with `E_S1_DIRECT_MUTATION_FORBIDDEN`.

#### Scenario: Epoch-Fenced Reclaim Rejects Stale Charter Tokens
<!-- tests: TestCharterFenceRejectsStaleEpoch -->
- **GIVEN** an active Epoch 1 charter `c1`
- **WHEN** `RevokeAndReclaim` is executed by `S1`
- **THEN** `c1` transitions to `REVOKED` with incremented `Epoch` and rotated `Fence`, and any subsequent `Verify` call presenting the old epoch or fence token fails closed.
