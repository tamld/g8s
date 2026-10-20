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

- Worker result ok=true does not assert file existence; verify diff on disk.
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
  supervisor** by default (`autonomy_level=0`), or executes through
  fail-closed gated auto-merge (`tools/merger.sh`) only when explicitly
  enabled under `autonomy_level=1` (see `docs/user-guide/verifier-and-autonomy.md`).
- Brief = complete contract (pinned semantics + red tests + receipt-scoped
  file list with `$G8S_REPO_ROOT`-style placeholders, never absolute home
  paths); submit prompt stays tiny and neutral.

## 10. Push topology

- origin is GitHub-only since 2026-09-30 (the LAN `ct122` pushurl was
  retired after a sleeping Windows host failed every push with a
  misleading "correct access rights"). Re-add manually for sync sessions.
- Concurrent writers on main: `git pull --rebase` + retry loop; a rebase
  rewrites the release commit SHA after tagging — same tree, note it.

## 11. Two supervisors, one checkout (#481 harvest, 2026-10-02)

- A handoff session that "closed" may still be alive and executing its
  queue: the #481 harvest saw a foreign checkout+commit+amend land 64s
  after the successor's own branch creation, plus an `index.lock`
  collision mid-command. Evidence is in `git reflog --date=iso`.
- Deconfliction that worked: verify foreign content against your own
  verification (worktree doubles were byte-identical), then LANE-SPLIT
  (yield the slice the other actor already committed, stake yours by
  pushing first), never `rm .git/index.lock` while the other actor may
  hold it — poll up to 60s. A foreign pushed branch WITHOUT a PR: open
  the PR from it yourself (duplicate-PR risk is benign; a blocked
  pipeline is not).
- Workers double-write deliverables (worktree AND main checkout,
  byte-identical here — #448 non-determinism still live in the agy
  transport). Treat the result-envelope worktree dir as canonical; diff
  before trusting either copy.

## 12. Keyword auto-close fires on ANY main push

- Any commit reaching the default branch whose message contains
  keyword+#N closes the issue — including docs commits ABOUT an issue
  ("fix #481 briefs" in a plans commit closed #481 before the
  implementation existed; the #480 "Part of, not Closes" lesson has an
  inverse now).
- Rule: non-fix commits reference issues without an adjacent keyword
  ("issue #481 briefs"). After every push to main, expect exactly the
  closes you intended. Recovery when one fires early: reopen → evidence
  comment → `gh issue close --reason completed`.

## 13. Cross-repo `workspace_write` isolation & preserved-worktree delivery (2026-10-14 mercenary campaign)

- When F0 dispatches a `workspace_write` task from `g8s` into a sibling
  repository (`aegis`, `tuneflow`, `homelab-proxmox`), pass BOTH
  `--scope-root <sibling-repo>` and `--add-dir <sibling-repo>` along with
  `--receipt-id <id>` (where receipt `--path` globs are relative to the
  sibling repository root) and `AGY_MCP_ALLOW_WORKSPACE_WRITE=1`.
- `RepoWorktreeIsolator` (`AcquireRepoWorktree`) branches the isolated git
  worktree (`$G8S_STATE_DIR/worktrees/wt-<hash>`) from the sibling
  repository (`add_dirs[0]`), never from `g8s`.
- `g8s deliver <task-id>` resolves `add_dirs[0]` automatically. If a worker
  times out after completing its file edits on disk (`#427` deliverable
  survival), inspect the diff in `$G8S_STATE_DIR/worktrees/wt-<hash>` and
  run `g8s deliver <task-id> --worktree <wt-dir>` — this still enforces the
  full Zero-Trust Receipt Gate (`VerifyScope` + `ConsumeWriteReceipt`).
- After delivering to a sibling repo, pass `g8s deliver <task-id> --prune-worktree`
  (or manually run `git -C <repo> worktree remove --force <wt-dir>` and
  `git -C <repo> branch -D wt/<task-id>`) before running the sibling repo's
  pre-push hooks.

## 14. Atomic F1 slice quota (`<= 8 files / worker`) & JS/TS worktree `node_modules` trap

- Empirical benchmark from parallel `aegis` + `tuneflow` waves:
  - Bundling `18+ files` (3 issues) into a single `workspace_write` worker
    causes `130–151+` tool steps and hits the `360s` timeout ceiling.
  - Right-sizing each F1 worker to **`<= 8–9 files`** (`1 issue-slice = 1
    worker = 1 receipt`, `<= 40` tool steps) brings completion down to
    **`19.5s–149s`** (`26K–187K` tokens) with `100% succeeded` status across
    3 parallel cross-repo rounds.
- Git worktrees (`wt-<hash>`) do NOT contain gitignored `node_modules`.
  In Node.js/TypeScript sibling repos (`aegis`, `tuneflow`), instruct F1
  workers either to perform file edits only (leaving test runner execution
  to F0 post-delivery) or to invoke `<sibling-repo>/node_modules/.bin/vitest`
  directly — never let F1 workers waste 30+ steps debugging missing
  `node_modules` or creating symlinks inside the worktree.

## 15. Sibling pre-push & layer-ownership gate matrix

- `tamld/aegis`: enforces `rule-anti-cowboy` on `pre-push` (`feat/` and
  `fix/` commits cannot push directly to `main`; push a feature branch and
  merge via `gh pr create` + `gh pr merge --squash --delete-branch`) and
  enforces L1 Domain Lock (no top-level `tools/*.sh`; `g8s offer check`
  uses embedded gate scripts via `bash -s`).
