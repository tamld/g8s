# OpenSpec DELTA-02: Receipt-Based Write Delegation

**Status**: `APPLIED`  
**Milestone**: M1 (Foundation)  
**Package**: `internal/receipt`  

---

## 1. Goal & Context
Implement the capability delegation gate that allows Brain orchestrators to issue time-limited ($\le 3600s$), path-scoped (`allowed_paths` glob) write receipts to workers. Receipts are stored in SQLite and consumed atomically upon single-use validation.

## 2. Interface Definition (Concern A — Authorization)

```go
package receipt

import "time"

type WriteReceipt struct {
    ReceiptID      string    `json:"receipt_id"`
    Issuer         string    `json:"issuer"`
    AllowedPaths   []string  `json:"allowed_paths"`
    ExpiresAt      time.Time `json:"expires_at"`
    Consumed       bool      `json:"consumed"`
    ConsumerTaskID *string   `json:"consumer_task_id,omitempty"`
    CreatedAt      time.Time `json:"created_at"`
}

type ReceiptManager interface {
    IssueReceipt(issuer string, allowedPaths []string, ttl time.Duration) (*WriteReceipt, error)
    ValidateAndConsume(receiptID string, consumerTaskID string) (*WriteReceipt, error)
    RevokeReceipt(receiptID string) (bool, error)
    ListActiveReceipts() ([]*WriteReceipt, error)
}
```

## 3. Security Rules
1. `allowedPaths` must not be empty.
2. `ttl` must satisfy $1s \le \text{ttl} \le 3600s$.
3. Receipts must be consumed atomically inside an `EXCLUSIVE` SQLite transaction.
4. Attempting to consume an expired, already consumed, or revoked receipt returns a typed validation error.

---

## 4. Concern B — Provenance & Replay Context (v0.9.0)

### 4.1 Motivation
A receipt issued today may be audited 6+ months later. Concern A captures *who is allowed to do what*, but not *under which conditions*. After the rule registry evolves and tool versions change, an old receipt must remain:
- **Decodeable** — wire format stable across producer versions.
- **Replayable** — the ruleset and pipeline used at issue time must be pinnable.
- **Traceable** — actor chain, trace ID, source commit, tool version pinned at issue.

### 4.2 Additive Fields

```go
type CanonicalEnvelope struct {
    SchemaURI      string   `json:"schema_uri"`       // e.g. "g8s://envelope/write-receipt/v1"
    FieldOrder     []string `json:"field_order"`      // ordered field names
    RequiredFields []string `json:"required_fields"`  // subset of FieldOrder that MUST be present
}

type RuleGraphSnapshot struct {
    RulesetVersion string `json:"ruleset_version"`  // e.g. "v1.2.3"
    PipelineDigest string `json:"pipeline_digest"`  // SHA256 of DAG config
    ADRRef         string `json:"adr_ref,omitempty"` // optional ADR path
}

type ProvenanceLineage struct {
    IssuedBy     string   `json:"issued_by"`        // "supervisor-7"
    ToolVersion  string   `json:"tool_version"`     // "g8s/v0.9.0"
    TraceID      string   `json:"trace_id"`         // W3C TraceContext
    ActorChain   []string `json:"actor_chain"`      // ["brain:root","supervisor:1","worker:42"]
    SourceCommit string   `json:"source_commit"`    // git HEAD at issue
}

type WriteReceipt struct {
    // ... existing Concern A fields ...
    SupervisorMeta   *SupervisorMeta   `json:"supervisor_meta,omitempty"`     // v0.8.0
    CanonicalEnvelope *CanonicalEnvelope `json:"canonical_envelope,omitempty"` // v0.9.0
    RuleGraphSnapshot  *RuleGraphSnapshot `json:"rule_graph_snapshot,omitempty"` // v0.9.0
    ProvenanceLineage  *ProvenanceLineage `json:"provenance_lineage,omitempty"` // v0.9.0
}
```

### 4.3 Strictness Model

| Field | Caller provides | System provides | Strictness |
|---|---|---|---|
| `CanonicalEnvelope` | `WithCanonicalEnvelope(env)` | — | **Mandatory**: `IssueReceipt` returns `ErrEnvelopeRequired` if omitted AND caller is a Brain-tier caller (MCP server, supervisor). Field validation: `SchemaURI` must start with `g8s://`; `FieldOrder` non-empty. |
| `RuleGraphSnapshot` | `WithRuleGraph(rs)` | — | **Mandatory**: same as above. Caller (CLI/MCP server) builds the snapshot from its own pipeline metadata; Brain-tier callers must not omit it. |
| `ProvenanceLineage` | — | Auto-populated in `IssueReceipt` from runtime context (caller identity, build ldflags, OTel span, git HEAD) | **Auto-fill**: zero friction for Brain. Brain should not write `"unknown"` because system knows better. |

### 4.4 Primary Key: UUID v4 (retained)

- `ReceiptID` remains `uuid.NewString()` (RFC 4122 v4). UUID v7 was considered during Concern B design but deferred: the B3 pre-consume decode reorder + B4 NULL/empty handling provided sufficient correctness, and v7's time-ordering was not load-bearing for any current consumer.
- Chronological ordering remains available via `ORDER BY created_at` (indexed in `write_receipts`).
- Library: `github.com/google/uuid v1.6+` (already in `go.mod`).

### 4.5 Schema Migration: v2 → v3

`SchemaVersion` const bumps from `2` to `3`. Idempotent `migrateSupervisorSchema`-style helper `migrateConcernBSchema` adds the following nullable columns via `ALTER TABLE ADD COLUMN`:

```sql
envelope_schema_uri          TEXT
envelope_field_order_json    TEXT
envelope_required_fields_json TEXT
ruleset_version              TEXT
pipeline_digest              TEXT
adr_ref                      TEXT
issued_by                    TEXT
tool_version                 TEXT
trace_id                     TEXT
actor_chain_json             TEXT
source_commit                TEXT
```

Legacy receipts (v0.8.0) remain readable; all new columns are NULL-tolerant.

### 4.6 New Method: PurgeExpired

```go
// PurgeExpired deletes consumed-or-expired receipts older than maxAge,
// bounded by maxRows per call to avoid long DB locks under load.
// Returns the number of rows actually deleted.
PurgeExpired(maxAge time.Duration, maxRows int) (int64, error)
```

Caller responsibility: schedule the purge (cron, ticker, supervisor loop). The method is idempotent and safe to call concurrently with `IssueReceipt` / `ValidateAndConsume` because both write paths acquire `m.mu`.

### 4.7 Out of Scope
- Multi-platform Ed25519 signing (Phase 2)
- Sharded DB by issuer/time (Phase 3)
- Event-sourcing append-only log (Phase 3)
