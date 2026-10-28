---
name: g8s-supervisor
description: "Charter for operating g8s as the canonical supervisor: bounded worker dispatch (agy et al.) with supervisor and worker roles, multi-worker multi-role fan-out per task, evidence-verified acceptance, and leak-free upstream reporting."
domain: orchestration
version: 4.5.0
last_updated: "2026-10-25"
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
| Documentation standards, pedagogical README, single MIT license, zero-stale manifests, evidence-bound claims | **Reference** | `references/doc-standard.md` (v4.3 addendum — 10-section README, zero-stale paths, single MIT, no unverified claims) |

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
   - All log-hunting, CI failure extraction, documentation sync, formatting, and file editing are delegated to F1 workers (`agy` or `9router` combos: `fast-agent`, `docs-retriever`, `reasoning-free`, `deep-coding`).
   - Raw logs are isolated to disk at `$G8S_STATE_DIR/logs/verify-<task>-<ts>.log`, while F0 consumes only the compact, structured verdict.

## Mandatory 3-Pillar Engineering Reflex (Research -> Dialectical Root-Core -> Skill Chaining)

Before designing or dispatching any non-trivial feature, architectural change, or multi-slice campaign, F0 MUST execute this 3-Pillar Engineering Reflex:

1. **Pillar 1 — Upstream/GitHub Success-Case Research Gate (`ck:xia` + Multi-Worker Research Fan-Out)**:
   - Never design in a vacuum. Before drafting a plan, dispatch parallel `read_only` research scouts (`g8s submit --role scout --permission read_only`) or activate `ck:xia` + `ck:research` to inspect battle-tested open-source implementations on GitHub (e.g., `getzep/graphiti` & `letta-ai/letta` for bi-temporal RRF memory, `SWE-agent` & `Aider` for Agent-Computer Interface diagnostics, `zkat/miette` for actionable error envelopes, `open-telemetry/weaver` & `semgrep` for AST/live conformance auditing).
   - Every architectural proposal must cite concrete `Local`, `Upstream`, and `Docs` evidence.
2. **Pillar 2 — Multi-Round Root-Core Dialectical Loop (`wiki-deep-why` -> `wiki-socratic` -> `ck:predict`)**:
   - Do not stop at surface symptoms (Level 1 WHAT) or local mechanisms (Level 2 HOW). Iterate across at least 3 dialectical rounds to reach the Root-Core Invariant (Level 3 WHY):
     - **Round 1 (`wiki-deep-why`)**: Trace `Surface Symptom -> Structural Mechanism -> Root Architectural Core`.
     - **Round 2 (`wiki-socratic`)**: Formulate the Socratic Antithesis (`Thesis -> Antithesis -> Synthesis`) to falsify naive assumptions (e.g., self-contained binary independence vs. external tool coupling, scale-free RRF vs. ad-hoc SQL sorting).
     - **Round 3 (`ck:predict`)**: Stress-test the synthesis across Architecture, Security, Reliability, Performance, and DX/Operator personas before locking the spec.
3. **Pillar 3 — Deterministic Skill Routing & Chaining (`skill-router.py --strict`)**:
   - Query `python3 ~/.agents/skills/_shared/scripts/skill-router.py --strict "<task>"` at task intake and chain skills deterministically across the full lifecycle:
     `ck:xia` / `ck:research` -> `wiki-deep-why` -> `wiki-socratic` -> `ck:predict` -> `ck:plan` -> `prove` (FSM + RED test before code) -> `ck:cook` -> `code-simplification` -> `g8s feature audit --live`.
4. **Experiential Mastery Closed Loop (`g8s coordinator intake|mastery` & `g8s memory autopsy|outcome|compound`)**:
   - Run `./bin/g8s coordinator intake --goal "<objective>" [--files "<paths>"] --json` (`#688`) to mechanically synthesize feature orientation, upstream research gate, 3-round Root-Core probes, and tool/skill/worker routing.
   - Harvest failure-to-fix lessons via `./bin/g8s memory autopsy --task <id> --auto-propose --json` (`#689`), record corroborations/counter-examples via `./bin/g8s memory outcome --ids <id> --session <sid> --passed=true|false` (`#690`), compound corroborated entries into `DOCTRINE` via `./bin/g8s memory compound`, and audit domain mastery via `./bin/g8s coordinator mastery --json`.


## Universal Config-Driven LLM Call Consumer, 9Router Fleet & Zero-Trust Execution Hardening (DELTA-25..31, v0.19.1)

