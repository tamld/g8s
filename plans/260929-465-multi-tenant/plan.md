---
plan: 260929-465-multi-tenant
issue: tamld/g8s#465
status: active
created: 2026-09-29
mode: hard (research satisfied by live incident forensics)
origin: operator directive — RCA + solution plan for multi-project g8s contention
---

**Session type**: T1 (strategy)

# Plan: Multi-project g8s contention — ownership, sweeps & worktree tenancy

## 1. The fact (operator-confirmed)

1 project / 1 supervisor / N workers = proven stable (this campaign: 12+ tasks,
parallel drains, zero cross-contamination). **2+ projects sharing g8s on one
host = design gap.** Discovered live when a second main agent (project aegis)
used g8s at the same tier while the g8s debt-zero session was draining.

## 2. RCA (root cause analysis)

### Confirmed structural defects (code-level)

| # | Defect | Evidence | Location |
|---|--------|----------|----------|
| A | **Default state dir is host-global** — two sessions share `~/.local/state/g8s/g8s.db` + `receipts.db` by default; no per-project/instance namespacing unless the operator sets `G8S_STATE_DIR` | isolation probe: setting `G8S_STATE_DIR` cleanly forks a fresh g8s.db; aegis contention appeared only when sharing | `internal/pathutil/pathutil.go:48-61` |
| B | **Host-wide destructive sweeps in cleanup** — (i) `sweepOrphanWorktreeDirs` (cleanup.go:1029-1094, default target of `g8s cleanup`) RemoveAll's every dir under the host-global `$TMPDIR/g8s-worktrees` (cleanup.go:1048) not registered in the *invoking session's repo* — no age check, no dirty check: it deletes other sessions' live/preserved trees. (ii) `cleanup-worktrees` itself is repo-scoped and dirty-skip (worktrees.go:195-202) — NOT the reaper. | wave-1 worktree logged `preserved (uncommitted deliverables)` by `Pool.Release`, gone ~30 min later while its owner session ran nothing | `internal/cleanup/cleanup.go:1029-1094`, `internal/orchestrator/worktree.go:36,56` |
| C | **Kill/reap primitives lack attribution** — when a child dies, the result records only `worker process exited with code -1` (worker.go:1537); sweeps/cleanups log nothing about WHAT they killed/reaped or WHO owned it → cross-session incidents are undiagnosable from one side | wave-3: 3 concurrent attempts died in one window, `ok=false`, exit -1, zero attribution | `internal/worker/worker.go:1537`, `internal/cleanup/cleanup.go:936,951` |

### Kill vector (red-team adjudicated)

- **(b → LEADING) `sweepGhostProcesses` (cleanup.go:936 SIGTERM / 951 SIGKILL)**:
  candidates are ANY host process named `agy*`/`claude*` (cleanup.go:285-288);
  a stale repo-local heartbeat file whose PID was recycled onto another
  session's live worker (cleanup.go:350-360), or `--force-foreign`, triggers
  the kill. The aegis session's `g8s cleanup` therefore SIGKILLs this
  session's live agy children — matches 3 simultaneous `exit -1` deaths.
- (a) **Lease machinery is fragile but causality OVERSTATED**: ANY `GetTask`
  error is treated as `lease_lost` (worker.go:771-773, before renewal is
  attempted); a renewal error is also immediately fatal (worker.go:783-784);
  `extendLease` returns a generic error indistinguishable from
  `ErrLeaseLost` (store.go:1539). However WAL + busy_timeout 30s +
  MaxOpenConns(10) make a >30s write stall unlikely — busy-storm is ONE
  candidate among several transient DB errors, not the leading one.
- Run-marker sweep (`G8S_RUN_MARKER` exact-match, worker.go:835-968) is
  attempt-unique and never kills by age — NOT the cross-session killer.

### Why it matters

Silent destruction of in-flight work by an unrelated consumer = the
operational twin of the #448 delivery-contract gap. Undiagnosable without
cross-session correlation (here: the operator supplied the missing half).

## 3. Design options considered

1. **Per-project state dir by default** (derive state dir from repo root
   hash) — rejected: changes on-disk layout for every existing user; breaks
   `g8s` sharing one queue across repos deliberately (some operators want the
   shared queue); surprising migration.
2. **File locking / mutex on the state dir** — rejected: serializes instances
   (turns multi-project into single-tenant with extra steps); does not fix
   worktree reaping.
3. **CHOSEN: instance ownership + scoped blast radius** — every g8s instance
   gets an identity; every kill/reap/sweep only ever touches resources its
   own instance (or state dir) owns; everything else gets attribution logs.
   Plus docs: concurrent sessions must use distinct `G8S_STATE_DIR`
   (interim, proven this session) until defaults change by explicit ADR.

## 4. Slices (phased, each = one worker brief + PR, DEBT-34 layer rules applied)

### Slice 1 — Attribution & forensics (P0, small — unblocks everything)
- Child kill paths record killer + reason: "killed by supervisor (reason:
  lost_lease|cancelled|timeout)" vs "exited on its own (code N)" vs
  "terminated by external signal N" (distinguish ExitError signal death
  from exit-code death) in the result envelope + stderr capture.
- Every cleanup/sweep item logs: resource, owner-session (if known),
  action, why. `sweepGhostProcesses` must log PID + the heartbeat-file
  provenance (repo, mtime) BEFORE each kill, and never kill silently.
