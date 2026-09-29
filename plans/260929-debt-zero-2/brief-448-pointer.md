# Task: Fix #448 (slice A, worker layer) — supervisor records a deterministic deliverable pointer

Repo: g8s @ main. Tracking: tamld/g8s#448. This is slice A of a 2-PR
split forced by the DEBT-34 layer gate: A = worker layer (this brief),
B = CLI layer (`g8s deliver <task-id>` command, follows after A merges).

## Design contract (operator-approved, do not deviate)

Delivery must stop being "whatever the agent happens to do". The
supervisor owns delivery provenance:

- On a workspace_write attempt that ran inside a worktree isolator AND
  succeeded, the supervisor records a **deliverable pointer** in the
  attempt result it writes via FinishAttempt:

  ```json
  "deliverable": {"mode": "worktree", "dir": "<absolute worktree path>"}
  ```

  The pointer is recorded only when the worktree was PRESERVED (the
  release(true) path — the dirty worktree survives per the #450
  contract). A worktree that was released for discard gets no pointer.
- All other attempts (read_only, no isolator, failed) produce results
  byte-identical to today (no `deliverable` key).
- The pointer intentionally carries NO git data: slice B's
  `g8s deliver <task-id>` resolves the dir, lists dirty files with git,
  validates them against the task's write receipt, and applies them.
  One seam, one job per layer.

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- internal/worker/worker.go
- internal/worker/deliverable_test.go (new file)

Do NOT run git commit. Do NOT modify any other file. Two other workers
are operating in parallel on unrelated files (docs/,
internal/harness/probe/) — touching anything outside the list above
corrupts their delivery.

## Required implementation

0. **Live-evidence check (do this FIRST, report findings)**: during the
   2026-09-29 debt-zero wave, a workspace_write attempt logged
   "worktree preserved (uncommitted deliverables)" from Pool.Release but
   the preserved directory was gone ~30 minutes later. Hypothesis: the
   release closure is invoked TWICE for one attempt (e.g. an explicit
   success-path call plus a defer with a different `keep` value), so the
   second call removes what the first preserved. Trace every
   `releaseWorktree` / release invocation path in RunOnce; if a
   double-release exists, fix it in THIS slice (same layer) with a
   regression test (a preserved worktree dir must still exist after
   RunOnce returns, when keep=true was recorded).
1. In `RunOnce` (internal/worker/worker.go ~line 500), the workspace_write
   isolator path acquires a worktree via `s.isolator.AcquireWorktree`
   (returns dir + release func). Capture the dir alongside the existing
   release closure handling.
2. Locate the SUCCESS path where the supervisor records the attempt
   result via `s.cp.FinishAttempt(... FinishAttemptParams{Result:
   mustJSON(map[string]any{...})})`. When (and only when) the attempt
   used the isolator AND succeeded AND the worktree was preserved with
   keep=true, add `"deliverable": map[string]any{"mode": "worktree",
   "dir": dir}` to that result map. Every other FinishAttempt result map
   is untouched.
3. Keep the existing release(keep) semantics EXACTLY as they are — this
   slice adds provenance, not behavior.

## Tests — deliverable_test.go (red first)

Use the existing fake-isolator test style in this package (check
worker_provider_test.go / worker tests for a WorktreeIsolator fake to
reuse or mimic; the fake returns a temp dir it creates):

1. workspace_write task + fake isolator + attempt succeeds → the result
   recorded via FinishAttempt parses as JSON containing
   `deliverable.mode == "worktree"` and `deliverable.dir` == the fake
   worktree dir.
2. workspace_write task + isolator nil (legacy shared checkout) → no
   `deliverable` key.
3. read_only task + fake isolator present → no `deliverable` key (the
   isolator is only consulted for workspace_write).
4. workspace_write + attempt fails → no `deliverable` key.
5. Regression: the rest of the result map is unchanged (assert an
   existing known key like `ok` still present alongside).

## Constraints

- No changes to the WorktreeIsolator interface signature.
- No changes to cmd/ or orchestrator/ (slices B / already-shipped #450).
- Match surrounding code style; comments only for non-obvious constraints.
- Run `go test ./internal/worker/` locally — full package green.
