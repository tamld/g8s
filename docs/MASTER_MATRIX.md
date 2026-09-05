# g8s Master Architectural & Capability Matrix

> **The Definitive Long-Term Compass for g8s (The Gatekeepers)**  
> *"Intelligence directs; Muscle executes; Harness protects; Runtime proves."*  
> **Status**: Living Master Specification  

---

## 1. The Cross-Cutting Master Architecture Matrix

```
┌────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
│                                          g8s MASTER ARCHITECTURAL MATRIX                                               │
├─────────────────────────┬───────────────────────────────────┬──────────────────────────────────┬───────────────────────┤
│ ARCHITECTURAL PLANE     │ SPECIFICATION & GOVERNANCE ARTIFACT│ RUNTIME ENGINE (Pure-Go Kernel)  │ PHYSICAL INFRASTRUCTURE│
├─────────────────────────┼───────────────────────────────────┼──────────────────────────────────┼───────────────────────┤
│ 1. DESIGN LANGUAGE      │ • Spec Kit Constitution           │ • Request Validator              │ • Versioned Markdown  │
│    (The Intent & Laws)  │ • OpenSpec Deltas (`spec/openspec`)│ • Contract Prompt Injector       │ • Git Tree Specs      │
│                         │ • `RoleProfile` & `PermProfile`   │ • JSON Schema Gatekeeper         │ • Pure Data Contracts │
│                         │ • `receipt-v1.schema.json`        │   (`internal/harness`)           │                       │
├─────────────────────────┼───────────────────────────────────┼──────────────────────────────────┼───────────────────────┤
│ 2. CONTROL & SCHEDULING │ • Finite State Machine (8 States) │ • Pure-Go SQLite WAL Queue       │ • Local SQLite DB     │
│    (The Brain-Muscle)   │ • Task Lineage & Idempotency Keys │ • Atomic CAS Lease Engine        │   (`0600` POSIX perm) │
│                         │ • Priority Queue Indexing         │ • Concurrency Governor ($1:N$)   │ • Append-only Event   │
│                         │ • Prompt Hash Redaction Standard  │   (`internal/controlplane`)      │   JSONL Stream        │
├─────────────────────────┼───────────────────────────────────┼──────────────────────────────────┼───────────────────────┤
│ 3. ZERO-TRUST CAPABILITY│ • Single-Use Write Receipt Policy │ • Receipt Issuer & TTL Validator │ • Scoped File Paths   │
│    (The Write Gate)     │ • Max TTL Ceiling ($\le 3600s$)   │ • Atomic Transaction Consumer    │ • Dry-run Git Diffs   │
│                         │ • Scoped `allowed_paths` Globs    │ • Revocation Engine              │ • Physical Hash State │
│                         │ • Blast Radius Index (BRI)        │   (`internal/receipt`, `analyzer`)                       │
├─────────────────────────┼───────────────────────────────────┼──────────────────────────────────┼───────────────────────┤
│ 4. PROCESS CONTAINMENT  │ • Process Group Isolation Laws    │ • OS Syscall Runner (`Setpgid`)  │ • Child Processes     │
│    (The Execution Cage) │ • Signal Propagation (`SIGTERM`)  │ • Timeout Killer / Process Tree  │ • OS Sandboxes        │
│                         │ • Post-Run Mutation Scan RegEx    │ • Stream Redactor (Max 2MB)      │ • PGID Process Groups │
│                         │ • Exit Code Contract (`0` to `5`) │   (`internal/worker`)            │                       │
├─────────────────────────┼───────────────────────────────────┼──────────────────────────────────┼───────────────────────┤
│ 5. PROTOCOLS & PLUGINS  │ • Model Context Protocol (MCP)    │ • Stdio JSON-RPC 2.0 Server      │ • Claude Desktop      │
│    (The Universal Bus)  │ • Language Server Protocol (LSP)  │ • Lightweight LSP Client Sidecar │ • Cursor / Windsurf   │
│                         │ • Provider Registry Schema        │ • Resource Pool Discovery Engine │ • `gopls` / `pyright` │
│                         │ • Native Provider Drivers         │   (`internal/mcp`, `provider`)   │ • Local CLI & Ollama  │
├─────────────────────────┼───────────────────────────────────┼──────────────────────────────────┼───────────────────────┤
│ 6. EXPERIENCE (DX / AX) │ • SDE Self-Describing Standard    │ • Interactive Cobra CLI Wizard   │ • Terminal ANSI TUI   │
│    (Human & Agent UX)   │ • Machine Remediation Commands    │ • Health Inspector `g8s doctor`  │ • Shell Tab Auto-Comp │
│                         │ • Headless Silent Flags (`--agent`)│ • Atomic Config Setters (`set`)  │ • Standard Stdio Pipe │
│                         │ • Rich Feynman Contextual Errors  │   (`cmd/g8s`)                    │                       │
└─────────────────────────┴───────────────────────────────────┴──────────────────────────────────┴───────────────────────┘
```

