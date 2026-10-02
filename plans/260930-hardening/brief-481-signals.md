# Task: #481 slice 1 (controlplane layer) — terminal-transition signal file

Repo: g8s @ main. Tracking: tamld/g8s#481 (event-driven supervision —
design + cross-check pinned in the issue comments; the acceptance items
are authoritative).

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- internal/controlplane/store.go (ONLY: signal-file plumbing + append
  hook at terminal transition commit points)
- internal/controlplane/signals_file_test.go (new file)

Do NOT run git commit. Do NOT modify any other file. Another worker owns
cmd/g8s/ (the watch consumer) in parallel.

## Context (design ratified — see #481 issue body + comments)

The control plane already records every state transition in
state_event_log, and lease expiry already transitions tasks honestly
(verified by the overnight incident where 3 tasks went FAILED with zero
silent loss). What's missing: a **push edge** so a supervisor session can
WAKE on failure instead of polling.

Design decisions already made (do not relitigate):
- Signal file: `<db-dir>/signals/tasks.jsonl` — append-only JSONL, one
  line per TERMINAL transition: FAILED, WORKER_COMPLETED, SUCCEEDED,
  CANCELLED, NEEDS_INFO (NEEDS_INFO included: stage-1 honesty — the
  operator wants to know when a task parks, too).
- The line is written AFTER the transition's DB transaction COMMITS
  (a file append cannot join a SQL tx). The crash window (committed
  transition without signal line) is acceptable BY DESIGN: the DB is
  the truth and the supervisor's fallback deadline-check re-derives
  from state_event_log. Document this in a comment.
- Single writer per state dir = the Store itself (SQLite tx
  serialization). O_APPEND writes of complete single lines (build the
  full line in memory, one Write call).

## Required implementation

1. In internal/controlplane/store.go: the Store learns its signals path
   — derive from the db path it already holds:
   `filepath.Join(filepath.Dir(dbPath), "signals", "tasks.jsonl")`.
   Lazily MkdirAll the signals dir on first append; keep an
   `*os.File` handle open in append mode for the Store's lifetime
   (close in Store.Close()).
2. Add an unexported `appendTaskSignal(taskID, from, to string, ts
   time.Time)` — builds
   `{"ts":"<RFC3339>","task_id":"...","from":"...","to":"..."}` and
   appends one line. Errors: log to stderr with a `[warn] controlplane:`
   prefix, NEVER fail the transition (the signal is a hint; the DB is
   the truth).
3. Hook: at every point where a task lands in one of the five terminal
   states above, call appendTaskSignal with the from/to states. Trace
   the transition commit sites (grep the lifecycle/store update
   statements for the five target states) and place the hook where the
   transaction has committed. Non-terminal transitions get no signal.
4. The consumed signal lines must be one-complete-JSON-per-line even
   under concurrent writes (single Write of the whole line per event;
   the mutex-serialized tx path already serializes these calls).

## Tests — signals_file_test.go (red first)

- Terminal transition → file created + one valid JSON line with correct
  task_id/from/to (use the existing store test fixture style; drive a
  real submit→claim→finish if cheap, else call the transition method
  directly).
- Non-terminal transition → NO new line.
- Each of the five states produces a line when reached.
- Store.Close() then reopen → appends still work (handle reopened).
- Crash-window honesty: assert the DB transition committed even if the
  signal write target is made unwritable (chmod the dir 0500 before the
  transition on a POSIX host, skip on windows) — transition succeeds,
  warn goes to stderr.

## Constraints

- stdlib + existing deps; no schema changes; match style.
- `go test ./internal/controlplane/` and `go build ./...` locally green
  (the full suite — other tests must not break from the new appends;
  use the store's actual db path so test stores write signals into
  their own temp dirs).
