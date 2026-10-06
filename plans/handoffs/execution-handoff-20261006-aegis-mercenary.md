---
handoff-version: 1
generated: 2026-10-06T23:55:00+07:00
generator: handoff@1.0.0
focus: "MERCENARY session — pay aegis technical debt WITH the g8s factory (everything through g8s workers), return artifacts (lessons, defects, telemetry, ladder traversals) to g8s. Special directive from the operator."
workspace: github.com/tamld/aegis (work) + github.com/tamld/g8s (returns)
branch: aegis main
head: 89a6c1c7d
---

# MERCENARY HANDOFF: pay aegis tech debt — dogfood g8s, ship artifacts home

**Session type**: T2 (execution), F0 supervisor role over aegis work.
**Operator directive**: "trả nợ kỹ thuật project aegis... lính đánh thuê,
vừa làm tận dụng tối đa resource của hệ thống, vừa dogfood từ project
đánh thuê để về lấy artifacts cho g8s."

## The two-sided contract (this is the whole mission)

1. **Aegis gets paid**: its technical debt inventoried, triaged, and
   fixed in waves — using the g8s factory for every unit of work.
2. **g8s gets paid back**: everything the factory touches teaches it —
   filed as issues/lessons/telemetry per the Returns Ledger below.
   A mercenary that only fixes aegis has done half the job.

## Context (read before anything)

- **aegis** = `@tamld/aegis` (formerly web-login-solo): TypeScript +
  infra (docker-compose, minio, infrastructure/), Universal Gateway
  routing into `.agents/` SSoT (V2.1) per its AGENTS.md. Own issue
  tracker with 4-digit numbers (#2351+). NO CI — that is why worker
  verification matters here.
- **Known debt seeds**: `docs/audits/` holds a pass-2 audit with a
  follow-up queue — "5 red files remain (#2354)". Wave-3 first-audit
  (g8s task afa1d218) additionally found: 4 orphaned/stale root docs
  artifacts, legacy repo name ("web-login-solo") citations, ADR
  status/version drift.
- **Onboarded to g8s (wave-3)**: `.g8s/effort-classes.yml` committed
  (infra-review→high HYPOTHESIS for infrastructure/**, test→medium,
  docs→low), AGENTS.md wired. The registry's high is a hypothesis —
  your dispatches are the evidence that earns or kills it.
- Working tree: 1 untracked dir `records/metrics/` — NOT yours, never
  touch it. Everything else clean on `main` @ 89a6c1c7d.

## The dogfood protocol (how every unit of work happens)

- ALL work through g8s: brief (in-repo, aegis side:
  `plans/<slug>/brief-*.md`) → `g8s submit` → worker drain → verify →
  commit/PR. You are the F0 supervisor; you never hand-edit aegis
  deliverables except recorded-exception pinches (precedent exists,
  justification mandatory).
- Effort: declare signals (`-blast-radius`, `-loc-estimate`) — the
  aegis registry + matrix v1.1 resolve; infrastructure/** tasks will
  declare high blast → expect infra-review high, and RECORD whether
  high was worth it (that datum goes home).
- Receipts: workspace_write needs `AGY_MCP_ALLOW_WORKSPACE_WRITE=1` on
  submit AND worker, a fresh receipt (TTL ≤3600, always pass explicit
  --ttl), scoped to the aegis paths you touch.
- State dir: use YOUR OWN `G8S_STATE_DIR=~/.local/state/g8s-sess-aegis`
  (ADR-0028 doctrine — never share a state dir across sessions).
- Parallel drain allowed for disjoint receipts (proven). NEVER reap
  worktrees while tasks are RUNNING. Harvest preserved worktrees
  immediately (delivery-loss history, #551).
- Verify like the patrol does: after each fix wave, re-run the
  matching read_only audit — last wave's findings are this wave's
  regression tests.

## Returns Ledger (ship these home to g8s)

1. **Defects**: any g8s misbehavior discovered while working (wrong
   classification, transport bugs, ladder misses) → file issues on
   g8s. NEVER patch g8s main from this session.
2. **Lessons**: operational lessons → `g8s lesson` CLI (Wave I
   pipeline: LLM proposes, machine verifies, operator ratifies).
3. **Telemetry**: cost-per-class data points for the infra-review
   class (first real evidence for the high hypothesis) → comment on
   g8s #550 with task ids + tokens + durations.
4. **Ladder traversals**: real effort-shaped failures in aegis work
   are the ladder's first rung-climbing candidates — run
   `g8s ladder status/advance`, record on #550.
5. **End-of-session handoff**: `plans/handoffs/execution-handoff-
   <date>-aegis.md` + SCORECARD row update + #442 comment.

## Hard boundaries

- **tamld-llm-wiki = hardgate, HANDS OFF** (another agent owns it,
  optimizing query/memory).
- **g8s main untouched** (issues + lessons only; no PRs to g8s).
- `records/metrics/` in aegis — not yours.
- Artifacts in English; Vietnamese only with the operator. NO
  keyword+issue-number adjacency in commit/PR bodies ("Part of #N"
  phrasing). No local absolute paths in committed docs.
- Quota wall → skip + reschedule (one-shot retry pattern is proven);
  never retry blindly.
- v0.16.0 of g8s may be cut mid-session by another session — a `git
  pull` of g8s + binary rebuild mid-session is expected (stale-binary
  lesson: rebuild before trusting classifications).

## Exact next actions

1. Read aegis AGENTS.md + `docs/audits/` (pass-2 + the #2354
   follow-up queue with 5 red files) + `docs/adrs/` — then dispatch
   read_only inventory sweeps (code debt: TODO/FIXME/dead paths/test
   health; docs debt: the wave-3 findings still present?) — 2–3
   parallel disjoint tasks.
2. Triage the inventory into fix waves (disjoint file scopes for
   parallel receipts; infra tasks declare high blast honestly).
3. Execute fix waves; verify each with a re-run audit; commit per
   wave with "Part of #2354"-style references per aegis convention.
4. Maintain the Returns Ledger continuously (not at the end).
5. Close the session with the handoff + ledger report on g8s #550.

## Source pointers

- g8s effort design + pass-3: plans/261004-effort-optimization/ on g8s
- Patrol + wave evidence: g8s issues #550 (comments tail), #442
- v0.16 cut plan: plans/261006-v016-cut/plan.md (g8s)
- Memory: g8s-effort-optimization, g8s-dispatch-mechanics-v012,
  feedback-jev-escalation-autonomy
