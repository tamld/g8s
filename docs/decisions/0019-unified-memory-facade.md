# ADR-0019: Unified Decoupled Memory Facade Adapter over Decentralized Storage Packages

> **Status**: Accepted (2026-09-24, ratified for Issue #257 / DELTA-21)  
> **Date**: 2026-09-24  
> **Deciders**: tamld (owner), g8s supervisor (advisor), antigravity (implementer)  
> **SSoT**: `docs/DECOUPLED_MEMORY_ARCHITECTURE.md`  
> **Implements**: DELTA-21 unified-memory-adapter-spec  
> **Supersedes**: none  

---

## Context

The `g8s` Decoupled Memory Architecture (`docs/DECOUPLED_MEMORY_ARCHITECTURE.md`) formalizes four cognitive memory taxonomies:
1. **Working Memory**: Ephemeral reasoning scratchpad and prompt context for the immediate execution step.
2. **Episodic Memory**: History of execution runs, retries, task events, and causal ancestor/child lineages.
3. **Semantic Memory**: Persistent codebase knowledge, rules, architectural invariants, and distillation records.
4. **Capability Memory**: Cryptographic single-use, path-scoped write receipts and permission delegations.

However, physical implementation across the repository has historically been dispersed:
- `internal/controlplane` manages Episodic Memory (tasks, task events, recursive CTE lineage graphs).
- `internal/vault` manages Semantic Memory (SQLite FTS5 + BM25 ranking over tri-anchor distillation records).
- `internal/receipt` manages Capability Memory (single-use write receipts).
- Working Memory was managed ad-hoc as raw strings in CLI flags or in-flight process envelopes.

This physical dispersion forces supervisor orchestrators and CLI commands to bind tightly to three separate storage mechanisms. Furthermore, semantic retrieval was limited strictly to lexical BM25 matching, lacking pure-Go vector similarity capabilities for semantic concept matching without CGO.

## Decision

We establish `internal/memory` as a **Pure-Go Facade Adapter** (`MemoryAdapter`) providing a unified abstraction across all four cognitive taxonomies:

1. **Unified Interface (`MemoryAdapter`)**:
   - **Working Memory**: `StoreWorkingContext`, `LoadWorkingContext`, `PurgeWorkingContext` (enforcing SHA-256 prompt redaction upon terminal states).
   - **Episodic Memory**: `RecordEpisode`, `GetLineageTree` (wrapping `internal/controlplane.Store`).
   - **Semantic Memory**: `StoreKnowledge`, `SearchKnowledge` (wrapping `internal/vault.Vault`).
   - **Capability Memory**: `ValidateCapability` (wrapping `internal/receipt.ReceiptManager`).
   - **Hybrid Retrieval**: `HybridRetrieve` (combining BM25 text search, causal lineage DAG extraction, and pure-Go vector similarity).

2. **Pure-Go Vector Similarity Engine**:
   - Vector embeddings are serialized as IEEE 754 Little-Endian `[]byte` BLOBs.
   - Cosine similarity is computed using pure Go scalar math with zero external C dependencies, compliant with the Zero-CGO Constitution (`modernc.org/sqlite`).

3. **Storage Partitioning & Security**:
   - Working memory and vector embeddings reside in a dedicated POSIX `0600` SQLite WAL database (`working_contexts`, `vector_embeddings`).
   - Maintains strict prompt redaction: once a task finishes, hot prompt text is purged and replaced with its `sha256:<hex>` digest, preventing database bloat and memory leaks.

## Rationale

1. **Decoupling Orchestration from Storage Internals**: Higher layers (`internal/supervisor`, `cmd/g8s`) program against `memory.MemoryAdapter` rather than coupling directly to three separate SQLite schemas.
2. **Zero-CGO & Portability**: Computing vector cosine similarity in pure Go maintains instant compilation and zero CGO cross-compilation friction across macOS, Linux, and Windows runners.
3. **Deterministic Latency**: Wrapping local SQLite WAL connections with query latency $\le 2\text{ms}$ satisfies the strict real-time constraints of agent execution loops.

## Consequences

### Positive
- Unified query interface eliminates boilerplate across supervisor routines.
- Hybrid retrieval allows retrieving semantic lessons and causal lineage DAGs in a single call.
- Mocking memory subsystems in unit and integration tests becomes straightforward via `MemoryAdapter`.
- Zero-CGO invariant and POSIX `0600` security guarantees are strictly preserved.

### Trade-offs & Mitigations
- *Trade-off*: An additional adapter layer introduces small function call indirection.
- *Mitigation*: The adapter holds direct references to existing stores; zero-copy and byte buffer reuse ensure benchmarks show $\le 2\text{ms}$ latency without regression.
