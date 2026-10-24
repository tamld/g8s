# Phase 03 — Controlled `F1 -> F2` Subtask Containment & Cross-Repo `g8s fleet status` Gate

- **Status**: `pending`
- **Targets**: `cmd/g8s/submit.go`, `cmd/g8s/fleet.go`, `cmd/g8s/fleet_test.go`, `cmd/g8s/main.go`

## Steps
- [x] **3.1 (`F1 -> F2` Subtask Containment in `submit.go`)**: When `--parent-task-id` is non-empty:
  1. Look up the parent task in `store.GetTask(ctx, *parentTaskID)`. If not found, reject with `E_USAGE`.
  2. If the parent task itself has a non-empty `ParentTaskID` (`depth >= 2`), reject with `E_HARNESS` (`"containment depth exceeded: F2 tasks cannot spawn F3 subtasks (ADR-0032 max depth is F0->F1->F2)"`).
  3. Validate that all child `dirs` stay within the parent task's `AddDirs` scope roots.
- [x] **3.2 (`g8s fleet status` Command in `cmd/g8s/fleet.go`)**: Implement `g8s fleet status [--dir <workspace>] [--require-clean] [--json]` to scan all git repositories in the workspace directory and report:
  - `name`, `path`, `branch`, `dirty_count`, `unpushed_count`, `worktree_count`, `extra_branches`, `latest_tag`, `commits_since_tag`, `clean`.
- [x] **3.3 (Unit Tests)**: Add unit tests in `cmd/g8s/fleet_test.go` and `cmd/g8s/submit_route_test.go` verifying `F1 -> F2` acceptance, `F2 -> F3` rejection, and `g8s fleet status` detection of dirty/unpushed repos.