With `v0.19.0`–`v0.19.1` (`DELTA-25..31`), `g8s` acts as a universal, config-driven consumer for any remote HTTP LLM gateway declared in `providers.json` or `.g8s/agent-models.yml` with strict zero-trust execution boundaries:
- **Direct CLI Invocation**: `g8s llm-call --provider 9router --model <combo> --prompt "..." [--json] [--apply-files]`.
- **Worker Queue Execution & Pin Affinity (`DELTA-27`, `#657`)**: `g8s submit --provider 9router --model <combo>` + `g8s worker --once [--provider <p>] [--model <m>]`, enforcing strict claim affinity across both serial (`RunOnce`) and concurrent (`RunConcurrent`) worker loops without silent cross-provider/model hijacking.
- **Strict Output Schema Validation (`DELTA-29`, `#659`)**: `g8s submit --output-schema <file.json>` enforces recursive JSON Schema Draft-7 validation (`required`, `type`, `enum`, `minLength`, `minItems`, nested `properties`/`items`, `additionalProperties`) on worker JSON responses, failing closed (`StateFailed` + `SCHEMA_VALIDATION_ERROR`) on malformed or truncated output.
- **Fail-Closed Write Isolation (`DELTA-26`, `#656`)**: `workspace_write` tasks unconditionally require isolated git worktree creation (`RepoWorktreeIsolator` / `DefaultWorktreeIsolator`). Any worktree acquisition failure transitions the task directly to `FAILED` without spawning the child process against the live checkout.
- **Receipt Export Visibility & Retry (`DELTA-28`, `#658`)**: Every `g8s worker` envelope includes structured `receipt_export: {status, path, attempts, last_error}`, and `g8s receipt-export --task <id> [--json]` allows idempotent recovery from SQLite without re-running the worker.
- **Mechanical DoD Check Binding (`DELTA-31`, `#648`) & Non-Loopback Auth (`DELTA-30`, `#655`)**: `EvaluateDoDMatrix` requires real executed `VerificationCheck` records (`exit_code=0`, matching check ID) before marking any criterion `MET`; `g8s serve` refuses non-loopback binds (`0.0.0.0`, `::`, LAN IPs) unless `G8S_API_TOKEN` is configured.
- **System-1 Reflex (`Jev`) Routing & 3-Tier Fallback (`Sensor ≠ Decider`)**:
  - **Tier 1 (Jev Online)**: `g8s reflex triage --summary "..." --files "..."` (`source: "jev", is_fallback: false`) scores `risk_score`, `breach_prob`, ALDC lane (`0/D/R/F/S`), and recommends capability class (`fast-agent`, `docs-retriever`, `reasoning-free`, `deep-coding`, `reasoning-high`, `agy`) + verification skill bundle. F0 retains final `Decider` authority.
  - **Tier 2 (Deterministic Rule Engine + F1 Scout)**: When Jev is offline (`is_fallback: true`), `.g8s/effort-classes.yml`, `.g8s/lane-bundles.yml`, `.g8s/trust-boundaries.yml`, and `skill-router.py --strict` deterministically map path globs and blast radius; if ambiguity remains, F0 dispatches a cheap `read_only` F1 scout (`fast-agent` / `reasoning-free`) for 15–30s reconnaissance.
  - **Tier 3 (F0 Direct Brain Override)**: Reserved strictly for architectural tradeoffs (`T3`/`T4`), cross-worker contradictions, and final `g8s deliver` / commit / merge judgment.
- **Two-Layer Separation Rule**: Public repo mechanisms and standard models remain strictly separated from untracked local policies (`~/.config/g8s/providers.json` and `~/.agents/skills/`). Never commit homelab LAN IPs or private topology.
- **Orthogonal Stratum Federation (`DELTA-33` / `ADR-0035`) & Verified `9router` Strategic Routing**:
  - **Axis X (Containment Rings)**: `F0` (Host/Brain), `F1` (Isolated git worktree + `Setpgid` worker), `F2` (Nested subagent/subtask `F2 ⊆ F1 ⊆ F0`).
  - **Axis Y (Control Strata)**: `S0 Principal` (Human) $\to$ `S1 FederationCoordinator` (Multi-repo charter allocator via `g8s charter issue|verify|revoke|complete|portfolio`) $\to$ `S2 DomainGovernor` (Single-repo sovereign supervisor) $\to$ `S3-M BoundedMutator` / `S3-A BlindAdjudicator` + `O-Plane TelemetryProjector`.
  - **Live-Verified `9router` Quota-Saving Model Matrix**:
    - `deep-coding`: Primary complex coding & multi-file F1 worker combo (`S3-M`).
    - `reasoning-high`: Deep Socratic inversion, RCA, and architectural analysis combo.
    - `docs-retriever`: Fast repository reconnaissance, scouting, and documentation sync combo.
    - `cl/anthropic/claude-sonnet-4.6` & `cl/anthropic/claude-haiku-4.5`: Anthropic family routes for disjoint-family `S3-A` / `T3` blind adjudication.
    - `cl/openai/gpt-4.1-mini` & `cl/google/gemini-2.5-flash`: Fast OpenAI / Google Gemini routes for cross-family verification.
  - **OS-Level MCP Integration**: Use registered MCP servers `g8s` (`~/.local/bin/g8s mcp`, 19 tools including `g8s_charter_issue`, `g8s_charter_verify`, `g8s_portfolio_status`) and `wiki-mcp` (`~/.local/bin/mcp-wiki.py`, 8 tools wrapping `wiki.py`) to keep capability delegation and durable knowledge retrieval native in every session.

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
