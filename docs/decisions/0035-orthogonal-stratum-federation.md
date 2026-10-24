# ADR-0035: Orthogonal Stratum Federation (`S0..S3` + `O-Plane`) and Epoch-Fenced Charter Engine

* **Status**: Accepted
* **Date**: 2026-10-25
* **Deciders**: Operator (`S0 Principal`) & `g8s` Supervisor (`F0`)
* **Related**: [`docs/decisions/0032-containment-levels.md`](0032-containment-levels.md), [`docs/decisions/0033-f0-verified-delegation.md`](0033-f0-verified-delegation.md), [`spec/openspec/33-stratum-federation-and-charter-spec.md`](../../spec/openspec/33-stratum-federation-and-charter-spec.md)

---

## 1. Context and Problem Statement

`ADR-0032` reserved bare tokens `F0`, `F1`, and `F2` exclusively for **physical OS/worktree containment rings**:
- **`F0`**: Host/Brain process with repository commit and receipt-issuance authority.
- **`F1`**: Isolated git worktree + OS process-group (`Setpgid`) worker bound to a single-use `WriteReceipt`.
- **`F2`**: Recursive subagent spawned inside an `F1` worker's sandbox (`F2 ⊆ F1 ⊆ F0`).

When scaling to multi-repository portfolio coordination (inspired by the `BOARD -> CEO -> TERRA -> FLASH` + `LUNA` architecture in sibling repository `claude-org`), two failure modes arise if organizational roles are conflated with containment rings or named with anthropomorphic titles ("CEO", "Manager", "Staff"):
1. **Ring-Stratum Conflation**: Introducing `F(-1)` or `F3` violates `ADR-0032` and confuses OS process sandboxing with control-plane authority.
2. **RLHF Persona & Sycophancy Bias**: Anthropomorphic role names ("CEO", "Board", "Subordinate") trigger LLM roleplay, verbose corporate prose, and sycophantic deference instead of mechanical contract verification.

## 2. Decision: Orthogonal Two-Axis Taxonomy (`Ring × Stratum`)

We establish an **orthogonal two-axis governance model** in `internal/charter` and `cmd/g8s/charter.go` that separates **Axis X — Physical Containment Rings (`F0 / F1 / F2`)** from **Axis Y — Hierarchical Control Strata (`S0 / S1 / S2 / S3-M / S3-A / O-Plane`)** (grounded in Stafford Beer's Viable System Model and Butler Lampson's Protection Rings):

| Stratum ID | Formal Systems Identifier | Ring Binding | Allowed Authority & Mechanical Boundary |
| :--- | :--- | :--- | :--- |
| **`S0`** | **`Principal`** | Human / Root | Sets strategic outcomes, risk appetite, and capital/quota budgets; approves `S1` portfolio charters. |
| **`S1`** | **`FederationCoordinator`** | `F0` (Read-Only Repo) | Allocates cross-repository **Charters** (`g8s charter issue|revoke|complete`), arbitrates resource contention, and consumes sealed `RoundReceipt` / `O-Plane` projections. **Kernel-blocked** from direct file mutation (`fsops.VerifyWriteScope`) and direct `S3` worker dispatch (`g8s submit`). |
| **`S2`** | **`DomainGovernor`** | `F0` (Repo-Scoped) | Single-repository sovereign supervisor. Authors task briefs, issues single-use `WriteReceipt`s, dispatches `S3-M` / `S3-A` workers, runs `g8s verify`, and commits verified changes within an active `S1 -> S2` Charter. |
| **`S3-M`** | **`BoundedMutator`** | `F1` / `F2` | Executes code/test mutations strictly inside an isolated git worktree under a single-use `WriteReceipt` (`AllowedPaths`). Cannot issue charters or receipts. |
| **`S3-A`** | **`BlindAdjudicator`** | `F1` (Read-Only) | Disjoint-model-family verifier operating over `BlindReviewPacket` (`Spec + RawGitDiff + MechanicalTestOutput`). Kernel-blocked from file mutation (`VerifyMutationAllowed`). |
| **`O-Plane`** | **`TelemetryProjector`** | Deterministic Code (`g8s charter portfolio` / `g8s fleet status`) | Zero-LLM-rewrite read-only state projection over SQLite WAL (`charters.db` + `gatekeepers.db`). Cannot issue charters, receipts, or mutations. |

## 3. Mechanical Enforcement Laws (Zero-Trust Guardrails)

1. **Strict Stratum Adjacency Law (`S_k -> S_{k+1}`)**:
   - `ValidateAdjacency(caller, target)` enforces that `S0 -> {S1, S2}`, `S1 -> {S2, O-Plane}`, and `S2 -> {S3-M, S3-A, O-Plane}`.
   - If `G8S_STRATUM=S1`, calling `g8s submit` fails closed with `E_STRATUM_ADJACENCY_VIOLATION` (`S1` cannot bypass `S2` to micromanage `S3` workers).
2. **Cross-Stratum Mutation Firewall**:
   - `VerifyMutationAllowed(stratum, targetPath)` is wired into `fsops.ResolveDelegationContext` and `fsops.VerifyWriteScope`.
   - Any process with `G8S_STRATUM` in `{S1, S3-A, O-Plane}` is rejected with `E_S1_DIRECT_MUTATION_FORBIDDEN` on any write/edit operation.
3. **Anti-Spoofing Child Clamping**:
   - `CurrentStratum()` inspects `G8S_TIER` (`F1`/`F2`) and `G8S_RECEIPT_ID` first. Any process inside `F1` or `F2` is clamped to `S3-M` or `S3-A` even if it attempts to set `G8S_STRATUM=S1` or `G8S_STRATUM=S2`.
4. **Epoch-Fenced Charter Reclaim (`Kleppmann Fencing Tokens`)**:
   - Every `Charter` stored in `$G8S_STATE_DIR/charters.db` carries a monotonically increasing `Epoch`, a deterministic 128-bit `Fence` token (`HMAC/SHA-256` over `charter_id|repo_id|epoch`), and a canonical `CharterDigest` (`SHA-256` over immutable charter fields).
   - `RevokeAndReclaim` atomically increments `Epoch`, rotates `Fence`, and transitions `State` to `REVOKED`, immediately invalidating stale in-flight tokens.

## 4. Consequences

- **Positive**: Preserves `ADR-0032` (`F0/F1/F2` unchanged), eliminates anthropomorphic prompt bias, enables multi-repository `S1 -> S2` portfolio governance, and enforces stratum boundaries in Go code rather than markdown prose.
- **Negative / Trade-offs**: Adds one lightweight SQLite table (`charters` in `charters.db`), mitigated by defaulting `CurrentStratum()` to `S2` (`DomainGovernor`) when `G8S_STRATUM` is unset so single-repository workflows have zero friction.
