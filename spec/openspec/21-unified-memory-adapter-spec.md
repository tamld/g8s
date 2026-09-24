# OpenSpec Delta 21: Unified Decoupled Memory Layer & Hybrid Retrieval Adapter

* **Specification ID**: `DELTA-21`
* **Title**: Unified Decoupled Memory Layer & Hybrid Retrieval Adapter
* **Status**: `ACCEPTED`
* **Milestone**: `M5` (Robustness & Evals)
* **Target Package**: `internal/memory`
* **Foundational Axiom**: Axiom 1 (Zero-CGO Pure-Go SQLite), Axiom 5 (Decoupled Memory & Cognitive Architecture)
* **Architecture Decision**: [`docs/decisions/0019-unified-memory-facade.md`](../docs/decisions/0019-unified-memory-facade.md)

---

## 1. Context & Motivation

In accordance with the `g8s` Decoupled Memory Architecture (`docs/DECOUPLED_MEMORY_ARCHITECTURE.md`), the LLM operates as a CPU, while `g8s` acts as the Memory Management Unit (MMU). Context windows remain compact (<4,000 characters), with historical, structural, and semantic knowledge externalized to persistent, POSIX 0600 storage.

Prior to `DELTA-21`, storage was fragmented across `internal/controlplane` (Episodic), `internal/vault` (Semantic), and `internal/receipt` (Capability), while Working Memory was managed ad-hoc. `DELTA-21` formalizes a unified Facade Adapter (`internal/memory.MemoryAdapter`) and introduces a Pure-Go Hybrid Retrieval Engine combining BM25 keyword matching, causal DAG lineage traversal, and pure-Go IEEE 754 vector cosine similarity.

---

## 2. Structural Requirements

### 2.1 The 4 Cognitive Memory Taxonomies

1. **Working Memory**: Immediate step context, bounded by `MaxPromptChars`. Stored in `working_contexts` table with POSIX 0600 security, transitioning to `COMPACTED`/`PURGED` with SHA-256 prompt redaction on completion.
2. **Episodic Memory**: Causal lineage DAGs and append-only event logs, backed by `internal/controlplane`.
3. **Semantic Memory**: Tri-anchor distillation records indexed via SQLite FTS5 BM25, backed by `internal/vault`.
4. **Capability Memory**: Single-use, path-scoped write receipts, backed by `internal/receipt`.

### 2.2 MemoryAdapter Interface

```go
type MemoryAdapter interface {
    // Working Memory
    StoreWorkingContext(ctx context.Context, taskID string, wCtx *WorkingContext) error
    LoadWorkingContext(ctx context.Context, taskID string) (*WorkingContext, error)
    PurgeWorkingContext(ctx context.Context, taskID string) error

    // Episodic Memory
    RecordEpisode(ctx context.Context, taskID string, event *EpisodicEvent) error
    GetLineageTree(ctx context.Context, taskID string) (*LineageDAG, error)

    // Semantic Memory
    StoreKnowledge(ctx context.Context, record *vault.DistillationRecord) error
    SearchKnowledge(ctx context.Context, query string, limit int) ([]vault.SearchResult, error)

    // Capability Memory
    ValidateCapability(ctx context.Context, receiptID string, consumerTaskID string) (*receipt.WriteReceipt, error)

    // Vector Similarity Engine
    StoreVector(ctx context.Context, recordID string, vector []float32, metadata map[string]any) error
    SearchVector(ctx context.Context, queryVector []float32, limit int, minScore float64) ([]VectorMatch, error)

    // Hybrid Retrieval
    HybridRetrieve(ctx context.Context, req HybridQuery) (*HybridResult, error)

    // Lifecycle
    Close() error
}
```

### 2.3 SQLite Schemas

The dedicated working memory and vector database (`memory.db`) operates in SQLite WAL mode:

```sql
CREATE TABLE IF NOT EXISTS working_contexts (
    task_id TEXT PRIMARY KEY,
    prompt TEXT NOT NULL,
    role TEXT NOT NULL,
    allowed_paths TEXT NOT NULL, -- JSON array of string globs
    status TEXT NOT NULL, -- ACTIVE, COMPACTED, PURGED
    redacted_digest TEXT, -- sha256:<hex>
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS vector_embeddings (
    record_id TEXT PRIMARY KEY,
    vector_blob BLOB NOT NULL, -- IEEE 754 Little-Endian float32 slice
    dimensions INTEGER NOT NULL,
    metadata TEXT, -- JSON object
    created_at TIMESTAMP NOT NULL
);
```

### 2.4 Pure-Go Vector Cosine Similarity Engine

Vector cosine similarity is calculated without CGO:
$$\text{sim}(\mathbf{u}, \mathbf{v}) = \frac{\mathbf{u} \cdot \mathbf{v}}{\|\mathbf{u}\| \|\mathbf{v}\|} = \frac{\sum_{i=1}^d u_i v_i}{\sqrt{\sum_{i=1}^d u_i^2} \sqrt{\sum_{i=1}^d v_i^2}}$$

- Validated for mathematical edge cases: identical vectors ($1.0$), orthogonal vectors ($0.0$), opposing vectors ($-1.0$), and zero-magnitude vectors ($0.0$).
- Serialized as IEEE 754 Little-Endian byte slices via `EncodeVectorBlob` / `DecodeVectorBlob`.

### 2.5 Hybrid Retrieval Engine

`HybridRetrieve` executes combined lookups:
- BM25 full-text query over distillation records (`vault.Query`).
- Causal lineage DAG retrieval (`controlplane.GetTaskLineage` + child resolution).
- Vector cosine similarity ranking over dense vector embeddings.
- Bounded execution latency: $\le 2\text{ms}$ with warm SQLite cache.

---

## 3. Zero-Leak Prompt Redaction & Security

- Hot working memory rows must be redacted upon task termination (`PurgeWorkingContext`): the raw prompt string is replaced with `sha256:<digest>`.
- File creation uses POSIX `0600` permissions (`-rw-------`).
- 100% Pure-Go (`modernc.org/sqlite`), zero CGO.

---

## 4. Verification & Testing

1. Concurrency: Multi-goroutine read/write tests confirming zero `SQLITE_BUSY` errors under WAL mode.
2. Vector math precision: Comprehensive table-driven tests in `internal/memory/vector_test.go`.
3. Invariant gate: `CGO_ENABLED=0 go test -race ./internal/memory/...` passing with test coverage $\ge 90\%$.
4. Benchmarks: `BenchmarkHybridRetrieve` and `BenchmarkCosineSimilarity` verifying sub-millisecond execution.
