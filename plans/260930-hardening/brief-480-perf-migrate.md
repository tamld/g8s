# Task: #480 items 3-4 — queue perf smoke benchmark + migration E2E (old DB → new binary)

Repo: g8s @ main. Tracking: tamld/g8s#480.

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- internal/controlplane/perf_smoke_test.go (new file; guarded by
  `testing.Short()` skip semantics — runs only when not -short)
- cmd/g8s/migrate_e2e_test.go (new file)
- docs/user-guide/performance.md (new file — the recorded baseline)

Do NOT run git commit. Do NOT modify any other file. Another worker owns
.github/workflows/ and internal/config|receipt|worker|dispatch in
parallel.

## Context (verified at main)

- Queue ops (submit→claim→finish) have no measured latency baseline;
  store.go:1762 carries `TODO(OWNER=tamld): Check hourly rate limit` —
  the anti-runaway throttle cannot be designed without numbers.
- `cmd/g8s/migrate_test.go` exists but the old-schema → new-binary upgrade
  path (the thing protecting the oldest users) is unverified end-to-end.

## Required implementation

1. **Perf smoke** (internal/controlplane/perf_smoke_test.go):
   - Benchmark-style test: in-memory temp DB, N=10 goroutine workers
     looping claim→heartbeat→finish against a pre-seeded queue of ~200
     tasks; measure per-op latency for submit, claim, finish
     (histogram buckets or simple p50/p95 over ≥200 samples per op).
   - Guard: skip when `testing.Short()`; assert NO absolute performance
     threshold as a hard gate (this is a smoke/baseline recorder, not a
     regression gate yet) — but DO fail on pathology: any single op
     p95 > 10s indicates a lock catastrophe and should fail with the
     numbers in the message.
   - Output: machine-readable line `PERF submit=p50=…ms p95=…ms …` on
     stdout for docs capture.
2. **Record the baseline** (docs/user-guide/performance.md): run the
   smoke on this machine, paste the PERF lines, note the environment
   (CPU count, Go version, date, "not a SLA — baseline for the throttle
   design, see store.go:1762 TODO").
3. **Migration E2E** (cmd/g8s/migrate_e2e_test.go):
   - Fixture: create a DB with the OLDEST schema the migrate path
     supports — trace `cmd/g8s/migrate.go` first: if it ships fixture
     SQL or a schema-version constant chain, use it; if the migrate
     command only migrates between known versions, construct the oldest
     supported schema it accepts (copy the CREATE TABLE statements from
     the corresponding schema-version constant/history).
   - E2E: old DB → run the migrate path the CLI uses → open with
     controlplane.NewControlPlane → SubmitTask + ClaimTask + FinishAttempt
     round-trip succeeds → assert PRAGMA user_version == current.
   - If the migrate path cannot accept an old fixture (it only no-ops on
     current), report that finding instead of forcing it — the finding
     itself (what the oldest supported upgrade is) is deliverable
     information for #480 item 4.

## Tests

- perf_smoke_test.go passes locally (not -short) with the PERF lines
  captured; passes under -short as skipped.
- migrate_e2e_test.go green, or the documented finding if blocked.

## Constraints

- stdlib + existing deps (modernc sqlite already in). No production code
  changes. Match test style; hermetic temp dirs only.
