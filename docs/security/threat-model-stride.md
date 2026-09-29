# g8s Threat Model — STRIDE over Declared Trust Surfaces

**Status**: Accepted (living doc — re-validated per ADR-0026 Red Cell gate)
**Date**: 2026-09-29
**Owner**: operator + Brain (worker-delivered fixes cite rows below)
**Asset inventory**: `.g8s/trust-boundaries.yml` (Lane-S routing registry, ADR-0024)
**Related**: #438 (this doc), ADR-0025 (single-node CP), #446 (artifact-altered), #451 (self-audit suite)

---

## Method

Every declared trust surface gets a STRIDE row set: **threat → existing
mitigation → gap → severity**. Gaps either become tracked issues or are
accepted **with written rationale** — an unexplained gap is a finding.
Severity: `high` (exploitable from worker/model-controlled input),
`medium` (requires local access or unusual conditions), `low`
(defense-in-depth redundancy).

The inventory is the routing source of truth: a PR touching any
`trust_boundaries` path routes to Lane S, and a change to a surface must
update its rows here in the same PR.

## S1 — Receipt (internal/receipt)

Delegation is the root capability; a forged or replayed receipt is total
compromise of the write path.

| Threat | Mitigation | Gap | Sev |
|---|---|---|---|
| **S** — forged receipt | schema v3 canonical envelope; issued only by the local CLI after harness validation | none known | — |
| **T** — replay of a consumed receipt | single-use flag consumed at claim; `consumed` + `consumer_task_id` recorded | none known | — |
| **R** — "I never granted that write" | receipt rows persist (issuer, paths, TTL, consumer) in receipts.db | no export/audit UI yet (Evidence Ledger #441) | low |
| **I** — receipt paths leak operator home layout | paths stored as absolute globs (needed for enforcement) | accepted: local single-operator file, not transmitted | low |
| **D** — receipt table flooding | TTL (1..3600s) expiry; rows reaped by cleanup | none known | — |

## S2 — Containment (internal/worker proc_*)

A worker that escapes containment turns `workspace_write` into host write.

| Threat | Mitigation | Gap | Sev |
|---|---|---|---|
| **E** — process-group escape (setsid double-fork) | Layer 1: group sweep (`kill(-pgid)`, #417). **Layer 2: marker-based descendant walk, exact `G8S_RUN_MARKER` match, bounded — #453** | none known on POSIX; Windows: Job Objects (#429) | — |
| **E** — delivery bypass (agent writes outside worktree) | receipt path-scope is the real enforcement (proven #434 task 7420893a); worktree is advisory hygiene | **#448**: delivery contract non-deterministic (agent-choice); deterministic `deliver` path pending | medium |
| **D** — worker storms (runaway fleets) | local CCU ceilings; orphan sweep; `cleanup` reapers | no cross-session CCU admission control yet (per-session only) | low |
| **T** — worktree deliverable destruction | `Release(keep=true)` preserves dirty worktrees (#450); reaper is age-based | branch retention budget (2,350→0 reaped 2026-09-29; guard: merged-only) | low |

## S3 — Auth (internal/server)

The MCP/API surface is loopback-bound single-operator by non-goal.

| Threat | Mitigation | Gap | Sev |
|---|---|---|---|
| **S** — bearer token theft | loopback bind (not reachable off-host); token required | accepted: single-operator local surface — no RBAC/rotation until a networked deployment exists (same revisit trigger as ADR-0025) | low |
| **E** — local process impersonating the operator | filesystem permissions on the state dir; executable identity verification (#396) | accepted: local trust boundary = OS user | low |
| **D** — request flooding on the MCP surface | in-process rate/scope gates | accepted: loopback-only | low |

## S4 — Memory promotion (internal/memory)

The vault is the knowledge SSOT; poisoning it poisons every future session.

| Threat | Mitigation | Gap | Sev |
|---|---|---|---|
| **T** — vault poisoning by unverified worker output | memory promotion gate FSM + trust labels + payload-hash tombstones (#408) | promotion sensor wiring (#413) is advisory-by-default; blocking earned via #418 enriched runs | medium |
| **R** — "who promoted this entry?" | provenance fields on promoted records | no query surface for provenance audit yet (#441) | low |
| **I** — secrets entering the vault | naked-payload Jev gate; SanitizeOutput upstream | vault content is operator-local; accepted | low |
| **D** — SQLITE_BUSY writer storms | WAL + busy_timeout; conditional fix packet preserved (#440 — fires on third logged failure) | accepted per falsification clause | low |

## S5 — Dispatch sanitization (internal/dispatch)

Sanitizers sit on the model-controlled output path; both under-redaction
(secret leak) and over-redaction (payload corruption, #434) are threats.

| Threat | Mitigation | Gap | Sev |
|---|---|---|---|
| **I** — secret leakage through surfaced output | `SanitizeOutput` (URLs, credential assignments, backticks) | escaped-quote values now normalized (#445 case 8) | — |
| **T** — sanitizer corrupts legitimate payload (availability of evidence) | transport-fidelity rule (ADR-0026 §2): escape pairs atomic, public-literal core test (#445) | **#446 in flight**: `artifact_altered` flag so success ≠ fidelity claim | medium |
| **E** — refusal-classifier self-trigger discards evidence | classification scoped to final response (#444) | blocked-verdict worktree preservation (#450) | — |
| **D** — regex catastrophic backtracking | RE2 (no backtracking) — constitution invariant | none known | — |

## Cross-cutting

| Concern | State |
|---|---|
| Self-audit of the harness itself | **#451** (probes encode rows above; ADR-0026 §4) |
| Evidence Ledger / audit export | **#441** |
| Eval PRI release gate | **Decision**: current PRI 0.625 < 0.8 — the gate is NOT adopted (adopting now would red every release). Adoption condition: PRI ≥ 0.8 measured on 2 consecutive eval runs → promoted to the Red Cell release checklist. Advisory until then. |
| Red Cell release gate | ADR-0026 §1 (proposed, operator ratification pending) |

## Change log

- 2026-09-29: initial STRIDE pass (#438). Session-closed gaps: setsid
  escape (#453), sanitizer transport corruption (#445), refusal
  self-trigger (#444), worktree discard (#450). Open: #448 (medium),
  memory promotion blocking earned trust (#418, medium), #446 (medium,
  in flight), #441/#451 (low).
