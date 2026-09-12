# Supervisor-Driven Fix Loop

> **Version**: v1.2.0-ACCEPTED
> **Last Changed**: 2026-09-12
> **Status**: ACCEPTED — Concern A complete (T020), Concern B complete (T021), Concern C read-only ingestion complete (T022)
> **Scope**: Concerns A, B, and C (read-only) of the orchestration roadmap.

---

## 1. Problem Statement

The current `internal/orchestrator` package executes one bounded worker subprocess per task. The orchestrator does not decide what to fix, when to fix it, or whether the fix succeeded. Those decisions live outside the runtime. Without a supervisor that:

1. Discovers work autonomously,
2. Decomposes work into shaped task envelopes,
3. Reviews worker receipts and decides whether to revise, retry-with-different-approach, or escalate,

the runtime stays a passive executor and every fix loop must be hand-driven by a human.

This document proposes a **supervisor-driven fix loop** that wraps the existing `internal/orchestrator` with planning, RCA, ADR, and escalation logic.

### 1.1 Why this matters (the three north stars)

| North Star | Risk today | What this doc changes |
| ---------- | ---------- | --------------------- |
| **Disciplined worker dispatch** with receipt + harness, not prompt injection | Skill/orchestrator systems rely on the model cooperating. A model update, a jailbreak, or a misaligned prompt can leak past the gate. | g8s gates worker behavior in Go (`internal/harness`), in SQL (`internal/receipt`), and in git diff (`internal/orchestrator`). The model has no path around any of them. Receipts are proof, not claims. |
| **g8s as the harness / guard rail / skills substrate** for the whole ecosystem | Each tool reinvents its own contract: roles live in YAML, permissions live in markdown, receipts live in JSON-LD. Nothing interoperates. | g8s ships typed Go contracts for Role, Permission, Receipt, Worktree, FSM. Other tools import the same types. One substrate, one source of truth. |
| **Code is the truth** — g8s as SSoT | Specs and code drift. Receipts are freeform text. There is no way to prove what actually ran. | Every gate is a function with a test. Every receipt is a row in SQLite WAL. Every ADR is a versioned file. `go test -race ./...` is the contract. The diff between main and the running system is the spec. |

---

## 2. Goals & Non-Goals

### Goals

- **G1**. Supervisor (Brain) drives the loop end-to-end. AGY is one worker backend among many, never the decision maker.
- **G2**. Every task has a **measurable envelope** (a subset of SRS, PRD, DoR, DoD, DnD, Validateds, FSM) chosen by the supervisor based on task complexity.
- **G3**. Every failed approach shift produces a **Root Cause Analysis (RCA) + Architecture Decision Record (ADR)** explaining why the prior approach was wrong and what the new approach changes.
- **G4**. The loop has a **bounded iteration policy** that prevents infinite revision but allows persistent problem solving within a single approach.
- **G5**. After exhaustion, the loop **escalates to HITL (Human-In-The-Loop)** with a digest of attempts, RCAs, and ADRs so the human can intervene meaningfully.
- **G6**. Every supervisor decision is **measurable** so the supervisor can learn from success and failure (Concern C).

### Non-Goals

- **N1**. This document does **not** introduce a meta-optimizer with write/tuning capability — Concern C is **read-only** in this tranche. Writes and automatic tuning are a later tranche.
- **N2**. This document does **not** add a UI. CLI and JSON output only.
- **N3**. This document does **not** change `internal/orchestrator` Worker interface. The supervisor sits above it.

---

## Concern B: Receipt Evolution for Supervisor

This section specifies the receipt schema extensions required for the supervisor to make decisions (approach shift, RCA confidence, ADR linkage, attempt lineage).

### B.1 Additive Supervisor Metadata Columns

The `write_receipts` table SHALL be extended with four nullable columns:

| Column | Type | Purpose |
|--------|------|---------|
| `approach_idx` | INTEGER | Supervisor approach index (0-based) |
| `attempt_idx` | INTEGER | Supervisor attempt index within approach (0-based) |
| `rca_confidence` | REAL | RCA confidence [0, 1] that drove the approach shift |
| `adr_path` | TEXT | Filesystem path to the ADR written for this approach shift |

