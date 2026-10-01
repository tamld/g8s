# Operational Hard Lessons — v4.1 addendum (2026-09-29/30 campaign)

Field-proven mechanics from the debt-zero + multi-tenant campaign (13 merged
PRs, v0.13.0 release). Each lesson cites the incident that bought it. Fold
these into dispatch planning; they are contracts, not advice.

## 1. Multi-project tenancy (one host, several g8s consumers)

- Every concurrent session sets its **own `G8S_STATE_DIR`** (queue/DB
  isolation) — proven live: `~/.local/state/g8s-sess-<session>`.
- **Only one session runs `g8s cleanup` at a time** until #465 P1 lands:
  `sweepGhostProcesses` (SIGKILLs host-wide `agy*`/`claude*` via heartbeat
  PIDs) and orphan-dir sweeps were host-wide before the #468 hot fix.
- Incident cost: 6 delivery losses across 4 dispatch rounds when two
  supervisors shared one host.

## 2. Supervision is event-driven; polling is the fallback (operator directive 2026-10-02)

A supervisor is server/client: it cannot pull and push continuously —
deliberate repeated polling (heartbeat-delta probes every 12s, CI checks
every 30s) burns quota on useless, intentionally repeated work. The wakeup
model:

```
PRIMARY   — terminal-event signals: drain-shell completion notification,
            `g8s watch --task <id> --milestone worker-complete` per task
            (one sleeping long-poll per task, zero quota), the control
            plane's own lease-expiry → FAILED transition (signal-file
            design: issue #481)
FALLBACK  — ONE one-shot deadline check per wave at timeout+buffer; it
            exists only to catch a MISSED primary signal. A fallback that
            fires while signals flowed normally is a bug in the wave plan.
DIAGNOSTIC— worker liveness tiers are run ON DEMAND when a signal or
            fallback says something is wrong — never as a routine:
            DB state RUNNING (weakest; stale-lease illusion)
            → ps + child.pid (exists ≠ progressing)
            → heartbeat delta (lease_expires_at advancing = renewing)
            → disk harvest (the only truth for "delivered")
```

- CI poll scripts must exit on **`pending == 0`**, never `pass >= 5` (8
  pending checks hid behind 6 passes once).

## 3. Verdict ≠ delivery

- Worker verdicts (`ok=true`) lied repeatedly while the disk was empty.
  After every drain: **harvest worktrees immediately** (they are the
  delivery surface), then never-trust-verify the diff yourself.
- Worktrees live under `$G8S_STATE_DIR/worktrees` since #468 — harvest from
  there, not TMPDIR.

## 4. Provider walls

- Quota exhaustion surfaces as a task failure with
  "Individual quota reached … Resets in <duration>" — retryable after
  reset; schedule a one-shot automation for the retry window instead of
  burning attempts.
- Probe tasks need sane budgets: a 5s-timeout probe dies `exit -1` from the
  worker's own timeout law and reads as a kill, not a quota error.

## 5. Version-coupled tests

- Any test mocking "latest release" must **compute** the mock from
  `Version` (+ patch bump) — a hardcoded `v0.13.0` mock broke the moment
  the release bumped `Version` to 0.13.0 (`TestRunVersionCaptureStdout`).

## 6. Stale binary trap (recurring)

- Rebuild `bin/g8s` after EVERY merge before dispatching — a pre-merge
  binary ran `eval list` against a suite it could not see, and the stale
  probe filter read as "no probes matched".

## 7. Release mechanics

- `release.sh` bumps version.go + CHANGELOG but NOT manifest.json or
  packaging templates — the version-sync guard blocks the push until they
  are synced by hand (v0.13.0 proof, twice). Sync them before invoking, or
  land the release.sh sync slice.
- Pre-fill the `## [X.Y.Z]` CHANGELOG section before running release.sh —
  its insert is skipped when the header exists.
- Tag push triggers GoReleaser (5 platforms); the tagged commit may differ
  in SHA from main's head after a rebase — same tree, cosmetic only.
- `release.sh`'s self-reference limit: `latest_release.commit` cannot be
  synced by the script that creates the commit — record it in a follow-up
  chore commit.

## 8. Bash 3.2 for shipped tools

- Tools consumed by other machines (link gate, release scripts) run on
  stock macOS bash 3.2: `${arr[@]}` under `set -u` is fatal even for
  declared-empty arrays — use `${arr[@]+"${arr[@]}"}` (#466).

## 9. Dogfood automation pattern

- One-shot automation for deferred retry windows: it dispatches, drains,
  verifies, and opens PRs — **merging stays with the interactive
  supervisor** (the boundary kept the automation honest and the review
  human).
- Brief = complete contract (pinned semantics + red tests + receipt-scoped
  file list with `$G8S_REPO_ROOT`-style placeholders, never absolute home
  paths); submit prompt stays tiny and neutral.

## 10. Push topology

- origin is GitHub-only since 2026-09-30 (the LAN `ct122` pushurl was
  retired after a sleeping Windows host failed every push with a
  misleading "correct access rights"). Re-add manually for sync sessions.
- Concurrent writers on main: `git pull --rebase` + retry loop; a rebase
  rewrites the release commit SHA after tagging — same tree, note it.
