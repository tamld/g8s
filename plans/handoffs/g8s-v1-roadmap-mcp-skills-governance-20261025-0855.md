---
handoff-version: 1
generated: 2026-10-25T08:55:00+07:00
generator: ak:handoff@2.1.0
focus: "g8s v1.0 release governance, F0/F1/F2 MCP & shared-skill inheritance, cloud-harness-mcp analysis, domain profile dogfooding review, pre-execution research/CVE debate gate, and SSoT manifest/schema/feature-map audit"
workspace: .
branch: main
head: 59b82fc
---

# HANDOFF: g8s v1.0 Roadmap, F0/F1/F2 MCP & Skill Inheritance, and Architecture Audit

> **Session type**: T1 (strategy)

## Mission and current status

Focus: "g8s v1.0 release governance, F0/F1/F2 MCP & shared-skill inheritance, cloud-harness-mcp analysis, domain profile dogfooding review, pre-execution research/CVE debate gate, and SSoT manifest/schema/feature-map audit".

Done:
- **Wave 1 (`30d8edc`)**: Repository decontamination — removed 211 junk/stale/foreign-agent files, pruned 22 stale branches on `origin` and 176 stale branches on `ct122`, and hardened `tools/ci_doc_contract_check.sh` `[10/14]`.
- **Wave 2 (`a944450`, `3619f66`, `59b82fc`)**: Implemented `DELTA-33` / `ADR-0035` Orthogonal Stratum Federation (`S0` Principal, `S1` FederationCoordinator, `S2` DomainGovernor, `S3-M` BoundedMutator, `S3-A` BlindAdjudicator, `O-Plane` TelemetryProjector) with SQLite WAL `stratum_charters` epoch fencing, kernel firewalls (`E_S1_DIRECT_MUTATION_FORBIDDEN`, `E_STRATUM_ADJACENCY_VIOLATION`), CLI (`./bin/g8s charter`), and 3 MCP tools (`g8s_charter_issue`, `g8s_charter_verify`, `g8s_portfolio_status`).
- **Wave 3 (Current commit)**: Implemented Continuous Model Quality Evaluation & Dynamic Quarantine (`internal/routing/scorecard.go`, `cmd/g8s/providers_scorecard.go`, `./bin/g8s providers scorecard`, `./bin/g8s providers evaluate`) + resolved Windows live queue counter freeze and `>83%` MP3 stall in sibling repo `adopter-utility-01` (`US-13..US-15`, `AC-06..AC-09`, `640/640` tests passing).

Remaining (6 Core Pillars for the Next Session):
1. **Release Governance & Semantic Versioning Roadmap (`v0.20.x -> v0.30 -> v0.40 -> v1.0.0`)**:
   - Standardize deterministic rules and criteria for patch releases (`0.20.1`, `0.20.2`), minor architectural milestones (`0.30.0`, `0.40.0`), and the gate requirements before declaring `v1.0.0` GA.
2. **F0 / F1 / F2 MCP & Shared Skills Inheritance Architecture**:
   - Audit why `F1` workers (`g8s worker` / `g8s llm-call`) and `F2` subagents currently do not fully inherit or invoke `g8s` MCP tools and OS/repo shared skills (`~/.agents/skills/` and `skills/manifest.json`), and standardize a zero-trust capability hydration mechanism so `F0`, `F1`, and `F2` all benefit from MCP + shared skills within their receipt boundaries.
3. **External Architecture Review — `https://github.com/bestagentkits/cloud-harness-mcp`**:
   - Analyze the ideas, tool topology, and execution patterns of `bestagentkits/cloud-harness-mcp` and synthesize what `g8s` MCP (`internal/mcp/server.go`) should adopt (porting ideas only, zero code copying).
4. **Role & Offer Profile Fitness Evaluation (`research`, `code`, `security`, `analyst`)**:
   - Evaluate whether current `g8s` role profiles (`internal/harness/roles.go`) and offer profiles (`offer/profiles/`: `knowledge`, `security`, `infra`, `utility`) truly satisfy real-world `research`, `code`, `security`, and `analyst` workflows based on dogfooding lessons from `g8s` and `adopter-utility-01`.
5. **Pre-Execution Research, Deep-Dive, CVE & Adversarial Debate Gate Design**:
   - Design a mandatory pre-execution workflow stage (`Intake -> World State/CVE/Use-Case Research -> Socratic Debate & Threat Modeling -> Plan -> Cook`) so agents never jump straight into shallow implementation without surveying industry best practices, CVEs, and failure modes.
6. **Big-Picture SSoT Status Audit (`manifest.json`, Schemas, System Design, Feature Map)**:
   - Conduct a holistic status assessment of `manifest.json`, `spec/schemas/*.json`, `docs/ARCHITECTURE.md`, `docs/decisions/`, `.g8s/feature-map.yml`, and `docs/FEATURE_MAP.md`.

