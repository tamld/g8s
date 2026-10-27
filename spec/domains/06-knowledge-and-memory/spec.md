# Living Domain Spec 06 — Decoupled Knowledge Vault & Memory Promotion

- **Domain ID**: `06-knowledge-and-memory`
- **Collision Domain**: `knowledge-memory`
- **Feature IDs**: `FEAT-VAULT-MEMORY-01`
- **Constitutional Laws**: Law 2 (Deterministic State), Law 6 (Evidence over Verdicts)

---

### R1 — Pure-Go SQLite FTS5 Knowledge Vault & Multi-Project Partitioning {#R1-vault-fts5-partitioning}

<!-- inherited-from: DELTA-11, DELTA-21 -->
<!-- feature_id: FEAT-VAULT-MEMORY-01 -->

Operational lessons and Tri-Anchor knowledge records MUST persist in a pure-Go SQLite FTS5 vault partitioned by `repo_id` and `scope_tier`.

#### Scenario: Knowledge vault and memory CLI commands are registered
<!-- tests: TestCanonicalCommandsAndAliases -->
- **GIVEN** the canonical CLI surface registry
- **WHEN** `CanonicalCommands()` is inspected
- **THEN** `vault`, `memory`, and `lesson` are registered
