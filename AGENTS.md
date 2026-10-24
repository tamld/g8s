# AGENTS.md — Agent-First Onboarding & Execution Protocol for g8s

> **SYSTEM DIRECTIVE FOR AI AGENTS**: Any AI Agent (Claude, Cursor, Copilot, Codex, Antigravity) entering this repository MUST read this document first. This repository uses the **Spec-Driven Development (SDD)** framework and enforces strict **Zero-Trust Capability Delegation**.

---

## 0. The Machine-Readable SSoT Manifest & Declarative Registries

For programmatic inspection without parsing prose, read [`manifest.json`](manifest.json) at the repository root and the declarative governance registries under `.g8s/`:
- [`manifest.json`](manifest.json): JSON schema maps, active OpenSpec deltas, roles, permission matrices, and release metadata.
- [`.g8s/agent-models.yml`](.g8s/agent-models.yml) & [`.g8s/effort-classes.yml`](.g8s/effort-classes.yml): Provider-neutral model effort catalog (`named` / `budget` / `baked-name` / `toggle`) and path/signal-driven effort-class defaults.
- [`.g8s/verifier-classes.yml`](.g8s/verifier-classes.yml), [`.g8s/lane-bundles.yml`](.g8s/lane-bundles.yml), [`.g8s/trust-boundaries.yml`](.g8s/trust-boundaries.yml): Machine-check acceptance verifiers, ALDC gate-lane routing (`0/D/R/F/S`), and P0 trust boundaries.

---

## 0.1. Vocabulary Reservation & Recursive Agent Architecture: F0 / F1 / F2