- Layer: internal/worker + internal/cleanup (both unguarded relative to
  each other — cleanup is not layer-classified; single PR).
- Red tests: kill under lost_lease produces attribution string; external
  signal death is labeled as such; ghost kill logs provenance.

### Slice 2 — Retarget `sweepOrphanWorktreeDirs` + worktree root under state dir (P0)
- `sweepOrphanWorktreeDirs` default candidate dir becomes
  `<state_dir>/worktrees` (pathutil.DefaultStateDir()); remove the host
  TMPDIR fallback (cleanup.go:1048) and the cmd default; a tree registered
  in another repo is by definition not an orphan.
- `Pool` default root follows: `<state_dir>/worktrees` (TMPDIR fallback
  only when state dir unavailable) — orchestrator/worktree.go:36,56.
- Interim docs note in `cleanup-worktrees --help` + `g8s cleanup --help`:
  sweeps are state-dir scoped after this slice; run cleanup per project.
- Layer: internal/cleanup + internal/orchestrator (S) — single PR (no cmd/
  code change needed; flag defaults live in cleanup.go).
- Red tests: orphan-dir sweep with state-dir X must NOT remove a tree
  registered in another repo; two pools with different state dirs never
  share a root.

### Slice 3 — Instance identity (P1)
- `g8s` bootstraps an `instance_id` (UUID, persisted under the state dir);
  stamped into run markers, heartbeat rows, cleanup logs, result envelopes
  (`instance` field).
- Sweeps match markers only for the local instance_id (defense-in-depth —
  today's marker is already attempt-unique; this closes the class).
- Layer: internal/worker + internal/controlplane (W+D allowed) + result
  envelope (dispatch, unguarded) — single PR.
- Red tests: heartbeat rows carry instance_id; foreign-instance markers are
  ignored by sweep (unit-testable via injected env).

### Slice 4 — Lease resilience under transient DB errors (P1, same family as #440)
- `awaitOutcome` must NOT treat ANY `GetTask` error as `lease_lost`
  (worker.go:771-773) nor any renewal error as fatal (worker.go:783-784):
  gate BOTH paths with a `lease_expires_at` re-check (DB-authoritative)
  plus a one-heartbeat grace re-check before declaring the lease lost;
  `extendLease` should surface a distinguishable error for genuine
  `ErrLeaseLost` (store.go:1539-1542).
- `RenewHeartbeat` gains bounded busy-retry (reuse the #440
  `withBusyRetry` idiom) for transient SQLITE_BUSY.
- Layer: internal/worker + internal/controlplane (W+D allowed) — single PR.
- Red tests: renewal fails once with busy → attempt survives; GetTask
  transient error → attempt survives; lease genuinely expired (DB-side) →
  lost-lease fires; kill reason carries attribution from Slice 1.

### Slice 5 — Tenancy story & docs (P2)
- docs/user-guide: "Running multiple projects" — G8S_STATE_DIR per project
  (interim), the ownership model (post-Slice 2/3), what is shared by design
  (providers.json, binary) vs owned (state, worktrees, queue).
- ADR-0028: multi-project tenancy decision (per-project default vs shared
  queue as an explicit mode) — operator ratifies.
- Multi-project smoke: two state dirs on one host, concurrent drains, zero
  interference (extends the #427 PR-3 e2e).

## 5. Red-test proof obligations (falsification doctrine)

- Slice 1: a kill without attribution string = test failure.
- Slice 2: cross-state-dir worktree visibility = test failure.
- Slice 3: foreign-instance marker matched = test failure.
- Slice 4: single busy renewal kills attempt = test failure.
- Each slice's tests cite this incident + wave evidence (tasks 1d800abc,
  098d34c8, 9d16ecc9; wave-1 preserved-worktree vanishing).

## 6. Risks & non-goals

- **Interim rule has a hole (red-team confirmed)**: distinct `G8S_STATE_DIR`
  per session protects the QUEUE and DBs, but `sweepGhostProcesses` and
  `sweepOrphanWorktreeDirs` are HOST-WIDE today (heartbeat files are
  repo-local; the orphan-dir scan is TMPDIR-driven) — they kill/reap across
  state dirs until Slices 1-2 land. Until then: do not run `g8s cleanup`
  in any session while another session is draining; the P0 is Slice 2.
- Migration: existing operators with TMPDIR worktrees must run cleanup once
  against the old root (documented, non-breaking).
- Non-goal (v1): networked/multi-host tenancy; queue ACLs between projects.
- Non-goal: changing the default to per-project state dirs without an
  operator-ratified ADR (Slice 5).
- Interim operating rule (effective immediately, documented in #465):
  concurrent sessions on one host set distinct `G8S_STATE_DIR` (queue/DB
  isolation) AND coordinate so only one session runs `g8s cleanup` at a
  time (host-wide sweep gap, closed by Slices 1-2).

## 7. Delivery protocol

Each slice: brief in `plans/260929-465-multi-tenant/brief-*.md` → receipt →
submit → drain → never-trust-verify → PR (`Part of #465`) → CI green →
merge. Slices 1→2 sequential (cleanup overlaps), 3/4 parallelizable after 2,
5 docs-only parallel anytime. Dogfood transport; bypass requires recorded
reason.
