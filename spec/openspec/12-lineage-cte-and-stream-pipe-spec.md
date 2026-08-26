# OpenSpec DELTA-12: Core Resilience, Recursive CTE Lineage & Unbuffered Streaming Pipe

## 1. Context & Motivation
- **Ancestry Traversal**: Task lineage lookup (`GetTaskLineage`) in SQLite WAL control plane previously executed iterative N round-trip queries. For deep subtask trees, this introduced database round-trip overhead.
- **Windows Process Group Containment**: On Windows, process termination lacked recursive child process cancellation, risking orphaned background workers when subagents spawned CLI subprocesses.
- **Unbuffered Streaming**: Streaming stdout/stderr output from workers previously suffered from libc 4KB buffering delays, preventing real-time observability.
- **Code Symbol Search**: FTS5 `unicode61` tokenizer indexed camelCase/snake_case symbols as single tokens, preventing sub-word queries (e.g. searching "blast" for `CalculateBlastRadius`).

## 2. Technical Architecture & Invariants

### 2.1. Atomic SQLite Recursive CTE
Ancestry lineage retrieval is executed via an atomic Recursive Common Table Expression (CTE):
```sql
WITH RECURSIVE lineage_tree(task_id, parent_task_id, depth) AS (
    SELECT task_id, parent_task_id, 0
    FROM tasks
    WHERE task_id = ?
    UNION ALL
    SELECT t.task_id, t.parent_task_id, lt.depth + 1
    FROM tasks t
    JOIN lineage_tree lt ON t.task_id = lt.parent_task_id
    WHERE lt.depth < 1000
)
SELECT t.task_id, t.parent_task_id, ...
FROM tasks t
JOIN lineage_tree lt ON t.task_id = lt.task_id
ORDER BY lt.depth DESC;
```
- **Time Complexity**: $O(1)$ single query database execution.
- **Cycle Defense**: `WHERE lt.depth < 1000` prevents infinite recursion on corrupt/circular parent references.
- **Ordering**: `ORDER BY lt.depth DESC` guarantees exact Root $\rightarrow$ Leaf ordering.

### 2.2. Windows Process Tree Termination
- Configures `CreationFlags: 0x00000200` (`CREATE_NEW_PROCESS_GROUP`) on Windows.
- Terminates whole process hierarchies using `taskkill /T /F /PID <pid>` with fallback to `Process.Kill()`.

### 2.3. Unbuffered Pipe Streamer
- Provides `UnbufferedPipeStreamer` with `PipeTee(dst)` wrapping child stdout and stderr.
- Emits real-time `StreamEvent` lines to registered `StreamCallback` handlers without buffering delays.

### 2.4. Code Symbol Token Decomposition
- `TokenizeCodeSymbols(s)` decomposes `camelCase`, `PascalCase`, `snake_case`, and `kebab-case` identifiers into constituent tokens.
- FTS5 query sanitizer automatically expands queries with sub-tokens for bidirectional fuzzy matching.

## 3. Verification & Compliance
- Pass 1: `CGO_ENABLED=0 go test ./...`
- Pass 2: `CGO_ENABLED=1 go test -race ./internal/...`
- Zero external CGO dependencies; 100% Pure-Go.
