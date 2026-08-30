---
version: 1.0
based_on: aegis prompt 2026-08-30
---

<!--
Attribution: This template was originally drafted by the aegis
project PIC agent (see https://github.com/tamld/aegis) and
adapted for the g8s repo. Per the clean-room policy in
docs/ORCA_AUDIT.md, no code is copied; the design language
and structure are adapted to fit g8s's documentation style.
-->

# g8s Integration Prompt Template for Project PIC Agents

> **Purpose**: A compact, code-first onboarding template for Lead/PIC (Person-In-Charge) AI Agents integrating `g8s` (The Gatekeepers) multi-agent worker execution harness into their projects.
>
> **Related Documentation**:
> - Detailed Contribution Guide: [`docs/CONTRIBUTING_TO_G8S.md`](CONTRIBUTING_TO_G8S.md)
> - Standardized Task Brief: [`examples/briefs/CONTRIBUTOR_BRIEF.md`](../examples/briefs/CONTRIBUTOR_BRIEF.md)

---

## 1. Core Loop

Integrate and drive the `g8s` worker runtime using this 5-step lifecycle:

```bash
# 1. Probe available CLI worker providers and system health
g8s providers --jsonl

# 2. Submit a sandboxed task to the local SQLite control plane
g8s submit --role scout --prompt "Audit AST symbols in src/" --add-dir /path/to/project --jsonl

# 3. Dispatch a worker turn against pending queue tasks
g8s worker --once --jsonl

# 4. Check worker process status and verify execution envelope
g8s status --worker --jsonl

# 5. Prune stale worktrees and expired runtime artifacts
g8s cleanup --stale-hours 1
```

---

## Pin to a specific release (not local build)

When integrating g8s into another project, ALWAYS pin to a
release tag, not the local `~/.local/.../bin/g8s` (or local build) binary.

```bash
# Get the latest release (preferred)
G8S=$(gh release view --repo tamld/g8s --json tagName --jq .tagName)
PLATFORM=$(uname -s | tr A-Z a-z)
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
G8S_URL="https://github.com/tamld/g8s/releases/download/${G8S}/g8s_${G8S#v}_${PLATFORM}_${ARCH}.tar.gz"
curl -L "$G8S_URL" | tar xz
G8S=./g8s
```

**Why this matters:**
- GHA releases include all merged fixes since the last tag
- Local builds may be stale (especially after rebase/cherry-pick)
- The aegis agent on 2026-08-30 reported 4 BUGs that were ALREADY
  fixed in main but not yet in a release — if aegis had used a
  stale local build, it would have missed those fixes entirely

**Don't use** a local `~/.local/.../bin/g8s` or workspace build for production integration.
That's only for the Sisyphus supervisor's own testing.

### Discover the latest g8s version

```bash
gh release view --repo tamld/g8s --json tagName,publishedAt
```

Output:
```json
{
  "tagName": "v0.4.0",
  "publishedAt": "2026-08-30T..."
}
```

Cross-check this against the BUG list. If a BUG you found is
already in the latest release's CHANGELOG, do NOT report it
again — it was already fixed.

---

## 2. Golden Rule: Parse `stdout` Envelope Before Trusting State

**Never assume `task.state == "SUCCEEDED"` or `result.ok: true` implies success.**

Task status is an execution claim, not verified proof:
- A worker subprocess may crash, exit with CLI errors (e.g. unknown flag), or emit an error envelope while the outer orchestrator records a process termination.
- Always unmarshal and inspect the JSON payload in `stdout` / `result`:
  ```json
  {
    "kind": "error",
    "cmd": "--prompt-file",
    "code": "USAGE_ERROR",
    "message": "Unknown command --prompt-file"
  }
  ```
- **Invariant**: If `kind == "error"` or the inner payload is missing expected domain artifacts, treat the task as **FAILED**, regardless of the outer status code. Success requires evidence, not status theater.

---

## 3. Self-Update Instruction: Live Issue Probing

Static prompt summaries and issue status markers rot over time.

When initializing your context or handling g8s errors, **do not rely on hardcoded issue lists**:
```bash
# Query active open issues and capability requests fresh on read
gh issue list --repo tamld/g8s --state open --limit 10

# Check current installed version against latest releases
g8s version --jsonl
gh release list --repo tamld/g8s --limit 3
```

Always verify whether a known issue has been fixed in a newer release of `g8s` before writing bespoke workarounds.

---

## 4. Quota Economics

`g8s` is architected around a strict **Two-Tier Multi-Agent System**:

```text
┌────────────────────────────────────────────────────────┐
│            BRAIN TIER (Orchestrator: PIC)              │
│  • High intelligence (Claude 3.7 Sonnet, GPT-4o, R1)   │
│  • High token cost ($3.00 - $15.00 / 1M tokens)        │
│  • Holds architectural state, issues Write Receipts     │
└───────────────────────────┬────────────────────────────┘
                            │ Delegates mechanical work
                            ▼
┌────────────────────────────────────────────────────────┐
│            WORKER TIER (Muscle: g8s Sandboxed)         │
│  • Lightweight / Local models (Gemini Flash, Haiku)    │
│  • Ultra low cost ($0.10 - $0.40 / 1M tokens, or free) │
│  • Fast startup (<15ms Go harness), bounded directory  │
└────────────────────────────────────────────────────────┘
```

- Keep orchestrator context lean: delegate code indexing, test execution, linting, and AST extraction to lightweight CLI workers.
- Avoid burning expensive Brain tokens on large raw file reads or repetitive trial-and-error loops.

---

## 5. Hard Boundaries

When integrating `g8s` into your project:

1. **Never modify the `g8s` repository directly**: Consume `g8s` as a standalone compiled binary or external tool dependency.
2. **Zero-CGO & Path Traversal Lockdown**: `g8s` sandboxes strictly forbid access to sensitive paths (`.ssh`, `.aws`, `.env`, symlink escapes) and destructive commands (`rm -rf`, `drop database`).
3. **No Direct Writes Without Receipts**: Worker subagents cannot mutate codebase files unless presented with a time-limited, cryptographic write receipt issued by the Brain.
4. **Clean Process Hygiene**: Always prune stale workers, orphan processes, and scratch worktrees via `g8s cleanup`.

---

## 6. Contribution Back Format

When encountering bugs, missing CLI providers, or documentation gaps in `g8s`, contribute fixes or report issues back using the **Pilot + Evidence** model:

1. **File an issue** on [`github.com/tamld/g8s/issues`](https://github.com/tamld/g8s/issues) or submit a brief using [`examples/briefs/CONTRIBUTOR_BRIEF.md`](../examples/briefs/CONTRIBUTOR_BRIEF.md).
2. **Include 4 mandatory fields**:
   - **Context**: 1–2 sentences on your project environment and workflow.
   - **Repro**: Exact CLI command or script snippet.
   - **Evidence**: `file:line`, commit SHA, and `trace_id` from the JSON envelope.
   - **Expected vs Actual**: Exact mismatch between expected behavior and observed output.
3. Consult [`docs/CONTRIBUTING_TO_G8S.md`](CONTRIBUTING_TO_G8S.md) for full submission and triage protocols.

---

> Attribution: This template was originally drafted by the aegis project PIC agent (see https://github.com/tamld/aegis) and adapted for the g8s repo. Per the clean-room policy in docs/ORCA_AUDIT.md, no code is copied; the design language and structure are adapted to fit g8s's documentation style.
