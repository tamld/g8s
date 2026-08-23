# Receipt-Based Write Delegation — System Design

## Overview

The AGY Dispatch plugin uses a **receipt-based delegation gate** to allow
the Brain orchestrator (Opus-tier model) to selectively grant write
permission to Flash workers. This prevents unauthorized file mutation while
enabling multi-agent collaboration.

## Architecture

```
┌──────────────────┐                    ┌────────────────────┐
│  Brain (Opus)    │  1. issue_receipt  │  ControlPlane      │
│  Orchestrator    ├───────────────────►│  SQLite WAL DB     │
│                  │  {issuer,          │  write_receipts    │
│                  │   allowed_paths,   │  table             │
│                  │   ttl_seconds}     └────────┬───────────┘
│                  │                             │
│                  │  4. audit                   │ 2. Worker claims
│                  │◄────────────────────────────│    --receipt-id
│  list_active()   │                             │
│  revoke()        │                    ┌────────▼───────────┐
└──────────────────┘                    │  Flash Worker      │
                                        │  validate_dispatch │
                                        │  → receipt gate    │
                                        │  → contract prompt │
                                        │    (paths injected)│
                                        └────────────────────┘
```

## Boundary Contract

This system enforces a strict boundary between the **plugin** and the
**wiki.py runtime**:

```
┌─────────────────────┐  TEXT ONLY  ┌─────────────────────┐
│  agy-dispatch       │ ──────────► │  wiki.py engine     │
│  (Plugin)           │  contract   │  (Runtime)          │
│                     │  prompt     │                     │
│  • SQLite receipts  │             │  • JSONL event log  │
│  • Permission gates │   NO CODE   │  • Note files       │
│  • Contract prompts │   IMPORT    │  • act-check gates  │
│                     │             │                     │
│  Independent DB     │ ◄─ ─ ─ ─ ─ │  Independent state  │
└─────────────────────┘             └─────────────────────┘
```

**Rules**:
1. Plugin NEVER imports `wiki.py` or `wiki_runtime.*` modules
2. Wiki.py NEVER imports `agy_dispatch` or `agy_harness` modules
3. Wiki policy is communicated to workers via **text in contract prompt** only
4. Each system enforces its own gates independently (defense in depth)

## Receipt Lifecycle

```
ISSUED ──► CONSUMED (one-time use, by worker)
   │
   ├──► REVOKED  (by Brain, before consumption)
   │
   └──► EXPIRED  (TTL elapsed, max 3600s)
```

### Issue (Brain only)

```python
receipt = cp.issue_write_receipt(
    issuer="opus-session-xyz",
    allowed_paths=["/worktree/src/*.py", "/worktree/tests/*.py"],
    ttl_seconds=600,
)
```

### Consume (Worker via dispatch)

```bash
python3 agy_dispatch.py \
  --permission workspace_write \
  --receipt-id <UUID> \
  --role collector \
  --prompt-file task.md \
  --add-dir /worktree
```

### Revoke (Brain, before consumption)

```python
revoked = cp.revoke_write_receipt(receipt["receipt_id"])  # True if revoked
```

### Audit (Brain)

```python
active = cp.list_active_receipts()  # Only unconsumed + unexpired
```

## Security Gates (6 layers)

| Gate | Blocks | Evidence |
|:---|:---|:---|
| No receipt | `workspace_write` without `--receipt-id` | Harness ValueError |
| Fake receipt | Non-existent UUID | ControlPlane ValueError |
| Expired receipt | TTL elapsed | Timestamp comparison |
| Consumed receipt | One-time use enforced | `consumed=1` in DB |
| Revoked receipt | Brain cancelled | Row deleted from DB |
| Scope awareness | Worker prompt contains `allowed_paths` | Injected by `build_contract_prompt()` |

## Worker Contract Prompt Injection

### Read-only workers receive:

```
Wiki engine policy (MANDATORY):
- ALLOWED: wiki.py query, wiki.py search, wiki.py read, wiki.py classify
- FORBIDDEN: wiki.py write, wiki.py reflect, wiki.py orient, wiki.py claim, wiki.py bypass
  These commands mutate shared session state and are reserved for the Brain orchestrator.
```

### Write-delegated workers receive:

```
This task has DELEGATED WRITE permission via receipt.
You may ONLY write to files matching these path patterns:
  - /worktree/src/*.py
  - /worktree/tests/*.py
Writing to ANY path outside this scope is a policy violation.
Receipt ID: <UUID>
Issuer: opus-session-xyz
```

## Multi-Agent Coordination

| Resource | Concurrent Safety | Mitigation |
|:---|:---|:---|
| ControlPlane SQLite | ✅ WAL + IMMEDIATE transactions | SQLite handles locking |
| Receipt consumption | ✅ Atomic update in transaction | Only one consumer wins |
| Wiki query_vault() | ✅ Pure read function | No shared state |
| Wiki EVENT_LOG | ⚠️ Append safe, but cross-agent pollution | Pending: agent_id filter |
| Wiki note files | ❌ LAST WRITE WINS | Pending: advisory lock |

## Test Coverage

| Suite | Count | Scope |
|:---|:---|:---|
| `test_agy_dispatch.py` | 12 | Core dispatch + CLI |
| `test_receipt_delegation.py` | 38 | Receipt CRUD + security + edge cases |
| `test_safety_coordination.py` | 32 | Prompt injection + revocation + multi-agent |
| **Total** | **82** | All passing |

## Error Correction Pattern

When a gate or hook rejects worker output:

```
Flash draft → Gate rejects (YAML enum / citation rot / language)
  → Brain sends error message BACK to Flash (new dispatch)
    → Flash fixes mechanically
      → Brain verifies final output only
```

**Anti-pattern**: Brain (Opus) manually fixing YAML typos, enum values, or
citation format. This wastes expensive tokens on mechanical corrections.

**Prevention**: Include domain constraints in dispatch prompt:
- YAML enum values: `genealogy_branch` must be one of `[architecture, automation, cognitive, design, governance, knowledge, operational, project]`
- Required fields: `title, tags, summary, epistemic_status, genealogy_level, genealogy_branch, created, last_updated`
- Language: ALL content must be English (no Vietnamese diacritics)
- Citations: no bare absolute paths to remote servers (triggers citation rot checker)
