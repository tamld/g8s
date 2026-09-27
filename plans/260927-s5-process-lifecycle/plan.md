# Plan: S5 — Process Lifecycle Audit (#415)

**Session type**: T2 (execution)

```yaml
issue: 415
session_type: T2 (execution)
slice: S5 (post-v0.12.0 reliability slice)
ratified_by: operator suspicion 2026-09-27 — "spawn worker, gọi command mà
  không kill, thu hồi → orphan/zombie chạy âm thầm; đảm bảo sạch trên
  Windows, Mac, Linux"
```

## Objective

Provably clean process reaping on all three OSes: no silent orphan or zombie
survives a g8s worker spawn, a supervisor crash, or a daemon shutdown.

## Audited state (issue #415)

- POSIX: process groups + SIGTERM→SIGKILL escalation + pid-file `reapOrphans`
  at RunLoop start. Windows: `CREATE_NEW_PROCESS_GROUP` + two-stage
  `taskkill /T(/F)`.
- **Gaps**: Windows process groups ≠ Job Objects (grandchildren survive);
  `reapOrphans` is pid-file-gated and pre-run only; serve/mcp/autopilot
  children have no lifecycle owner; platform subagent grandchildren
  unverified; no cross-OS survivor test.

## Slice split (layer/OS discipline)

- **PR-1 (POSIX, internal/worker)**: post-run orphan sweep — after each
  attempt, verify the spawned process group is gone; kill survivors
  (best-effort, telemetry-reported `orphan_killed`); harden `reapOrphans`
  to also sweep the run root without pid files (bounded). Grandchild-escape
  test on POSIX (setsid double-fork must not survive). Windows skipped where
  the mechanism is POSIX-only (skip-tracked).
- **PR-2 (Windows)**: Job Object containment
  (`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`) replacing/augmenting process groups
  in `proc_windows.go`, with a grandchild-escape test runnable on
  windows-latest CI.
- **PR-3 (observability)**: lifecycle ownership for serve/mcp/autopilot
  children + `orphan_killed` telemetry surfacing.

## Verification contract

RED-first per PR (survivor tests fail before the sweep/containment lands),
dual-pass CI, pre-push 12/12, review. POSIX sweep tests skip on Windows;
Job Object tests require windows-latest.
