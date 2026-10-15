# OpenSpec DELTA-16: Lifecycle Hygiene, Worker Heartbeats & Unified JSON Envelope v1

* **Specification ID**: `DELTA-16`
* **Title**: Lifecycle Hygiene, Worker Heartbeats, Cross-Platform Process Lister & Unified JSON Envelope v1
* **Status**: `APPLIED`
* **Milestone**: `M4` (Lifecycle Hygiene & Agent Ergonomics)
* **Target Packages**: `internal/heartbeat`, `internal/process`, `internal/cleanup`, `internal/cli`, `internal/watch`
* **Tracking Issues**: #118, #119, #120, #124, #130, #138, #141

---

## 1. Context & Motivation

`DELTA-16` standardizes worker process observability (`internal/heartbeat`, `internal/watch`), cross-platform OS process enumeration (`internal/process`), automated worktree and ghost-process cleanup (`internal/cleanup`), and the Unified JSON Envelope v1 (`internal/cli`) across all `g8s` CLI commands.

---

## 2. ADDED Requirements

### Requirement: Atomic Worker Heartbeats & Event-Driven Watch (`internal/heartbeat`, `internal/watch`)

1. Workers SHALL emit atomic JSON heartbeat files recording PID, task ID, session ID, state, and timestamp (`internal/heartbeat`).
2. `g8s watch` SHALL monitor task milestones (`worker-complete`) and failure transitions (`--failed`) without requiring blind polling loops.

#### Scenario: heartbeat atomic persistence, staleness detection, and milestone watch
<!-- tests: TestRecordRoundtrip, TestIsStale, TestAtomicWrite, TestWatchPassedOnSecondPoll, TestWatchFailedTerminal, TestWatchRunWithWorkerCompleteMilestone -->
- Heartbeat records are written atomically, stale records are detected deterministically, and `watch` wakes immediately on terminal or milestone events.

### Requirement: Cross-Platform Process Enumeration & Safe Ghost Cleanup (`internal/process`, `internal/cleanup`)

1. `internal/process` SHALL provide a pure-Go `Lister` across Darwin, Linux, and Windows (`ps` and `tasklist`/CIM) to inspect PIDs, command lines, and working directories.
2. `internal/cleanup` and worktree pool release hooks SHALL reap orphan sessions and ghost processes while refusing to delete dirty worktrees or receipt-protected deliverables.

#### Scenario: cross-platform process listing and safe worktree/ghost cleanup
<!-- tests: TestProcessLister_ListCurrentProcess, TestParsePsOutput, TestParseTasklistCSV, TestFindGhostProcesses_StaleHeartbeatRequiresIdentityEvidence, TestSweepOrphanWorktreeDirs_NeverRemovesLiveWorktree, TestReleasePreservesGitignoredProtectedDeliverable -->
- Live worktrees and receipt-scoped deliverables are preserved while stale ghost processes and orphan worktree directories are safely reaped.

### Requirement: Unified JSON Envelope v1 & Deterministic Exit Codes (`internal/cli`)

1. Every CLI subcommand supporting `--json` / `--jsonl` SHALL emit the canonical `cli.Envelope` (`schema_version: "v1"`, `ok`, `command`, `subcommand`, `actor`, `trace_id`, `timestamp`, `duration_ms`, `data`, `error`) and map errors to deterministic exit codes (`0`..`5`).

#### Scenario: unified JSON/JSONL envelope generation and command compliance
<!-- tests: TestNewEnvelope, TestNewErrorEnvelope, TestWriteJSONAndWriteJSONL, TestUnifiedEnvelopeCommands -->
- CLI commands emit valid `v1` JSON envelopes with trace IDs and structured error codes on both success and failure paths.
