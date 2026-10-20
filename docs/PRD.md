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
4. **Pluggable CLI Providers**: Universal adapter layer supporting `agy`, `claude`, `gemini`, `ollama`, and custom shell scripts without locking safety invariants to any single provider's hooks.
5. **Durable SQLite WAL Control Plane**: Atomic CAS leases, task deduplication via idempotency keys, heartbeat leases, retry ceilings, and parent-child task lineage.
6. **Stdio MCP Server & Daemon Manager**: Native JSON-RPC MCP server + built-in macOS `launchd`, Linux `systemd`, and Windows Service integration.
