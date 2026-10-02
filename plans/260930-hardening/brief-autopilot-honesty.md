# Task: #485 stage 1 — autopilot honesty (end the nil-var lies)

Repo: g8s @ main. Tracking: tamld/g8s#485 stage 1 + #476 P1.1.

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- cmd/g8s/autopilot.go
- cmd/g8s/autopilot_honesty_test.go (new file)

Do NOT run git commit. Do NOT modify any other file. Other workers own
internal/ and tools/ in parallel.

## Context (verified at main; audit #476 P1.1)

`cmd/g8s/autopilot.go`: the scheduler lives in a package-level var, so a
NEW process running `autopilot status|stop|trigger` always sees nil and
reports "not running"/"scheduler not running" — which is TRUE only by
accident and MISLEADING in every real case (a `start` process may be
running in another terminal; status cannot see it). The handler for due
work is literally `// For now, just log` ("would submit task"). `start`
prints "started in background" and returns — the process exits and the
scheduler dies with it.

Stage 1 of the ratified staged path (#485): make every autopilot
subcommand HONEST. No daemon is being built here (that is stage 3, with
the tick handler + native schedulers per #481).

## Required implementation

1. **`status`**: replace the nil-var answer with the truth: report
   "autopilot runs only inside a `start` process; no cross-process
   scheduler exists in this build (staged path #485 — stage 3 will add
   OS-native scheduling)". Include whether a start-process lockfile
   exists IF you implement the optional lockfile below.
2. **`stop`**: report truthfully that it cannot stop a scheduler in
   another process (no cross-process scheduler exists in this build);
   if the optional lockfile (below) exists and names a live PID, offer
   the PID so the operator can signal it — do NOT kill it yourself.
3. **`trigger`**: report truthfully that triggers are not persisted yet
   (stage 3) and exit non-zero with the explanation.
4. **`start`**: keep the in-process scheduler (it is honest within its
   own process), but (a) print the experimental banner naming #485's
   staged path, (b) write a PID+start-time lockfile under the state dir
   (`$G8S_STATE_DIR/autopilot.lock` via pathutil) and remove it on
   clean shutdown, (c) refuse to start (clear error) when the lockfile
   names a live PID (os.FindProcess + signal 0 probe; stale lockfile →
   remove and continue).
5. The due-work handler stays a logged no-op — with a LOUD comment
   pointing at #485 stages 2-3 (throttle first, then tick handler).

## Tests — autopilot_honesty_test.go (red first)

- status on a fresh process (no lockfile) → truthful message, exit 0.
- stop on a fresh process → truthful message, non-zero exit (nothing to
  stop) — match whatever exit convention sibling commands use; assert it.
- start refuses when a live lockfile exists (fabricate lockfile with the
  current test PID → FindProcess succeeds) and proceeds when the
  lockfile names a dead PID (fabricate with an unallocated PID — e.g.
  4194303 heuristic may be flaky on CI; instead delete-before-assert
  pattern: write lockfile, then remove the process it names by using the
  test's own finished subcommand... prefer injecting a probe func).
- start writes the lockfile and removes it on SIGTERM (integration-style,
  skippable on flaky environments).

## Constraints

- stdlib + existing deps; match style; comments only for non-obvious
  constraints. Do NOT implement the scheduler or tick handler (stage 3).
- `go test ./cmd/g8s/` and `go build ./...` locally green.
