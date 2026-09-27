# Hard Boundary Rules

> Loaded by every `g8s-supervisor` mode.

## Default authority

- g8s supervises workers. Claude selects scope, authorizes actions, verifies evidence, and accepts output.
- g8s MCP is transport. `agy-dispatch` is compatibility bridge. Neither proves worker identity,
  containment, output quality, canonical-write authority, or permission elevation.
- Normal bounded project work may use a runtime-admitted `read_only` worker. No blanket ban on
  g8s status, task inspection, or submission after explicit task-level authorization and DoR.

## Stop boundary

| Action | Requirement |
|---|---|
| Submit/monitor `read_only` task | Explicit packet, current executable/interface proof, sandbox, scope, receipt fields, zero-change oracle |
| `workspace_write`, write receipt, worktree/branch | Separate human-approved writable campaign and exact path containment proof |
| Config, installation, upgrade, provider configuration | Separate explicit authorization; never inspect credentials or secrets |
| Remote issue/PR publication, push, merge | Verified packet plus named repository/destination/action authorization; PiC reviews first |
| g8s source fix | Reproduced blocker plus PiC approval of named local scope; then follow `g8s/AGENTS.md` and OpenSpec-first flow in owned worktree |

Never use direct AGY shell, `--dangerously-skip-permissions`, `--no-sandbox`, credentials, raw
prompts, raw logs, provider configuration, or sensitive paths as workaround.

## Evidence rules

- Task state, exit code, release tag, docs, local source HEAD, and worker assertion are transport
  evidence only. Verify cited output, declared oracle, actual scope, baseline/status/diff, and redaction.
- `kind:"error"` ends that attempt. Reproduce before calling it a g8s fix.
- Missing/extra/uncited evidence, failed oracle, scope drift, or unexpected change is `BLOCKED` or
  `EVIDENCE_GAP`; do not widen scope or retry with bypasses.
- Records contain sanitized structured facts only. Exclude prompts, tokens, API keys, provider
  configuration, raw stdout/stderr, credentials, and sensitive paths.
