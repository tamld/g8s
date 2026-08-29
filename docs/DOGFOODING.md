# Self-Dogfooding Diagnostic Cycle

> **SSoT Reference**: Constitution Axiom 5 (*Empirical Verification & Post-Scan*) & DEBT-27 (Issue #114).  
> **Package / Tooling**: `tools/dogfood_report.sh` | `cmd/g8s/self_audit.go` | `.github/workflows/dogfood.yml`

---

## 1. Overview & Architectural Motivation

g8s uses itself to develop and operate g8s ("dogfooding"). In shipping previous releases, real-world usage surfaced gaps before users encountered them (e.g., temporary brief storage gaps and worktree accumulation during multi-agent dispatch). 

To ensure continuous system health and prevent silent regressions, g8s codifies an automated **Self-Dogfooding Diagnostic Cycle**. This cycle executes regularly in CI and on demand via CLI to enforce four core runtime capabilities.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           g8s Self-Dogfooding Engine                        │
├───────────────────┬───────────────────┬───────────────────┬─────────────────┤
│   Check 1         │   Check 2         │   Check 3         │   Check 4       │
│   Brief Roundtrip │   Worktree Leak   │   Expired Briefs  │   Consumed      │
│   (make dogfood)  │   Scanner         │   Ledger Query    │   Ledger Audit  │
└───────────────────┴───────────────────┴───────────────────┴─────────────────┘
                                     │
                 ┌───────────────────┴───────────────────┐
                 ▼                                       ▼
       [Human Summary Table]                   [Machine JSON Report]
```

---

## 2. What Is Dogfooded (The 4 Core Checks)

The diagnostic cycle exercises four non-negotiable subsystems:

| Check ID | Target Command | Primary Verification Rule | Failure Mode Detected |
| :--- | :--- | :--- | :--- |
| **`make_dogfood`** | `make dogfood` | Exit code 0 on `brief-issue` $\rightarrow$ `brief-consume` cycle | Broken brief serialization, WAL lockups, schema mismatch |
| **`cleanup_worktrees`**| `g8s cleanup-worktrees --older-than 24h --dry-run` | Exit code 0, safe evaluation of linked worktrees | Orphaned agent worktrees, git worktree parsing bugs |
| **`brief_list_expired`**| `g8s brief-list --expired --json` | Exit code 0 querying expired brief state | Database corruption, query index regression, time conversion errors |
| **`brief_list_consumed`**| `g8s brief-list --consumed --json` | Exit code 0 verifying consumed brief ledger | Ledger state inconsistency, status transition drop |

---

## 3. How to Execute Self-Audit

### A. CLI Command (`g8s self-audit`)
The `self-audit` subcommand is a native wrapper that streams diagnostic reports to standard output:

```bash
# Run standard audit report
g8s self-audit

# Emit pure machine-readable JSON (ideal for piping into jq or log aggregators)
g8s self-audit --json

# Save JSON report to a specific file
g8s self-audit --output /tmp/dogfood-report.json
```

### B. Direct Shell Script (`tools/dogfood_report.sh`)
The underlying script is pure POSIX bash and runs across macOS, Linux, and CI runners:

```bash
# Run directly
bash tools/dogfood_report.sh

# Run with custom g8s binary path
bash tools/dogfood_report.sh --bin /usr/local/bin/g8s

# Run in JSON mode
bash tools/dogfood_report.sh --json
```

### C. Automated Weekly CI Schedule
The GitHub Action in `.github/workflows/dogfood.yml` runs every **Monday at 09:00 UTC**.
- When an audit fails, it automatically opens an issue labeled `bug` with full failure logs and reproduction instructions.
- The workflow can also be triggered manually via `workflow_dispatch`.

---

## 4. Reading the Audit Report

### Human-Readable Output Format
```text
================================================================================
                      g8s SELF-DOGFOODING AUDIT REPORT
================================================================================
Timestamp: 2026-08-29T18:30:00Z | Total: 4 | Passed: 4 | Failed: 0 | Skipped: 0 | Status: PASSED

No.  | Check Description                        | Status   | Duration  
-----+------------------------------------------+----------+-----------
1    | Brief Roundtrip (make dogfood)           | PASS     | 320ms     
2    | Worktree Leak Scanner (cleanup-worktrees) | PASS     | 45ms      
3    | Expired Briefs Query (brief-list --expired) | PASS     | 12ms      
4    | Consumed Briefs Audit (brief-list --consumed) | PASS     | 14ms      
================================================================================
```

### Machine-Readable JSON Schema (`report.json`)
```json
{
  "version": "1.0.0",
  "timestamp": "2026-08-29T18:30:00Z",
  "overall_status": "PASSED",
  "total_checks": 4,
  "passed_checks": 4,
  "failed_checks": 0,
  "skipped_checks": 0,
  "duration_ms": 391,
  "checks": [
    {
      "id": "make_dogfood",
      "name": "Brief Roundtrip (make dogfood)",
      "command": "make dogfood",
      "status": "PASS",
      "duration_ms": 320,
      "pass_rule": "Exit code 0 with successful brief issuance and consumption",
      "details": "..."
    }
  ]
}
```

---

## 5. How to Add a New Check

To add a new health check to the dogfooding suite:

1. Open `tools/dogfood_report.sh`.
2. Call `run_check` with four parameters:
   - `check_id`: Unique snake_case identifier (e.g. `vault_integrity`).
   - `check_name`: Descriptive human-readable label.
   - `check_cmd`: Shell command string to execute.
   - `pass_rule`: Description of the expected pass condition.

```bash
# Example: Adding a vault consistency check
run_check "vault_integrity" \
    "Vault Tri-Anchor Integrity (vault list)" \
    "\"$G8S_BIN\" vault list --limit 1 --json" \
    "Exit code 0 verifying Tri-Anchor query engine accessibility"
```

3. Add test coverage in `tools/dogfood_report_test.sh` to ensure the new check is tracked in the JSON report.
4. Update this documentation to reflect the expanded check catalog.

---

## 6. Disabling a Check (Emergency Override)

### When to Disable
Checks should **only** be disabled under strict conditions:
- An external dependency is temporarily unreachable in a local offline environment.
- A breaking architectural migration is actively under development in a non-main branch.

> [!WARNING]
> Disabling a dogfood check in production or CI requires an explicit tracking issue reference in git history. Never silently disable checks.

### How to Disable
Pass the `--skip` argument or configure the `G8S_DOGFOOD_SKIP` environment variable with a comma-separated list of check IDs:

```bash
# Skip a single check via flag
g8s self-audit --skip cleanup_worktrees

# Skip multiple checks via environment variable
export G8S_DOGFOOD_SKIP="cleanup_worktrees,brief_list_expired"
g8s self-audit

# Skip all checks (dry audit run)
G8S_DOGFOOD_SKIP="all" tools/dogfood_report.sh
```
