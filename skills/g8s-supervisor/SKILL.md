---
name: g8s-supervisor
description: "Charter for operating g8s as the canonical supervisor: bounded worker dispatch (agy et al.) with supervisor and worker roles, multi-worker multi-role fan-out per task, evidence-verified acceptance, and leak-free upstream reporting."
domain: orchestration
version: 4.3.0
last_updated: "2026-10-21"
risk_level: medium
evidence_level: runtime_required
args_syntax: "g8s-supervisor [pilot|dispatch|fanout|contribute|audit] \"<objective>\" [--role <r>] [--root <dir>] [--timeout 300s] [--plan <file.json>] [--json]"
---

# g8s Supervisor Charter

`g8s` is the canonical worker supervisor: a Zero-CGO binary that queues tasks,
dispatches CLI workers (primary: `agy`), isolates them in process groups and
worktrees, and seals every run in a durable receipt. Its MCP server is the
Claude-facing transport; the agy-dispatch plugin translates g8s JSON-RPC
requests to AGY CLI args. **Claude remains the supervisor**: selects the slice, authorizes
mutation, verifies evidence, accepts output, and owns integration.

## Invocation & args

| Invocation | Mode | What happens |
|---|---|---|
| `g8s-supervisor` | — | Print this charter and the current loop position |
| `g8s-supervisor pilot "<objective>"` | Mode 1 | Qualify the g8s executable, run one bounded pilot, seal a pilot record |
| `g8s-supervisor dispatch "<objective>" --role scout --root <dir>` | Mode 2 | One admitted worker packet end-to-end (DoR → dispatch → verify → verdict) |
| `g8s-supervisor fanout "<objective>" --plan plan.json` | Mode 2 | Multi-worker, multi-role decomposition of ONE task (see `references/multi-worker-fanout.md`) |
| `g8s-supervisor contribute "<defect evidence>"` | Mode 3 | Verified g8s defect → sanitized packet for PiC review |
| `g8s-supervisor audit` | — | Zero-change audit: `git status`, receipt-vs-scope cross-check, leak scan of drafted evidence |
| `--json` | any | Machine-readable envelopes where the underlying g8s CLI supports them |

Unknown or missing args: state the gap, default to the smallest safe step
(`dispatch` with `--role scout`, `read_only`, one root). Never widen scope to
compensate for a missing arg.

## Two roles, one charter

| | SUPERVISOR (Claude) | WORKER (agy/claude/codex via g8s) |
|---|---|---|
| Owns | slice selection, oracle, scope, stop conditions, integration | one bounded mechanical packet |
| Proves | executable admission, DoR, dispatch authorization | cited evidence inside its allowed root |
| Verifies | receipt shape, citations, zero-change audit, acceptance checks | nothing outside its packet |
| Never | bypasses g8s transport; accepts uncited output | widens scope, writes without a receipt, reviews another worker |

The worker role is ALSO how you dogfood g8s itself: real dispatches produce
real receipts, telemetry, and failure modes — the raw material for honest
optimization. Optimization issue drafting requires at least one completed
task receipt (operator mandate 2026-09-27).

## Choose one mode

| Need | Mode | Load |
|---|---|---|
| Qualify executable or run bounded pilot | **Mode 1** | `references/mode-1-pilot-release.md` |
| Parallel project reconnaissance/drafts (1..N workers, mixed roles) | **Mode 2** | `references/mode-2-supervisor-worker.md` + `references/multi-worker-fanout.md` |
| Verified g8s defect or contribution | **Mode 3** | `references/mode-3-upstream-contribution.md` |
| Detect problems → report without leaking project data | **Security** | `references/security-redaction-playbook.md` |
| Admission/failure patterns | **Reference** | `references/binary-admission.md`, `references/anti-patterns.md` |
| Multi-project tenancy, cross-repo worktrees, atomic F1 quotas, verdict-vs-delivery, release mechanics | **Reference** | `references/operational-hard-lessons.md` (v4.2 addendum — field-proven contracts through 2026-10-14 cross-repo campaign) |

Read `references/hard-boundary.md` for every mode. Load only the chosen mode.
Verified delegation boundary: see [ADR-0033](../../docs/decisions/0033-f0-verified-delegation.md) for evidence-gated autonomy (T1–T3 supersede synchronous HITL; T4 requires PiC approval).

## F0 Evidence Toolkit (ADR-0033 & Wave B)