All columns are nullable so that receipts issued before the migration remain readable. The Go struct `SupervisorMeta` encapsulates these fields and is an optional field on `WriteReceipt`.

### B.2 Idempotent Backward-Compatible Migration

The migration from schema version 1 → 2 SHALL:
1. Inspect `write_receipts` columns via `PRAGMA table_info`.
2. For each missing column in the set above, execute `ALTER TABLE write_receipts ADD COLUMN <col> <type>`.
3. Run inside the exclusive transaction in `Manager.initialize()`.
4. Be idempotent: re-running on an already-migrated database is a no-op.

The migration is implemented in `migrateSupervisorSchema` and is invoked automatically on `NewReceiptManager`.

### B.3 Nil-Safe IssueReceipt Signature Extension

`IssueReceipt` SHALL accept an optional `SupervisorMeta` via the unexported `WithSupervisorMeta` option:
- Callers that do not pass it (legacy code) MUST succeed with `SupervisorMeta == nil`.
- `VerifyReceipt`, `ValidateAndConsume`, and `ListActiveReceipts` SHALL read the four columns when present.
- If all four columns are NULL, `SupervisorMeta` SHALL be `nil` (not zero values, not error).

### B.4 Provenance-and-Replay Context (Schema Version 3)

Schema version 3 adds provenance-and-replay context for forensic audit:

