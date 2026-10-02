# Task: #481 slice 2 (CLI layer) — `g8s watch --failed` long-poll consumer

Repo: g8s @ main. Tracking: tamld/g8s#481. DEPENDS ON: slice 1 (the
signal-file writer in internal/controlplane) — if that slice has not
merged yet, develop against the format contract below and note it; the
file format is frozen: one JSON object per line,
`{"ts":"<RFC3339>","task_id":"...","from":"...","to":"..."}`.

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- cmd/g8s/watch.go (ONLY: add the `--failed` mode to the existing watch
  command — no restructuring of the existing task-watch path)
- cmd/g8s/watch_failed_test.go (new file)

Do NOT run git commit. Do NOT modify any other file. Another worker owns
internal/controlplane/ in parallel.

## Context (design ratified — #481)

The supervisor wakes on worker terminal events instead of polling. The
control plane appends terminal transitions to
`<db-dir>/signals/tasks.jsonl`. This slice: `g8s watch --failed` — a
blocking long-poll that SLEEPS (one process, zero task-submission quota)
and exits when a matching signal appears. This replaces every poll loop.

## Required implementation

1. **`g8s watch --failed [--since <RFC3339|duration>] [--timeout <dur>]`**
   (extend the existing watch command's flag set; the existing task-watch
   mode stays byte-identical when --failed is absent):
   - Resolve the signals file: `filepath.Join(filepath.Dir(dbPath),
     "signals", "tasks.jsonl")` (same derivation the writer uses — db
     path via the existing databasePath() helper).
   - Startup: read the whole file if it exists; print every line whose
     `to` is a terminal state AND ts > --since (when given; --since
     accepts RFC3339 or a Go duration meaning "now minus duration") as
     a JSON envelope (kind: "task_signal", data: the line's fields).
     If a matching line exists at startup → print and EXIT 0
     immediately (a failure that already happened is still news).
   - If no match: tail the file (check for appended bytes every 500ms;
     pure time.Sleep loop is acceptable here — one sleeping process,
     not a task-submission poll) and print+exit 0 on the first matching
     new line.
   - `--timeout <dur>` (default 0 = block forever): exit 3 with a
     timeout envelope when elapsed.
   - File-not-exists = zero signals yet: keep waiting (do NOT error).
   - Malformed lines: skipped with a stderr warning, never fatal.
2. On ctx/signal interrupt (SIGINT/SIGTERM): exit 4 with a clean
   envelope (kind: "task_signal_timeout" only for timeout; interrupt is
   a plain exit 130 convention — document in --help text).
3. --help text documents: --failed, --since, --timeout, the file path,
   and the exit codes (0 = signal matched, 3 = timeout, 130 = interrupt).

## Tests — watch_failed_test.go (red first, hermetic)

Use a temp state dir + write fixture signals/tasks.jsonl lines directly
(the reader must not care who wrote them) and invoke the watch logic in
-process (if the implementation is a testable func taking a path +
deadline, call it directly; avoid long real sleeps — use short test
timeouts):

1. File with a FAILED line (ts now) → exits immediately, envelope
   carries task_id/to.
2. File with only non-terminal transitions → keeps waiting until the
   timeout budget (use a 100ms test budget) → timeout exit code 3.
3. File appears MID-wait (write it from a goroutine after 100ms) →
   wakes and exits with the event (proves the tail, not just startup
   read).
4. --since filters: an old FAILED line before --since → ignored.
5. Malformed lines interleaved with a valid FAILED line → skipped
   without error, valid line still reported.
6. Existing task-watch mode (no --failed) unaffected: run the existing
   watch tests — they must stay green (regression guard).

## Constraints

- stdlib only; match the existing watch.go style; comments only for
  non-obvious constraints. No changes to internal/ (slice 1's writer
  defines the format; you consume the documented contract).
- `go test ./cmd/g8s/` and `go build ./...` locally green;
  `GOOS=windows go build ./cmd/g8s/` compiles.
