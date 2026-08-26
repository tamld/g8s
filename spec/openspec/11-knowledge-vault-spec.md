# OpenSpec Delta 11: Decoupled Pure-Go Knowledge Vault with SQLite FTS5 & BM25

* **Specification ID**: `DELTA-11`
* **Title**: Decoupled Pure-Go Knowledge Vault with SQLite FTS5 & BM25 Ranking
* **Status**: `APPLIED`
* **Milestone**: `v0.3.0`
* **Target Package**: `internal/vault`, `cmd/g8s`
* **Foundational Axiom**: Axiom 1 (Zero-CGO & Pure-Go SQLite), Axiom 5 (Decoupled Memory & Cognitive Architecture)

---

## 1. Context & Motivation

Multi-agent autonomous systems suffer severe attention degradation when long execution traces and lessons are kept in active LLM context. `g8s` addresses this by decoupling cognitive memory from execution.

`DELTA-11` introduces the **Pure-Go Decoupled Knowledge Vault** (`internal/vault`), providing persistent storage, Tri-Anchor schema validation, and sub-millisecond BM25 full-text search indexing over distilled knowledge artifacts using SQLite FTS5.

---

## 2. Structural Requirements

### 2.1 The Tri-Anchor Schema

Every distilled record MUST adhere to the 3-anchor structure:
1. **Causality & Intent Anchor** (`causality`): `problem`, `trade_off`, `root_cause`.
2. **Spatial & Code Coordinates Anchor** (`spatial_coordinates`): `package`, `file`, `symbol`, `denied_fragments`.
3. **Forensic & Verification Anchor** (`forensic_verification`): `test_file`, `test_case`, `exit_criteria`, `receipt_hash`.

### 2.2 Storage Engine & FTS5 Synchronization

* Database: SQLite WAL mode with `_txlock=immediate` and `busy_timeout=5000`.
* Physical table: `vault_records`
* Virtual index: `vault_fts USING fts5(...)` with automatic triggers on `INSERT`, `UPDATE`, and `DELETE`.
* Ranking: SQLite FTS5 `ORDER BY rank` with `snippet()` extraction.

---

## 3. CLI Command Surface

* `g8s vault store --id <id> --title <title> --problem <p> --trade-off <t> [options]`
* `g8s vault query <search-term> [--limit 10]`
* `g8s vault get <record-id>`
* `g8s vault list [--milestone <m>] [--status <s>] [--package <pkg>] [--limit 50]`
* `g8s vault delete <record-id>`

---

## 4. Verification & Testing

* Unit tests in `internal/vault/vault_test.go` asserting CRUD operations, FTS5 BM25 search ranking, trigger updates, and concurrency.
* Dual-pass verification: `CGO_ENABLED=0 go test ./...` and `CGO_ENABLED=1 go test -race ./...`.