Urgency: high.

## Scope and guardrails

Workspace: `.` (`tamld/g8s` repository root)
In scope:
- Release governance specification, roadmap documentation, and version gate automation.
- `internal/mcp/`, `internal/harness/`, `cmd/g8s/`, `skills/`, `.g8s/*.yml`, `manifest.json`, `spec/openspec/`, `docs/decisions/`, and `docs/FEATURE_MAP.md`.
Out of scope:
- Copying licensed source code from external repositories (`reason-network`, `cloud-harness-mcp`); only architectural ideas and patterns may be adapted.
Constraints:
- Prioritize `9router` models governed by `./bin/g8s providers scorecard` / `./bin/g8s providers evaluate` to optimize quota while maintaining verified quality.
- Enforce Two-Layer Separation: never commit private LAN IPs, operator credentials, or local workstation paths (`/Users/...`) into tracked files.
Safety boundaries:
- Always run `make local`, `./bin/g8s doctor --json`, and `bash tools/ci_doc_contract_check.sh` before committing or dispatching workers.

## Current state

Branch: `main`
HEAD: `59b82fc` (plus Continuous Model Quality Scorecard commit on `main`)
Working tree: clean (all changes committed and pushed to `origin/main` and `ct122/main`)
Changed files:
- `cmd/g8s/llm_call.go`
- `cmd/g8s/main.go`
- `cmd/g8s/providers_scorecard.go`
- `internal/routing/router.go`
- `internal/routing/scorecard.go`
- `internal/routing/scorecard_test.go`
Untracked files: none
Intentional local modifications: no

## Decisions and rationale

- **Decision**: Introduce Rolling Model Quality Scorecard & Dynamic Quarantine (`internal/routing/scorecard.go`, `./bin/g8s providers scorecard`, `./bin/g8s providers evaluate`).
  - **Rationale**: Live probing on `9router` proved that model/combo health drifts over time (`deep-coding` and `docs-retriever` scored `100.0 HEALTHY`, while `fast-agent` and `reasoning-free` timed out and degraded to `7.4 DEGRADED`). Static model lists cause silent worker stalls.
  - **Alternative rejected**: Relying on a static list of verified models in OS memory.
  - **Reference**: `internal/routing/scorecard.go`, `cmd/g8s/providers_scorecard.go`.
- **Decision**: Enforce Orthogonal Control Strata (`S0/S1/S2/S3-M/S3-A/O-Plane`) orthogonal to Containment Rings (`F0/F1/F2`).
  - **Rationale**: Prevents model homogenization ("Manager-as-Coder" collapse) via mechanical kernel firewalls (`E_S1_DIRECT_MUTATION_FORBIDDEN`, `E_STRATUM_ADJACENCY_VIOLATION`) and SQLite WAL epoch-fenced charters.
  - **Alternative rejected**: Adding `F3+` containment rings, which violates `ADR-0032`.
  - **Reference**: `docs/decisions/0035-orthogonal-stratum-federation.md`, `spec/openspec/deltas/delta-33-orthogonal-stratum-federation.md`.
- **Decision**: Require real execution and live DOM verification in sibling dogfooding (`adopter-utility-01` Windows RCA).
  - **Rationale**: Unit tests that set `queue.isPaused = true` or `MOCK_FFMPEG = 'success'` create an illusion of green CI while missing `yt-dlp -x` missing-`ffprobe.exe` crashes at `83%–85%` and `i18n.js` `.innerHTML` DOM node detachment.
  - **Alternative rejected**: Surface-level mock unit tests.
  - **Reference**: Sibling `adopter-utility-01` commit on `master` (`tests/download-pipeline-and-counters.test.js`, `tests/puppeteer-e2e.test.js`).

## Work performed

- Implemented `internal/routing/scorecard.go` and `internal/routing/scorecard_test.go` with rolling window (`N=20`) composite scoring (`SuccessRate`, `VerifyPassRate`, `P50/P95 Latency`, `StagnationRate`, `ConsecutiveFailures`) and automatic filtering of `QUARANTINED` candidates in `RouteDeterministic`.
- Wired automatic outcome recording in `cmd/g8s/llm_call.go` on every LLM call completion or failure.
- Added `./bin/g8s providers scorecard` and `./bin/g8s providers evaluate` in `cmd/g8s/providers_scorecard.go` and `cmd/g8s/main.go`.
-Executed live evaluation against `9router` combos (`deep-coding`, `docs-retriever`, `fast-agent`, `reasoning-free`) and verified dynamic scoring.
- Fixed sibling `adopter-utility-01` Windows bugs (`public/js/i18n.js`, `public/js/app.js`, `src/engine/queue.js`, `src/engine/ffmpeg.js`, `src/security/binary_guard.js`, `scripts/bundle-sidecars.js`, `scripts/sync_local_install.ps1`, `installer/windows/adopter-utility-01Launcher.cs`) and expanded `docs/PRD.md`, `docs/SRS.md`, `docs/AC.md`, `docs/FSM.md`, and real execution tests (`640/640` passing).
- 0 redactions applied.