| Column | Type | Purpose |
|--------|------|---------|
| `envelope_schema_uri` | TEXT | Canonical envelope SchemaURI (g8s://) |
| `envelope_field_order_json` | TEXT | JSON-encoded field order array |
| `envelope_required_fields_json` | TEXT | JSON-encoded required fields array |
| `ruleset_version` | TEXT | Rule registry version at issue time |
| `pipeline_digest` | TEXT | Pipeline digest at issue time |
| `adr_ref` | TEXT | ADR reference from rule graph |
| `issued_by` | TEXT | Issuer identity |
| `tool_version` | TEXT | Tool version (e.g., g8s v0.9.1) |
| `trace_id` | TEXT | Distributed trace ID |
| `actor_chain_json` | TEXT | JSON-encoded actor chain |
| `source_commit` | TEXT | Source commit SHA at issue time |

These are populated via `WithCanonicalEnvelope`, `WithRuleGraph`, and `WithProvenanceContext` options. The migration is implemented in `migrateConcernBSchema`.

---

## Concern C: Meta-Optimizer Read-Only Ingestion

This section specifies the read-only aggregate and streaming APIs that the meta-optimizer consumes to learn from supervisor history. **No writes or automatic tuning occur in this tranche.**

### C.1 Read-Only Aggregate API

The supervisor SHALL expose a read-only aggregate API over `supervisor_tasks` and `supervisor_metrics` tables. The API SHALL compute the following aggregate metrics across all persisted runs (optionally filtered by time range, worker name, or task state):

| Metric | Description |
|--------|-------------|
| `total_runs` | Total supervisor runs in the filter window |
| `first_attempt_success_rate` | Fraction of runs where `FirstAttemptSuccess = true` |
| `avg_attempts_to_success` | Mean `AttemptsToSuccess` across runs |
| `avg_approaches_to_success` | Mean `ApproachesToSuccess` across runs |
| `rca_confidence_avg` | Mean `RCAConfidenceAvg` across runs |
| `escalation_rate` | Fraction of runs with `EscalationCount > 0` |
| `avg_cycle_duration_seconds` | Mean `CycleDurationSeconds` across runs |
| `false_escalation_rate` | Fraction of escalations where user feedback indicates should have succeeded (future Concern C tuning) |

The aggregate SHALL be queryable via `g8s supervisor metrics --aggregate` (CLI) and `supervisor.Aggregate()` (Go API). The query SHALL be strictly read-only — no writes to any table.

#### Scenario: aggregate over empty store
- `Aggregate()` on an empty database returns zero-value `AggregateMetrics` with `TotalRuns = 0`, no error.

#### Scenario: aggregate over N tasks
- `Aggregate()` computes all eight metrics correctly, matching the formulas in §10.

#### Scenario: filter by time_range
- `AggregateOptions{TimeRange: 1*time.Hour}` restricts to tasks created in the last hour.

#### Scenario: filter by worker_name
- `AggregateOptions{WorkerName: "agy"}` restricts to tasks attributed to the "agy" worker (via envelope JSON, task row, or decision row).

### C.2 Streaming API + Flag Collision Guard

The supervisor SHALL expose a streaming API `StreamMetrics()` that emits one `TaskMetricsItem` per task matching the filter, allowing incremental processing without loading all rows into memory. The CLI SHALL surface this via `g8s supervisor metrics --json-stream`.

The CLI SHALL reject the combination `--task-id` + `--aggregate` (flag collision) with a typed usage error (exit code 2), because `--task-id` requests single-run mode while `--aggregate` requests cross-run aggregation. `--task-id` + `--json-stream` SHALL also be rejected.

#### Scenario: streaming emits per-task items
- `StreamMetrics()` calls the callback once per matching task with `TaskMetricsItem` populated from the stored metrics.

#### Scenario: flag collision --task-id + --aggregate
- `g8s supervisor metrics --task-id sup-1 --aggregate` exits with code 2 and prints usage error "cannot combine --task-id with --aggregate".

#### Scenario: flag collision --task-id + --json-stream
- `g8s supervisor metrics --task-id sup-1 --json-stream` exits with code 2 and prints usage error "cannot combine --task-id with --json-stream".

---

## 3. Architecture Overview

```
                     ┌─────────────────────────┐
                     │   Supervisor (Brain)    │
                     │   = g8s orchestrate CLI │
                     └────────────┬────────────┘
                                  │ Task envelope
                                  │ (fields selected by complexity)
                                  ▼
              ┌──────────────────────────────────────┐
              │   internal/supervisor                │
              │   - planner     (SRS/PRD slice)      │
              │   - enforcer    (DoR/DoD gating)     │
              │   - reviewer    (receipt inspection) │
              │   - rca         (failure analysis)   │
              │   - escalator   (HITL routing)       │
              └────────────┬─────────────────────────┘
                           │ Task + Worktree + Role + Permission
                           ▼
              ┌──────────────────────────────────────┐
              │   internal/orchestrator (unchanged)  │
              │   FanOut → Worker.Spawn → Handle.Wait│
              │   git diff → Receipt.FilesModified    │
              └────────────┬─────────────────────────┘
                           │ Receipt
                           ▼
              ┌──────────────────────────────────────┐
              │   internal/harness (CODE-LEVEL GATE) │
              │   GetRole / GetPermission /          │
              │   ValidateRequest / BuildContract-   │
              │   PromptWithReceipt                  │
              │   internal/receipt (DB-LEVEL GATE)   │
              │   IssueReceipt / ValidateAndConsume  │
              │   write_receipts (SQLite WAL)        │
              └──────────────────────────────────────┘
```

The supervisor owns a new package `internal/supervisor`. It does not import AGY or worker code directly — it talks to the existing orchestrator via the public Worker / Receipt interface. This keeps `internal/orchestrator` unchanged and unblocks Concern B and C from evolving independently.

### 3.1 Why the gate is code, not prompt

The supervisor's discipline comes from three existing layers that **enforce** behavior at the runtime, not at the language model:

1. **`internal/harness`** (`harness.go`, `roles.go`, `permissions.go`): defines Role and Permission as typed Go values, not as text. `ValidateRequest(role, permission, allowedPaths, skipPermissions)` returns an error if any combination is invalid. `BuildContractPromptWithReceipt(prompt, role, permission, allowedPaths, receiptRef)` injects the contract as a structured block — the worker receives a **token**, not freeform instructions, and the supervisor validates the token later.
2. **`internal/receipt`** (`receipt.go`, `write_receipts` table): every write is gated by `IssueReceipt` and consumed by `ValidateAndConsume`. Receipts expire, get revoked, and get listed. A worker cannot write outside the receipt scope without the gate raising an error.
3. **`internal/orchestrator`** (`fanout.go`): scope violations are detected at the diff level (`diffScope(receipt.FilesModified, task.AllowedFiles)`) and recorded on the receipt. The worker cannot claim a file it did not touch and cannot hide a file it did touch.

**Trust but verify.** The supervisor trusts the worker only as far as the harness and receipt layer can prove. Anything not provable by code is escalated to HITL. There is no third option.

This is the central contrast to "skill dispatch / orchestrator" tools that rely on prompt injection: g8s's gates are written in Go, tested with `-race`, and persist to SQLite WAL. They survive a model update. They do not depend on the model cooperating.

---

## 4. Trigger Sources (Dual-Mode)

The supervisor accepts work from two sources:

### Mode 1: Manual — User-Initiated

- Operator invokes `g8s orchestrate "..."` with a problem statement.
- The planner slices the statement into a SRS-lite / DoR-lite envelope and routes it to the fix loop.
- Use case: ad-hoc investigation, incident response, on-call escalation.

### Mode 2: Auto-Pilot — Supervisor-Initiated

- A scheduler tick (every N minutes) triggers `g8s orchestrate --autopilot`.
- The autopilot loader scans:
  - GitHub issues with label `agy-fix` or `good-first-issue`.
  - Failing CI runs (`gh run list --status failure`).
  - Static analysis findings (golint, staticcheck, gitleaks) persisted under `internal/lints/`.
  - Stale TODOs in code marked `@agy-fix-me`.
- Each finding becomes a candidate task; the supervisor picks the highest-value one based on a priority queue (priority = severity × confidence × cost-inverse).

Both modes funnel into the same fix loop. There is **no special-case branching** for manual vs auto — only the trigger source differs.

---

## 5. Task Envelope (Supervisor-Selected Fields)

The supervisor does not enforce a rigid template. It picks fields based on task class:

| Field        | Triggered when                                          | Optional? |
| ------------ | ------------------------------------------------------- | --------- |
| `SRS`        | Multi-file or cross-package change                      | yes       |
| `PRD`        | Feature work touching public API or UX                  | yes       |
| `DoR`        | Always (minimum readiness gate)                         | no        |
| `DoD`        | Always (minimum done gate)                              | no        |
| `DnD`        | Cycle-level done gate (covers the attempt, not just the code) | yes  |
| `Validateds` | Behavior-level checks (test, smoke, e2e)                | always    |
| `FSM`        | Anything with non-trivial state (3+ states or branches) | yes       |

The supervisor's choice is itself **measurable**: an envelope is scored by `envelope_score = (fields_present × weight) − (fields_absent × cost)`. Concern C uses this score to learn which envelopes succeed.

### DoR / DoD Anchors

Existing `docs/DOD_DOR.md` is the canonical DoR / DoD. The supervisor **imports** those checks rather than redefining them. This doc only adds `DnD` (cycle-level done) and `Validateds` (behavior-level).

`DnD` definition (new, this document proposes):

> **Definition of Done for the Cycle**: The supervisor accepts a worker receipt as cycle-complete when (1) DoD checks pass, (2) `Validateds` (smoke/e2e checks specific to the cycle) all pass, and (3) the supervisor's reviewer module has no outstanding objections after the configured iteration cap.

`Validateds` are behavior-level checks the supervisor adds on top of DoD when the task class warrants them. For example, a CLI fix adds `--help` round-trip validation; an API change adds JSON schema round-trip validation.

---

## 6. Iteration Policy (Bounded but Persistent)

The supervisor enforces this state machine on the fix loop:

```
                    ┌─────────────────┐
                    │  attempt = 0    │
                    └────────┬────────┘
                             │ spawn worker
                             ▼
                ┌─────────────────────────┐
                │  attempt in same        │◄─────────┐
                │  approach (max 3)       │          │
                └────────┬────────────────┘          │
                         │                           │
                         ▼                           │  revise
                ┌─────────────────────────┐          │
                │  reviewer verdict       ├──────────┘
                │  pass / revise / fail   │
                └────────┬────────────────┘
                         │
                         │ 3 fails in same approach
                         ▼
                ┌─────────────────────────┐
                │  RCA + ADR synthesis    │
                │  approach shift?        │
                └────────┬────────────────┘
                         │
                ┌────────┴────────┐
                │ yes             │ no
                ▼                 ▼
        ┌──────────────┐    ┌─────────────────┐
        │ approach = +1│    │ HITL escalate   │
        │ attempt = 0  │    │ (terminal)      │
        └──────┬───────┘    └─────────────────┘
               │
               ▼
        ┌──────────────┐
        │ attempts at  │── 3 approaches exhausted ──► HITL escalate
        │ approach = 3 │
        └──────────────┘
```

### Bounds (initial values; Concern C may adjust)

- **Attempts per approach**: 3 (1 initial + 2 revises)
- **Approaches per task**: 3 (initial + 2 shifts)
- **Total attempts**: 9 (3 × 3)
- **Escalation**: after 9 attempts, the loop is terminal → HITL.

### Why these numbers

- 3 attempts per approach covers "tweak prompt", "tweak scope", "tweak role" without abandoning the approach.
- 3 approaches covers "refactor vs patch", "library swap", "rewrite from scratch".
- 9 total attempts is the floor — Concern C (meta-optimizer) may raise or lower this based on observed success rates.

### Why not bound to 1

A single attempt per approach turns every task into HITL. A single approach per task prevents learning from the supervisor's own RCA pipeline. 3×3 is the minimum that lets the supervisor demonstrate self-correction without burning compute.

---

## 7. RCA & ADR

### RCA (Root Cause Analysis)

Every approach shift produces an RCA record. Fields:

- `failed_attempt_ids`: list of receipts that failed in the prior approach
- `symptom`: distilled failure mode (test fail, scope violation, timeout, missing context, etc.)
- `root_cause`: hypothesis after reviewing all failed receipts + their diffs
- `evidence`: file paths, log excerpts, receipt IDs backing the hypothesis
- `confidence`: 0.0–1.0

The RCA is generated by the supervisor's `rca` module. It runs a structured prompt against the failed receipts. If `confidence < 0.6`, the supervisor pauses and asks HITL before shifting approach (this is a safety valve, not the normal path).

### ADR (Architecture Decision Record)

Each approach shift also produces an ADR. Fields follow the standard ADR template:

- **Status**: Proposed / Accepted / Superseded
- **Context**: task envelope + RCA digest
- **Decision**: what the new approach does
- **Consequences**: trade-offs, risks, mitigations
- **Supersedes**: ADR ID of the prior approach (if any)

ADRs are persisted under `docs/decisions/` (filename: `NNN-slug.md`, zero-padded, slug derived from the decision title). They are version-controlled alongside code.

---

## 8. State Persistence

The supervisor persists per-task state in `internal/controlplane` (SQLite WAL) by introducing two new tables:

```sql
CREATE TABLE supervisor_tasks (
  id              TEXT PRIMARY KEY,
  envelope_json   BLOB NOT NULL,        -- selected fields + values
  approach_idx    INT NOT NULL DEFAULT 0,
  attempt_idx     INT NOT NULL DEFAULT 0,
  state           TEXT NOT NULL,        -- queued, running, reviewing, rca, shifted, escalated, succeeded, failed
  parent_task_id  TEXT,                 -- for sub-tasks spawned by RCA
  created_at      DATETIME NOT NULL,
  updated_at      DATETIME NOT NULL
);

CREATE TABLE supervisor_decisions (
  id              TEXT PRIMARY KEY,
  task_id         TEXT NOT NULL REFERENCES supervisor_tasks(id),
  kind            TEXT NOT NULL,        -- 'rca' | 'adr' | 'review_verdict' | 'escalation'
  payload_json    BLOB NOT NULL,
  created_at      DATETIME NOT NULL
);
```

Concern B will extend these tables with receipt-related fields. This doc only introduces the supervisor-side skeleton.

---

## 9. HITL Escalation Contract

When the loop escalates, the supervisor emits a JSON digest on stdout and exits non-zero. Schema (informal):

```json
{
  "task_id": "sup-2026-08-28-001",
  "trigger": "auto-pilot",
  "envelope_summary": "...",
  "approaches_tried": 3,
  "total_attempts": 9,
  "failed_receipt_ids": ["rcp-...", "rcp-..."],
  "rca_summary": "...",
  "adr_chain": ["docs/decisions/001-...", "docs/decisions/002-...", "docs/decisions/003-..."],
  "last_diff_summary": "...",
  "recommended_human_action": "review ADR chain + decide next step"
}
```

The operator is expected to read the digest, inspect the ADR chain, and either (a) inject a new approach hint via CLI flag, (b) accept the partial result, or (c) cancel.

---

## 10. Metrics & Measurability

Each fix-loop run emits the following metrics (also persisted in `supervisor_tasks` and `supervisor_decisions`):

| Metric                     | Purpose                                          |
| -------------------------- | ------------------------------------------------ |
| `envelope_score`           | Did the chosen envelope match the task class?    |
| `first_attempt_success`    | Did the worker succeed on attempt 1? (Y/N)       |
| `attempts_to_success`      | How many attempts until pass?                    |
| `approaches_to_success`    | How many approaches until pass?                  |
| `rca_confidence_avg`       | Average RCA confidence per shift                 |
| `cycle_duration_seconds`   | Wall-clock for the whole cycle                   |
| `escalation_count`         | Times the loop hit HITL                          |
| `false_escalation_rate`    | (Concern C) fraction of HITL where user said "should have worked" |

Concern C ingests these metrics to retune envelope selection, iteration caps, and RCA confidence thresholds.

---

## 11. Open Questions

- **Q1**. Should the supervisor run in-process with `g8s orchestrate` or as a long-lived daemon? Proposal: in-process for now (matches current CLI topology). Daemon mode is a later iteration.
- **Q2**. How does the supervisor share state with concurrent runs? Proposal: SQLite WAL handles concurrency; multiple `g8s orchestrate` invocations can run in parallel safely.
- **Q3**. Does RCA need an oracle subagent, or is structured prompt + receipts enough? Proposal: start with structured prompt; promote to oracle if RCA quality is poor. Measurable via `rca_confidence_avg`.
- **Q4**. What is the priority queue for auto-pilot? Proposal: severity × confidence × cost-inverse, weights tunable via config.

---

## 12. Document Control

- **v0.1.0-draft** (2026-08-28): Initial proposal. Awaiting stakeholder review.

## Changelog

- v1.0.0-ACCEPTED (2026-08-28): Promoted from v0.1.0-draft. Implementation tracked under T020 of g8s-orchestration-roadmap goal. No behavioral changes; status reflects owner ratification of the architecture.
- v1.1.0-ACCEPTED (2026-09-12): Added Concern B (Receipt Evolution) specification (§ Concern B). Receipt schema extensions (SupervisorMeta, provenance-and-replay context), idempotent migration, and nil-safe IssueReceipt signature are already implemented in `internal/receipt` (SchemaVersion 3). Promoted from DRAFT → ACCEPTED as part of T021.
- v1.2.0-ACCEPTED (2026-09-12): Added Concern C (Meta-Optimizer Read-Only Ingestion) specification (§ Concern C). Read-only aggregate API (8 metrics), streaming API (StreamMetrics), and flag collision guard (--task-id + --aggregate/--json-stream) are already implemented in `internal/supervisor/optimizer.go` and `cmd/g8s/supervisor_metrics.go`. Test coverage in `optimizer_test.go` (12 tests). Promoted from DRAFT → ACCEPTED as part of T022.
