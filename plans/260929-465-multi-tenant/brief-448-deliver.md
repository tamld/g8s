# Task: #448 slice B (CLI layer) — `g8s deliver <task-id>`: deterministic, receipt-validated apply

Repo: g8s @ main (slice A — the deliverable pointer — is MERGED: result
JSON carries `deliverable: {mode: "worktree", dir}` on successful
worktree-isolated workspace_write attempts). Tracking: tamld/g8s#448.

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- cmd/g8s/deliver.go (new file)
- cmd/g8s/deliver_test.go (new file)
- cmd/g8s/main.go (ONLY: the `case "deliver"` dispatch line in the
  command switch + one usage line in printUsage — no other edits)

Do NOT run git commit. Do NOT modify any other file. Other workers may be
operating on internal/ packages in parallel — touching anything outside
the list corrupts their delivery.

## Design contract (operator-approved, do not deviate)

`g8s deliver <task-id>` is the supervisor's deterministic join: it applies
the worktree deliverable to the current checkout — the receipt, not the
agent, governs what may land.

1. Resolve the task from the control plane (same store open pattern as
   other commands: databasePath() + controlplane.NewControlPlane).
2. Read the task's result JSON. If `deliverable.mode != "worktree"` or
   `deliverable.dir` is empty/missing → usage error: "no deliverable
   pointer for <task-id> (attempt was not worktree-isolated, failed, or
   predates the pointer contract)".
3. In the pointer dir, run `git status --porcelain` (exit non-zero →
   runtime error naming the dir). Parse entries: modified (M), added (A),
   untracked (??) — renamed (R) and deleted (D) entries are reported and
   SKIPPED with a warning (copy-apply does not model them in v1).
4. **Receipt gate (the enforcement moment)**: read the task's payload
   `receipt_id`; load the receipt via the internal/receipt manager
   (follow how cmd/g8s/main.go runReceipt loads receipts for
   show/verify). Every dirty file path (relative to the worktree's repo
   root — the pointer dir IS a worktree of the same repo) must be covered
   by the receipt's allowed_paths globs. If ANY file is out of scope:
   apply NOTHING (atomic), exit non-zero listing every offending path.
   Missing/empty receipt_id → same refusal.
5. Apply: copy each in-scope file from the worktree to the same
   relative path in the CURRENT working directory (create parent dirs,
   0o644/0o755 preserving the source mode), then report one line per
   applied file. Do NOT git add/commit — staging stays with the operator.
6. `--dry-run` flag: perform steps 1-4 and print the would-apply list
   without copying.

JSON envelope shape follows the other commands (cli.NewEnvelope with
kind "deliver", data: {task_id, deliverable_dir, applied: [...],
skipped: [...], receipt_id}).

## Tests — deliver_test.go (red first, hermetic)

Use the package's existing test fixtures style (see worker_concurrency /
dogfood e2e tests for how a temp git repo + store is built). Cases:

1. Task with pointer + worktree containing 2 in-scope dirty files +
   receipt covering them → apply copies both to cwd, envelope lists both.
2. `--dry-run` → files listed, nothing copied.
3. One out-of-scope file among in-scope ones → NOTHING applied (both
   in-scope files absent from cwd), non-zero exit, offending path listed.
4. Task without pointer → clear usage error.
5. Missing receipt_id in payload → refusal, nothing applied.
6. Untracked file in scope is applied; renamed entry reported + skipped.

Receipt fixture: issue a real receipt via the receipt manager into a temp
receipts DB (same calls runReceipt uses) with allowed_paths covering the
fixture paths, and submit the fixture task with that receipt_id.

## Constraints

- stdlib + existing deps; match surrounding style; comments only for
  non-obvious constraints.
- No changes to internal/ packages (slice A already merged; the manager
  APIs you need are exported).
- Run `go test ./cmd/g8s/` and `go build ./...` locally — green.
