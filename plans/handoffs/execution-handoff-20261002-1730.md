---
handoff-version: 1
generated: 2026-10-02T17:15:00+07:00
generator: handoff@1.0.0
focus: "AI-factory Wave F landed (auto-retry + router + CI lanes + hygiene); next = Wave G routing integration + first unattended round"
session-type: T2 (execution)
workspace: $G8S_REPO_ROOT
branch: main
head: 3578885 (pre-F7-harvest; F7 = issue-505 provider-resolution fix in flight)
---

# HANDOFF: Factory Wave F shipped — next: Wave G + the first unattended round

**Session type**: T2 (execution)

## Mission and current status

The operator ratified the AI-factory program (2026-10-02): g8s evolves
toward self-allocating / self-splitting / self-evaluating / self-
maturing rounds, proven by ground truth — SCORECARD or it did not
happen. Binding correction: **orchestrator thinks, workers do, Jev
distributes** — and **Jev is OPTIONAL** (measured, benchmarked, never
mandatory).

Landed THIS session (main @ 3578885 + F7 in flight):

- **#501 + #502**: budgeted auto-retry (ADR-0029 ACCEPTED via the
  kickoff directive) — deny-by-default classification, budget caps
  (2/task, 10/dir-hour), 5/20/60m backoff, derived idempotency keys,
  `g8s resubmit` + `autopilot tick` retry job (receipt-free only),
  `auto_retry_enabled` ships OFF. **#485 closed.**
- **#503**: ADR-0030 context router — deterministic-first (works for
  every install, zero network), Jev layer env-gated + suggestion-
  validated, eval category `task_routing` (RT-F fallback parity),
  routing coverage 98.7%.
- **#504**: root hygiene sweep (13 drain dirs, 7 coverage outputs,
  root binaries, 147MB dist/, orphan worktrees/ — deleted + gitignored;
  doc-contract gate 10/10 root-hygiene check now ENFORCES it),
  structure whitelist widened to EVERY root entry, README rewritten
  under the **stop-slop** skill (facts to v0.13.0; unverifiable counts
  → claims.yml pointers), README.vi.md de-calqued + terms table.
- **#506**: ADR-0031 CI LANES — build vs docs, self-detected by
  changed paths (deny-by-default), job/required-check names unchanged,
  pre-push fast path. Probe finding recorded honestly: the lane split
  is live for PRs; direct-to-main docs pushes still run the full
  battery (post-push diff vs origin/main is empty → deny-default).
  Fix candidate: push-event `github.event.before` SHA detection.
- **Coverage ratchet campaign (F4/F5)**: worker-dispatched lifts —
  controlplane 68→85.5%, routing 74.8→98.7%. The operator's nudge
  ("em quên dùng worker để đánh thức mình à?") corrected a
  supervisor-direct grind; F4/F5/F6/F7 all dispatched + woken via
  `g8s watch --failed --since` — the event-driven loop consuming its
  own product, 4 consecutive dogfood rounds.
- **Scorecard**: S-1/S-3/S-4 LANDED with artifacts, S-10 added;
  directives **D-09** (retry budget discipline). #480 closed (D-08
  mutation clock + ledger rows via #500). **ADR-0028 tenancy
  ACCEPTED** (ratified with the kickoff).
- **#505 (OPEN, F7 in flight)**: provider-resolution false-negative —
  registered platform_dispatch provider WITHOUT an argv template fell
  into the unknown-provider error; fix = registered-without-template
  falls back to the default agy argv (same as the empty-provider
  miss). Real bug on the multi-provider path the router builds on.

## Exact next actions

**First safe step**: `gh issue list --state open` (expect #505, #465,
#442) + read memory `reference-g8s-campaign-ssot` (factory block).
Then:

1. **F7 harvest** (if the wake hasn't been consumed): task
   2b4ec709-…, brief plans/261002-factory/brief-F7-provider-
   resolution.md, receipt TTL 3600. Verify, PR with `Closes #505`,
   merge after green.
2. **Wave G** (next code): submit-time routing integration
   (#513) + ADR-0024 S6-3 lane router (#514) consuming
   internal/routing. Brief pattern: brief-WF3-router.md.
3. **Wave H (the M1 milestone)**: verifier-class registry (#515) +
   FIRST unattended closed round (#516): goal → brief → dispatch →
   drain → verify → merge with ZERO foreground actions. SCORECARD
   S-7/S-8. The autonomy ladder gates: docs-class first, N clean
   rounds before any level flip (the level-1 flip = operator decision
   recorded on the round).
4. **M3 real-Jev benchmark**: run task_routing against real (not
   mocked) Jev when the operator's TYPESAFE keys are present; record
   the delta on S-4 — tracked as #518 (BLOCKED, non-release-blocking).
5. **Release v0.14.0**: tracked as #517 (Red Cell on new surface +
   8 gates + #510 F4/F5 fold-ins). Wave I = #519 (v0.15).
5. **ADR-0031 follow-up (small)**: main-push docs detection via
   `github.event.before` (see S-10 probe finding).

## Standing gotchas (new this session — do not re-learn)

- `env VAR=~` through nohup does NOT expand → workers silently open an
  empty DB at `./~/...` and report "drained"; use `$HOME`. Stagger
  worker spawns (`sleep 3`) — simultaneous pin-init = SQLITE_BUSY.
- Receipt TTL default = 10 MINUTES; always `--ttl` ≥ task budget
  (3600 for 45m). Consume happens at FINISH; a consume-failure means
  the work is in the preserved worktree — verify + harvest (3× proven).
- `watch --failed` REPLAYS signals at startup and exits — arm with
  `--since <now>` to wake on the NEXT event.
- Coverage metric = per-package MEAN (CGO_ENABLED=0, excl. cmd/g8s);
  darwin-vs-linux gap ≈ 2.8pp — push margins, not exact thresholds.
- Commit messages: keyword+#N auto-closes issues on main push (2nd
  occurrence this session); non-fix commits avoid adjacent keywords.
- Vietnamese prose: product nouns stay English (Brain/worker/task/
  receipt); verbs + syntax natural Vietnamese; calque ban codified in
  plans/261002-factory/brief-anti-slop-doc-review.md.

## Source pointers

- Factory program: plans/261002-factory/{plan,SCORECARD}.md + briefs
  F1-F7; ADR-0028/0029/0030/0031 (all Accepted).
- Memory: reference-g8s-campaign-ssot (factory block),
  g8s-dogfood-mandate (miss+correction), g8s-dispatch-mechanics-v012
  (tilde/stagger/TTL/watch-since gotchas),
  g8s-concurrent-session-d6 (dual-supervisor deconfliction).
- Skills: stop-slop (~/.agents/skills/stop-slop/, sibling session's
  port, MIT×2) — the anti-slop writing contract.
- Dogfood state dir: G8S_STATE_DIR=~/.local/state/g8s-sess-g8s4efe
  (queue + receipts + signals/ live; bin/g8s rebuilt at factory main).
