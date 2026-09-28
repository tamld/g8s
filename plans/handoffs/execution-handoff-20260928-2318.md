# HANDOFF: v0.12.0 Campaign Complete — Architecture Review + Enterprise Spearheads

**Session type**: T2 (execution)
**Date**: 2026-09-28 23:18 · **Branch**: main @ fd10dbf · **Author**: Tâm

## Mission state (complete)

All 12 campaign issues closed (#379–#420, #427), zero open at campaign end. v0.12.0 released (11 assets). Then post-campaign review opened 6 new issues (#437–#442) from the 5-dimension architecture review — these are the next session's backlog.

## Merged this session (17 PRs, author Tâm, full ALDC each)

| Slice | PRs | Delivers |
|---|---|---|
| #394 concurrent dispatch | #403/#404/#405 | `--concurrency N`, per-attempt worktrees, OnError seam |
| #397 gate covers handoffs | #406 | session-type marker gate on handoffs |
| #383 result schema validation | #407 | whitelist schema, sanitization, single-read TOCTOU |
| #395 memory promotion gate | #408 | FSM + tombstones + naked-payload Jev + CLI |
| #379 live eval | #409 | agy/claude adapters + semantic scoring, PRI 0.625 |
| #415 PR-1/2/3 | #417/#423 | orphan sweep + containment runbook |
| #411 | #412/#413 | L3 advisory default + promotion sensor wired |
| #416 README + skills | #416 | scientific restructure + g8s-supervisor v4.0.0 vendored |
| #420 S6 gates | #424/#425/#426/#428 | G1–G6 gates + router + registry + audit runner (worker-delivered) |
| #427 isolation | #429/#430 | WorktreeIsolator + cmd wiring |
| #431 offer CLI | fd10dbf | `g8s offer init/check/version` — bundle embedded in binary |

## Architecture review (system-design-primer cross-check, 2026-09-28)

Five-dimension scores (full review in session log):

| Dimension | Score | Gap |
|---|---|---|
| An toàn | 8/10 | external audit + threat model doc |
| Tin cậy | 7/10 | Windows SQLITE_BUSY flake (watched, #440); setsid POSIX escape (#439) |
| Sẵn sàng | 5/10 | single-node CP by design — needs ADR-0025 to NAME the trade-off (#437) |
| Tường minh | 8/10 | access-audit vs operation telemetry split + Evidence Ledger (#441) |
| Đa phiên/đa nhiệm | 7/10 | concurrent write-attempt e2e test (#427 DoD residual) |

**Blast radius isolation (7 layers, all live)**: per-attempt (run dir 0700 + worktree #427), per-receipt (single-use/TTL/path), per-session (flock + registry + reap), per-lane (gate bundles), per-trust-zone (P0 routing), per-surface (memory gate + sanitizer), per-cleanup (ghost/orphan/scratch sweeper). Only POSIX setsid-escape is outside the containment envelope (Windows covered by Job Objects).

## Open issues (next-session backlog, priority order)

| # | Issue | Priority | Slice size |
|---|---|---|---|
| #438 | **Threat model STRIDE** over trust surfaces (registry = ready asset inventory) | P2 · spearhead 2 | 1-2 slices |
| #442 | **Offer rollout waves** wiki → DiD → aegis/homelab → hash-checker + SCORECARD | P2 · bundle proof | per-wave |
| #439 | **POSIX setsid escape hardening** (process-tree accounting layer 2) | P2 | 1 slice |
| #441 | **Evidence Ledger + access-audit/SLA split** | P3 | 1 slice |
| #440 | **Memory SQLITE_BUSY retry-with-backoff** (conditional: 2 failures logged; third = must-fix) | P2-conditional | small |
| #437 | **ADR-0025 CAP/CP decision doc** | P3 | tiny |

Recommended order: #438 (threat model converts enterprise conversations) → #442 Wave 1 (wiki onboarding, dogfood the pull path) → #439/#440 as reliability hygiene → #437/#441 as docs polish.

## Standing directives (do not relitigate)

- **Dogfood mandate**: every task flows through g8s (brief-issue → submit → concurrent drain → verify). Bypassing = robbing improvement evidence (memory: g8s-dogfood-mandate).
- **Operator = operator, not code-typist**: tactics are hooks; the checklist rides in the packet (memory: g8s-operator-hooks-doctrine).
- **Pull-based distribution**: projects adopt the offer at their own pace; findings return via Mode-3 contribution packets. No push harvesting (ADR-0024 addendum).
- **Sequential drain for code-writing tasks** until #427 PR-3 (per-attempt pool worktrees for attempts in one process) — the 3-way drain failure is the evidence.
- **Language**: Vietnamese with TamLD; all artifacts in English.
- **Commits**: author `Tâm <63218248+tamld@users.noreply.github.com>` (repo config set).
- **Falsification**: every gate/rule/bundle must cite a bug it caught; zero catches across a campaign → simplify by amendment.

## Entry points for the next session

1. `gh issue list --state open` — 6 issues, priority order above.
2. #438 first: `gh issue view 438` + `.g8s/trust-boundaries.yml` is the asset inventory to write STRIDE against.
3. #442 Wave 1: tamld-llm-wiki main agent self-onboards via `offer/ONBOARDING.md` pull path (raw-URL or git). Scorecard: `offer/SCORECARD.md` (create on first wave).
4. S6 plan: `plans/260927-s6-gate-lanes/plan.md` (S6-1..3 done; S6-4 registry learning loop and S6-5 runner are MERGED — the remaining S6 work is the falsification review ritual at campaign close).
5. Skills: g8s-supervisor v4.0.0 vendored at `skills/g8s-supervisor/` — fold next lessons per the knowledge loop (candidate: v4.1 with the offer/profiles doctrine).
