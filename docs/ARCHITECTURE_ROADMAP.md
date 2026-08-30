# g8s Architecture Roadmap & Single Source of Truth Matrix

> **Governance Authority**: TamLD (`github.com/tamld/g8s`)  
> **Status**: Living Architectural Specification (SSoT)  
> **Tracking Issue**: DEBT-33 (#125)  
> **Axiom**: *"Intelligence directs; Muscle executes; Harness protects; Runtime proves."*

---

## Executive Summary

This document serves as the unified **Single Source of Truth (SSoT)** connecting the immutable facts of the g8s technology stack, long-term strategic decisions, phase progressions across all OpenSpec deltas, cross-phase invariants, Architecture Decision Records (ADRs), and failure mode catalog.

Whether an autonomous AI agent (e.g., Sisyphus, Antigravity, Claude, Codex) or a human engineer enters the repository, this matrix provides immediate, unambiguous context on whether any proposed modification is **on-strategy**, **on-phase**, and **invariant-compliant**.

---

## 1. Techstack (Immutable Facts Table)

The following table defines the non-negotiable architectural boundaries, runtime engine choices, and physical infrastructure powering the g8s kernel:

| Subsystem / Layer | Technical Choice / Dependency | Version / Standard | Rationales & Architectural Constraints |
| :--- | :--- | :--- | :--- |
| **Core Language** | Go | `1.22+` (toolchain `go1.25.14`+) | High concurrency, predictable memory footprints, sub-millisecond execution latency. |
| **Compilation Standard** | Pure-Go (`CGO_ENABLED=0`) | Zero CGO | Eliminates dynamic C runtime links, cross-compilation toolchain pain, and shared library vulnerabilities. |
| **Embedded Database** | `modernc.org/sqlite` | Pure-Go SQLite engine | File-based state management, SQLite WAL mode, `PRAGMA busy_timeout=5000`, `0600` POSIX file security. |
| **CLI & Flag Parsing** | Standard Library `flag.FlagSet` | Pure Go standard | Lightweight, zero-allocation subcommand dispatch with deterministic flag validation. |
| **Structured Output** | Unified JSON Envelope v1 | Schema v1 (`envelope.go`) | Machine-readable envelope (`v`, `kind`, `command`, `trace_id`, `actor`, `data`, `error`) supporting `--json` and `--jsonl`. |
| **IPC & Protocols** | Stdio JSON-RPC 2.0 (MCP) | Model Context Protocol | Standardized bidirectional tool protocol interfacing with IDE hosts (Claude Desktop, Cursor, Windsurf). |
| **Code Intelligence** | AST / LSP Client Sidecar | `internal/analyzer` | Language Server Protocol integration (`gopls`, `pyright`) for Blast Radius calculation and write boundary enforcement. |
| **Memory & Indexing** | SQLite FTS5 + BM25 | Pure Go FTS Tokenizer | Decoupled knowledge vault indexing code symbols, identifiers, CamelCase, and snake_case tokens. |
| **Process Containment** | OS Process Groups (`Setpgid`) | POSIX / Win32 JobObjects | Guarantees clean subtree process termination (`SIGTERM` / `SIGKILL` to `-pgid`) without orphan processes. |
| **Packaging & Release** | GoReleaser & NFPM | GoReleaser `v2.10+` | Static single-binary builds for `darwin/arm64`, `darwin/amd64`, `linux/amd64`, `linux/arm64`, and `windows/amd64`. |
| **Quality Gates** | Go Test, Staticcheck, Gosec, AI Lint | Multi-layer CI | Enforces `-race` detector, zero unchecked errors (`errcheck`), memory optimization (`gofumpt`), and zero AI anti-patterns. |

---

## 2. Strategy (Architecture Decisions)

The design and development of g8s are guided by core strategic principles:

```
┌──────────────────────────────────────────────────────────────────────────┐
│                         TWO-TIER AGENT TAXONOMY                         │
├─────────────────────────────────────┬────────────────────────────────────┤
│         SUPERVISOR (Brain)          │          WORKER (Muscle)           │
│  • High-Tier (Claude 3.7 / R1)      │  • Low-Tier (Gemini Flash / Haiku) │
│  • System Architecture & SSoT       │  • Bounded Sandboxes & Tool Exec   │
│  • Strategy & Approach Shift        │  • High Throughput & Low Latency   │
│  • Write Receipt Issuance           │  • Receipt-Gated File Mutations    │
└──────────────────┬──────────────────┴──────────────────┬─────────────────┘
                   │                                     │
                   ▼                                     ▼
        ┌─────────────────────┐               ┌─────────────────────┐
        │  CODE-LEVEL GATES   │               │ LEGO-BLOCK MOUNTS   │
        │ • ADR-0001 Fix Loop │               │ • SkillMount        │
        │ • Write Receipts    │               │ • HookMount         │
        │ • Unified Envelope  │               │ • MemoryMount       │
        └─────────────────────┘               └─────────────────────┘
```

1. **Two-Tier Agent Governance**: Strict separation between the reasoning supervisory brain (architecture, spec authorship, receipt signing, git commits) and mechanical execution workers (scouting, tests, file modifications).
2. **Code-Level Orchestration over Prompt-Level Trust (ADR-0001)**: The supervisor does not merely prompt the worker; it exercises code-level verification using typed Go interfaces and receipts as authoritative proof.
3. **Zero-Trust Capability Delegation (ADR-0002)**: Filesystem mutations are prohibited unless accompanied by an unexpired, single-use, path-scoped cryptographic Write Receipt.
4. **Dual-Audience Interface Design**: Equal focus on Human Developer Experience (Human DX: Feynman diagnostic hints, TUI wizards) and Autonomous Agent Experience (Agent AX: unified JSON envelope v1, `--jsonl` streaming, deterministic exit codes `0` to `5`).
5. **Non-Invasive Lego Block Mounts**: Stateful workers mount capabilities (`SkillMount`, `HookMount`, `MemoryMount`) dynamically without coupling worker processes to the core orchestrator.
6. **Resilient Process Lifecycle Hygiene**: Real-time heartbeat mapping (`internal/heartbeat`), process enumeration (`internal/process`), automated worktree pruning (`tools/cleanup_worktrees.sh`), and safe kill confirmation.

---

## 3. Phases (Time-Ordered Specification & PR Lineage Matrix)

The evolution of g8s is tracked across time-ordered phases, OpenSpec deltas, GitHub pull requests, and tracking issues:

| Phase | Milestone / Domain | OpenSpec DELTA | Tracking Issue | Key PRs Merged | Status |
| :---: | :--- | :--- | :--- | :--- | :---: |
| **0** | **Foundation & Pure-Go Harness** | DELTA-01, DELTA-02, DELTA-03, DELTA-04, DELTA-05, DELTA-06 | SSoT Baseline | #48, #50 | **Done** |
| **1** | **Core Lifecycle, Evidence Lake & Knowledge Vault** | DELTA-07 (Blast Radius)<br>DELTA-08 (Dispatch)<br>DELTA-09 (Supervisor)<br>DELTA-10 (Two-Class Providers)<br>DELTA-11 (Knowledge Vault)<br>DELTA-12 (Lineage CTE) | #58, #59, #67, #68, #69, #72, #73, #74, #75 | #53, #60, #61, #62, #63, #64, #65, #66, #70, #71 | **Done** |
| **2** | **Resilience, Hardening & DX/AX** | DELTA-13 (DX/AX Wizard & Auto-repair)<br>DELTA-14 (Core Hardening) | #77, #78, #79, #80 | #76, #81, #89, #98, #99, #102 | **Done** |
| **3** | **Orchestrator FSM, Lego Blocks & Quality Gates** | DELTA-15 (FSM Lifecycle)<br>DELTA-17 (Receipt Lake Wiring)<br>DELTA-18 (AIC Orchestration)<br>DELTA-19 (Lego Mounts)<br>Concern A/B/C Fix Loop | #82, #83, #84, #85, #86, #87, #91, #92, #93, #94, #95, #96, #97, #107, #112, #113, #114 | #100, #101, #103, #104, #105, #106, #108, #109, #110, #111, #115, #117 | **Done** |
| **4** | **Lifecycle Hygiene, Heartbeats & Worktree Tooling** | DEBT-28 (Lifecycle Cleanup)<br>DEBT-29 (Worker Heartbeats)<br>DEBT-30 (Unified JSON Envelope)<br>DEBT-32 (Auto-cleanup Hook)<br>DEBT-36 (Process Lister & Kill)<br>DEBT-37 (Worktree Helpers) | #118, #119, #120, #124, #130, #138 | #121, #122, #128, #129, #131, #132, #133, #134, #135, #136, #139 | **Done** |
| **5** | **Active Roadmap & Next Initiatives** | DEBT-31 (Pure FSM Validator)<br>DEBT-33 (Architecture Roadmap)<br>DEBT-34 (Layer-Ownership Gate)<br>DEBT-35 (Adaptive Polling & Escalation) | #123, #125, #126, #127 | Active Sprint | **In Progress** |

### Complete PR Mapping Index

* **PR #48**: OpenSpec manifest synchronization and Homebrew tap release preparation.
* **PR #50**: Language purity standardization and bilingual README switcher.
* **PR #53**: Decoupled Memory Architecture whitepaper, IDE integrations, and sync CLI reference.
* **PR #60**: Tri-Anchor Knowledge Distillation specification and v0.3.0 roadmap registry.
* **PR #61**: Task resumption engine, evidence lake storage, CLI adapter, and regex hardening.
* **PR #62**: DELTA-10 two-class provider architecture (Brain vs Worker).
* **PR #63**: Bolt performance optimization in `matchSnippet` avoiding rune allocation.
* **PR #64**: Pure-Go Decoupled Knowledge Vault backed by SQLite FTS5 and BM25 ranking.
* **PR #65**: AST & LSP Blast Radius Analyzer for automated write boundary scoping.
* **PR #66**: Cross-platform OS service manager, doctor diagnostics, and unbuffered stream pipe.
* **PR #70**: Operations runbook, security verification guide, and distribution validation workflow.
* **PR #71**: DELTA-10 operator-defined command templates and fallback provider drivers.
* **PR #76**: SQLite recursive CTE task lineage, Windows process tree management, and symbol tokenizer.
* **PR #81**: DELTA-13 interactive `init` wizard, `doctor --fix` auto-repair, and shell completions.
* **PR #89**: Bolt optimization for `CaptureBounded` stream allocations.
* **PR #98**: Bolt optimization for `TokenizeCodeSymbols` string normalizer.
* **PR #99**: Sentinel security patch resolving SQLite connection string injection in doctor.
* **PR #100**: Re-enabling strict quality checks (DEBT-22 staticcheck, DEBT-23 errcheck, DEBT-24 gosec).
* **PR #101**: DELTA-15 Orchestrator FSM engine (`PLAN` $\rightarrow$ `SPAWN` $\rightarrow$ `MONITOR` $\rightarrow$ `RECEIPT` $\rightarrow$ `MERGE|ESCALATE`).
* **PR #102**: GoReleaser v2.10+ schema migration.
* **PR #103**: DELTA-17 Receipt lake wiring to control plane task metadata.
* **PR #104**: DELTA-11 Concern B receipt schema evolution for supervisor provenance.
* **PR #105**: DELTA-19 `SkillMount`, `HookMount`, and `MemoryMount` lego blocks for stateful workers.
* **PR #106**: DELTA-18 AIC intent orchestration wrapper.
* **PR #108**: Subcommands `brief-issue` and `brief-consume` dispatch contract.
* **PR #109**: DELTA-11 Concern C read-only meta-optimizer query layer and CLI streaming.
* **PR #110**: Dogfooding CI gate executing `make dogfood` on every PR.
* **PR #111**: AI Anti-Pattern CI gate (`tools/ai_lint.sh`) detecting common LLM code smells.
* **PR #115**: Automatic git worktree cleanup for agy subagents (`cleanup-worktrees`).
* **PR #117**: Integration of brief workflow with orchestrator and Sisyphus dispatch loop.
* **PR #121**: `g8s cleanup` subcommand for ghost processes and orphan resources (DEBT-28).
* **PR #122**: Per-session heartbeat file and `g8s status --worker` observability (DEBT-29).
* **PR #128**: Unified JSON envelope v1 with `--actor`, `--trace-id`, and `--jsonl` support (DEBT-30).
* **PR #129**: Heartbeat emission shim for agy sessions.
* **PR #131**: Orchestrator auto-cleanup hook on terminal state transitions (DEBT-32).
* **PR #132**: One-shot sweeper for orphan worktree refs and ghost sessions.
* **PR #133**: Cross-platform `ProcessLister` interface and Darwin/Linux/Windows implementations (DEBT-36).
* **PR #134**: Heartbeat-PID mapping and safe-kill confirmation engine (DEBT-36).
* **PR #135**: Bulk cleanup of orphan `agy-sup-*` git branches.
* **PR #136**: Bulk cleanup of orphan `feat/*` git branches.
* **PR #139**: Git worktree helper tools (`spawn_worktree.sh`, `drop_worktree.sh`, `cleanup_worktrees.sh`) and Makefile targets (DEBT-37).

---

## 4. Invariants (Cross-Phase Engineering Laws)

All past, current, and future contributions must satisfy the **8 Invariant Rules**:

1. **Zero-CGO & Pure-Go Invariant**: The repository must compile cleanly with `CGO_ENABLED=0` across all target architectures. SQLite MUST use `modernc.org/sqlite`.
2. **Zero-Trust Capability Receipts**: Filesystem modifications require a valid, path-scoped, time-limited ($\le 3600s$), single-use Write Receipt verified atomically in SQLite.
3. **Process & State Isolation**: All subprocesses execute inside dedicated Process Groups (`Setpgid`). Databases, logs, and receipt caches enforce `0600`/`0700` POSIX file security.
4. **Deterministic Clocks**: Any component handling time (leases, TTLs, heartbeats) must accept an injectable `clock func() time.Time` to ensure deterministic, zero-sleep unit tests.
5. **Two-Tier Governance**: High-tier reasoning models hold exclusive authority over code commits and receipt issuance. Low-tier workers are bounded to read-only sandboxes by default.
6. **Self-Describing Executable (SSoT)**: The binary itself is the documentation. All capabilities, flags, schemas, and remediation hints are discoverable via CLI flags (`--help`, `--json`).
7. **Quality & AI Anti-Pattern Gate**: All code must pass `go test -race ./...`, `staticcheck`, `gosec`, `errcheck`, `gofumpt`, and `tools/ai_lint.sh` (zero panics, zero unhandled errors, zero unassigned TODOs).
8. **100% Language Purity**: Code, comments, docs, and git commit messages must be written in professional English with zero non-ASCII diacritics.

---

## 5. Decision Log (Architecture Decision Records)

The key architecture decision records governing g8s development include:

* **[ADR-0001](decisions/0001-supervisor-driven-fix-loop.md)**: **Supervisor-Driven Fix Loop with Code-Level Enforcement**
  * *Decision*: Rejects prompt-injected fuzzy trust in favor of typed Go contracts (`internal/harness`, `internal/receipt`, `internal/orchestrator`) where worker receipts are the sole authoritative proof of completion.
  * *Implements*: DELTA-11 Orchestration Roadmap (Concern A).
* **[ADR-0002](decisions/0002-receipt-evolution-for-supervisor.md)**: **Receipt Evolution for Supervisor Provenance**
  * *Decision*: Extends the SQLite `write_receipts` table with `approach_idx`, `attempt_idx`, `rca_confidence`, and `adr_path` columns to support structured audit trails across strategy pivots.
  * *Implements*: DELTA-11 Concern B (Receipt Evolution).
* **[ADR-0014](decisions/0014-gofumpt-over-gofmt.md)**: **Use `gofumpt` over `gofmt` for Strict Formatting**
  * *Decision*: Adopts `gofumpt` as the canonical formatter in CI to enforce strict consistency and eliminate whitespace drift across AI-generated patches.
* **[ADR-0015](decisions/0015-pin-linter-versions.md)**: **Pin Linter Versions in CI Workflows**
  * *Decision*: Pins explicit versions for all CI tools (`staticcheck`, `gosec`, `errcheck`, `gofumpt`) to prevent sudden breakage from upstream breaking changes.
* **[ADR-0016](decisions/0016-exclude-cmd-g8s-coverage.md)**: **Exclude `cmd/g8s` from Aggregate Coverage Threshold**
  * *Decision*: Focuses strict unit test coverage ($\ge 80\%$) on core domain logic (`internal/*`) while testing `cmd/g8s` through CLI envelope integration tests.
* **[ADR-0017](decisions/0017-errcheck-excludes-defer-close.md)**: **Use `.errcheck_excludes` for `defer _ = X.Close()` Idiom**
  * *Decision*: Whitelists benign deferred Close calls on read-only descriptors while maintaining strict zero-swallowed-error enforcement elsewhere.

---

## 6. Failure Modes Catalog (GAPS, Stales, Conflict, Ghost)

This catalog details known failure modes in multi-agent autonomous execution loops, their detection mechanisms, and built-in mitigations:

```
┌────────────────────────────────────────────────────────────────────────┐
│                        FAILURE MODES CATALOG                          │
├──────────────────────┬────────────────────────┬────────────────────────┤
│ FAILURE MODE         │ DETECTION MECHANISM    │ SYSTEM MITIGATION      │
├──────────────────────┼────────────────────────┼────────────────────────┤
│ 1. Ghost Subprocesses│ • ProcessLister        │ • Setpgid PGID SIGTERM │
│    & Orphan Daemons  │ • Heartbeat Age > TTL  │ • g8s cleanup command  │
├──────────────────────┼────────────────────────┼────────────────────────┤
│ 2. Stale Git         │ • cleanup_worktrees.sh │ • Safe Ref Pruning     │
│    Worktrees         │ • g8s cleanup-worktrees│ • Owner Command Print  │
├──────────────────────┼────────────────────────┼────────────────────────┤
│ 3. Database Locking  │ • SQLite SQLITE_BUSY   │ • WAL Mode + CAS       │
│    & CAS Conflicts   │ • Lease Contention Log │ • 5s Busy Timeout      │
├──────────────────────┼────────────────────────┼────────────────────────┤
│ 4. Subagent Stalls   │ • Heartbeat Monitor    │ • Adaptive Polling     │
│    & Silent Freezes  │ • Stale Timestamp      │ • Silence-Escalation   │
├──────────────────────┼────────────────────────┼────────────────────────┤
│ 5. Capability Bypass │ • Post-Run Mutation Reg│ • Exit Code 3 Forced   │
│    & Unreceipted File│ • SQLite Receipt Audit │ • CAS Atomicity Rollback│
├──────────────────────┼────────────────────────┼────────────────────────┤
│ 6. AI Code Smells    │ • tools/ai_lint.sh     │ • Quality Gate Rejection│
│    & Swallowed Errors│ • errcheck / staticcheck│ • OWNER= Annotation Req│
└──────────────────────┴────────────────────────┴────────────────────────┘
```

### 1. Ghost Subprocesses & Orphan Daemons
* **Root Cause**: Worker or subagent process crashes or loses parent TTY while child processes remain running in the background.
* **Detection**: `internal/process.ProcessLister` scans system processes; `internal/heartbeat` identifies stale PID timestamps exceeding heartbeat TTL.
* **Mitigation**: Process group execution (`Setpgid: true`) enables terminating entire process trees via `-pgid`. Subcommand `g8s cleanup` safely terminates orphaned processes with PID confirmation (PR #133, PR #134).

### 2. Stale Git Worktrees & Directory Accumulation
* **Root Cause**: Autonomous agents spawn temporary git worktrees for isolated subtasks and fail to remove them upon failure or early exit.
* **Detection**: `git worktree list --porcelain` cross-referenced with disk paths in `/tmp`, `$TMPDIR`, and `/private/var/folders`.
* **Mitigation**: `tools/cleanup_worktrees.sh` prunes expired git references and generates explicit `rm -rf` cleanup commands for repository owner execution without tripping Safety Net barriers (PR #115, PR #139).

### 3. Database Locking & CAS Contention
* **Root Cause**: High-concurrency worker dispatch simultaneously reading and writing to the SQLite control plane.
* **Detection**: `SQLITE_BUSY` errors or database lock contention logs.
* **Mitigation**: Strict SQLite WAL (`PRAGMA journal_mode=WAL`), 5000ms busy timeout (`PRAGMA busy_timeout=5000`), and Compare-And-Swap (CAS) state transition queries preventing race conditions.

### 4. Subagent Stalls & Silent Execution Freezes
* **Root Cause**: Subagent CLI hanging on unbuffered stdio, deadlocked network call, or unbounded tool recursion.
* **Detection**: Heartbeat emission monitor fails to receive periodic ticks within configured window (DEBT-29 / PR #122, PR #129).
* **Mitigation**: Adaptive polling and silence-escalation in supervisor (DEBT-35 / #127) automatically terminating stalled workers and escalating to alternate strategies.

### 5. Capability Violations & Unreceipted Workspace Mutations
* **Root Cause**: Worker attempting to modify files outside its assigned role contract or after receipt TTL expiry.
* **Detection**: `internal/worker` post-run mutation scan comparing stdout/stderr against mutation regex patterns; `internal/receipt` atomic consumption check.
* **Mitigation**: Worker execution immediately halted with Exit Code `3` (`READ_ONLY_CONTRACT_EXIT`); unauthorized modifications rolled back.

### 6. AI Anti-Patterns & Code Smells
* **Root Cause**: LLM code generation injecting unchecked panics, swallowed errors (`_ = ...`), unassigned TODO markers, or conversational boilerplate.
* **Detection**: `tools/ai_lint.sh` CI gate running `check_no_panic`, `check_no_ignored_errors`, `check_no_type_assertion_in_library`, `check_todo_owner`, and `check_no_ai_artifacts`.
* **Mitigation**: Immediate CI quality gate failure blocking PR merge until cleaned (PR #111).

---

## 7. Next Actions & Strategic Queue

The active engineering queue is prioritized according to the following progression:

1. **DEBT-31 (#123)**: `internal/state` package — pure FSM transition validator and append-only event log.
2. **DEBT-34 (#126)**: Layer-ownership CI gate enforcing modular package dependency isolation.
3. **DEBT-35 (#127)**: Adaptive polling and silence-escalation mechanism in supervisor event loop.
4. **v0.5.0 Distribution**: Fleet coordination over mTLS and distributed lock consensus.
