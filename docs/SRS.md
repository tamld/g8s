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
  * Expose 19 tools: `g8s_run`, `g8s_submit`, `g8s_get`, `g8s_receipt_issue`, `g8s_self_awareness`, `g8s_blast_radius`, `g8s_dispatch`, `g8s_list_tasks`, `g8s_cancel_task`, `g8s_list_roles`, `g8s_list_permissions`, `g8s_search`, `g8s_read`, `g8s_edit`, `g8s_select`, `g8s_llm_call`, `g8s_charter_issue`, `g8s_charter_verify`, `g8s_portfolio_status`.

* **FR-6 (Cross-Platform Daemon Service)**:
  * Support `g8s service install|start|stop|uninstall|status` on macOS (`launchd`), Linux (`systemd`), and Windows (`sc.exe`); unsupported platforms fail closed with a clear error.
  * Non-loopback HTTP daemon binds (`g8s serve`) require bearer token authentication (`G8S_SERVE_TOKEN`, `DELTA-27`).

* **FR-7 (Decoupled Pure-Go Knowledge Vault)**:
  * Provide persistent storage and SQLite FTS5 + BM25 ranked full-text search indexing over Tri-Anchor distillation records (`internal/vault`) with multi-project partitioning (`repo_id` / `scope_tier`: `repo | domain | global`, `#661`).
  * Hydrate pre-execution toolchain grounding (`go.mod` / `package.json`) and repo-scoped Vault whispers prior to Turn 1 execution (`#661`).
  * Support CLI subcommands: `g8s vault store`, `g8s vault query`, `g8s vault get`, `g8s vault list`, `g8s vault delete`.

* **FR-8 (Information Firewall & Schema-Enforced Multi-Round Contracts)**:
  * **Downward Contract (`BriefSchema`, `F0 -> F1`)**: Every delegated round defines an explicit goal, enumerated acceptance items `dod_items: [{id: "D1..Dn", description, verifier_hint}]`, and a path-scoped `receipt_id` (`allowed_paths`).
  * **Worktree-Targeted Verification (`g8s verify`)**: When verifying a task prior to delivery, `g8s verify --task <id>` executes registered checks directly against the task's isolated worktree (`res.Deliverable.Dir`), captures both `stdout` and `stderr`, persists the full raw log to `$G8S_STATE_DIR/logs/verify-<task-id>.log` (`0600`), and flags vacuous runs (`0 tests ran`).
  * **Upward Synthesis Contract (`RoundReceiptSchema`, `F1/Harness -> F0`)**: Combines deterministic `execution_facts` (`worktree_dir`, `tree_sha`, `exit_code`, `tests_ran`, `tests_failed`, `applied_files`, `deleted_files`, `untracked_files`, `raw_log_path`, `raw_log_sha256`) with lossless semantic signal (`dod_matrix` mapping every `D1..Dn` to `MET | UNMET | PARTIAL` + `evidence_ref`, and untruncated `failure_diagnostics` for every distinct failure).
  * **Signal-Proportional Sizing (No Fixed Line Truncation)**: Receipt payload size scales strictly with `f(|DoD Items| + |Distinct Failures| + |Changed Boundaries|)`; 100% of mechanical noise (passing test lines, polling frames) is stripped while 100% of diagnostic and DoD signal is preserved.

* **FR-9 (Verified Delegation & Commitment-Point Gates `T0–T4`, ADR-0033)**:
  * Enforce reversibility tiers `T0` (Observation/F1 dispatch), `T1` (Branch push/PR/Issue close), `T2` (Standard merge), `T3` (Trust-boundary merge requiring dual independent verification), and `T4` (Irreversible/Sovereign actions requiring explicit synchronous PiC approval).
  * Enforce fail-closed T3 blind verification (`EvaluateT3Response` requiring explicit `VERDICT: PASS`) and resolved model-family lineage disjointness (`ExtractModelFamily(model_resolved)`, `#662`).
  * Enforce deterministic commitment checks: completeness of `dod_matrix` (`all D1..Dn == MET`), `tree_sha`/`head_sha` non-vacuous test evidence (`G1`/`G5`), forge CI completion (`G2`), worktree-to-commit blob provenance (`G3`), and issue-closure criterion completeness (`G7`).
  * Enforce Orthogonal Stratum Federation (`S0 Principal` $\to$ `S1 FederationCoordinator` $\to$ `S2 DomainGovernor` $\to$ `S3-M BoundedMutator` / `S3-A BlindAdjudicator` + `O-Plane TelemetryProjector`, `DELTA-33` / `ADR-0035`) with epoch-fenced SQLite WAL charters (`g8s charter`), strict adjacency (`E_STRATUM_ADJACENCY_VIOLATION`), and cross-stratum mutation firewall (`E_S1_DIRECT_MUTATION_FORBIDDEN`).

