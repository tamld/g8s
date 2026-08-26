# CLI Reference

The `g8s` binary is the self-describing, single entry point for task submission, control-plane inspection, task lineage queries, write receipt delegation, and Stdio MCP serving. All state lives in a Zero-CGO SQLite database (`modernc.org/sqlite`) running in WAL mode.

---

## Environment Variables

| Variable | Purpose | Default |
| :--- | :--- | :--- |
| `G8S_DB` | Path to shared SQLite control-plane & receipt database | `~/.local/state/g8s/g8s.db` |
| `AGY_BIN` | Explicit worker binary path (overrides PATH lookup) | Resolved from `PATH` |

---

## Subcommands

### 1. `g8s submit`
Queues an asynchronous durable task into the SQLite WAL control plane after validating it against the security harness.

```sh
g8s submit \
  --idempotency-key "task-refactor-001" \
  --prompt "Scan internal/harness for security bypasses" \
  --role "collector" \
  --permission "read_only" \
  --add-dir "." \
  --model "gemini-3.7-flash-high" \
  --priority 10 \
  --max-attempts 3
```

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--idempotency-key` | `string` | *(Required)* | Unique idempotency key. Resubmissions deduplicate atomically. |
| `--prompt` | `string` | *(Required)* | Task prompt handed to the worker LLM. |
| `--role` | `string` | `"collector"` | Worker role contract (`collector`, `scout`, `mcp-mapper`, `summarizer`, `verifier`, `test-runner`). |
| `--permission` | `string` | `"read_only"` | Permission profile (`read_only`, `automation_read`, `workspace_write`). |
| `--add-dir` | `string` | `[cwd]` | Allowed filesystem directory (repeatable). Validated against forbidden paths. |
| `--receipt-id` | `string` | `""` | Write Receipt ID (mandatory when `--permission workspace_write`). *(Arrives in v0.2.0 via PR #51)* |
| `--parent-task-id`| `string` | `""` | Parent task ID for subtask lineage tracking and tree queries. *(Arrives in v0.2.0 via PR #51)* |
| `--skip-permissions`| `bool` | `false` | Bypass permission checks (allowed only if permission profile permits). |
| `--model` | `string` | `"gemini-3.7-flash-high"` | Target worker model identifier. |
| `--priority` | `int` | `0` | Queue priority (`-100` to `100`). Higher priority tasks are claimed first. |
| `--max-attempts` | `int` | `1` | Retry budget (`1` to `10`). |

---

### 2. `g8s get <task-id>`
Prints the current durable JSON representation of a task from the control plane.

```sh
g8s get 3d6f4520-21a4-4f4a-9cbb-9d7fb2389d31
```

---

### 3. `g8s tasks`
Lists durable tasks in the control-plane queue with optional state filtering and pagination limits.

```sh
# List all tasks
g8s tasks

# Filter by state with custom limit
g8s tasks --state QUEUED --limit 20
```

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--state` | `string` | `""` | Filter by state: `QUEUED`, `LEASED`, `RUNNING`, `SUCCEEDED`, `FAILED`, `CANCELLED`, `NEEDS_INFO`, `BLOCKED`. |
| `--limit` | `int` | `50` | Maximum number of tasks to return (`1..200`). |

---

### 4. `g8s lineage <task-id>` *(Targeted: v0.2.0 / Issue #44)*
Prints the full ancestry chain of a task up to the root parent, ordered chronologically (`Root -> Child -> Grandchild`).

```sh
g8s lineage grandchild-task-id-123
```

---

### 5. `g8s children <parent-task-id>` *(Targeted: v0.2.0 / Issue #44)*
Lists all direct child subtasks submitted under a specified parent task ID.

```sh
g8s children root-task-id-123
```

---

### 6. `g8s receipt issue`
Issues a cryptographic, single-use, TTL-bounded, path-scoped Write Receipt on behalf of the Brain orchestrator.

```sh
g8s receipt issue \
  --issuer "brain-orchestrator" \
  --path "./internal/receipt/*" \
  --allow "./spec/openspec/*" \
  --ttl 600
```

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--issuer` | `string` | `"operator"` | Identity of the issuing agent/orchestrator recorded on the receipt. |
| `--path`, `--allow` | `string` | *(Required)* | Allowed file path glob pattern (repeatable). |
| `--ttl` | `int` | `600` | Time-to-live in seconds (`1` to `3600`). |

---

### 7. `g8s mcp`
Serves the standard Stdio JSON-RPC 2.0 Model Context Protocol (MCP) server on `stdin`/`stdout`.

```sh
g8s mcp
```

Tools exposed over MCP:
* `g8s_dispatch`: Submit tasks to the control-plane queue with role and capability enforcement.
* `g8s_get_task`: Check task status and execution results.
* `g8s_list_tasks`: List active tasks.
* `g8s_cancel_task`: Request early cancellation for a running task.

---

### 8. `g8s roles` & `g8s permissions`
Inspect built-in security profiles directly in your terminal:

```sh
g8s roles
g8s permissions
```

---

### 9. `g8s version`
Prints binary version banner, Go runtime, and Zero-CGO pure Go status.

```sh
g8s version
```

---

## Standalone 1-Liner Installation *(Available in v0.2.0)*

Install or upgrade `g8s` directly via curl:

```sh
curl -fsSL https://raw.githubusercontent.com/tamld/g8s/main/scripts/install.sh | bash
```

Custom installation directory:
```sh
G8S_INSTALL_DIR="/usr/local/bin" curl -fsSL https://raw.githubusercontent.com/tamld/g8s/main/scripts/install.sh | bash
```