---

## 2. The Two-Tier Model & Role Matrix

| Dimension | Tier 1: Supervisor (Brain) | Tier 2: Worker (Muscle) |
| :--- | :--- | :--- |
| **Target Models** | Claude 3.7 Sonnet (Thinking), Claude Opus, GPT-4o, DeepSeek R1 | Gemini 3.8 Flash (High), Claude 3.5 Haiku, DeepSeek V3, Local Ollama |
| **Cognitive Strengths** | Deep multi-step reasoning, architectural synthesis, risk evaluation. | High token throughput, sub-second latency, ultra-low cost. |
| **Assigned Roles** | System Architect, Spec Author, Receipt Issuer, PR Gatekeeper. | `collector`, `scout`, `mcp-mapper`, `summarizer`, `verifier`, `test-runner`. |
| **Permissions** | Root capability authority, exclusive SSoT & Git commit rights. | `read_only` by default. Can only mutate files matching a valid Write Receipt. |
| **Blast Radius Policy** | Reviews AST Blast Radius reports before signing receipts. | Hard-jailed inside Process Group; cannot traverse `..` or `.env`. |
| **Memory Model** | Multi-turn contextual continuity across project milestones. | Stateless single-turn snapshot. Process dies upon completion. |

---

## 3. The 8 Iron Governance Rules Matrix

| Rule # | Name | Invariant Standard | Verification / Enforcement Mechanism |
| :---: | :--- | :--- | :--- |
| **1** | **Zero-CGO Invariant** | 100% Pure-Go compilation with `CGO_ENABLED=0`. | Multi-OS CI matrix (`darwin/arm64`, `linux/amd64`, `windows/amd64`). Uses `modernc.org/sqlite`. |
| **2** | **Capability Receipts** | Zero filesystem writes without an active, time-limited Write Receipt. | Atomic SQLite transaction (`consumed = 1` inside `EXCLUSIVE` lock). |
| **3** | **Process Group Isolation**| All worker commands must execute inside dedicated Process Groups. | Syscall `Setpgid: true` (Unix) / JobObjects (Windows). Sends `SIGTERM` to `-pgid`. |
| **4** | **Zero-Leakage & Redaction**| Raw prompts deleted and converted to SHA-256 on completion. | SQLite task table stores `prompt_hash`. DB permissions `0600`, directory `0700`. |
| **5** | **Deterministic Time** | Time logic must accept injectable clock functions. | Unit tests verify TTL expiry in $\le 1\mu s$ without `time.Sleep`. |
| **6** | **Post-Run Mutation Scan**| `stdout`/`stderr` scanned for forbidden mutation side effects. | Automatically forces Exit Code `3` (`READ_ONLY_CONTRACT_EXIT`) if mutation detected. |
| **7** | **Language Purity** | 100% of code, tests, docs, commits in professional English. | Automated pre-commit linting and zero-diacritic verification. |
| **8** | **Self-Describing Executable**| CLI binary is the living Single Source of Truth (SSoT). | Complete `--help` enums, `--json` modes, explicit exit codes (`0` to `5`). |

