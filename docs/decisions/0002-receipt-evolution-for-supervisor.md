# ADR-0002: Receipt evolution for supervisor provenance and backward compatibility

> **Status**: Accepted
> **Date**: 2026-08-29
> **Deciders**: tamld (owner), g8s supervisor (advisor)
> **SSoT**: `spec/openspec/11-orchestration-roadmap-spec.md` §ADDED B.1–B.3, `docs/designs/supervisor-fix-loop.md`
> **Implements**: DELTA-11 Concern B (receipt evolution)
> **References**: ADR-0001 (supervisor-driven fix loop)
> **Supersedes**: none

## Context

g8s uses zero-trust, single-use write receipts (`internal/receipt`) backed by a pure-Go SQLite WAL database (`modernc.org/sqlite`) to gate workspace mutations. Under DELTA-11 (Orchestration Roadmap), the supervisor fix loop (Concern A) drives multi-attempt, multi-approach task execution.

To support Concern C (meta-optimizer aggregate scoring), DELTA-22 (playbook learning), and structured auditability across approach shifts, the supervisor needs to record provenance metadata on every write receipt:
1. `approach_idx`: which high-level strategy was active (0..2).
2. `attempt_idx`: retry attempt within the active approach (0..2).
3. `rca_confidence`: Root Cause Analysis diagnostic confidence score (0.0..1.0) preceding this attempt.
4. `adr_path`: path to the architecture decision record documenting the approach shift rationale.

Crucially, receipts issued under prior versions (v0.2.0 production) already reside on disk. The system MUST maintain strict backward compatibility:
- Pre-migration receipts must continue to verify without error.
- Existing programmatic callers of `IssueReceipt` must not break.
- Migration must be idempotent and resilient across concurrent process invocations.

## Decision

Extend `internal/receipt` with additive supervisor provenance fields and an idempotent schema migration mechanism:

1. **`SupervisorMeta` struct**:
   Define an exported `SupervisorMeta` struct with exported fields:
   ```go
   type SupervisorMeta struct {
       ApproachIdx   int     `json:"approach_idx"`
       AttemptIdx    int     `json:"attempt_idx"`
       RCAConfidence float64 `json:"rca_confidence"`
       ADRPath       string  `json:"adr_path"`
   }
   ```
   Embed `SupervisorMeta *SupervisorMeta` into `WriteReceipt` with `json:"supervisor_meta,omitempty"`.

2. **Functional Options for `IssueReceipt`**:
   Extend `IssueReceipt` to accept variadic functional options (`opts ...IssueOption`), providing `WithSupervisorMeta(*SupervisorMeta)` while preserving the existing 3-argument signature for existing callers.

3. **Schema Evolution & Version Gate**:
   - Bump schema version to `PRAGMA user_version = 2`.
   - Add four nullable columns to `write_receipts`: `approach_idx INTEGER`, `attempt_idx INTEGER`, `rca_confidence REAL`, `adr_path TEXT`.
   - Implement `migrateSupervisorSchema` using a `PRAGMA table_info` scan and `ALTER TABLE write_receipts ADD COLUMN` for missing columns.

4. **NULL Tolerance & Verification Contract**:
   - New columns have no `DEFAULT` values (old receipts truly have no supervisor metadata).
   - `VerifyReceipt(receiptID string) (*WriteReceipt, error)` and read paths (`ValidateAndConsume`, `ListActiveReceipts`) scan nullable columns using `sql.Null*` and yield `SupervisorMeta == nil` when columns are NULL.
   - Verification succeeds for both v1 and v2 receipts without false rejections.

## Rationale

- **Code is the Truth**: Storing provenance directly in SQLite WAL receipts ties proof-of-work to the exact supervisor decision context that authorized it.
- **Zero Breaking Changes**: Functional options allow existing callers (`cmd/g8s`, `internal/mcp`) to remain unchanged while enabling supervisor-tier callers to attach metadata.
- **Semantic Integrity**: Nullable columns without synthetic defaults (such as `0`) ensure that pre-migration receipts truthfully reflect absence of supervisor metadata (`nil`), distinguishing them from approach 0, attempt 0.
- **Idempotency & Concurrency**: Scanning `PRAGMA table_info` before executing `ALTER TABLE` prevents "duplicate column name" errors when multiple processes open the shared WAL database.

## Consequences

### Positive
- Concern C can run aggregate SQL queries across `approach_idx`, `attempt_idx`, and `rca_confidence`.
- Full lineage audit trail from receipt back to ADR and RCA.
- Complete backward compatibility with existing databases and receipts on disk.
- Zero CGO dependencies preserved.

### Negative / Neutral
- Slightly increased scan complexity across `sql.NullInt64`, `sql.NullFloat64`, and `sql.NullString`.
- Additional table metadata scan on initial database open (mitigated by fast-path `user_version` check).

## Alternatives Considered

### 1. Store supervisor metadata in a separate table
- *Pros*: Keeps `write_receipts` table schema untouched.
- *Cons*: Requires cross-table joins during receipt validation and verification; risks orphaned rows or inconsistent transactions.
- *Verdict*: Rejected in favor of a single unified receipt entity.

### 2. Store supervisor metadata as a JSON column (`supervisor_meta_json TEXT`)
- *Pros*: Flexible schema for future fields.
- *Cons*: Cannot be directly indexed or aggregated efficiently in SQLite without json extraction functions; weaker schema typing in SQL.
- *Verdict*: Rejected in favor of explicit typed columns.

### 3. Add default values (`DEFAULT 0`, `DEFAULT ''`) to new columns
- *Pros*: Simpler scanning without `sql.Null*` types.
- *Cons*: Semantically corrupts old receipts; approach 0 and attempt 0 are valid indices, so a default 0 falsely asserts that an old receipt was issued under approach 0 attempt 0.
- *Verdict*: Rejected; NULL is the only truthful representation of missing metadata.

### 4. Breaking `IssueReceipt` parameter signature
- *Pros*: Forces all callers to explicitly pass metadata.
- *Cons*: Breaks existing CLI commands, tests, and MCP tools.
- *Verdict*: Rejected in favor of variadic functional options `WithSupervisorMeta`.
