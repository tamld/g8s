# Product Requirements Document (PRD)

## 1. Executive Summary
`g8s` (The Gatekeepers) is a lightweight, zero-trust process execution and capability delegation harness for AI CLI workers (Antigravity `agy`, Claude Code CLI, Gemini CLI, Aider, and Ollama). It empowers smart "Brain" orchestrators (e.g. Claude 3.7 Sonnet / Opus, GPT-4o, DeepSeek R1) to safely delegate mechanical, heavy-lifting tasks (code scanning, test generation, MCP mapping, artifact extraction) to fast, inexpensive workers without risking system damage, state corruption, or infinite execution loops.

## 2. The Problem
1. **Token Cost & Context Saturation (Supervisor Context Rot)**: Running broad repo exploration, raw test execution (`go test -json`), or CI log polling directly inside the Supervisor (`F0`) context window floods the control plane with megabytes of mechanical noise, causing attention dilution, context compaction loss, and **Role Collapse** (`F0` degrading into `F1`). Conversely, naive fixed-line truncation starves the Supervisor of diagnostic signal (**Decision Blindness**).
2. **The "Rogue Worker" & "Unchecked Supervisor" Threat**: Autonomous lightweight models (`F1`) executing in unconstrained environments can hallucinate destructive commands or mutate out-of-scope files; meanwhile, long-running Supervisors (`F0`) under completion pressure can drop files during squash-merge, close issues with unmet acceptance criteria (`D1..Dn`), or merge while CI checks are still pending.
3. **Heavy & Fragile Runtimes**: Existing multi-agent frameworks (LangGraph, CrewAI, AutoGen) require heavy Python virtual environments, lack OS-level process group lifecycle controls, and cannot be packaged as a single static binary.

## 3. Target Personas
* **Persona A (Agentic AI Developer)**: Building multi-agent workflows in Claude Desktop, Cursor, or Windsurf who wants instant delegation via MCP without permission prompt fatigue.
* **Persona B (Homelab & DevOps Engineer)**: Running autonomous 24/7 background tasks, log digestion, and CI/CD diagnostic loops on Linux/Proxmox/Docker with minimal RAM overhead (<15MB).
* **Persona C (Enterprise Security Administrator)**: Requiring strict audit trails, zero-leakage prompt redaction, and time-limited, path-scoped write delegation.

## 4. Core Value Propositions (USP)
1. **Zero CGO, Single Static Binary**: Distributed as a single ~10-15MB standalone binary with no runtime dependencies.
2. **Symmetric Zero-Trust Architecture (`F0` Control Plane ↔ `F1` Data Plane)**:
   * **Worker (`F1`) Trust via Physical Containment**: Locked into isolated git worktrees and path-scoped single-use `WriteReceipt`s (`AllowedPaths`).
   * **Supervisor (`F0`) Trust via Information Firewall & Boundary Schemas**: F0 never touches raw execution logs. Iteration rounds are free in count/topology, while Go code strictly enforces `BriefSchema` (`D1..Dn` downward) and `RoundReceiptSchema` (lossless failure/DoD signal upward, zero mechanical noise) at every round boundary and commitment gate (`T0–T4`).
3. **Decoupled Memory & Pure-Go Knowledge Vault**: Sub-millisecond FTS5 + BM25 ranked indexing of Tri-Anchor distillation records (`internal/vault`).
4. **Pluggable CLI Providers & Universal Config-Driven LLM Consumer (`DELTA-25`)**: Universal adapter layer supporting `agy`, `claude`, `gemini`, `ollama`, and direct HTTP `api_call` gateways (`9router` multi-model combos, OpenAI-compatible `/v1/chat/completions`, Anthropic `/v1/messages`) with live model discovery (`g8s providers discover`) and direct synchronous CLI execution (`g8s llm-call`).
5. **Durable SQLite WAL Control Plane**: Atomic CAS leases, task deduplication via idempotency keys, heartbeat leases, retry ceilings, and parent-child task lineage.
6. **Stdio MCP Server & Daemon Manager**: Native JSON-RPC MCP server + built-in macOS `launchd`, Linux `systemd`, and Windows Service integration.

## 5. Release Roadmap & Milestones

| Milestone | Scope & Grounded Milestones | Status |
| :--- | :--- | :---: |
| **`v0.18.0`** (2026-10-21) | **F0 Verified Delegation & Symmetric Zero-Trust Control Plane** ([ADR-0033](decisions/0033-f0-verified-delegation.md)): hash-chained evidence ledger, signed mandates (`T0`–`T4`), commit/merge gates `G0`–`G7`, lossless verify firewall. | **Done** |
| **`v0.19.0`** (2026-10-21) | **Universal Config-Driven LLM Call Consumer & Multi-Model Fleet** ([ADR-0034](decisions/0034-config-driven-llm-consumer-9router.md), `DELTA-25`): 16-tool MCP Server (`g8s_llm_call`, `g8s_select`), Offer Bundle `v0.19.0`, Skills `v4.3.0` (`doc-standard.md`), Single `MIT` License, Zero-Stale `manifest.json` & Structure Sync. | **Done** |
| **`v0.20.0`** (Planned — Issue #639) | **Test Suite Execution Optimization & Empirical PRI Lane Routing** (`>=90%` first-attempt pass), Industry Security Standards Alignment (OWASP Top 10 for LLM/Agentic Apps 2025, SLSA Build Provenance, CycloneDX/SPDX SBOM), Cross-Platform Packaging Hardening & Internal Package Consolidation. | **Planned** |
| **`v0.21.0+`** (Planned — Pre-1.0 Burn-In) | **90-Day Multi-Repo Offer Retention** (`SCORECARD.md` M4 across 5 repositories with 0 gate regressions) and formal pre-1.0 API freeze criteria (`v1.0.0` is gated on empirical burn-in evidence, not calendar dates). | **Planned** |
