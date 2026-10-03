# ADR-0032: Containment Levels F0/F1/F2 — Canonical Definitions, Routing Rules, Implementation Audit

- **Status**: Accepted (2026-10-03) — operator clarification directive:
  "F0, F1, F2 là cấp độ lũy thừa, theo cấu trúc supervisor-worker;
  supervisor spawn worker, do đó **F2 là worker được đệ quy bởi chính
  các worker, là subagent của worker**. Làm rõ quan điểm này trong thiết
  kế để không dùng từ ngữ sai, dẫn đến ngữ cảnh sai."
- **Supersedes**: the memory-only routing recommendation of 2026-09-27
  (which stood "awaiting operator ratification" — this ADR is that
  ratification, with the operator's own phrasing as the anchor).
- **Depends on**: ADR-0021 (two-tier axiom), #465/#468 (cardinality
  anti-pattern + ownership partitioning), ADR-0029 (D3: nothing
  automated mints receipts — true at every level), ADR-0030.

## Context

The F-terms lived only in session memory, never as a repo definition.
Worse: the factory program's first wave used `brief-F1..F7` as slice
ORDINALS — colliding with the containment namespace. An agent joining
later reads "F2" and cannot tell subagent-tier from "second slice".
This ADR fixes the vocabulary once, audits the implementation against
it, and reserves the namespace.

## Decision

### 1. Canonical definitions (the exponential containment tree)

- **F0 — Brain/supervisor tier.** Full trust; the authority holder
  (git commits, vault promotion, receipt issuance). One per session
  (Operator↔F0 = 1-1).
- **F1 — worker tier.** Spawned BY F0 THROUGH the g8s harness: queue
  claim, lease, path-scoped receipt, worktree isolation, killable
  process group. F0↔F1 = 1-n fan-out.
- **F2 — subagent tier.** Spawned BY F1 WORKERS THEMSELVES — recursion
  inside the worker, NOT through g8s. Default route: the platform's
  native subagent feature (agy `define_subagent`/`invoke_subagent`),
  which shares F1's sandbox, tool surface, and receipt scope.
  Containment packet: **F2 ⊆ F1 ⊆ F0** — one authority holder per
  chain, matching the two-tier axiom.

The exponential shape is the point and the danger: F0 spawns N workers,
each F1 spawns M subagents — depth 2 multiplies agent count (N×M) while
the F1 token ceiling (~450K for a flash-class worker) is shared across
that worker's whole subtree. Every depth-2 edge therefore carries its
own budget/admission consideration by default.

### 2. Routing rules (the F1→F2 edge)

- **Default: platform-native subagent.** Blast radius (recursive g8s
  would make every F1 a g8s operator — registry/lease/receipt/worktree
  contention multiplies at depth 2+), token economics (platform
  subagents share the parent session; a g8s hop re-pays transport and
  receipts), trust clarity (one authority holder per chain).
- **Reserved: recursive g8s** (worker calls `g8s submit` and runs its
  own worker loop) — only when (a) F2 needs a different provider than
  F1's platform, or (b) F2 needs g8s-grade guarantees the platform
  lacks (receipts, worktree isolation, durable queue). Recursive g8s
  requires a receipt-DELEGATION protocol — a deliberate, ADR-gated
  extension. It does not exist today.

### 3. Cardinality law (restated; operator-endorsed 2026-09-29)

Inside one containment tree every edge is 1-n or decomposed into 1-1
contracts — never n-n. Concretely: Operator↔F0 = 1-1; F0↔F1 = 1-n;
receipt↔worker↔brief = strict 1-1 ("1 issue = 1 worker = 1 receipt");
**F1↔F1 = no edge** (coordination only via frozen briefs + disjoint
receipts); **F0↔F2 = 0 direct** (F2 is visible only through F1's
evidence — no layer-skipping); n workers↔queue = n-1 (WAL
single-winner). n-n exists only ACROSS trees (two supervisors on one
host) and is exactly where g8s broke (#465) — the fix was ownership
partitioning restoring n × (1-n trees). Raising cardinality always
costs a serialization mechanism; low cardinality is a safety feature.

### 4. Naming discipline (the collision rule)

Bare tokens **F0/F1/F2 are RESERVED** for containment levels. Wave and
slice ordinals MUST be prefixed (`WF1..WF7`; the historical
`brief-FN` names predate this rule and are renamed in the same commit).
No future document may use a bare `FN` token for anything else — bare
F3+ does not exist as a level (there are exactly three).

## Implementation audit (2026-10-03, honest)

| Edge | Status | Evidence | Gap |
|---|---|---|---|
| F0→F1 | **SHIPPED + proven** | Every campaign dispatch since v0.12: receipts, leases, worktrees, concurrent drain; 7 worker slices in the factory kickoff alone, zero delivery losses with TTL'd receipts | none known |
| F1→F2 (platform-native) | **SUPPORTED, never exercised** | agy dispatch init exposes `define_subagent`/`invoke_subagent`/`manage_subagent`; zero recorded g8s-session usage | containment is INHERITED (an F2 shares F1's sandbox and receipt gate) but unproven — no test that an F2 cannot exceed F1's scope; no depth-2 telemetry |
| F1→F2 (recursive g8s) | available de facto, **reserved** | the worker CLI can call `g8s submit` today | receipt-delegation protocol does not exist (ADR-gated); deliberately unbuilt |

## Falsification clocks

- If any session records an **F2 exceeding its F1's receipt scope**,
  inherited containment is FALSE → the F2 admission gate becomes
  mandatory (de jure scoping, not inherited).
- If depth-2 telemetry shows platform subagents failing at rates that
  receipts would have caught, the default route flips for that class.
- The naming rule fails the first time a document needs bare `F3+` as
  a level — there are exactly three levels; anything else is an
  ordinal and must carry its prefix.

## Consequences

- The vocabulary now lives in the repo, not in one session's memory.
- Follow-ups (unscheduled, trigger-gated): depth-2 telemetry fields;
  one containment test (F2 cannot write outside F1's receipt scope);
  a receipt-delegation ADR if recursive g8s is ever actually needed.
- The factory waves route F1 dispatch (ADR-0030's router is an F0-side
  tool); F2 routing stays inside the worker's platform by default.

## Related

ADR-0021 (two-tier axiom), ADR-0024 (lane principle), ADR-0029
(budgeted retry), ADR-0030 (context router), #465/#468 (the n-n
anti-pattern and its fix), plans/261002-factory/ (WF ordinal namespace).
