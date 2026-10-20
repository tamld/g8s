# Hard Boundary Rules

> Loaded by every `g8s-supervisor` mode.

## Default authority

- g8s supervises workers. Claude selects scope, authorizes actions, verifies evidence, and accepts output.
- g8s MCP is transport. `agy-dispatch` is compatibility bridge. Neither proves worker identity,
  containment, output quality, canonical-write authority, or permission elevation.
- Normal bounded project work may use a runtime-admitted `read_only` worker. No blanket ban on
  g8s status, task inspection, or submission after explicit task-level authorization and DoR.

## Stop boundary

Commitment points require `f0 gate check` PASS; exploration is free (Axiom 1: Freedom on the road, toll at the booth). See [ADR-0033](../../../docs/decisions/0033-f0-verified-delegation.md).

| Tier | Name | Governed Actions | Authorization & Gate Requirements |
|---|---|---|---|
| `T0` | Free / Observation | Local exploration, worktree edits, F1 dispatch via WriteReceipt, read files, search codebase, reason, draft plans, `f0 status` | Free exploration; path-scoped WriteReceipt for F1 mutations; zero hard gates (Axiom 1). |
| `T1` | Low-Impact Mutation | Push branch, open PR, comment, close issue | Requires active campaign mandate (`T1+`) and `f0 gate check` PASS; closing an issue requires per-criterion evidence (G0, G6, G7). |
| `T2` | Standard Merge | Merge PR to default branch (outside trust boundaries and excluded paths) | Requires active campaign mandate (`T2+`), `f0 gate check --action merge` PASS (G0–G6), and `F0-Ledger-Head` anchor trailer. Pre-created revert, watch, and auto-revert enforced. |
| `T3` | High-Impact Merge | Merge touching trust boundaries / `LaneSecurity` / `excluded_paths` | Escalated automatically from `T2`; requires `T3` mandate plus second independent verifier (`g8s eval` probes or distinct provider); disagreement or missing verifier = automatic veto & escalate to PiC (Decision D9). |
| `T4` | Critical / Sovereign | Release/tag, force-push, deleting refs, credentials/Keychain, editing `hooks.json` or ledger | Never pre-authorized; mandate cannot authorize T4; always requires synchronous PiC approval (`force_ask`). |
| g8s source fix | Internal Fix | Fix to g8s source code | Reproduced blocker plus PiC approval of named local scope; then follow `g8s/AGENTS.md` and OpenSpec-first flow in owned worktree |

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