## Verification

| Check | Command | Outcome | When |
| :--- | :--- | :--- | :--- |
| Routing & Scorecard Unit Tests | `go test ./internal/routing/... ./cmd/g8s/... -run "TestComputeScorecard\|TestProviders"` | PASS (`0.53s` / `1.60s`) | 2026-10-25 |
| Live `9router` Model Evaluation | `./bin/g8s providers evaluate --provider 9router --model reasoning-free,fast-agent,docs-retriever,deep-coding --timeout 20s` | PASS (`deep-coding` & `docs-retriever` = `100.0 HEALTHY`) | 2026-10-25 |
| Doc-Code Contract Gate (14/14) | `bash tools/ci_doc_contract_check.sh` | PASS (14/14 checks synchronized) | 2026-10-25 |
| adopter-utility-01 Full Lint & Test Suite | `npm run lint && npm test` (in `adopter-utility-01`) | PASS (`640/640` tests, `202` suites, `0` failures) | 2026-10-25 |

Not run:
- Full external network fetch of `https://github.com/bestagentkits/cloud-harness-mcp` (deferred to Item 3 of the new session).

## Open risks and blockers

- **Type**: risk — **Owner**: `F0` Supervisor — **Impact**: `F1` workers (`g8s worker` / `g8s llm-call`) and `F2` subagents currently receive pre-execution grounding whispers (`vault.BuildPreExecutionGrounding`), but do not yet have a standardized protocol to invoke `g8s` MCP tools or load domain-specific shared skills from `skills/manifest.json` inside their isolated worktrees (Agenda Item 2).
- **Type**: question — **Owner**: `S0` Principal / `F0` Architect — **Impact**: Formalizing the exact version bump taxonomy (`0.20.x` patch vs `0.30.0`/`0.40.0` minor vs `1.0.0` GA readiness criteria) so automated release gates enforce it mechanically (Agenda Item 1).

## Exact next actions

1. **First safe step**: Run `./bin/g8s doctor --json`, `./bin/g8s providers scorecard --provider 9router`, and `bash tools/ci_doc_contract_check.sh` to confirm baseline health and inspect current `9router` model quality scores before dispatching any workers.
2. **Agenda 1 (Release Governance & `v1.0` Roadmap)**: Conduct Socratic inversion and draft an ADR + declarative release policy (`.g8s/release-policy.yml` or `docs/RELEASE_GOVERNANCE.md`) defining exact mechanical criteria for `0.20.x` (bugfix/hardening/scorecard patches), `0.30.0` (F0/F1/F2 MCP + Skill hydration & Pre-Execution Research Gate), `0.40.0` (Multi-repo S1->S2 Autonomous Federation & Full Dogfooding Certification), and `1.0.0` (Zero-Trust GA lock).
3. **Agenda 2 & 3 (F0/F1/F2 MCP & Skill Inheritance + `cloud-harness-mcp` Review)**: Fetch and analyze `https://github.com/bestagentkits/cloud-harness-mcp`; audit how `g8s` exposes MCP (`internal/mcp/server.go`) and skills (`skills/manifest.json`); design and implement capability-scoped MCP & skill injection for `F1` and `F2` workers.
4. **Agenda 4 & 5 (Role/Offer Profile Evaluation & Pre-Execution Research/CVE/Debate Gate)**: Evaluate current profiles (`research`, `code`, `security`, `analyst`) against dogfooding evidence (`g8s` & `adopter-utility-01`), and design the mandatory Pre-Execution Research, CVE/Risk & Socratic Debate workflow gate.
5. **Agenda 6 (Holistic SSoT Audit)**: Audit and synchronize `manifest.json`, `spec/schemas/*.json`, `docs/ARCHITECTURE.md`, and `.g8s/feature-map.yml` / `docs/FEATURE_MAP.md`.

## Source pointers

- `AGENTS.md`
- `manifest.json`
- `.g8s/feature-map.yml`
- `.g8s/agent-models.yml`
- `docs/FEATURE_MAP.md`
- `docs/ARCHITECTURE.md`
- `docs/decisions/0032-containment-levels.md`
- `docs/decisions/0035-orthogonal-stratum-federation.md`
- `internal/routing/scorecard.go`
- `cmd/g8s/providers_scorecard.go`
- `internal/mcp/server.go`
- `internal/harness/roles.go`
- `skills/manifest.json`
- `offer/SCORECARD.md`
- `https://github.com/bestagentkits/cloud-harness-mcp`
