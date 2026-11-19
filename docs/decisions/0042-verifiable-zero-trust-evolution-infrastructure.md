# ADR-0042: Verifiable Zero-Trust Execution & Evolution Infrastructure (TRUST North Star)

- **Status**: Accepted (2026-11-19) — ratified under Epic [#934](https://github.com/tamld/g8s/issues/934)
- **Depends on**: [ADR-0023](0023-memory-lifecycle.md) (memory lifecycle & tombstone monotonicity), [ADR-0032](0032-containment-levels.md) (`F0/F1/F2` tiers), [ADR-0033](0033-f0-verified-delegation.md) (`F0` verified delegation), [ADR-0035](0035-orthogonal-stratum-federation.md) (`S0..S3` strata), [ADR-0038](0038-mesh-fence-enforcement-point.md) (claim fence), [ADR-0039](0039-dogfooding-honesty-ledger.md) (provenance honesty), [ADR-0040](0040-mechanized-ssot.md) (mechanized SSoT), [ADR-0041](0041-worker-git-proxy.md) (worker git proxy)
- **Tracker**: Epic [#934](https://github.com/tamld/g8s/issues/934) (Wave 1 [#935](https://github.com/tamld/g8s/issues/935)–[#937](https://github.com/tamld/g8s/issues/937), [#945](https://github.com/tamld/g8s/issues/945), Wave 2 [#938](https://github.com/tamld/g8s/issues/938)–[#939](https://github.com/tamld/g8s/issues/939), Wave 3 [#940](https://github.com/tamld/g8s/issues/940)–[#943](https://github.com/tamld/g8s/issues/943))

---

## Context

The independent `v0.25.0` security & technical re-audits (`F01`–`F14`, `B01`, `G8S-01`–`G8S-12`) evaluated `g8s` across provider adoption, firewall separation of concerns, execution enforcement, task durability, test verification, and release supply chain. They established two truths:

1. `g8s` already possesses real control-plane and containment primitives (OS sandboxing on macOS/Linux, Win32 `JobObject` process-tree containment, Ed25519 receipts and charters, SQLite WAL CAS leases, fault-injection E2E, and provider abstraction).
2. Pointing a self-improving AI loop at a codebase without sealing the **Trusted Computing Base (TCB)** turns *self-improving* into *self-deceiving*: a worker could exploit parser gaps (`#935`), quiet `policy_only` fallbacks (`#936`), weak evaluation gates (`#938`, `#943`), silent effort downgrades (`#940`), or poisoned knowledge memories (on either `PASS` or `FAIL`) to relax safety rules instead of genuinely improving capability.

Therefore, `g8s` does not need to prove that an AI model is always right. **It must prove that even when an AI agent errs, is prompt-injected, or modifies behavior over time, safety boundaries, independent verification, and atomic recovery hold — agents may experiment and improve, but may never self-authorize or weaken the rules defining safety. Self-improvement is what TRUST permits — never what holds authority to redefine TRUST.**

---

## Decision

We position `g8s` as **A Verifiable Zero-Trust Execution & Evolution Infrastructure for Autonomous AI Agents** and govern all releases from `v0.25.1` forward by the following TRUST North Star specification.

### 1. Seven Foundational TRUST Principles

| Principle | Binding Operational Meaning |
| :--- | :--- |
| **Bounded** | Every action is strictly bounded in capability, path scope, TTL, process tree, and token/cost budget. |
| **Verifiable** | No task or self-improvement candidate self-certifies PASS; claims require executable tests and independent evidence. |
| **Attributable** | Every artifact, receipt, commit, and promotion traces back to principal, model, policy, diff, and verification SHA. |
| **Recoverable** | Worker or coordinator crashes isolate dirty attempts, reap orphans, preserve accepted state via SQLite WAL + CAS, and roll back cleanly. |
| **Governed** | Signing keys, TCB policy files, verification adjudicators, and promotion authority sit strictly outside untrusted worker reach and outside LLM inference. |
| **Evolvable** | Controlled self-improvement (`SENSE -> PROPOSE -> EXPERIMENT -> GATE -> PROMOTE/ROLLBACK`) operates measurably within hard bounds. |
| **Non-Self-Certifying** | Trust must be measurable and non-self-certifying: no agent, coordinator, or model provider may certify its own trustworthiness; every TRUST claim must bind to independent, reproducible evidence valid for the actual deployment environment. |

### 2. Non-Compensatory Security Veto Rule

Aggregate readiness rubrics (such as `>= 85/100` for production evolution readiness) are subject to a **strict non-compensatory security veto**: any critical boundary escape, unauthorized privilege escalation, forged receipt acceptance, or promotion without valid evidence fails TRUST unconditionally regardless of scores in other categories.

### 3. Six Mandatory Trust Gates (`TRUST-G1`..`TRUST-G6`)

> **Namespace Rule**: `TRUST-G1`..`TRUST-G6` are intentionally namespaced separately from CI/PR engineering gates `G1..G6` (`.g8s/lane-bundles.yml`) and `G0–G7` ([ADR-0033](0033-f0-verified-delegation.md)) to prevent policy and automation collisions.

| Gate | Requirement | Falsifiable Enforcement Target |
| :--- | :--- | :--- |
| **TRUST-G1 — Hostile Worker Containment & Mediated vs. Enforced Isolation** | Pre-execution OS containment (file/process/secret) enforces scope before exit; post-run diff firewall is a NUL-safe `-z` backstop, never a substitute for pre-exec isolation. Explicitly distinguish **mediated access** (valid calls routed via `v0.30.0` Scoped Capability Proxy) from **enforced network isolation** (OS/container blocks bypass sockets via `bwrap --unshare-net`, Seatbelt `(deny network-outbound)` outside loopback proxy, or container/VM boundaries). Whenever OS-level socket blocking is absent, `g8s` MUST report `network_enforcement: mediated_only` and never claim full zero-trust network isolation on proxy alone. | `0` critical boundary escapes; fail-closed `--require-kernel-sandbox` refusal when required kernel enforcement is unavailable (`#935`, `#936`, `#672`). |
| **TRUST-G2 — Trust Root Outside LLM Inference & No Self-Authorization** | Per [ADR-0035](0035-orthogonal-stratum-federation.md), `S0 Principal` is the **Human Operator**, whereas `F0`/`S2` Brain is a high-tier reasoning LLM. Because frontier LLMs remain vulnerable to prompt injection and context drift, **the Trust Root MUST reside strictly outside LLM inference** (deterministic Go policy engine + external key custody + `S0` Human Principal). Signing keys (`f0-signing.key`), `.g8s/trust-boundaries.yml`, deterministic verification binaries, and promotion authority remain immutable to `F1`/`F2` workers and bounded against `F0` bypass. | `0` unauthorized privilege escalations; `.g8s` mutations never exempted (`#935`); `Lane S` TCB changes require deterministic checks + explicit `S0` Human approval. |
| **TRUST-G3 — Independent Evaluation** | Evaluation executes out-of-band against an **immutable out-of-worktree holdout suite**, seeded mutant canaries (`VetoCanarySurvived`), runtime-proven claims (`--test-report`), and explicit capability parity (no silent effort downgrades). | `0` vacuous false-PASSes (`as-002`); `100%` runtime-proven bound claims (`#938`, `#940`, `#941`, `#943`). |
| **TRUST-G4 — Recoverability** | Hard process crashes, `SIGKILL`, stale leases, clock skew, and duplicate retries lose zero accepted work and leave zero runaway grandchildren or dirty main-branch state. | Automated POSIX orphan sweep proof (`#942`), `exactly-one` accepted `deliver` invariant under WAL contention, and rollback within committed RTO (`5m`). |
| **TRUST-G5 — Evidence Integrity & Metric Epistemology** | Every release, receipt, and promotion records matching provenance (`manifest.json`, Cosign Sigstore bundle, SPDX SBOM) and surfaces `requested` vs `effective` enforcement honestly. Every assurance or risk number (including System-1 Jev `risk_score` and `breach_prob`) MUST declare its epistemic class: **`measured_metric`** (empirical result pinned to commit SHA, OS, sample size, and command) vs. **`estimated_heuristic`** (uncalibrated triage prior — never presented as measured exploit probability). | Deterministic installer fail-closed (`#937`), honest benchmark/manifest telemetry (`#936`, `#939`), tagged `measured_metric` vs `estimated_heuristic` fields, and `100%` promotion provenance completeness. |
| **TRUST-G6 — Bounded Evolution Economics, Universal (`PASS`/`FAIL`) Memory Promotion & ToS-Safe Automation** | Self-improvement loops operate under hard per-cycle token, quota, wall-clock, retry, rate, and anti-oscillation (`K` non-improving rounds) caps. **ToS-Safe Automation Rule**: autonomous execution and evolution loops MUST use only operator-owned official API keys or official vendor CLIs under a single official account posture (prohibiting free-tier account rotation, subscription-proxy routing, or proprietary IDE-protocol evasion). Because **Task Success != Knowledge Validity**, **BOTH `PASS` AND `FAIL`** paths emitting knowledge artifacts (memories, lessons, routing heuristics, policy proposals) MUST attach `(provenance, confidence, applicability_scope, promotion_status=PROPOSED, revocable=true)` and pass through the Memory Promotion Gate + Tombstone Monotonicity ([ADR-0023](0023-memory-lifecycle.md)) + Jev L3 verification before becoming durable policy or active routing law. | `0` runaway F0 quota burn (Lesson #23); `0` unverified self-promotions or over-generalized lessons from either `PASS` or `FAIL` runs (`#943`); `0` prohibited proxy/account-rotation routes (`#945`). |

### 4. Trust Loop & Two-Tier Evolution Autonomy Split

Extending `REFLEX-CYCLE` (`SENSE -> JUDGE -> ACT -> PRUNE-LAW`):

```text
SENSE (Failures, Receipts, Holdout Metrics)
  -> PROPOSE (Prompt / Skill / Routing / Code Candidate)
  -> EXPERIMENT (Isolated Worktree A/B + Regression + Red-Team Suite, Bounded TRUST-G6 Budget)
  -> INDEPENDENT TRUST GATE (Policy Contract TRUST-G1..G2 + Holdout/Canary TRUST-G3 + TRUST-G5 Provenance)
       |-- PASS --> Canary -> Promote -> Monitor (Auto-Rollback on Regression)
       |              '-- Knowledge Artifact (PROPOSED, Provenance+Scope+Revocable) -> Memory Promotion Gate
       '-- FAIL --> Reject -> Quarantined Record -> Verified Learn (TRUST-G6 Tombstone + Jev L3)
```

To avoid freezing evolution behind 100% manual review while keeping the Trust Root strictly outside LLM inference:
- **Tier A — Bounded Autonomous Evolution (Non-TCB)**: tuning routing weights, skill/prompt canaries, model scorecards, and auto-rollback on holdout regression may iterate autonomously inside `TRUST-G6` budgets and `TRUST-G3` holdout gates, emitting revocable `PROPOSED` records gated before permanent promotion.
- **Tier B — `S0` Human-Gated TCB Evolution (`Lane S`)**: any proposal touching `.g8s/trust-boundaries.yml`, `internal/sandbox/`, `internal/receipt/`, `internal/fsops/`, `internal/verifier/`, `internal/worker/diff_firewall.go`, or `scripts/install.sh` can generate proposals and red-test proofs only; deterministic checks + explicit `S0` Human approval are strictly mandatory before merge or promotion.

### 5. Evidence-Gated Milestone Mapping & Explicit Non-Goals

Priority order starts strictly with P0 Trust Boundary (`v0.25.1`) before expanding autonomous self-improvement:

| Milestone | Scope & Trust Gate Closure |
| :--- | :--- |
| **`v0.25.1`** (Wave 1–2: [#935](https://github.com/tamld/g8s/issues/935)–[#939](https://github.com/tamld/g8s/issues/939), [#945](https://github.com/tamld/g8s/issues/945)) | **P0 Trust Root, Supply-Chain & Vendor-Neutral ToS Hygiene Hardening**: unified NUL `-z` non-claim diff firewall (no `.g8s` exemption, `.gitignore` delta, `read_only` fail-closed), `RequireKernelEnforced` strict sandbox mode, narrowed `9router` env allowlist, fail-closed installer (`darwin_all` fix, no `v0.1.0` downgrade, optional Cosign/attestation gate), vendor-neutral Layer 1/Layer 2 gateway separation & ToS-safe automation posture (`#945`), runtime-proven `claims_check.sh --test-report`, and accurate release tag `manifest.json`. |
| **`v0.26.0`** (Wave 3: [#940](https://github.com/tamld/g8s/issues/940)–[#943](https://github.com/tamld/g8s/issues/943)) | **P1 Independent Evaluator & Provider Parity**: `ProcessTransport` vs `RequestTransport` split, async HTTP `Handle.Cancel()` + streaming + Anthropic effort forwarding, `agent-models.v2` capability gate, offline 8-CLI + HTTP adapter contract suite, automated POSIX `SIGKILL` orphan sweep E2E, single-host multi-coordinator WAL fencing stress E2E, unified `PolicyEnforcementReport`, immutable out-of-worktree holdout eval, and `TRUST-G6` bounded evolution / universal `PASS`+`FAIL` memory promotion guards. |
| **`v0.30.0`** | **Scoped Capability Proxy & Controlled Self-Improvement v1**: F1/F2 MCP/HTTP capability proxy (mediated access by default; enforced network isolation where OS/container blocks direct sockets, explicitly reporting `network_enforcement`), `Tier A` canary self-improvement loop under `TRUST-G1`–`TRUST-G6`, and 90-day multi-repo Offer burn-in. |
| **`v0.40.0` -> `v1.0.0`** | **Cryptographic Pre-Execution Gate & GA Lock**: artifact-bound CVE/NVD & Socratic debate receipts before `WriteReceipt` minting, full `TRUST-G1`–`TRUST-G6` pass, `>= 85/100` independent audit rubric with non-compensatory security veto satisfied, `>= 50` receipt-backed real dispatches, `0` P0 escapes, and `>= 90%` First-Pass Yield. |

**Explicit Non-Goals (`v0.25`–`v0.40`)**:
- Multi-node distributed Raft or network-partition consensus (verified architecture is single-host SQLite WAL + read-only `S1 -> S2` charter federation).
- Root-requiring kernel eBPF/iptables network firewalls inside standard developer/CI user environments (egress control relies on the `v0.30.0` userspace Scoped Capability Proxy — reported as `mediated_only` unless backed by OS/container socket isolation).

---

## Consequences

- Documentation (`README.md`, `README.vi.md`, `docs/security/containment-matrix.md`) strictly maintains **Claims <= Proof**, distinguishing verified `v0.25.0` containment from active `v0.25.1` and `v0.26.0` hardening items, and tagging heuristic triage signals separately from measured assurance metrics.
- Epic [#934](https://github.com/tamld/g8s/issues/934) tracks all 9 concrete engineering deliverables (`#935`–`#943`) mapped directly to `TRUST-G1`–`TRUST-G6`.
- Future `self-improving` workflows must satisfy both `TRUST-G3` holdout gates and `TRUST-G6` universal `PASS`/`FAIL` memory promotion gating before modifying active routing, prompts, or durable memory.
