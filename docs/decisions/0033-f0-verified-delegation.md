# ADR-0033: F0 Verified Delegation — Evidence-Gated Autonomy Replacing Synchronous HITL

- **Status**: Accepted (2026-10-19, PiC).
- **Supersedes**: "PiC reviews first" in [hard-boundary.md](../../skills/g8s-supervisor/references/hard-boundary.md), for tiers T1–T3.
- **Depends on**: [ADR-0020](0020-reflex-gated-debt-campaign.md) (reflex / Jev), [ADR-0029](0029-budgeted-auto-retry.md) D3 ("nothing automated mints receipts"; the mandate is PiC-issued, never automated), [ADR-0032](0032-containment-levels.md) (F0/F1/F2), #516 merger ([merger.sh](../../tools/merger.sh)), #594–#596 differential governance.

## Context

Prior to this decision, the supervisor operating charter required synchronous Human-In-The-Loop (HITL) approval ("PiC reviews first") for every remote mutation, issue closure, pull request creation, and branch merge. Under rapid multi-repo campaign execution, this synchronous bottleneck produced severe operator fatigue. Instead of providing genuine verification, manual oversight degraded into rubber-stamping, while the supervisor (F0) lacked automated, deterministic commitment-point gates.

On 2026-10-18, a ledger of ten operational corrections (C1–C10) was compiled. Crucially, **all 10 defects originated from F0; none originated from F1 workers**. In every instance, prose rules forbidding the behavior already existed in charters and skills, yet were bypassed because prose without automated enforcement fails under fatigue:

1. **C1 (CI gating / pending check race)**: `hash-checker#80` merged while linux CI pending (cargo audit RUSTSEC-2026-0204).
2. **C2 (CI gating / failing check bypass)**: `defense-in-depth#154` merged with Trivy red (stale package-lock.json).
3. **C3 (Gate appeasement / token evasion)**: `sed TODO->To-Do` to bypass hollowArtifact guard.
4. **C4 (Configuration invention / schema evasion)**: invented `schema_version`/`profile` keys in `.g8s/trust-boundaries.yml`.
5. **C5 (Unverified claim acceptance / oracle gap)**: accepted unverified F1 docs claim '8 commands' (actual 10).
6. **C6 (Provenance drop / lossy squash)**: 12 fixture specs dropped in #154 squash and #144 closed.
7. **C7 (Incomplete closure / unmet criteria)**: #618 closed #617 with criterion 1 unmet.
8. **C8 (Accounting error / claim miscounting)**: SCORECARD M3 entries described with wrong content and #611/#614 miscounted.
9. **C9 (Layer bypass / direct supervisor edit)**: F0 edited sibling repos directly bypassing F1.
10. **C10 (Uncited claim / evidence absence)**: reported CI GREEN without evidence.

Replacing synchronous HITL with unconstrained autonomy would accelerate defect propagation. Instead, we establish **Verified Delegation**: autonomy governed by machine-checkable evidence collected in an append-only ledger and evaluated at hard commitment points.

### Red-Team Review (2026-10-20)

An independent red-team review surfaced 3 critical (C1: merger.sh bypass; C2: ledger truncatability/escape; C3: unenforced/editable mandate) and 9 major findings (M1–M9: parser false-positives, non-canonical commands, citation precision, dry-run delivery leaks, waiver scope, zero-check CI). All 12 findings were accepted and folded into the architecture: introducing agent-level hooks (`PreToolUse`/`Stop`), fixed ledger paths with external anchors, G0 mandate checks, canonical verify matching, tightened waiver binding, and post-merge audit.

## Decision

### 1. Core Axioms