- `tamld/g8s`: enforces `DEBT-34` (`tools/ci_layer_check.sh`) across
  `origin/main...HEAD`. Never push commits touching two layers (`cmd/g8s/`
  and `internal/worker/`) in the same `git push`; push each layer-disjoint
  commit sequentially so `origin/main...HEAD` stays single-layer per push.

## 16. Cross-platform search standard: `ripgrep` (`rg`) vs `grep` / `find` (`offer/SEARCH_GUIDE.md`)

- **Why `rg` over `grep` / `find`**:
  1. **Zero BSD/GNU/Windows regex divergence**: macOS ships BSD `grep` (no `-P` PCRE2, flag drift vs Linux GNU `grep`), while `rg` provides identical Rust `regex` + PCRE2 (`-P`) and `-g` glob semantics across `darwin`, `linux`, and `windows`.
  2. **Automatic `.gitignore` & binary hygiene**: Raw `grep -rn` or `find . | xargs grep` blindly scans `.git/`, `node_modules/`, `vendor/`, and `$G8S_STATE_DIR/worktrees/`, bloating worker tokens and risking `360s` timeouts. `rg` skips ignored/binary/hidden files automatically (use `--hidden -g '!.git'` when inspecting `.g8s/`, `.agents/`, or `.github/`).
  3. **Dual-Engine CI Gate Pattern**: Portable scripts (`tools/ci_*.sh`) MUST use `if command -v rg >/dev/null 2>&1; then rg ...; else grep ...; fi` so workstations get `5x–20x` SIMD speedups while minimal containers degrade gracefully to POSIX `grep`/`find`.
  4. **Windows path normalization**: Pass `rg --path-separator /` whenever matching output paths against `WriteReceipt` globs or OpenSpec manifests.

## 17. F0 Verified Delegation & Anti-Hallucination Laws (2026-10-18 self-audit C1–C10, ADR-0033)

Following the 2026-10-18 self-audit of ten supervisor-originated defects (`C1–C10`), verified delegation replaces synchronous HITL with machine-checkable evidence evaluated at commitment points. These 8 laws are strict contracts for F0:

1. **Never state CI status without a verified workflow run ID or `ev-` evidence ID (`C10`)**: Uncited claims ("CI is GREEN", "tests pass") are rejected by the G6 claim citation linter. Always cite a passing ledger record (`ev-xxxxxxxx`) bound to the current HEAD SHA or a resolved forge run ID (`actions/runs/<id>`).
2. **Never merge with checks pending or failing; a known flake requires a recorded `f0 waive` entry linked to an OPEN same-repo issue and debits the error budget (`C1`, `C2`)**: Merging with pending checks caused a race with cargo audit in `tamld/hash-checker#80` (`C1`, fixed in `tamld/hash-checker#81`). Merging with failing checks bypassed Trivy security scan in `tamld/defense-in-depth#154` (`C2`, fixed in `tamld/defense-in-depth#155`). Zero checks reported is treated as failure; pending checks block immediately; failing checks require an explicit waiver debiting budget.
3. **Never satisfy a guard by rewriting its matched tokens (`sed TODO->To-Do`) or inventing unread config keys (`C3`, `C4`)**: Superficial evasion (`sed TODO->To-Do` to bypass hollowArtifact in `C3`) and inventing unregistered schema keys (`schema_version`/`profile` in `.g8s/trust-boundaries.yml` in `C4`, fixed in `tamld/g8s#619`) corrupts governance. G4 anti-appeasement halts superficial renames; schema modifications require actual spec/schema updates.
4. **Verify every F1 claim against disk/CLI reality before accepting or repeating it (`C5`, `C8`)**: An F1 worker reported '8 commands' when 10 existed (`C5`); SCORECARD M3 entries were described with wrong content and miscounted in `#611`/`#614` (`C8`). Supervisor must independently inspect disk, receipts, and CLI output (`f0 status`) before incorporating worker claims into reports or PR descriptions.
5. **Compare delivered files (`g8s-deliver` applied/deleted + blob hashes) with the final `base...head` diff before squash/merge (`C6`)**: In `tamld/defense-in-depth#154` squash, 12 fixture specs were silently dropped while closing `#144` (`C6`). G3 provenance check diffs the commit tree against non-dry-run delivery records and blob hashes to guarantee zero dropped or unattributed deliverables.
6. **Use `Closes #N` only when every acceptance criterion has verified evidence; otherwise use `Refs #N` and leave the issue open (`C7`, `#617`)**: PR `#618` prematurely closed issue `#617` while acceptance criterion 1 remained unmet (`C7`). G7 issue-closure gate blocks closing an issue unless all acceptance criteria checkboxes are corroborated by cited passing evidence.
7. **Never `kill` a running `g8s worker --concurrency` pool; let it drain naturally or use `--once=true` (`#617`)**: Abruptly killing an active worker pool corrupts SQLite WAL state, orphans worktrees, and leaves active task leases dangling. Allow the worker loop to drain naturally, invoke single runs via `--once=true`, or run controlled cleanup via `g8s cleanup` (`#617`, `#620`).
8. **Treat any instant verifier pass (`<50ms` or `items_checked == 0`) as a vacuous pass (`VACUOUS`) until item count > 0 is proven (`C2` regression)**: Verifiers returning instant success often didn't run tests at all (e.g. pattern didn't match files, or zero checks configured as seen in `C2`). G5 vacuous-pass guard parses test counts and marks any zero-item pass as `VACUOUS`, failing G1 commitment gates.