* **FR-10 (Universal Config-Driven LLM Consumer & Multi-Model 9Router Fleet, `DELTA-25..32`, ADR-0034)**:
  * Declarative provider config in `providers.json` supporting `api_call` with `openai`, `anthropic`, and `9router` protocols, runtime `base_url_env`, static `headers`, and wildcard/prefix model matching (`allow_unlisted_models`).
  * Direct synchronous and bounded agentic loop execution via `g8s llm-call` supporting `--provider`, `--model`, `--prompt`, `--prompt-file`, `--system`, `--timeout`, `--effort`, `--max-tokens`, `--base-url`, `--protocol`, `--auth-env`, `--apply-files`, `--context-files`, `--max-turns <1..5>`, `--verify-cmd`, and `--worker-envelope`, with Non-Markovian oscillation breaking (`A->B->A`), atomic per-turn file snapshot rollback (`#663`), `--verify-cmd` secret environment scrubbing, and `.git/**`/`.g8s/**` write protection (`#664`).
  * Native queue execution bridge in `g8s worker` wiring `api_call` providers to `g8s llm-call --worker-envelope`, capturing duration, token usage (`input_tokens`, `output_tokens`, `total_tokens`), and response text into `agyResult` format.
  * Live model discovery via `g8s providers discover [--provider <name>] [--json]` querying `/v1/models`.
  * Catalog registration of standardized 9router SWE combos (`reasoning-free`, `reasoning-high`, `deep-coding`, `fast-agent`, `docs-retriever`) in `.g8s/agent-models.yml`.
  * Two-Layer Separation Rule: Public repo files declare generic protocol mechanisms and standard combos only; private LAN endpoints or custom homelab combos reside strictly in untracked local configurations (`~/.config/g8s/providers.json`).
* **FR-11 (Reach Layer — Read-Only Platform Data Acquisition, `DELTA-47`, ADR-0037)**:
  * In-code channel→action→backend registry (no YAML) with stable declared ordering and terminal errors for unknown channel/action.
  * Ordered fallback dispatch: transient failures (network, timeout, 429, 5xx) classify fallback and advance to the next backend; bad requests classify terminal and stop; optional nil-safe `Orderer`/`Recorder` seams for the S2 health ledger (ADR-0037 Amendment A1).
  * `g8s reach read <url>` / `g8s reach <channel> read --url <url>` as the only agent-facing surface, emitting the standard versioned JSON envelope with backend provenance, url, fetched_at, body_sha256 (via `reach.DigestBody`), and a per-attempt trail.
  * Brief `reach-cite:` markers on lanes F/R/S MUST carry the same machine-artifact fields; citations without a valid parsed artifact are rejected by the deterministic DoR floor (`reach_citation_valid`), while briefs with no markers retain legacy upstream prose passthrough.
  * Read-only absolute invariant: no upstream mutation of any kind; credentials stay OUTSIDE g8s state (receipts, ledger, logs, ContextPacket); no Python port; no GUI automation in the steady-state loop.

## 2. Non-Functional Requirements (NFR)
* **NFR-1 (Cold Start Latency)**: CLI invocation cold start < 15ms.
* **NFR-2 (Memory Footprint)**: Idle daemon memory consumption < 15MB RAM.
* **NFR-3 (Data Security)**: State DB and exported files locked to POSIX `0600` permissions; directories locked to `0700`.
* **NFR-4 (Portability)**: 100% Pure Go with zero CGO dependencies. Compilable on `darwin/arm64`, `darwin/amd64`, `linux/amd64`, `linux/arm64`, `windows/amd64`.

## 3. Release Milestones & Grounded Roadmap

| Milestone | Scope & Grounded Milestones | Status |
| :--- | :--- | :---: |
| **`v0.18.0`** (2026-10-21) | **F0 Verified Delegation & Symmetric Zero-Trust Control Plane** ([ADR-0033](decisions/0033-f0-verified-delegation.md)): hash-chained evidence ledger, signed mandates (`T0`–`T4`), commit/merge gates `G0`–`G7`, lossless verify firewall. | **Done** |
| **`v0.19.0`** (2026-10-21) | **Universal Config-Driven LLM Call Consumer & Multi-Model Fleet** ([ADR-0034](decisions/0034-config-driven-llm-consumer-9router.md), `DELTA-25`): 16-tool MCP Server (`g8s_llm_call`, `g8s_select`), Offer Bundle `v0.19.0`, Skills `v4.3.0` (`doc-standard.md`), Single `MIT` License, Zero-Stale `manifest.json` & Structure Sync. | **Done** |
| **`v0.20.0`** (2026-10-22) | **Zero-Trust Execution Hardening, Bounded Agentic Loop & Epistemic Verification** (`DELTA-26..32`, `#661..#664`): Fail-closed worktree isolation (`#656`), Draft-7 `--output-schema` (`#659`), `g8s fleet status` (`DELTA-32`), multi-project Vault grounding (`#661`), fail-closed T3 model-family disjointness (`#662`), non-Markovian loop rollback (`#663`), and `--verify-cmd` DLP scrubbing (`#664`). | **Done** |
| **`v0.21.0+`** (Planned — Pre-1.0 Burn-In) | **90-Day Multi-Repo Offer Retention** (`SCORECARD.md` M4 across 5 repositories with 0 gate regressions) and formal pre-1.0 API freeze criteria (`v1.0.0` is gated on empirical burn-in evidence, not calendar dates). | **Planned** |
