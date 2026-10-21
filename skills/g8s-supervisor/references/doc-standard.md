# Documentation, License & Claims Verification Standard (v4.3.0)

> **Authority**: F0 Supervisor Charter (`skills/g8s-supervisor/SKILL.md`)  
> **Status**: Mandatory Invariant for all Documentation, Manifests, and Public Surfaces  
> **Target Release**: `v0.19.0+`  

---

## 1. The 10-Section Pedagogical & De-Noised README Standard

Both `README.md` and `README.vi.md` MUST strictly conform to the 10-section structure. Internal engineering issues, WIP commits, test file citations, and transitory debugging details belong in internal PRs or campaign ledgers (`plans/`), NEVER in public README surfaces.

The 10 Canonical Sections:
1. **Header, Badges & Language Switcher**: Product identity, logo, static badges (`License: MIT`, `Go-1.26.9`, `Platform`, `Release`), and clean English / Tiếng Việt toggle.
2. **Overview & Why g8s?**: Core problem statement, high-impact *Without g8s vs. With g8s* comparative table, and concise Core Terminology table (Brain, Worker, Task, Receipt, Attempt, Evidence Lake).
3. **Architecture & Execution Lifecycle**: 3-Tier F0/F1/F2 architectural diagram and clean 5-step execution flow (Submit -> Lease -> Contain -> Verify -> Seal).
4. **Core Capabilities**: Clean 5-domain capability matrix (Zero-Trust Containment, Durable Task Queue, Native Cross-Platform Agent I/O, Universal LLM Call Consumer, Decoupled Knowledge Vault).
5. **Installation**: Zero-friction setup (pre-built GitHub release binaries, `go install`, and `make build`).
6. **Usage Paradigms (Quickstart)**: Four pedagogical command paradigms:
   - `### Paradigm 1: Read-Only Reconnaissance` (safe exploration without write permissions).
   - `### Paradigm 2: Capability-Scoped Mutation` (receipt-bound write operations with blast radius control).
   - `### Paradigm 3: Universal Config-Driven LLM Gateway Consumer` (direct CLI invocation and durable worker queue execution across OpenAI/Anthropic/9Router).
   - `### Paradigm 4: Native Cross-Platform Agent I/O` (pure-Go `search`, `read`, `edit`, and `--select` / `g8s select`).
7. **MCP, Agent Skills & Offer Bundle**: Complete inventory of 16 stdio JSON-RPC MCP tools, supervisor operating skill (`g8s-supervisor` `v4.3.0`), sibling-repo adoption via Offer Bundle `v0.19.0`, and AI Agent SSoT discovery pointers (`manifest.json`, `AGENTS.md`).
8. **Scope & Non-Goals**: Clear, definitive product boundaries (why `g8s` uses Declarative Config + Standard HTTP/CLI Protocols instead of in-process dynamic code plugins, no GUI dashboard bloat, no heavy Python orchestration dependencies).
9. **Documentation & Quality Assurance**: Clear documentation navigation index, alongside the 4-pillar AppSec & Quality Gates table bound directly to `docs/claims.yml` and `tools/claims_check.sh`.
10. **Roadmap, Project Structure & License**:
    - Evidence-gated Roadmap table (`v0.18.0` Done, `v0.19.0` Done, `v0.20.0` Planned, `v0.21.0+` Pre-1.0 Burn-In).
    - Freshly rendered Project Structure tree bounded by `<!-- structure:start -->` and `<!-- structure:end -->` (`tools/ci_structure_sync.sh --render`).
    - Standard Single MIT License declaration.

---

## 2. Single Standard MIT License

To maximize adoption across open-source, homelab, and enterprise environments without licensing friction or legal ambiguity:
- `g8s` is distributed strictly under the **single, standard MIT License** (`LICENSE`).
- Dual-licensing options (Apache 2.0 / MIT hybrids), disclaimer files, and license decision matrices are decommissioned.
- Root `manifest.json` specifies `"license": "MIT"`.

---

## 3. Zero-Stale `manifest.json` & Declarative Registries

`manifest.json` is the machine-readable Single Source of Truth (SSoT) for autonomous AI agents entering the repository:
1. **Mandatory Sections**:
   - `governance`: Core specifications and operating charters.
   - `declarative_registries`: Path pointers to `.g8s/*.yml` (`agent-models.yml`, `effort-classes.yml`, `lane-bundles.yml`, `trust-boundaries.yml`, `verifier-classes.yml`).
   - `documentation_index`: Relative paths to canonical user guides, PRD, SRS, and offer bundle.
   - `schemas`: Formal JSON schemas for tasks, receipts, and execution results.
   - `specifications`: Comprehensive array of all applied OpenSpec deltas (`spec/openspec/*.md`).
2. **Zero-Stale Verification Gate**:
   - `tools/ci_doc_contract_check.sh` enforces at CI time that **every single path** declared in `manifest.json` exists on disk.
   - Stale links, renamed documentation, or uncommitted specs fail the gate immediately.

---

## 4. No Unverified Claims Rule

Integrity and honesty are core operating laws of `g8s`:
1. Every quantitative performance claim (e.g. cold start latency, RAM usage, binary size) and architectural invariant (e.g. process containment, receipt atomicity, secret redaction) asserted in public documentation MUST be registered in `docs/claims.yml`.
2. Every registered claim MUST bind to an executable Go test (`test: TestName`) or a deterministic evaluation check.
3. `tools/claims_check.sh` runs as a mandatory CI and pre-push gate. Claims that lack test bindings or point to non-existent tests cause immediate build failure.
4. Promotional or speculative claims without verifiable empirical proof are strictly forbidden.

---

## 5. Two-Layer Separation Rule (Public Mechanism vs. Operator Policy)

To guarantee portability and prevent accidental leakage of private infrastructure:
- **Layer 1 — Public Repository Mechanism**: All files tracked in Git declare only generic protocols (`openai`, `anthropic`, `9router`), standardized model combos (`reasoning-free`, `reasoning-high`, `deep-coding`, `fast-agent`, `docs-retriever`), standard wildcard patterns (`nvidia/*`, `openrouter/*`, `cc/*`, `cx/*`), and localhost defaults (`http://127.0.0.1:20128/v1`).
- **Layer 2 — Local Operator Policy**: Specific LAN IP addresses, private cluster hosts, API authentication tokens, and custom homelab routing combos reside strictly in untracked local configurations (`~/.config/g8s/providers.json` and `~/.agents/skills/`).
- Never commit private LAN IPs, internal hostnames, or proprietary combo definitions to tracked repository files.