Bare tokens **F0/F1/F2 are RESERVED** for the exponential containment tiers ([`docs/decisions/0032-containment-levels.md`](docs/decisions/0032-containment-levels.md)):
- **F0 — Brain / Supervisor Tier**: Full trust; exclusive authority holder (architecture, git commits, vault promotion, write receipt issuance). `Operator ↔ F0 = 1-1`.
- **F1 — Worker Tier**: Spawned BY **F0** THROUGH the `g8s` harness (`g8s submit` + `g8s worker`: queue claim, CAS lease, path-scoped single-use Write Receipt, isolated git worktree, killable OS process group `Setpgid`). `F0 ↔ F1 = 1-n` fan-out.
- **F2 — Subagent Tier**: Spawned recursively BY **F1** workers themselves inside the worker's platform (`define_subagent` / `invoke_subagent`) by default, sharing F1's sandbox and receipt boundary (`F2 ⊆ F1 ⊆ F0`). Recursive `g8s` (`F1` calling `g8s submit`) is reserved and ADR-gated.
- **Cardinality Law**: `1 issue = 1 worker = 1 receipt` (strict `1-1`); **`F1 ↔ F1 = 0`** (no horizontal edges; coordinate only via frozen briefs and disjoint receipts); **`F0 ↔ F2 = 0 direct`** (F2 is visible to F0 only through F1's sealed evidence — never skip layers); `n workers ↔ queue = n-1` (SQLite WAL single-winner).
- **Naming Discipline**: Wave and slice ordinals MUST carry a prefix (`WF1..WFN`, never bare `F1..FN`). Bare `F3+` does not exist.

---

## 1. Zero-Context Lazy Loading Protocol (Mandatory Reading Order)

Do NOT randomly scan the entire repository or the `reference/` directory. Follow this 5-step progressive disclosure reading order:

```text
1. README & MANIFEST -> 2. CONSTITUTION & CHARTER -> 3. PLANS & ISSUES -> 4. OPENSPEC & ADRs -> 5. JIT REFERENCE
   (ID & Surface)          (Invariants & 6 Laws)        (Active Slice)       (Spec Contracts)      (Python Baseline)
```

1. **Step 1 — Understand Identity & Surface**: Read [`README.md`](README.md) and [`manifest.json`](manifest.json).
2. **Step 2 — Anchor Invariant Rules & Supervisor Charter**: Read [`spec/constitution.md`](spec/constitution.md) and [`skills/g8s-supervisor/SKILL.md`](skills/g8s-supervisor/SKILL.md) (plus [`hard-boundary.md`](skills/g8s-supervisor/references/hard-boundary.md) and [`operational-hard-lessons.md`](skills/g8s-supervisor/references/operational-hard-lessons.md)).
3. **Step 3 — Locate Active Campaign, Offer Rollout & Debt**: Check [`plans/handoffs/`](plans/handoffs/), active campaign ledgers in [`plans/`](plans/), [`offer/SCORECARD.md`](offer/SCORECARD.md), open GitHub issues (`gh issue list`), and [`docs/REFACTORING_PLAN.md`](docs/REFACTORING_PLAN.md).
4. **Step 4 — Read Technical Spec Deltas & ADRs**: Read the corresponding spec in [`spec/openspec/`](spec/openspec/) and architectural decisions in [`docs/decisions/`](docs/decisions/).
5. **Step 5 (JIT Only) — Reference Python Baseline**: Only inspect the specific file in [`reference/python/`](reference/python/) matching your current task.

---

## 2. Supervisor Operating Protocol: Skills, Dogfooding, Offer Bundle & Tooling

1. **Skills & OS Shared Layer (`~/.agents/skills`)**:
   - **Repo Charter**: [`skills/g8s-supervisor/SKILL.md`](skills/g8s-supervisor/SKILL.md) (manifest at [`skills/manifest.json`](skills/manifest.json)) is the canonical supervisor operating charter. Keep `~/.agents/skills/g8s-supervisor` synced with `skills/g8s-supervisor`.
   - **OS Shared Layer**: Global skills reside in `~/.agents/skills/` (registered for Antigravity via `~/.gemini/config/skills.json`). Query the deterministic JIT router before starting complex tasks: `python3 ~/.agents/skills/_shared/scripts/skill-router.py --strict "<task>"`.
2. **Mandatory Self-Dogfooding (`F0 → F1` via `g8s`)**:
   - **Rebuild First (Lesson #6)**: Always run `make local` and `./bin/g8s doctor --json` after every merge before dispatching workers to avoid the stale-binary trap.
   - **Session State Isolation**: Concurrent supervisor sessions MUST isolate queues via `G8S_STATE_DIR=~/.local/state/g8s-sess-<id>`.
   - **Event-Driven Supervision**: Dispatch bounded worker packets via `./bin/g8s submit` and `./bin/g8s worker`, and wake via `./bin/g8s watch --task <id> --milestone worker-complete` or `./bin/g8s watch --failed` (polling is fallback only).
   - **Evidence over Verdicts**: Harvest worktrees under `$G8S_STATE_DIR/worktrees`, run `./bin/g8s verify --task <id>`, and inspect actual diffs on disk before accepting or committing.
3. **Offer Bundle (`offer/`) for Sibling Repositories**:
   - Pull-based adoption ([`offer/README.md`](offer/README.md), [`offer/ONBOARDING.md`](offer/ONBOARDING.md), [`offer/SCORECARD.md`](offer/SCORECARD.md)) provides 4 domain profiles ([`knowledge`](offer/profiles/knowledge.md), [`security`](offer/profiles/security.md), [`infra`](offer/profiles/infra.md), [`utility`](offer/profiles/utility.md)) and 3 repo-agnostic CI gates (`tools/ci_spec_code_sync.sh`, `tools/ci_structure_sync.sh`, `tools/ci_link_integrity.sh`). Defects discovered during sibling runs flow back to `g8s` as sanitized Mode 3 packets.
4. **System-1 Reflex (`Jev` / TypeSafe) & Cognitive Harness (`wiki.py`)**:
   - **Jev (TypeSafe SystemOne)**: Enforces **Sensor ≠ Decider** across 3 boundaries: (a) L1 Pre-Mutation Triage (`./bin/g8s reflex triage --summary "..." --files "..."` enriched with Context Broker `ContextPacket`), (b) Router Assist (`G8S_ROUTER_MODE=jev_assisted`), and (c) L3 Output Quality & Memory Promotion Gate. When Jev is offline (`is_fallback: true`), degrade via the **3-Tier Fallback** (Tier 1: Live Jev $\to$ Tier 2: `.g8s/*.yml` deterministic registries + `skill-router.py --strict` + bounded `read_only` F1 scout $\to$ Tier 3: F0 Brain override for `T3`/`T4`). Never expose raw API keys in JSON envelopes, logs, or issue packets.
   - **LLM Wiki (`wiki.py`)**: Run `~/.local/bin/wiki.py orient` to align with Second Brain governance (`R-Second-Brain-Partitioning`, `R-Zero-Leakage-DLP-Protocol`, `R-Git-Worktree-Isolation`, `R-Git-Push-Cleanliness`).
5. **Native Cross-Platform Agent I/O (`DELTA-23` / [`offer/SEARCH_GUIDE.md`](offer/SEARCH_GUIDE.md))**:
   - Prefer native pure-Go `./bin/g8s search` (replaces `rg`/`grep`/`find`), `./bin/g8s read --lines M:N` (replaces `cat`/`head`/`tail`/`sed -n`), `./bin/g8s edit --old ... --new ... [--if-sha256 ...]` (replaces `sed -i`/`awk`), and `--select <dot.path>` (replaces `jq`) across CLI, MCP (`g8s_search`, `g8s_read`, `g8s_edit`), and HTTP (`/api/v1/fs/*`).
   - F1 workers automatically inherit `G8S_TIER=F1`, `G8S_RECEIPT_ID`, and `G8S_SCOPE_ROOT` (passed down to F2 subagents), enforcing `WriteReceipt.AllowedPaths` in-process on every mutation.
6. **F0 Cognitive Purity & Information Firewall (`ADR-0033` Phase 06b + Wave B)**:
   - F0 operates strictly as Brain (reasoning, brief authoring, consuming `RoundReceiptSchema` from `g8s verify --task <id> --json`, `watch_verdict` from `g8s watch --pr <N> --json`, `f0 gate check --json`). F0 NEVER manually hunts raw CI logs, scans disk bloat, or runs procedural janitor tasks. All log-hunting, CI failure extraction, and file sync tasks are delegated to F1 workers.
7. **Universal Config-Driven LLM Call Consumer, `9router` Integration & Zero-Trust Hardening (`DELTA-25..32`, `v0.20.0`)**:
   - Direct synchronous & bounded agentic loop execution: `./bin/g8s llm-call --provider 9router --model <combo> --prompt "..." [--json] [--apply-files] [--max-turns <1..5>] [--verify-cmd "..."]`.
   - Durable worker queue execution, adaptive effort timeout & claim affinity (`DELTA-27..32`): `./bin/g8s submit --provider 9router --model <combo> [--output-schema <schema.json>]` (auto-scales default timeout by effort: `45s`/`90s`/`240s`; enforces `depth <= 2` and parent `add_dirs` jail on `--parent-task-id`) + `./bin/g8s worker --once [--provider <p>] [--model <m>]`.
   - Cross-repository fleet hygiene gate (`DELTA-32`): `./bin/g8s fleet status [--dir <workspace>] [--require-clean] [--json]`.
   - Zero-trust worker execution invariants (`DELTA-26..32`): fail-closed worktree isolation (`#656`), recursive Draft-7 `--output-schema` enforcement (`#659`), structured `receipt_export` visibility + `./bin/g8s receipt-export --task <id>` recovery (`#658`), non-loopback `g8s serve` token auth (`#655`), mechanical `EvaluateDoDMatrix` check binding (`#648`), and monotonic `Jev` gate-lane (`0/D/R/F/S`) & effort (`low..xhigh`) routing.
   - Live model discovery: `./bin/g8s providers discover [--provider 9router] [--json]`.
   - Standardized `9router` SWE combos (`reasoning-free`, `reasoning-high`, `deep-coding`, `fast-agent`, `docs-retriever`) and prefix wildcards (`nvidia/*`, `openrouter/*`, `cc/*`, `cx/*`) in `.g8s/agent-models.yml`.
   - **Two-Layer Separation Rule**: Public repo mechanism (`openai`/`anthropic`/`9router`, standard combos/wildcards, `http://127.0.0.1:20128/v1`) vs. local untracked operator policy (`~/.config/g8s/providers.json` & `~/.agents/skills/`). Never commit homelab LAN IPs or private topology.

---

## 3. Deep Reference Documentation

For the complete SSoT Architecture & Governance Map, JIT Task Routing Decision Tree, Strict Development Invariants, OpenSpec Deltas, and Multi-Agent Protocols, see [`docs/AGENTS_FULL.md`](docs/AGENTS_FULL.md).

