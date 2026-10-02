---
handoff-version: 1
generated: 2026-10-02T13:45:00+07:00
generator: handoff@1.0.0
focus: "#481 delivered + live-probed; next = ADR-0029 ratification + deferred decisions"
session-type: T2 (execution)
workspace: $G8S_REPO_ROOT
branch: main
head: 4c4a68a
---

# HANDOFF: #481 event-driven loop shipped — next: ratification queue

**Session type**: T2 (execution)

## Mission and current status

Focus: deliver the event-driven supervision surface (#481) end to end
and stage the deferred decisions. DONE this session (all merged, main @
4c4a68a, CI green):

- **#481 CLOSED** (all acceptance items):
  - #496 → e0bbb22: controlplane signal-file writer (W layer).
    Post-commit append to `<db-dir>/signals/tasks.jsonl`, crash window
    accepted by design, claim-path reconcile covered, 6 tests.
  - #495 → 4724568: `g8s watch --failed` consumer (C layer). Startup
    match + 500ms tail, `--since`, `--timeout` exit 3, SIGINT 130,
    malformed-line skip, 8 tests, Windows build green.
  - #497 → bbd955b: directives.md **D-07** (event-driven supervision,
    falsification clock cites the wave-B overnight death).
  - **Live probe PASSED** (evidence on #495): echo task d490a77b →
    transition → signal line → `watch --failed` exit 0 in ~1s; both
    consumer paths (immediate match + timeout exit 3) exercised.
- **ADR-0029 PROPOSED** (#494 → 40d2c22, docs only): #485 stage-4
  budgeted auto-retry. Three decision points await the operator (D1
  deny-by-default classes, D2 budget 2/task + 10/dir-hour + backoff +
  flag OFF, D3 tick retries receipt-free only / workspace_write via
  supervisor + fresh receipt). Pointer posted on #485.
- **#493** → 30facce: dead `resource.syso` Makefile target + stale
  versioninfo.json@0.5.0 deleted (the handoff's verify-or-delete item;
  verdict: delete — icon untracked, tool uninstalled, goreleaser never
  references it).
- **#499** → 4c4a68a: skill hard-lessons 11-12 (dual-supervisor
  harvest deconfliction; keyword auto-close trap); installed copy
  synced.

## Open risks / incidents this session (read before working)

1. **DISK CRITICAL**: Data volume hit 100% (go build failed mid-session
   with "no space left"; `go clean -cache` freed ~4Gi; it shrank back to
   ~1.2Gi by session end). The snapshot class needs OPERATOR action
   (tmutil thinlocalsnapshots / purge OS-update snapshots). Do not
   trust local builds until df shows headroom.
2. **Dual-supervisor D6 live**: the outgoing session stayed alive and
   harvested the same #481 output (foreign checkout+commit 64s after
   ours; later merged a byte-identical duplicate C-slice as #498 —
   tree-neutral, verified). Deconfliction protocol in skill
   hard-lesson 11 + memory g8s-concurrent-session-d6. If the other
   session is STILL alive at handoff read time, coordinate before any
   harvest: check `git reflog --date=iso` + `gh pr list` first.
3. **Keyword auto-close trap (2nd occurrence)**: a docs commit message
   "fix #481 briefs" closed #481 before the code existed. Never place
   keyword+#N in non-fix commit messages; recovery = reopen → evidence
   comment → close --reason completed (skill hard-lesson 12).
4. Watch CLI: `--interval 45` errors (Go duration needs `45s`) and a
   `| tail` pipe swallows the usage error's exit code — first watcher
   arm silently no-oped. Pass durations WITH units; check envelope
   output before trusting a watcher.

## Current state

- Branch main, HEAD 4c4a68a; working tree clean; 0 open PRs.
- Open issues (4): #485 (stage 4 — ADR-0029 awaits ratification),
  #480 (mutation cadence, quarterly; next run 2027-01), #465 (ADR-0028
  tenancy — operator), #442 (external waves).
- Binary: bin/g8s rebuilt at post-#481 main (tree-identical code to
  4c4a68a; only docs commits differ — rebuild if unsure).
- Dogfood: state dir `G8S_STATE_DIR=~/.local/state/g8s-sess-g8s4efe`
  (queue + receipts + the new signals/ dir live there); bin/g8s ready;
  export the var and the line is connected.
- CI: main green through bbd955b and at d67bd41; coverage baseline
  81.00%+.

## Exact next actions

**First safe step**: `gh issue list --state open` (expect #485 #480
#465 #442), read this handoff + memory `reference-g8s-campaign-ssot`
(latest block). Then:

1. **Operator decisions (not blocking, but gating)**:
   - Ratify ADR-0029 D1-D3 → then 2 implementation slices (DEBT-34:
     controlplane budget/classification → cmd tick job + `g8s
     resubmit` + settings keys). Brief pattern:
     plans/260930-hardening/brief-481-*.md.
   - ADR-0028 tenancy (#465) — when multi-project becomes routine.
   - #442 waves — external pacing.
2. **If dispatching anything**: event-driven doctrine is now SHIPPED —
   primary wake = `g8s watch --failed` (per state dir) + `g8s watch
   --task <id> --milestone worker-complete` per task; polling is the
   fallback (D-07). Durations with units.
3. **Hygiene when disk allows**: the 107 preserved worktrees in the
   dogfood state dir (143M) are safe to reap via
   `g8s cleanup-worktrees` once no harvest depends on them; Evidence
   Lake stays.
4. **Session end**: cross-check PR-merge ⇒ issue closures; verify no
   unintended auto-closes fired after every push (D6 trap); write next
   handoff.

## Source pointers

- Handoff chain: plans/handoffs/execution-handoff-20261002-1200.md
  (predecessor) → this file.
- Memory: reference-g8s-campaign-ssot (latest block = #481 delivered),
  g8s-concurrent-session-d6 (D6 live incident), feedback-pr-merge-
  closes-issues (keyword trap recovery).
- Briefs: plans/260930-hardening/brief-481-{signals,watch}.md
  (committed at 31f053d with in-flight fixes).
- ADR-0029: docs/decisions/0029-budgeted-auto-retry.md (Proposed).
- Live probe evidence: tamld/g8s#495 (issue comment, 2026-10-02).