---

## 4. Dual-Audience Interface Matrix (Human DX vs. Agent AX)

| Feature | Human Developer (Human DX) | Autonomous AI Agent (Agent AX) |
| :--- | :--- | :--- |
| **Onboarding** | `g8s init` (Interactive TUI Wizard with auto IDE detection). | `g8s init --agent --ide=cursor --json` (Headless 1-shot). |
| **Diagnostics** | `g8s doctor` (Formatted healthcheck with color alerts). | `g8s doctor --json` (Includes `remediation_cmd` for self-healing). |
| **Auto-Repair** | `g8s doctor --fix` (1-click automatic permission and daemon fix). | Agent executes recommended `remediation_cmd` directly. |
| **Configuration** | Guided prompts during `init` or editing YAML. | `g8s config set <key> <val>` (Atomic CLI key-value setter). |
| **Error Handling** | Feynman-style human-readable explanation (What, Why, Fix). | Deterministic Exit Codes (`0` to `5`) + JSON error objects. |
| **Shell Integration**| Auto-completion scripts (`g8s completion zsh/bash/fish`). | Standard Unix Stdio stream piping. |

---

## 5. Strategic Evolution Roadmap (v0.6.1 snapshot)

```
  M1-M3 (DONE)         M4 (CURRENT)         M5 (NEXT)              M6 (FUTURE)              v1.0.0 GA
  Foundation+CP+MCP   v0.6.1                Robustness+Evals       Distributed Fleet         Enterprise GA
       │                  │                     │                      │                       │
       ├──────────────────┼─────────────────────┼──────────────────────┼───────────────────────┤
       ▼                  ▼                     ▼                      ▼                       ▼
• Pure-Go Harness    • Gemini 3.8 Flash    • Closed-Loop          • Remote Worker Nodes    • Concern B+C
• SQLite WAL Queue     (High) Worker         Telemetry (#253)      via mTLS                  landed
• Write Receipts     • FSM Orchestrator    • Adversarial Eval     • Distributed Locks      • D9 Audit
• 38 Parity Tests    • Lego Mounts           Harness (#254)        • Enterprise Security      Signoff
• Stdio MCP Server   • Knowledge Vault     • Provider Reliability   Signoff                 • CNCF / Linux
• OS Daemons           FTS5 (DELTA-11)       Index (PRI)                                       Foundation
• DX Init Wizard     • Two-Tier Orch.
```

### 5.1 Version Train (v0.6.1 → v1.0.0)

| Version | Theme | Scope | Source |
|---|---|---|---|
| **v0.6.1** | Released 2026-09 | 3.8 Flash upgrade, effort injection, dual-remote release | tag `v0.6.1` |
| **v0.7.0** | Dynamic Provider | Config-driven model manifest, 9router adapter, `g8s providers` subcommand | spec `docs/superpowers/specs/2026-09-04-dynamic-provider-manifest-design.md` |
| **v0.8.0** | Concern A | Supervisor-driven fix loop (planner, enforcer, reviewer, RCA, escalator, metrics) | `docs/goals/g8s-orchestration-roadmap` T020 |
| **v0.8.1** | Governance | DoR/DoD overhaul, roadmap refresh, Iron Laws cross-ref | #255 |
| **v0.9.0** | Robustness+Evals | Closed-loop telemetry events+query (#253, scope-reduced), adversarial probe catalog + `g8s eval` stub (#254, scope-reduced) | #253, #254 |
| **v0.9.1** | M5 deferred | Pre-flight injection (telemetry → brief), pre-flight hook (probes → registration) | #253, #254 follow-ups |
| **v1.0.0** | GA | Concern B (receipt evolution), Concern C (optimizer), D9 formal audit | charter + #241 |
