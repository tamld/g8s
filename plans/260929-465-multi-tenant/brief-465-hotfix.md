# Task: #465 HOT FIX (P0) — de-fang the two host-wide destructive sweeps

Repo: g8s @ main. Tracking: tamld/g8s#465 (this is the P0 hot-fix slice;
instance identity / lease resilience / tenancy ADR stay P1 follow-ups).

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- internal/cleanup/cleanup.go
- internal/cleanup/ghost_sweep_test.go (new file)
- internal/cleanup/orphan_dir_test.go (new file)

Do NOT run git commit. Do NOT modify any other file. Other workers own
internal/worker/, internal/harness/probe/, docs/ in parallel — touching
anything outside the list corrupts their delivery. NOTE: another file in
this package (worktrees.go) is NOT in scope; do not touch it.

## Context (red-team verified at main)

Two cleanup primitives reach across g8s instances on one host:

1. `sweepGhostProcesses` (cleanup.go:936 SIGTERM, 951 SIGKILL; candidate
   scan at ~285-288): kill candidates are ANY host process named
   `agy*`/`claude*`, triggered via repo-local heartbeat files whose PID may
   have been RECYCLED onto another session's live worker (~350-360), or
   `--force-foreign`. Live incident: this killed 3 running workers of a
   concurrent session (tasks 1d800abc/098d34c8/9d16ecc9, exit -1).
2. `sweepOrphanWorktreeDirs` (cleanup.go:1029-1094; TMPDIR root at 1048):
   a default target of `g8s cleanup` that RemoveAll's EVERY dir under the
   host-global `$TMPDIR/g8s-worktrees` not registered in the INVOKING
   session's repo — no age check, no dirty check. Live incident: another
   session's preserved worktree (logged `preserved (uncommitted
   deliverables)` by Pool.Release) vanished.

`cleanup-worktrees` (worktrees.go) is NOT the reaper — repo-scoped and
dirty-skipping; leave it alone.

## Required implementation

1. **Identity-scoped ghost candidates** (cleanup.go): a process is a kill
   candidate ONLY when identity evidence ties it to the INVOKING instance:
   (a) its environment contains `G8S_RUN_MARKER` whose task-id prefix is
   recorded in THIS instance's run registry / state dir, OR (b) its command
   line references THIS instance's run root path. Process NAME alone
   (`agy`/`claude`) is NEVER sufficient. Keep `--force-foreign` as the
   explicit operator opt-in for the legacy cross-session behavior — it must
   still work, but log a prominent warning line naming the flag used.
   BEFORE every signal (TERM or KILL), log one attribution line: PID,
   process name, the identity evidence used, the heartbeat-file provenance
   (path, mtime) that nominated it. Reuse the existing logging helpers/
   style in the package.

2. **State-dir-scoped orphan-dir sweep** (cleanup.go:1029-1094): default
   scan root becomes `pathutil.DefaultStateDir()/worktrees`; the host
   TMPDIR `g8s-worktrees` scan moves behind an explicit opt-in flag
   (e.g. `--sweep-legacy-tmpdir`, default OFF) for one-time migration.
   Every removal logs: path, why (unregistered under this root), and the
   registry evidence consulted. Dirs whose registry row proves a LIVE
   session in ANY reachable registry are skipped even under the new root.

3. No behavior change to receipts sweeping, branch cleanup, or any other
   cleanup target.

## Tests — red first (table-driven, hermetic, no real agent processes)

ghost_sweep_test.go:
1. A fake long-running child process whose command line does NOT reference
   the invoking state dir and carries no run marker is NOT killed by the
   sweep (spawn `sleep 30` via exec; assert it survives the sweep; kill it
   in test cleanup).
2. A fake child whose command line references the test state dir's run
   root IS selected as a candidate and killed; the attribution line is
   emitted (capture the logger sink).
3. `--force-foreign` still kills a name-matched foreign process AND logs
   the warning naming the flag.
4. Heartbeat-file PID recycle scenario: heartbeat file nominates a PID,
   but the process lacks identity evidence → no kill, attribution line
   says skipped-with-reason.

orphan_dir_test.go:
5. Two state dirs A and B, each with a worktree dir under its own root;
   cleanup with state-dir A removes A's unregistered dir and NEVER
   touches B's.
6. TMPDIR scan is OFF by default: a foreign tree in $TMPDIR/g8s-worktrees
   survives `g8s cleanup` defaults; with `--sweep-legacy-tmpdir` it is
   removed and logged.
7. A dir registered to a LIVE session (fresh heartbeat in any reachable
   registry) is skipped even under the active root.

## Constraints

- stdlib + existing deps; match package style; comments only for
  non-obvious constraints.
- All tests hermetic: temp dirs, short sleeps (<1s), no network, no real
  agy/claude processes.
- Run `go test ./internal/cleanup/` and `go build ./...` locally — green.
