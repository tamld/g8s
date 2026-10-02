# Task: #485 stage 3 — `g8s autopilot tick` + native-scheduler install adapters

Repo: g8s @ main. Tracking: tamld/g8s#485 stage 3 (staged path ratified;
stage 2 submit throttle ALREADY MERGED on main — the anti-runaway
prerequisite is satisfied).

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- cmd/g8s/autopilot.go (add `tick` and `install-schedule`/`uninstall-
  schedule` subcommands to the existing dispatch; do not restructure
  existing subcommands)
- cmd/g8s/autopilot_tick_test.go (new file)
- cmd/g8s/autopilot_schedule_test.go (new file)
- docs/user-guide/autopilot.md (new file)

Do NOT run git commit. Do NOT modify any other file. Another worker owns
internal/ and internal/pathutil in parallel — do not touch internal/.

## Context (verified at main)

- `cmd/g8s/autopilot.go` currently has start/stop/status/trigger/config
  with stage-1 honesty semantics (status reports no cross-process
  scheduler exists; start runs in-process only, behind an [EXPERIMENTAL]
  banner, with a state-dir lockfile).
- The due-work handler is a logged no-op. The safe jobs to wire exist:
  doctor checks (internal/doctor), evidence retention sweep
  (internal/cleanup target `evidence`), orphan/ghost hygiene sweeps
  (internal/cleanup, state-dir scoped post-#468).
- `internal/worker/proc_windows.go` / `internal/cleanup/cwd_windows.go`
  show the repo's existing x/sys/windows patterns.
- Deliberately OUT: any auto-resubmission of failed tasks (stage 4),
  any long-running daemon. The tick is stateless and exits.

## Required implementation

1. **`g8s autopilot tick [--jobs <list>]`**: stateless, exits when done.
   Default jobs: `doctor` (run the doctor checks, summarize), `retention`
   (run the cleanup evidence target honoring `evidence_retention_days`),
   `hygiene` (run the cleanup orphan/ghost sweeps — state-dir scoped).
   Flags select a subset. Output: one JSON envelope per job result
   (`kind: "autopilot_tick"`, data: {job, status, detail}). Never
   submits/resubmits tasks.
2. **`g8s autopilot install-schedule --every <duration>`** (and
   `uninstall-schedule`): generates and installs a native schedule unit
   invoking `g8s autopilot tick`:
   - Linux: append/remove a crontab line (idempotent marker comment
     `# g8s-autopilot`), via `crontab -l`/`crontab -`.
   - macOS: write/remove a LaunchAgent plist
     (`~/Library/LaunchAgents/g8s.autopilot.plist`,
     StartInterval = seconds), then `launchctl load/unload`. Follow the
     existing internal service-manager patterns if importable from cmd
     (check what DELTA-06's manager exports; if it is not importable
     from cmd, generate the plist inline).
   - Windows: `schtasks /Create /F /SC MINUTE /MO <n> /TN g8s-autopilot
     /TR "<absolute g8s.exe path> autopilot tick"` via exec (documented
     in a comment).
   - Refuse to install when the g8s binary path cannot be resolved
     (os.Executable) — the schedule must point at a real binary.
   - Idempotent: installing twice replaces, not duplicates.
3. **Docs** (docs/user-guide/autopilot.md): the staged path summary
   (#485), what tick runs, per-OS install examples, the "ticks are
   hints; the queue is the memory" model, and stage-4 status (budgeted
   retry: NOT implemented yet).

## Tests — red first

- autopilot_tick_test.go: tick with fixture state dir runs the default
  jobs hermetically (temp dirs; doctor checks run, retention sweep
  removes an over-retention fixture dir, hygiene sweeps tolerate an
  empty registry); `--jobs retention` runs only that job; output
  contains one envelope per job.
- autopilot_schedule_test.go: generate-only mode (or OS-guarded):
  crontab line generation on Linux/darwin hosts asserts the marker +
  interval; plist generation asserts StartInterval + program path;
  Windows generation is compile-tested only (t.Skip when not windows).
  Uninstall removes the artifacts.
- Existing autopilot tests stay green.

## Constraints

- stdlib + existing deps (x/sys/windows already available); match style.
- `go test ./cmd/g8s/` and `go build ./...` locally green; also verify
  `GOOS=windows go build ./cmd/g8s/` compiles.
