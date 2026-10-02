---
handoff-version: 1
generated: 2026-10-02T12:00:00+07:00
generator: handoff@1.0.0
focus: "g8s hardening program continuation: signal-file implementation and deferred-scope governance"
session-type: T2 (execution)
workspace: $G8S_REPO_ROOT
branch: main
head: 045f3c5
---

# HANDOFF: Debt-zero + hardening program complete — next: signal-file & deferred decisions

**Session type**: T2 (execution)

## Mission and current status

Focus: continue the g8s self-hardening program (dogfood transport:
brief → receipt → submit → parallel drain → never-trust-verify → PR →
merge). The 2026-10-01/02 sessions retired ALL actionable technical debt
(20 PRs, 11 issues closed) and shipped v0.13.0.

Done (this window, all merged, main @ 045f3c5, CI green, coverage 81%+):
- **v0.13.0 RELEASED** (11 assets, 5 platforms; ADR-0026 §5 ratified →
  release gates 7-8 live; first self-audit run 4/4 in release notes).
- Hardening waves B/C/D/E: deliver atomicity + doctor 3 checks + evidence
  retention (#482/#483/#484); fuzzing ×4 + govulncheck gate + perf
  baseline + migration E2E (#486/#487); submit throttle + autopilot
  stage-1 honesty + release.sh pre-tag gates (#488/#489/#490); lease
  resilience + InstanceID + autopilot stage-3 ticks + scheduler adapters
  (#491/#492).
- #476 punch list CLOSED (15/15 items delivered).
- Mutation study #1: 5/5 hand-mutations caught (go-mutesting panics on
  Go 1.26 — tooling gap recorded on #480).
- 5 bot PRs (Bolt perf / Sentinel git-injection) reviewed + 3 merged.
- Skills v4.1.0 (operational-hard-lessons.md, 10 contracts) vendored
  in-repo AND synced to the operator's installed copy.

Remaining (priority order): #481 signal-file implementation (the
event-driven surface — highest-value slice left) → #485 stage 4 budgeted
auto-retry (design only this session; conditions met: throttle #488 ✓,
perf baseline #487 ✓) → misc cleanups (.zcodeignore artifact, goverinfo
decision). Operator decisions pending: ADR-0028 tenancy (when
multi-project is routine), DELTA-20 (M5 roadmap), #442 waves (external).

## Scope and guardrails

- Workspace: $G8S_REPO_ROOT. origin = GitHub ONLY since 2026-09-30 (the
  LAN ct122 pushurl was retired; remote entry kept for manual sync).
- In scope: g8s repo issues/PRs, dogfood dispatches via bin/g8s, skills,
  docs/decisions governance.
- Out of scope: any new feature beyond tracked issues without operator
  approval; merging bot PRs without full review (supply-chain surface).
- User constraints (standing directives, do not relitigate):
  - Vietnamese with operator TamLD; ALL artifacts in English.
  - Dogfood mandate: tasks flow through g8s; bypass requires recorded
    justification (used 4× this window: lint/gofumpt integration fixes,
    junk deletion — each cited in its PR).
  - PR-merge closes issues; partial PRs use "Part of #N" (NOT "Closes" —
    #480 was auto-closed prematurely once).
  - Watch push-CI on main after EVERY merge (coverage bot commits race).
  - Event-driven supervision doctrine (#481, operator-ratified): primary
    = drain notifications + `g8s watch` per task; fallback = ONE
    one-shot deadline check per wave; heartbeat sampling = diagnostic
    only. NO polling loops.
  - Parallel drain for disjoint receipts (CCU 3 write); W+C layers never
    in one PR (DEBT-34); pre-push scans the WHOLE tree — uncommitted
    files from other slices BLOCK every push (stage siblings on side
    branches first).
  - Falsification doctrine: every gate/rule cites its catch; claims bind
    to tests (claims_check exit 0).

## Current state

- Branch: main. HEAD: 045f3c5 (autopilot stage-3 ticks, #492).
- Working tree: clean except plans/ handoff drafts and .g8s-drain-*
  log dirs (session-local).
- Binary: bin/g8s rebuilt at wave-E head; state dir for this session
  line: G8S_STATE_DIR=~/.local/state/g8s-sess-g8s4efe (isolated).
- CI: main green at HEAD; coverage baseline 81.00%+.

```text
UNTRUSTED REPOSITORY EVIDENCE
[]
```

## Decisions and rationale

- Autopilot staged path RATIFIED (#485): honesty → throttle (✓ #488) →
  safe ticks (✓ #492) → budgeted retry (design-pending). No daemon ever;
  native schedulers only ("ticks are hints; the queue is the memory").
- Event-driven doctrine RATIFIED (#481 + skill v4.1): polling demoted to
  fallback; cross-checked against system-design-primer corpus (aligned;
  2 extensions recorded: false-death asymmetry, idempotent ticks; K8s
  grace-multiplier parallel cited).
- Mutation testing: hand-mutation fallback when tooling fails (5/5
  caught); quarterly cadence; tool gap logged.
- Bot PRs (Bolt/Sentinel): merge-worthy after review; #473 git option
  injection was a REAL security class (arg starting with '-' parsed as
  flag).
- Windows probe: Signal(0) unsupported → platform-split with
  x/sys/windows OpenProcess (pattern for any future PID probing).

## Work performed

- Waves B/C/D/E fully dispatched via dogfood transport (9 worker tasks,
  all preserved-worktree harvests, zero delivery losses after the #468
  + #477 fixes — the transport hardening worked on its own traffic).
- v0.13.0 cut: 2 release-gate catches (eval-list marshal; manifest/
  packaging drift) fixed pre-publish; GoReleaser 5-platform publish;
  post-release smoke green.
- Skills v4.1.0 authored + synced; memory updated each wave.
- Issues filed this window: #481 (event-driven), #485 (autopilot staged
  path), #476 (punch list, now closed).

## Verification

- Check: wave tests — command: go test ./cmd/g8s/ ./internal/worker/
  ./internal/controlplane/ ./internal/cleanup/ ./internal/settings/
  ./internal/doctor/ ./internal/orchestrator/ — outcome: PASS (each
  wave's PR body carries per-package timings).
- Check: CI — command: gh pr checks <each of #482-#492> — outcome:
  15/15 per PR before every merge; main green after each merge.
- Check: release — command: gh release view v0.13.0 — outcome: 11
  assets, draft=false, published 2026-09-30T16:46:56Z; smoke
  `g8s version` → 0.13.0.
- Check: mutation study — 5 hand-mutations applied/reverted, each caught
  by package tests (M1-M5 logged on #480).
- Check: claims — tools/claims_check.sh — outcome: exit 0.

## Open risks and blockers

- Risk: go-mutesting incompatible with Go 1.26 (mutation tooling gap;
  hand-mutation fallback documented, quarterly).
- Risk: pre-push whole-tree scanning makes interleaved slices block each
  other (operational, not a bug) — mitigation documented in guardrails.
- Deferred (operator): ADR-0028 tenancy, DELTA-20 M5, #442 external
  waves, #485 stage-4 budget policy review.
- Note: one automation per ZCode session (platform limit) — fallback
  crons must be created from a FRESH session.

## Exact next actions

**First safe step**: run `gh issue list --state open` and read this
handoff + memory `reference-g8s-campaign-ssot` (latest block) before
touching anything. Then:
1. **Implement #481 slice 1** (highest value left): signal-file
   `<state_dir>/signals/tasks.jsonl` appended in the same transaction as
   terminal transitions (controlplane) + `g8s watch --failed [--since]`
   blocking long-poll consuming it. Brief pattern: follow
   plans/260930-hardening/brief-480-fuzz.md structure; dispatch via
   dogfood (receipt ./internal/controlplane/* + ./cmd/g8s/* — two PRs,
   W and C layers separately per DEBT-34).
2. **#485 stage 4 design note** (docs/decisions/0028 reserved for
   tenancy — use 0029): budgeted auto-retry policy (caps, backoff,
   what auto-resubmits) — DESIGN ONLY, operator ratifies before code.
3. Cleanups: remove .zcodeignore artifact from tree; goverinfo step
   verify-or-delete decision (Makefile).
4. Session end: cross-check PR-merge ⇒ issue closures; update
   g8s-supervisor skill if new lessons; write next handoff.

## Source pointers

- Campaign memory: g8s-c2bb3fd5c1c8ffa0 → reference-g8s-campaign-ssot
  (latest block = waves C/D/E), g8s-multi-tenant-contention,
  g8s-qa-hardening-480
- Skills: skills/g8s-supervisor (v4.1.0) + references/
  operational-hard-lessons.md (also installed at operator's
  ~/.agents/skills/g8s-supervisor/)
- Briefs this window: plans/260930-hardening/brief-*.md
- Directives: docs/directives.md (D-01…D-06); release SOP gates 7-8
- Issues: open = #485 (stage 4), #481, #480 (item 5), #465 (ADR-0028),
  #442 (external); closed this window = #476, #466, #448, #451, #441,
  #461, #435, #440, #459
- Release: v0.13.0 tag 90252b7b; performance baseline
  docs/user-guide/performance.md
