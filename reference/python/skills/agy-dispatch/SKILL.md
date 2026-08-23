---
name: "agy-dispatch"
description: "Use when Codex should delegate bounded read-only artifact collection, path inventory, frontmatter extraction, or log summarization to the local AGY CLI using a fast Gemini Flash worker. Codex remains the reasoning and verification authority."
domain: "delegation"
version: "0.4.0"
risk_level: "high"
---

# AGY Dispatch

## Mission

Use the local `agy` CLI as a fast mechanical worker for scoped collection tasks. This skill is for muscle work: inventory, extraction, candidate listing, first-pass summaries, MCP mapping, and skill mapping.

Codex keeps ownership of:

- final interpretation;
- architectural decisions;
- wiki import and promotion;
- security and privacy boundaries;
- verification before any completion claim.

## Hard Boundaries

- Default permission profile is `read_only`.
- Do not ask AGY to edit, commit, delete, install, or mutate project files unless a non-default permission profile is explicitly selected by Codex.
- Do not ask AGY to read `.env`, private keys, token stores, password files, identity documents, or raw corporate-confidential content.
- For company sources, request metadata and reusable procedure summaries only.
- AGY output is evidence input, not truth. Verify important claims against disk or `wiki.py`.
- `--dangerously-skip-permissions` is allowed only through the harness flag `--skip-permissions`, and only when the selected permission profile allows it.
- Durable tasks require explicit scope roots, reject broad roots containing known credential stores, and do not accept custom executable paths.
- Use `parent_task_id` for clarified follow-up work; never mutate a paused task specification in place.

## Role and Permission Harness

Use worker roles instead of free-form delegation:

- `collector`: inventories paths, headings, metadata, reusable procedures.
- `scout`: finds candidate modules, skills, MCP servers, harnesses, loops, and project artifacts.
- `mcp-mapper`: maps MCP tools, providers, permissions, and runtime boundaries.
- `skill-map`: use `collector` or `scout` role with `--mode skill-map` for skills and operating procedures.
- `summarizer`: compresses existing artifacts without promoting claims.
- `verifier`: checks bounded claims against evidence.
- `test-runner`: runs explicit safe verification commands and reports exit codes.

Permission profiles:

- `read_only`: default; uses AGY sandbox; no mutation; no permission skipping.
- `automation_read`: read-only automation that may pass `--dangerously-skip-permissions` through the harness.
- `workspace_write`: future profile for explicit workspace mutation; the MCP minimum surface blocks it until a separate human-approved plan defines rollback, observability, and tests.

## Default Command

Prefer the MCP tools when this plugin is installed in a Codex thread:

- `agy_list_roles`: inspect role contracts.
- `agy_list_permissions`: inspect permission profiles and disabled surfaces.
- `agy_self_awareness`: check AGY binary availability and runtime boundaries.
- `agy_dispatch_task`: dispatch one bounded worker task through the harness.
- `agy_submit_task`: queue an idempotent durable read-only task.
- `agy_get_task`: inspect sanitized state without returning the raw prompt.
- `agy_list_tasks`: list durable task states.
- `agy_cancel_task`: request cancellation; the separate worker kills its local process tree.

The synchronous tool remains available for compatibility. Prefer the durable tools when the task needs retry lineage, cancellation, or status polling. Run `python3 ~/plugins/agy-dispatch/scripts/agy_worker.py` as a separate process to execute queued tasks. Durable dispatch remains application-only, requires at least one explicit `add_dirs` scope, and keeps `workspace_write`, `no_sandbox`, and custom AGY binaries disabled.

On macOS, a local operator may explicitly install the user-level worker service with `python3 ~/plugins/agy-dispatch/scripts/agy_service.py install`. The service manager pins canonical Python, worker, and AGY paths; blocks new claims through a leased maintenance gate; refuses lifecycle changes while tasks are active unless the operator uses `--force`; keeps task output out of service logs; and preserves state during uninstall.

When MCP tools are not available in the current thread, use the dispatcher script from this plugin:

```bash
python3 ~/plugins/agy-dispatch/scripts/agy_dispatch.py \
  --role collector \
  --permission read_only \
  --model "Gemini 3.5 Flash (Low)" \
  --add-dir /path/to/scope \
  --prompt "Collect a read-only inventory and return JSON."
```

For multi-scope collection:

```bash
python3 ~/plugins/agy-dispatch/scripts/agy_collect.py \
  --root /path/to/root \
  --mode inventory \
  --role collector \
  --permission read_only \
  --out-dir /path/to/reports
```

For MCP dispatch architecture reviews:

```bash
python3 ~/plugins/agy-dispatch/scripts/agy_collect.py \
  --root /path/to/scope \
  --mode mcp-map \
  --role mcp-mapper \
  --permission automation_read \
  --skip-permissions \
  --model "Gemini 3.5 Flash (High)" \
  --out-dir /path/to/reports
```

## Report Contract

Ask AGY to return:

- `scope`: exact paths inspected;
- `commands_or_methods`: how it collected evidence;
- `files_considered`: key files or counts;
- `findings`: concise bullets;
- `sensitive_flags`: any skipped or suspicious paths;
- `uncertainty`: what it could not prove;
- `recommended_next_actions`: concrete follow-up for Codex.

## Good Use Cases

- Build a file inventory for a large extracted archive.
- Extract headings/frontmatter from many markdown files.
- Find MCP, skill, harness, loop, or project candidates.
- Summarize long command output or test logs.
- Prepare a candidate report for Codex to verify.

## Bad Use Cases

- Deciding whether a rule is valid.
- Promoting knowledge to `validated`.
- Importing raw company or identity data into a personal vault.
- Editing `wiki.py` or governance files.
- Running destructive cleanup.

## Verification

After AGY returns a report:

1. Re-check critical file paths locally with `rg`, `find`, `sed`, or `wiki.py`.
2. If creating wiki artifacts, run `uv run python wiki.py search "<topic>"` first.
3. Verify durable notes with `uv run python wiki.py verify <path>`.
4. State any unverified AGY claims as hypotheses.