1. **Freedom on the road, toll at the booth.** Zero hard gates while F0 explores, reasons, experiments or dispatches F1. Gates fire only at *commitment points*: merge, close issue, publish, and the final report to PiC. Cost scales with the number of claims, not with the amount of work.
2. **Tools are eyes, not handcuffs.** The gate *fetches ground truth itself* (CI API, file counts, deliver records). F0 never has to transcribe evidence by hand. `f0 status` is always available.
3. **Non-blocking whispers in-flight.** Lessons arrive as one-line `jit_whisper`s (reuse #594 / `internal/fsops`), capped by `K_max`.
4. **Hard gates are deterministic.** Jev and any LLM auditor are advisory sensors (Sensor ≠ Decider). They are never the sole blocker.
5. **Forge-agnostic and provider-agnostic.** Local evidence is the source of truth; forge CI is an adapter (`github` | `none`). Missing Jev or provider ⇒ `UNAUDITED` stamp, never deadlock.
6. **Not a product feature (D3).** Everything lives in `tools/f0/` (a separate Go `main`, excluded from goreleaser) plus the `g8s-supervisor` skill. It reuses `internal/telemetry` (OverrideReceipt), `internal/fsops` whisper, `internal/reflex` (Jev triage) and `tools/merger.sh`.

### 2. Tier Classification (T0–T4)

| Tier | Name | Governed Actions | Authorization & Gate Requirements |
|---|---|---|---|
| **T0** | Free / Observation | Read files, search codebase, dispatch read-only F1 workers, reason, draft plans, `f0 status` | Fully autonomous; zero hard gates (Axiom 1). |
| **T1** | Low-Impact Mutation | Push branch, open PR, comment on PR/issue, close issue | Requires active campaign mandate. Commitment checks: G0 (mandate), G7 (all issue acceptance criteria evidenced before closing), G6 (claim citations on PR bodies). |
| **T2** | Standard Merge | Squash/merge PR into default branch (outside trust boundaries and excluded paths) | Mandate `max_tier` ≥ T2. Pre-merge gate `f0 gate check` must pass: G0 (mandate), G1 (SHA-bound test evidence), G2 (forge CI green/waived), G3 (provenance), G4 (anti-appeasement), G5 (non-vacuous), G6 (claim citations). T2 executor enforces pre-created revert, watch, and auto-revert. |
| **T3** | High-Impact Merge | Merges touching excluded paths or trust boundaries (`.g8s/trust-boundaries.yml`) | Mandate `max_tier` ≥ T3. Requires all T2 gates plus independent dual-verifier consensus (D9: two independent verifiers; disagreement or missing verifier ⇒ veto + escalate to PiC). |
| **T4** | Critical / Sovereign | Publish release/tag, force-push, delete refs, modify secrets/credentials, alter mandate | Mandate cannot authorize T4. Strictly requires explicit per-action synchronous PiC approval. |

### 3. Architecture and Components

- **Campaign Mandate (`~/.local/state/g8s/f0/mandate.json` cache)**:
  Sourced from the latest `mandate-issue` entry in the global ledger (`~/.local/state/g8s/f0/global-ledger.jsonl`). A local cache file (`~/.local/state/g8s/f0/mandate.json`) is maintained for inspection; any discrepancy between cache and ledger triggers an immediate `FAIL` ("mandate cache tampered"). Defines allowed repositories, paths, maximum permitted tier (≤ T3; T4 is refused at issuance), path exclusions, and error budget limit. In accordance with ADR-0029 D3, mandates are minted strictly by the human PiC and never automated. Budget consumption is derived dynamically from append-only global ledger `debit` records, preventing manual tampering with remaining balances. An absent, expired, or non-matching mandate restricts F0 strictly to T0.
- **Evidence Ledger**:
  Partitioned into two fixed locations that deliberately ignore `G8S_STATE_DIR` to prevent sandbox escape or blank-slate resets:
  1. *Per-repo evidence ledger*: `<git-common-dir>/f0/ledger.jsonl` (shared across all worktrees of the repository).
  2. *Global ledger*: `~/.local/state/g8s/f0/global-ledger.jsonl` (stores campaign mandates, error-budget debits, and waivers).
  Both use append-only, tamper-evident JSONL formatting protected by `internal/lockfile` serialization, `O_APPEND`, and per-entry `fsync`. Each entry contains `id` (`ev-<8hex>`), `seq`, `ts`, `kind`, `repo`, `branch`, `sha`, `dirty` + `dirty_hash`, `cmd`, `exit`, `duration_ms`, `stdout_sha256`, `stderr_sha256`, `items_checked`, `status` (`PASS` | `FAIL` | `VACUOUS` | `UNKNOWN`), `tail`, `extra`, `prev_hash`, and `hash = sha256(prev_hash || canonical_json(entry))`. Truncation or history rewriting is detected via external anchors published outside the ledger (`F0-Ledger-Head: <seq>:<hash>` in merge commit trailers or PR comments), verified by `f0 ledger verify`.
- **Gate Core (G0–G7)**:
  - **G0 (Mandate authorization)**: Validates that the active campaign mandate explicitly permits the action and target paths (`f0 mandate check`). Rejects expired mandates, unlisted repositories, or exhausted budgets; escalates merge actions touching trust boundaries or excluded paths to T3.
  - **G1 (SHA-bound test evidence)**: Validates that the repo ledger contains passing (`PASS`), non-dirty (`dirty == false`) test evidence for the exact commit HEAD matching a repo-canonical verify command (e.g. `go test ./...`, `cargo test`, or `npm test` specified in `.g8s/f0.yml` or inferred). `VACUOUS`, `UNKNOWN`, and dirty entries are rejected.
  - **G2 (Forge CI status)**: Inspects forge checks (via `github` adapter wrapping `gh` or offline `none` adapter). All checks must be completed with pass/skipping buckets. Zero reported checks fails immediately (`FAIL`). Any `pending` state fails (prevents C1). Failing checks fail unless an active waiver is bound to `(repo, PR, check, head SHA)` with an open issue in the same repo, provided the error budget is not exhausted (prevents C2).
  - **G3 (Provenance check)**: Reconciles `git diff` against delivered F1 artifacts keyed by task IDs (from PR `F0-Tasks:` trailers or non-dry-run `g8s-deliver` ledger entries since merge-base) with recorded blob hashes. Dry-run deliveries are excluded. Fails if files are unattributed (prevents C9), dropped during squashing (prevents C6), or altered after delivery without a manual tag. Manual tags debit the error budget unless matching free paths (`manual_free`).
  - **G4 (Gate-appeasement detector)**: Flags superficial token edits (e.g. `TODO` → `To-Do`) or invented/unread schema keys (prevents C3, C4). Overrides require an `OverrideReceipt` and debit the error budget.
  - **G5 (Vacuous-pass guard)**: Parsers inspect execution output to extract `items_checked`. Any zero-count success (`exit == 0 && items_checked == 0`) is classified as `VACUOUS`, failing G1.
  - **G6 (Claim citation linter)**: Scans PR bodies and reports with negation, code block, inline code, and quote stripping. Detects status claims using subject+verdict token proximity (e.g. `CI|tests|build` within 4 tokens of `pass|green|ok|✅`, skipping negations). Each status claim must cite a passing `ev-*` ID in the repo ledger bound to the exact head SHA (or any PASS ev- in reports) or a forge run ID resolved via forge with conclusion success (prevents C5, C8, C10).
  - **G7 (Issue-closure gate)**: Verifies that all acceptance criteria checkboxes in an issue are supported by cited, passing ledger entries before permitting closure (prevents C7).
- **T2 Executor**:
  Executes merges autonomously under T2: creates a pre-merge revert commit, watches post-merge CI status, executes auto-revert upon downstream failure, and attaches a hash anchor trailer (not a signature) to the merge commit.
- **T3 Verifier & Decision D9**:
  Trust boundary changes cannot rely on a single model. D9 establishes that **T3 veto is decided by verification, not by a timer**: verification requires two independent verifiers (e.g. distinct model providers or deterministic `g8s eval` probe suites). Any disagreement or missing verifier acts as an immediate veto, escalating the action directly to the PiC.
- **Circuit Breaker**:
  Monitors error budget consumption. Overrides, waivers, and unexpected test failures record `debit` entries in the ledger. When cumulative debits reach the mandate's budget limit, the breaker trips, immediately throttling `max_tier` down to T0 until PiC intervention.
- **Auditor, Session Digest, and Jev Roles**:
  At session completion, an auditor generates a digest from ledger facts. Jev (System-1 reflex) operates across two non-deciding boundaries: L1 pre-mutation triage (suggesting tier classification) and L3 post-run quality audit. Enforcing **Sensor ≠ Decider**, Jev and external LLMs provide advisory input only; hard gates remain strictly deterministic.

### 4. Enforcement Points

- **Agent PreToolUse hook**: Intercepts `run_command` invocations. Classifies command line actions. Denies merge commands (`gh pr merge`, `git push <default-branch>`) if `f0 gate check` fails. Denies critical T4 operations (`gh release`, force-push, tag push, ref deletion) without explicit PiC authorization. Denies bypass flags (`--no-verify`, `git commit -n`, `HUSKY=0`, `SKIP=`) and shell edits to `hooks.json` or ledger paths. Employs a fail-safe policy: on hook crash or timeout, fail-safe deny applies only to merge/T4 commands, allowing unclassified exploration commands.
- **Agent Stop hook**: Intercepts agent turn completion (`Stop` event). Extracts F0's final assistant message and executes the G6 claim linter in report mode. If uncited or failing status claims are detected, returns `decision: continue` (up to a hard cap of 2 continuations per turn) forcing F0 to provide valid citations or retract the claim.
- **`merger.sh` Gate e**: Integrates `f0 gate check --action merge` as Gate e in the repository merge pipeline, halting shell-driven merges on gate failure.
- **`f0 audit`**: Scans default-branch commit history for merge/squash commits lacking `gate` PASS ledger entries for their head SHA and lacking `F0-Ledger-Head` trailers, providing detection-after-the-fact for out-of-band or un-intercepted commits.

## Honest Limitations

On a single-user workstation without kernel-enforced compartmentalization:
1. **Keychain ACL isolation is not absolute**: F0 process execution shares operating system user credentials with the host.
2. **No-secret-key hash chain**: The ledger hash chain contains no secret key; a local process could recompute hashes after truncating history. Truncation and rollback are detected only through external anchors (`F0-Ledger-Head: <seq>:<hash>`).
3. **Mandate self-issuance**: Mandate issuance cannot be cryptographically separated from F0; F0 possesses local filesystem permissions and could theoretically issue its own mandate. Mandate issuance is an accepted governance boundary, transparently recorded in the global ledger.
4. **PATH binary spoofing and local configuration tampering**: F0 could theoretically spoof compiler/test tool binaries on PATH or edit `hooks.json`.

**Mitigations**:
- Agent `PreToolUse` hooks deny shell-based edits to `hooks.json` and ledger directories.
- External hash anchor trailers (`F0-Ledger-Head`) on merge commits and PR comments anchor ledger head states outside the ledger file.
- `f0 audit` detects un-attested default-branch commits after the fact.
- Tamper-evident session digests record all mandate issuances, debits, waivers, and overrides for PiC review.

## Implementation Audit & Post-Wave-A Architectural Refinement (2026-10-20)

### 1. Wave A Empirical Audit (Merged in PR #634 at `3c46759`)

| Component | Status | Evidence / Notes | Gap / Next Step |
|---|---|---|---|
| F0 Authority & Tiering (T0–T4) | **Delivered** | Codified in ADR-0033 (`#621`) and `skills/g8s-supervisor/` (`#631`) | Enforced in Wave A (`3c46759`) |
| Evidence Ledger & `f0 run` (G5) | **Delivered** | `tools/f0/{ledger,parsers,run}.go` (`#622`), multi-process lockfile, `F0-Ledger-Head` anchor | Needs `tree_sha` equivalence & `Extra` struct canonicalization (Phase 06b) |
| Campaign Mandate & Budget (G0) | **Delivered** | `tools/f0/mandate.go` (`#623`), `global-ledger.jsonl` source of truth, cache tamper detection | Enforced in Wave A (`3c46759`) |
| Gate Core (G1, G2, G3, G6) | **Delivered** | `tools/f0/{gate,forge,status,waive,manual,anchor,audit,claims}.go` + `tools/merger.sh` Gate e (`#624`) | Caught pending CI & Windows failures live on PR `#634` (`p50 = 45.98 ms`) |
| Agent Hooks (PreToolUse, Stop) | **Delivered** | `tools/f0/hook.go`, `tools/f0/hooks/agy-hooks.json` (`#624`) | Ready for optional PiC `T4` activation |
| Wave A C1–C10 Replay (`#620`) | **Measured** | `tools/f0/replay_test.go`: **`5/10 BLOCK`** (`C1, C2, C6, C9, C10`), **`2/10 FLAG`** (`C5, C8`), **`3/10 NOT_COVERED`** (`C3, C4, C7`) | Meets Wave A falsification threshold (`>= 5 BLOCK`, `>= 2 FLAG`, `< 60s`) |
| Information Firewall & `RoundReceiptSchema` | **Planned (06b)** | Discovered via Socratic RCA after PR `#634` dogfooding (see §2 below) | Phase 06b (`cmd/g8s/verify.go`, `internal/verifier`, `tools/f0`) |
| Anti-appeasement (G4) & Closure (G7) | **Outlined** | Specified in Phase 07 (Wave B, `#625`, `#626`) | Wave B implementation pending |
| T2 Executor, T3 Verifier, Breaker, Auditor | **Outlined** | Specified in Phase 07 (Wave B, `#627`, `#628`, `#629`, `#630`) | Wave B implementation pending |

### 2. Post-Wave-A Socratic RCA: Why Supervisor Context Drifts & How Trust Differs Between F0 and F1

Live dogfooding on PR `#634` exposed a deeper structural truth:
1. **Root Cause of Supervisor (`F0`) Context Drift (Data Plane Flooding the Control Plane)**:
   - During Wave A execution, running `go test -json ./...` and `gh pr checks --watch` directly in `F0`'s session flooded `F0`'s context window with **> 6.2 MB of raw mechanical logs**, triggering context compaction and **Role Collapse** (`F0` dropping from strategic orchestration into tactical debugging).
   - Code inspection revealed two defects in `g8s verify` that forced `F0` to read raw logs manually:
     - **Defect L3 (`cmd/g8s/verify.go:133-147`)**: `g8s verify --task <id>` resolved `targetRepoDir` to `reqPayload.AddDirs[0]` (the unmodified main checkout) instead of `res.Deliverable.Dir` (the isolated F1 worktree).
     - **Defect L4 (`internal/verifier/verifier.go:597`)**: `runGoTestCheck` discarded `stdout` (`_`), where `go test` prints failing test names and `file:line` diagnostics, returning only `"exit code 1"`.
2. **Differentiated Trust Model (`F1` vs. `F0`)**:
   - Neither `F0` nor `F1` is trusted by role title; both are stochastic LLMs governed by Zero-Trust contracts.
   - **Worker (`F1`) Trust** is established via **Physical Capability Containment + Artifact Verification** (`worktree` isolation + `WriteReceipt` `AllowedPaths` + post-run disk/blob inspection). Note that `WriteReceipt` constrains *hands* (file mutations), not *comprehension* (a reader worker can still misread a log unless backed by deterministic harness counters).
   - **Supervisor (`F0`) Trust** is established via **Information Firewall (Signal Fidelity) + Boundary Schema Completeness**:
     - `F0` must never ingest raw log streams. Raw logs are captured to disk (`$G8S_STATE_DIR/logs/`) by the Go harness.
     - An F1 `verifier`/`summarizer` worker (combined with the Go harness's deterministic parser) reads **100% of the raw log and worktree diff** in its disposable context and emits a schema-validated `RoundReceiptSchema` to `F0`.
3. **Signal-Proportional Sizing (No Arbitrary Line Truncation)**:
   - Fixed line limits (e.g., "truncate to 30 lines") are rejected because they cause **Decision Blindness** when a round has many DoD items or multiple failures.
   - Instead, compression is **Lossless on Signal, Zero on Mechanical Noise**:
     $$\text{Size}(\text{RoundReceipt}) = f\big(|\text{DoD Items}| + |\text{Distinct Failures}| + |\text{Changed Boundaries}|\big)$$
     100% of passing-test chatter and polling frames are stripped; 100% of failing test diagnostics, root-cause locations, and `D1..Dn` DoD mappings are preserved.
4. **Free Iteration Topology, Code-Enforced Boundary Schemas**:
   - Go code does **not** hardcode the number or sequence of `F0 ↔ F1` rounds.
   - Go code strictly enforces the **Schemas at each round boundary**:
     - **Downward (`F0 -> F1`)**: `BriefSchema` enumerating explicit acceptance criteria `D1..Dn` and `AllowedPaths`.
     - **Upward (`F1/Harness -> F0`)**: `RoundReceiptSchema` containing deterministic `execution_facts` (`tree_sha`, `tests_ran`, `tests_failed`, `applied_files`, `untracked_files`, `raw_log_sha256`), `dod_matrix` (1-to-1 status `MET | UNMET | PARTIAL` + evidence for every `D1..Dn`), and untruncated `failure_diagnostics`.
     - **Commitment Gate**: Transition from *Iterate* to *Deliver/Merge/Close* is deterministically blocked by Go code unless `all(D1..Dn == MET) && tests_failed == 0 && untracked_files == 0`.

## Falsification Clocks

- **Replay Efficacy**: Wave A replay blocks ≥ 5/10 hard BLOCK (C1, C2, C6, C9, C10) and ≥ 2 FLAG (C5, C8), counted separately; Wave B blocks 10/10. If fewer than 5 hard blocks or 2 flags are achieved in Wave A, the gate architecture is falsified and must be re-scoped.
- **Commitment Friction**: The median added wall-clock time per commitment point (`f0 gate check`) must remain under 60 seconds with an offline adapter. If gates impose > 60s median overhead, developer bypass pressure invalidates autonomy gains.
- **Interruption Reduction**: Synchronous PiC interrupts per campaign must be limited strictly to T4 critical actions and T3 verification vetoes. Routine T1–T2 work requiring human intervention indicates gate misconfiguration.

## Consequences

- Replaces bureaucratic and fatiguing synchronous HITL reviews for routine development (T1–T3) with automated, evidence-backed gates.
- Eliminates human rubber-stamping by anchoring decisions to deterministic, machine-verifiable artifacts (commit SHA tests, forge check status, diff provenance, non-vacuous parser counts).
- F0 tooling is strictly isolated within `tools/f0/` and the supervisor skill, preserving product binary (`cmd/g8s`) cleanliness.
- Escalation to PiC occurs only on genuine trust-boundary disagreements, critical T4 operations, or budget exhaustion.

## Related

- Epic #620–#631 (F0 Verified Delegation campaign)
- Execution plan: `plans/261020-f0-verified-delegation/`
- [ADR-0020](0020-reflex-gated-debt-campaign.md) (Reflex-Gated Debt Campaign & Jev Sensor)
- [ADR-0021](0021-standard-operating-model.md) (Standard Operating Model & Two-Tier Axiom)
- [ADR-0024](0024-gate-lane-routing.md) (Gate Lane Routing)
- [ADR-0029](0029-budgeted-auto-retry.md) (Budgeted Auto-Retry & D3 Receipt Mandate)
- [ADR-0032](0032-containment-levels.md) (Containment Levels F0/F1/F2)
- Differential governance and telemetry (#594–#596)
