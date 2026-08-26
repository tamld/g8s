# Software Requirements Specification (SRS)

## 1. Functional Requirements (FR)

* **FR-1 (Role & Permission Harness)**:
  * Enforce 6 distinct worker roles: `collector`, `scout`, `mcp-mapper`, `summarizer`, `verifier`, `test-runner`.
  * Enforce 3 permission profiles: `read_only` (default), `automation_read` (permits CLI permission skipping behind sandbox), `workspace_write` (requires write receipt).
  * Enforce pre-dispatch safety filters: `BLOCKED_TASK_PATTERNS` (destructive shell/database commands) and `DENIED_PATH_FRAGMENTS` (`.ssh`, `.aws`, `.env`, `id_rsa`, etc.).
  * Contract Prompt Injection: Inject role boundaries, mutation policies, and wiki/session constraints into the worker prompt.

* **FR-2 (Receipt-Based Write Delegation)**:
  * Brain issues one-time write receipts: `issue_write_receipt(issuer, allowed_paths, ttl_seconds)` (TTL max 3600s).
  * Worker must provide valid `--receipt-id` to unlock `workspace_write`.
  * Atomic consumption: Receipt is invalidated immediately upon single-use validation.
  * Auditability: Active receipts can be listed and revoked prior to consumption.

* **FR-3 (Durable Control Plane & Task Lifecycle)**:
  * State Machine: `QUEUED -> LEASED -> RUNNING -> SUCCEEDED | FAILED | CANCELLED | NEEDS_INFO | BLOCKED`.
  * Pure-Go SQLite in WAL mode (`modernc.org/sqlite`) with atomic migrations (`PRAGMA user_version`).
  * Concurrency control: Atomic Compare-And-Swap (CAS) task claiming, periodic heartbeat renewal, stale lease reclamation.
  * Prompt Redaction: Raw prompts are deleted and replaced by SHA-256 `prompt_hash` upon terminal state.

* **FR-4 (Worker Process Execution & Safeguards)**:
  * Process Group Isolation: Launch workers in isolated process groups (`Setpgid: true`) to ensure all subprocess descendants are cleanly terminated upon cancellation or timeout.
  * Stream Bounding: Max capture buffer capped at 2MB with automatic head/tail truncation.
  * Post-Run Contract Detection: Scan stdout/stderr for unauthorized mutation attempts (`git commit`, `wiki.py write`, etc.), converting zero exit codes into harness violations (Exit Code 3).
  * Data Sanitization: Automatic redaction of sensitive credentials, passwords, and tokens from stdout/logs.

* **FR-5 (Stdio MCP Server)**:
  * Implement JSON-RPC 2.0 stdio protocol for MCP compatibility.
  * Expose tools: `g8s_list_roles`, `g8s_list_permissions`, `g8s_self_awareness`, `g8s_run`, `g8s_dispatch`, `g8s_get_task`, `g8s_list_tasks`, `g8s_cancel_task`.

* **FR-6 (Cross-Platform Daemon Service)**:
  * Support `g8s service install|start|stop|restart|uninstall|status` on macOS (`launchd`), Linux (`systemd`), and Windows (`service`).

* **FR-7 (Decoupled Pure-Go Knowledge Vault)**:
  * Provide persistent storage and SQLite FTS5 + BM25 ranked full-text search indexing over Tri-Anchor distillation records (`internal/vault`).
  * Support CLI subcommands: `g8s vault store`, `g8s vault query`, `g8s vault get`, `g8s vault list`, `g8s vault delete`.

## 2. Non-Functional Requirements (NFR)
* **NFR-1 (Cold Start Latency)**: CLI invocation cold start < 15ms.
* **NFR-2 (Memory Footprint)**: Idle daemon memory consumption < 15MB RAM.
* **NFR-3 (Data Security)**: State DB and exported files locked to POSIX `0600` permissions; directories locked to `0700`.
* **NFR-4 (Portability)**: 100% Pure Go with zero CGO dependencies. Compilable on `darwin/arm64`, `darwin/amd64`, `linux/amd64`, `linux/arm64`, `windows/amd64`.
