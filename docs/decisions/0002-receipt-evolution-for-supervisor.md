# ADR-0002: Receipt evolution for supervisor

> **Status**: Proposed (2026-09-12)
> **Date**: 2026-09-12
> **Deciders**: tamld (owner), g8s supervisor (advisor)
> **SSoT**: `docs/designs/supervisor-fix-loop.md` § Concern B
> **Implements**: DELTA-11 orchestration-roadmap §ADDED B.1-B.3
> **Supersedes**: none
> **Depends on**: ADR-0001 (Accepted 2026-08-28)

## Context

ADR-0001 established the code-level supervisor-driven fix loop. The supervisor needs receipt metadata to make decisions:

- **Approach shift**: supervisor must know which approach/attempt a receipt belongs to.
- **RCA confidence**: supervisor must record the confidence that drove an approach shift.
- **ADR linkage**: each approach shift writes an ADR; the receipt must link to it.

The existing `internal/receipt` package (v1 schema) had no supervisor-tier fields. Every supervisor run would create receipts without approach/attempt lineage, making RCA and ADR linkage impossible.

Two paths were considered:

1. **Separate supervisor receipt table** — create a new `supervisor_receipts` table with FK to `write_receipts`. Clean separation but adds joins, complexity, and a new table to maintain.
2. **Extend existing `write_receipts` table** — add nullable columns (`approach_idx`, `attempt_idx`, `rca_confidence`, `adr_path`) directly. Legacy receipts remain readable (NULL = absent). Single source of truth, simpler queries.

## Decision

Adopt **extending the existing `write_receipts` table** with four additive nullable columns:

```sql
ALTER TABLE write_receipts ADD COLUMN approach_idx INTEGER;
ALTER TABLE write_receipts ADD COLUMN attempt_idx INTEGER;
ALTER TABLE write_receipts ADD COLUMN rca_confidence REAL;
ALTER TABLE write_receipts ADD COLUMN adr_path TEXT;
```

Plus provenance-and-replay context (schema version 3):

```sql
ALTER TABLE write_receipts ADD COLUMN envelope_schema_uri TEXT;
ALTER TABLE write_receipts ADD COLUMN envelope_field_order_json TEXT;
ALTER TABLE write_receipts ADD COLUMN envelope_required_fields_json TEXT;
ALTER TABLE write_receipts ADD COLUMN ruleset_version TEXT;
ALTER TABLE write_receipts ADD COLUMN pipeline_digest TEXT;
ALTER TABLE write_receipts ADD COLUMN adr_ref TEXT;
ALTER TABLE write_receipts ADD COLUMN issued_by TEXT;
ALTER TABLE write_receipts ADD COLUMN tool_version TEXT;
ALTER TABLE write_receipts ADD COLUMN trace_id TEXT;
ALTER TABLE write_receipts ADD COLUMN actor_chain_json TEXT;
ALTER TABLE write_receipts ADD COLUMN source_commit TEXT;
```

The migration SHALL be idempotent: inspect columns via `PRAGMA table_info` and only `ADD COLUMN` missing ones. Run inside the exclusive transaction in `Manager.initialize()`.

The Go API SHALL remain backward-compatible: `IssueReceipt` accepts optional `SupervisorMeta` via `WithSupervisorMeta` option. Legacy callers that omit it succeed with `SupervisorMeta == nil`. `VerifyReceipt`, `ValidateAndConsume`, `ListActiveReceipts` treat NULL columns as absent (return `SupervisorMeta == nil`).

Schema version bumped to 3. Unsupported versions rejected at open.

## Rationale

1. **Single source of truth** — receipt is the authoritative signal for the supervisor. Adding columns keeps the receipt self-contained; no FK joins, no separate table.
2. **Backward compatibility mandatory** — receipts issued before migration (v1 schema) MUST still verify. Nullable columns + idempotent migration achieve this without breaking callers.
3. **Idempotent migration** — `PRAGMA table_info` scan + `ALTER TABLE ADD COLUMN` is the same pattern used in `internal/controlplane` for `parent_task_id` migration. Safe to re-run.
4. **Nil-safe API** — `WithSupervisorMeta` option keeps `IssueReceipt` signature unchanged for legacy callers. The option pattern is already established in the package (WithCanonicalEnvelope, WithRuleGraph, WithProvenanceContext).
5. **Provenance for audit** — schema v3 adds canonical envelope, rule graph snapshot, and provenance lineage so receipts remain replayable after registry evolution. Auto-populated by `IssueReceipt`.

## Consequences

Positive:

- Supervisor can correlate receipts to approach/attempt, record RCA confidence, link ADRs.
- Legacy receipts (v1 schema) still verify and consume — zero breaking changes.
- Migration is idempotent; safe for CI/CD and repeated `NewReceiptManager` calls.
- Provenance fields enable forensic audit without external systems.

Negative:

- `write_receipts` table grows wider (15+ columns). Still acceptable for SQLite WAL.
- Migration logic adds ~100 lines to `receipt.go` (two migration functions).

Neutral:

- `internal/orchestrator.Worker` interface unchanged.
- `internal/harness` contracts unchanged.
- CLI surface unchanged.

## Alternatives considered

### A. Separate supervisor_receipts table

Pros: clean separation, narrower `write_receipts`.
Cons: requires JOIN for every supervisor query, new table to maintain, FK complexity, two sources of truth. Rejected because the supervisor needs receipt metadata on every read path (VerifyReceipt, ValidateAndConsume, ListActiveReceipts).

### B. JSON blob column

Pros: single column, flexible.
Cons: no column-level NULL semantics, harder to query/filter in SQL, loses type safety. Rejected because we need typed NULL handling (NULL = absent vs zero = meaningful).

### C. Breaking API change

Pros: cleaner signature.
Cons: breaks all existing callers. Rejected because backward compatibility is a hard requirement (T021 constraints).

## Follow-ups

- Concern C (meta-optimizer) will read `rca_confidence` and `approach_idx` from receipts for aggregate metrics.
- ADR-0003 will reference this decision for metrics ingestion.

## Change log

- **v1** (2026-09-12): Initial proposal. Status: Proposed. Will be Accepted on T021 completion.