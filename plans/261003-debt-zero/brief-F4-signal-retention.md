# Task: F4 — signal-file retention: size-capped rotation + consumer tolerance (issue #510, BUG(REDTEST) seed redtest_signal_test.go:467)

Repo: g8s @ main. The signal file grows ~103 bytes per terminal
transition with NO cap — a slow disk-fill vector on long-lived state
dirs. Design (supervisor decision, implement as specified):

## Design (do not relitigate; deviations = report)
1. **Writer rotation** (internal/controlplane, the appendTaskSignal
   path): before appending, if the current signals file exceeds
   `maxSignalFileBytes = 1 << 20` (1 MiB, a package const — not a
   setting), rotate: rename `tasks.jsonl` → `tasks.jsonl.1`
   (overwriting any previous .1 — exactly ONE generation kept), then
   continue appending to a fresh `tasks.jsonl`. Rotation happens
   under the same mutex as appends (single writer per state dir).
   Rotation failure (rename error) logs the standard `[warn]
   controlplane:` prefix and continues appending WITHOUT rotation
   (retention is best-effort; the wake contract must never break).
2. **Consumer tolerance** (cmd/g8s/watch.go, the --failed tail): the
   tail loop tracks a byte offset; if the file is now SMALLER than
   the offset (rotated/replaced), reset the offset to 0 and re-scan
   from the start (the rotated-away .1 is history — the consumer
   only owes the CURRENT file's lines). Startup read unchanged.
3. **Un-skip the red seed**: redtest_signal_test.go:467 becomes the
   rotation assertion — drive > maxSignalFileBytes worth of
   transitions (generate transitions programmatically; shrink the
   const for the test via an unexported var override if needed —
   prefer a package-level `var` the test can swap, restored by
   defer), verify: rotation happened (`.1` exists, current file
   fresh), no line lost WITHIN the current file, total content =
   current + .1.
4. **Consumer test**: mid-tail rotation — write enough lines to
   trigger rotation WHILE a watch is tailing (in-process test of the
   tail function), verify the watch still reports a matching line
   written after the rotation.

## Delivery protocol
Scratch worktree. Write ONLY to:
- internal/controlplane/store.go (rotation + const/var)
- internal/controlplane/redtest_signal_test.go (un-skip + rotation tests)
- cmd/g8s/watch.go (offset-reset tolerance)
- cmd/g8s/watch_test.go or watch_failed_test.go (rotation-tolerance test)
Do NOT run git commit. Do NOT touch routing/, lane/, settings/.

## Constraints
- stdlib; mutex discipline unchanged; no settings keys (const +
  test override var).
- `go test ./internal/controlplane/ ./cmd/g8s/` green; build green;
  GOOS=windows compiles.
- Report: rotation mechanics, the override-var name, consumer
  behavior on rotation, and any edge you found (e.g., two rotations
  in quick succession).
