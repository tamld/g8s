# Two-Tier Governance & Receipt Engine

> **Document Type**: Architecture & Security Specification  
> **Status**: Approved & Mandatory (Constitution Aligned)

---

## 1. The Two-Tier Agent Model

`g8s` enforces strict architectural separation between **System-2 (Brain)** planning and **System-1 (Worker)** execution.

```text
┌────────────────────────────────────────────────────────┐
│               BRAIN TIER (Orchestrator)                 │
│  • Strategic Architecture & Task Decomposition         │
│  • Knowledge Vault Curation & Permanent State Promotion │
│  • Write Receipt Minting & Path Governance             │
│  • Final Git Commits & Release Tagging                 │
└───────────────────────────┬────────────────────────────┘
                            │ Issues Time-Limited Write Receipt
                            ▼
┌────────────────────────────────────────────────────────┐
│              G8S CAPABILITY INTERMEDIARY               │
│  • Verifies path globs, TTL, and signature             │
│  • Enforces sandbox process group containment          │
│  • Records append-only SQLite WAL audit trails         │
└───────────────────────────┬────────────────────────────┘
                            │ Dispatches with Bounded Contract
                            ▼
┌────────────────────────────────────────────────────────┐
│               WORKER TIER (Muscle / CLI)               │
│  • Fast, parallel execution (Gemini Flash, Haiku, etc.)│
│  • Read-only scans or single-use path-scoped writes     │
│  • Auto-terminated on timeout or unauthorized syscall  │
└────────────────────────────────────────────────────────┘
```

---

## 2. Capability Receipts (Schema v3)

### Core Rules of Receipts
1. **Single-Use Atomicity**: Once a receipt is claimed during a mutation task, it is consumed atomically via SQLite transaction. It cannot be reused.
2. **Time-To-Live (TTL)**: Receipts have a maximum lifespan ($\le 3600\text{s}$, default $600\text{s}$). Expired receipts are rejected immediately.
3. **Strict Path Scoping**: Receipts specify allowed glob patterns (e.g., `["internal/worker/*.go", "internal/worker/*_test.go"]`). Any attempt to modify files outside these paths fails the run and flags a contract violation.
4. **No Sensitive Access**: Even with a valid receipt, access to sensitive directories (`.git`, `.ssh`, `.aws`, `.env`, secret files) is unconditionally blocked by the harness gate.

### Schema v3 Metadata Fields
With the DELTA-11 Supervisor integration, receipts capture full provenance context:
- `approach_idx`: The strategic approach index (1..3).
- `attempt_idx`: The iteration attempt within the current approach (1..3).
- `rca_confidence`: Root-cause analysis confidence score.
- `adr_path`: Associated Architectural Decision Record if applicable.

---

## 3. Built-In Roles & Permission Matrix

| Role | Default Permission | Mutation Allowed? | Requires Receipt? | Scope of Work |
| :--- | :--- | :---: | :---: | :--- |
| `collector` | `read_only` | ❌ | No | Multi-directory file tree inventory and AST extraction |
| `scout` | `read_only` | ❌ | No | Codebase pattern discovery and candidate mapping |
| `mcp-mapper` | `read_only` | ❌ | No | Tool schema mapping and MCP server discovery |
| `summarizer` | `read_only` | ❌ | No | Log digestion and context compression |
| `verifier` | `read_only` | ❌ | No | Output validation and physical evidence checks |
| `test-runner` | `workspace_write` | ⚠️ Yes | **Yes (Required)** | Test synthesis and test suite execution |

---

## 4. Jev AI Reflex Gating

For fast pre-flight mutation analysis, the **Jev Reflex Sensor** inspects proposed changes before full execution:
- **`grant_receipt`**: Safe, scoped, within approved policy.
- **`escalate_hitl`**: Ambiguous risk, broad blast radius, requires Human-in-the-loop approval.
- **`instant_kill`**: Malicious pattern detected (`rm -rf`, credential scraping, unauthorized binary execution).