*Axiom: "Freedom on the road, toll at the booth — zero hard gates during exploration or F1 dispatch; deterministic hard gates at commitment points."*

The F0 supervisor evidence toolkit (`tools/f0`) provides machine-checkable verification across T0–T4:

- `f0 run -- <cmd...>`: wraps test/verify/deliver commands, records tamper-evident evidence (`ev-xxxxxxxx`) into the per-repo ledger, enforces G5 vacuous-pass guard (`items_checked == 0 => VACUOUS`).
- `f0 status [--repo <path>] [--pr <N>] [--report-block]`: read-only ground truth viewer and trusted status block generator (`<!-- f0:status -->`).
- `f0 gate check --repo <path> --action <merge|close|publish> (--pr <N> | --head-sha <S> --base <B>)`: deterministic commitment-point gate evaluating G0 (mandate), G1 (head evidence), G2 (forge CI), G3 (provenance), G4 (gate-appeasement detector, #625), G6 (claim citations), and G7 (per-criterion issue-closure gate, #626).
- `f0 execute merge --pr <N>`: T2 merge executor with post-merge verification (#627).
- `f0 verify-independent`: T3 independent worktree second verifier (#628).
- `f0 breaker status|reset`: error-budget circuit breaker monitoring and threshold reset (#629).
- `f0 digest`: read-only audit & session digest generation (#630).
- `<!-- f0:bundle -->`: portable ground-truth bundle ensuring `TreeSHA` equivalence and tamper-evident receipts across platforms.
- `f0 waive`: records scoped CI waivers tied to an open same-repo issue in the global ledger (debits error budget).
- `f0 manual`: tags un-attributed files as intentional supervisor edits (debits error budget unless `manual_free`).
- `f0 mandate`: views, checks, or mints campaign mandates (`~/.local/state/g8s/f0/mandate.json` cache + global ledger).
- `f0 anchor`: publishes or verifies external ledger head anchors (`F0-Ledger-Head: <seq>:<hash>`) in commit trailers or comments.
- `f0 audit`: scans default-branch commit history for merge/squash commits lacking verified gate entries or anchor trailers.

## F0 Cognitive Purity & Information Firewall (Brain vs. Hands)

Under `ADR-0033` (Phase 06b + Wave B), F0 enforces a strict **Cognitive Firewall**:
1. **Brain vs. Hands Division**:
   - F0 ONLY reasons, authors `BriefSchema` / `Spec`, reads structured receipts (`RoundReceiptSchema` from `g8s verify --task <id> --json`, `watch_verdict` from `g8s watch --pr <N> --json`, `f0 gate check --json`), and makes commitment-point decisions (`T0`–`T4`).
   - F0 NEVER manually hunts raw CI logs, scans disk bloat, runs verbose test suites, or executes procedural janitor tasks in F0's context window.
2. **Lossless-Signal / Zero-Noise Delegation**:
   - All log-hunting, CI failure extraction, documentation sync, formatting, and file editing are delegated to F1 workers (`agy` or `9router` combos: `fast-agent`, `butler-free-sre`, `docs-retriever`, `reasoning-free`, `deep-coding`).
   - Raw logs are isolated to disk at `$G8S_STATE_DIR/logs/verify-<task>-<ts>.log`, while F0 consumes only the compact, structured verdict.

## Universal Config-Driven LLM Call Consumer & 9Router Fleet (DELTA-25, v0.19.0)

With `v0.19.0` (`DELTA-25`), `g8s` acts as a universal, config-driven consumer for any remote HTTP LLM gateway declared in `providers.json` or `.g8s/agent-models.yml`:
- **Direct CLI Invocation**: `g8s llm-call --provider 9router --model <combo> --prompt "..." [--json] [--apply-files]`.
- **Worker Queue Execution**: `g8s submit --provider 9router --model <combo>` + `g8s worker --once`, seamlessly bridged via `g8s llm-call --worker-envelope` with duration, usage, and response capture.
- **Live Model Discovery**: `g8s providers discover [--provider 9router] [--json]` queries live `/v1/models` from upstream.
- **Multi-Model SWE Fleet**: Standard combos (`reasoning-free`, `reasoning-high`, `deep-coding`, `fast-agent`, `docs-retriever`, `butler-free-sre`) and prefix globs (`nvidia/*`, `openrouter/*`, `cc/*`, `cx/*`) provide high-availability upstream failover with zero vendor lock-in.

## The six laws

1. **Admission before dispatch.** Prove g8s executable identity and exact
   callable interface; for AGY prove the actual worker binding, role,
   `read_only`, sandbox, allowed root, durable receipt fields, and zero-change
   oracle. Source/docs/model name/quota/`--help`/policy acceptance do NOT
   prove dispatch capability.
2. **Packet before worker.** No dispatch without the minimum packet below.
   Default result is `BLOCKED` — never a broader prompt, never a direct CLI
   fallback (`agy` shell), never provider shopping.
3. **Read-only default & Native Receipt-Bound I/O.** Writes require separate
   explicit authorization (`AGY_MCP_ALLOW_WORKSPACE_WRITE=1`), path-scoped
   single-use receipt proof (`g8s receipt issue --path <glob>`), and worktree
   isolation (`RepoWorktreeIsolator` for sibling repos via `--scope-root <repo>
   --add-dir <repo>`, harvested via `g8s deliver <task-id> [--worktree <dir>]`).
   F1 workers automatically receive `G8S_TIER=F1`, `G8S_RECEIPT_ID`, and
   `G8S_SCOPE_ROOT` (inherited by F2 subagents) and should use native pure-Go
   `g8s search`, `g8s read --lines M:N`, `g8s edit --old ... --new ...`, and
   `--select <dot.path>` instead of unscoped shell utilities (`sed -i`, `awk`,
   `grep -rn`, `find`, `jq`).
4. **Verify before accept.** Cited output, task/receipt shape, oracle,
   baseline/status/diff, redaction. Worker output never promotes canonical
   knowledge or task authority. One packet per worker; workers never review
   each other.
5. **Sanitize before report.** Findings leave the workspace only as sanitized
   packets (`references/security-redaction-playbook.md`); never secrets,
   provider config, raw prompts, raw logs, or raw receipts.
6. **One slice at a time.** Start a new slice only after the current verdict.
   Parallelism requires disjoint roots (`<= 8–9 files/worker`) and one packet per worker.

## Minimum worker packet

```text
Objective and task class:
Allowed files/root:
Forbidden actions:
Role, permission, sandbox, timeout:
Expected redacted output and citations:
Acceptance oracle and zero-change check:
Evidence sink:
Competing hypotheses:
Stop/escalate when:
```

DoR fails if any field, live interface proof, executable identity, scope,
redaction plan, or receipt field is absent.

## Multi-worker fan-out (one task → N workers, mixed roles)

One issue/task may decompose into several worker packets. Decomposition is the
supervisor's duty and obeys: **disjoint read roots, homogeneous role per
packet, one packet per worker, supervisor joins results only after each packet
independently passes its oracle.** Canonical shapes (fan-out inventory,
hunt-verify, digest-join), the role matrix, budgets, and the join protocol
live in `references/multi-worker-fanout.md`. Fan-out is supervisor-side (N
submits); pass `--concurrency` to a single `g8s worker` process only when the
campaign owns a git checkout for worktree isolation.

## Evidence and failure loop

- Classify `kind:"error"` as candidate **fix** only after reproduction.
- Classify an **optimization** only when a reproduced g8s transport/contract
  behavior fails its own declared acceptance check. A failed project oracle is
  project evidence, not reason to optimize g8s.
- Missing, extra, uncited, contradictory, or scope-drifted evidence is
  **evidence gap**: reject/retest/stop — never broaden the prompt.
- Capture sanitized reproduction, executable identity, expected/actual,
  acceptance result, competing hypotheses, and affected scope.
- Produce an issue/PR packet for PiC review. Publish externally only when a
  verified defect, target repository, destination, and current action
  authorization are explicit. Never merge or push on the worker's behalf.

## Session loop (normal project work)

1. Pick ONE debt/work slice with input files, output schema, oracle, stop
   conditions.
2. Prove admission (law 1); emit the packet (law 2).
3. Dispatch read_only collector/scout/summarizer/verifier work through g8s —
   single packet or fan-out per `references/multi-worker-fanout.md`.
4. Verify per law 4; accept as sidecar evidence or reject/block.
5. Record the verdict in the campaign ledger (`**Session type**:` marker per
   ADR-0022), then start the next slice only after the verdict.

**Inversion:** if transport availability or a worker receipt is mistaken for
containment and useful work, parallel dispatch turns hidden scope drift into
faster failure. Stop at admission and verify independently.
