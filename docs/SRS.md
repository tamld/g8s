# Software Requirements Specification (SRS)

## 1. Functional Requirements (FR)

* **FR-1 (Role & Permission Harness)**:
  * Enforce 6 distinct worker roles: `collector`, `scout`, `mcp-mapper`, `summarizer`, `verifier`, `test-runner`.
  * Enforce 4 permission profiles: `read_only` (default), `automation_read` (permits CLI permission skipping behind sandbox), `workspace_write` (requires write receipt), and `supplied_text` (disables all model-initiated tool calls at the provider adapter boundary).
  * Enforce tool policy options (`--tool-policy default|none`): `supplied_text` and `--tool-policy none` disable all model-facing tool calls at the provider adapter boundary (`PreToolUse` hook interceptor + callback audit). Boundary specification: this capability governs model-facing tool calls at the provider adapter boundary and does not claim an OS filesystem sandbox or prove that trusted provider/runtime housekeeping made no changes.
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
  * Expose 11 tools: `g8s_run`, `g8s_submit`, `g8s_get`, `g8s_receipt_issue`, `g8s_self_awareness`, `g8s_blast_radius`, `g8s_dispatch`, `g8s_list_tasks`, `g8s_cancel_task`, `g8s_list_roles`, `g8s_list_permissions`.

* **FR-6 (Cross-Platform Daemon Service)**:
  * Support `g8s service install|start|stop|uninstall|status` on macOS (`launchd`), Linux (`systemd`), and Windows (`sc.exe`); unsupported platforms fail closed with a clear error.

* **FR-7 (Decoupled Pure-Go Knowledge Vault)**:
  * Provide persistent storage and SQLite FTS5 + BM25 ranked full-text search indexing over Tri-Anchor distillation records (`internal/vault`).
  * Support CLI subcommands: `g8s vault store`, `g8s vault query`, `g8s vault get`, `g8s vault list`, `g8s vault delete`.

* **FR-8 (Information Firewall & Schema-Enforced Multi-Round Contracts)**:
  * **Downward Contract (`BriefSchema`, `F0 -> F1`)**: Every delegated round defines an explicit goal, enumerated acceptance items `dod_items: [{id: "D1..Dn", description, verifier_hint}]`, and a path-scoped `receipt_id` (`allowed_paths`).
  * **Worktree-Targeted Verification (`g8s verify`)**: When verifying a task prior to delivery, `g8s verify --task <id>` executes registered checks directly against the task's isolated worktree (`res.Deliverable.Dir`), captures both `stdout` and `stderr`, persists the full raw log to `$G8S_STATE_DIR/logs/verify-<task-id>.log` (`0600`), and flags vacuous runs (`0 tests ran`).
  * **Upward Synthesis Contract (`RoundReceiptSchema`, `F1/Harness -> F0`)**: Combines deterministic `execution_facts` (`worktree_dir`, `tree_sha`, `exit_code`, `tests_ran`, `tests_failed`, `applied_files`, `deleted_files`, `untracked_files`, `raw_log_path`, `raw_log_sha256`) with lossless semantic signal (`dod_matrix` mapping every `D1..Dn` to `MET | UNMET | PARTIAL` + `evidence_ref`, and untruncated `failure_diagnostics` for every distinct failure).
  * **Signal-Proportional Sizing (No Fixed Line Truncation)**: Receipt payload size scales strictly with `f(|DoD Items| + |Distinct Failures| + |Changed Boundaries|)`; 100% of mechanical noise (passing test lines, polling frames) is stripped while 100% of diagnostic and DoD signal is preserved.

* **FR-9 (Verified Delegation & Commitment-Point Gates `T0–T4`, ADR-0033)**:
  * Enforce reversibility tiers `T0` (Observation/F1 dispatch), `T1` (Branch push/PR/Issue close), `T2` (Standard merge), `T3` (Trust-boundary merge requiring dual independent verification), and `T4` (Irreversible/Sovereign actions requiring explicit synchronous PiC approval).
  * Enforce deterministic commitment checks: completeness of `dod_matrix` (`all D1..Dn == MET`), `tree_sha`/`head_sha` non-vacuous test evidence (`G1`/`G5`), forge CI completion (`G2`), worktree-to-commit blob provenance (`G3`), and issue-closure criterion completeness (`G7`).

## 2. Non-Functional Requirements (NFR)
* **NFR-1 (Cold Start Latency)**: CLI invocation cold start < 15ms.
* **NFR-2 (Memory Footprint)**: Idle daemon memory consumption < 15MB RAM.
* **NFR-3 (Data Security)**: State DB and exported files locked to POSIX `0600` permissions; directories locked to `0700`.
* **NFR-4 (Portability)**: 100% Pure Go with zero CGO dependencies. Compilable on `darwin/arm64`, `darwin/amd64`, `linux/amd64`, `linux/arm64`, `windows/amd64`.
