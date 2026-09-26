# HANDOFF: g8s Orchestrator Framework — Strategic Session Handoff

## Mission and current status

g8s is a Zero-Trust Process Execution & Capability Harness for AI Agent CLI Workers. Pure Go, Zero-CGO, single binary ~15MB. v0.11.0 released (11 assets, GoReleaser).

**Campaign result (2026-09-25 to 2026-09-26)**: 40+ PRs merged, 26 issues closed, 2 releases (v0.10.1, v0.11.0), 5 ADRs written, 0 open bugs, 0 open PRs, 0 open security findings, 1 remote branch (main), 0 local garbage. Framework is production-ready for single-tenant homelab/workstation orchestration.

**What the next session must do**: This was an EXECUTION session (T2). The next session should be a STRATEGY session (T1) — brainstorming, architecture exploration, creative thinking. Do NOT continue the execution pattern (issue → fix → PR → merge). The execution backlog is clean.

## Scope and guardrails

- **SOM (ADR-0021)** governs all work: roles (Operator/Brain/Worker), FSM, problem-class matrix, E2E checklist
- **DEBT-34**: never mix internal/worker with cmd/g8s or internal/orchestrator in one PR
- **JEV triage** before every mutation (ADR-0020): risk ≥ 1.0 → operator review
- **Dual-pass CI** mandatory: CGO_ENABLED=0 + CGO_ENABLED=1 -race
- **Pre-push 12-gate suite** — never bypass with --no-verify
- **Zero garbage policy**: no orphan worktrees, no untagged scratch branches, no draft releases
- **Layer ownership**: reflex + worker + controlplane + receipt are all internal/* — verify with tools/ci_layer_check.sh
- **Operator (tamld)** has final merge authority on threshold-exception mutations; delegated to main agent for execution rounds
- **agy on Windows** is the operator's tool for Windows-native debugging — do not attempt Windows-only fixes without that environment

## Current state

- main = 5def793 (ADR-0021 section 8: distributed reflex architecture)
- v0.11.0 released (11 assets: darwin_all, linux amd64/arm64, windows amd64, deb/rpm/apk)
- 42 packages green (dual-pass + race + lint + pre-push 12 gates)
- 0 open PRs, 3 open issues (all have plans, not debt)
- 1 worktree (main checkout only)
- Remote: 1 branch (main), all merged branches cleaned
- Local: 12 branches (main + race/test fixtures + backup), all others cleaned
- .env has TYPESAFE_API_KEY for live Jev calls (never commit)

### Open issues (3 — all have plans, not blockers)

| Issue | Scope | Blocker |
|-------|-------|---------|
| #383 | Worker output content-level validation (injection detection in stdout) | Design: what counts as prompt injection in worker output? |
| #379 | Eval live-provider adapter | Design: BLOCKED semantics for real model responses (3 options in issue) |
| #385 | Roadmap anchor | Mechanical — update after each release |

## Decisions and rationale

| Decision | ADR/Evidence | Do NOT re-litigate |
|----------|-------------|-------------------|
| Reflex-gated campaign (JEV before mutation) | ADR-0020 | Kill-rule is absolute; fast-path needs calibration fix (#329 closed, confidence derived from deterministic classifier) |
| Standard Operating Model (SOM) | ADR-0021 | FSM + gates + E2E checklist + problem-class matrix are canonical |
| Distributed reflex architecture (L1-L8) | ADR-0021 §8 | Jev is a nervous system, not a checkpoint; 3/8 shipped (L1/L3/L6) |
| Context Broker design | ADR-0021 §8.2 | Enriches TriageRequest before Jev; queries Vault + Telemetry + SOM state |
| DEBT-34 layer ownership | tools/ci_layer_check.sh | W≠C, W≠S in one PR; test-fake ripples split PRs |
| Permission-aware patterns | #376 merged | Destructive-execution patterns gate mutation-capable perms only |
| Workspace jail | #355 merged | Scope-roots + --scope-root opt-in for cross-root |
| Delegated-write E2E verified | #352 + #355 | Receipt → jail → host write → consume=1 → completed |
| v0.11.0 release scope | #388 tag | Minor release: 6 new features, 5 security fixes, 5 bug fixes |
| Session type separation needed | This handoff | T1 strategy ≠ T2 execution; needs ADR-0022 |

## Work performed

### v0.11.0 release content (15 PRs since v0.10.1)
- g8s watch: push-channel primitive (blocking, exits at terminal condition)
- g8s eval: adversarial probe harness (24 probes × 6 categories, PRI scoring)
- g8s reflex triage: System-1 mutation gate CLI
- L3 post-run Jev quality gate: hallucination detected and blocked at source
- Closed-loop telemetry: ingest → distill → preflight inject into briefs
- Workspace jail: scope-roots + --scope-root opt-in
- Dialectic Bounce FSM: ceiling N≤3, evidence-gated bounces
- HTTP API: bearer auth, JSON error contract, loopback bind, 1MiB body bounds
- Windows: native telemetry batch ingest, PEB CWD, CPU sampling, two-stage signals, SQLite URIs
- Security: reflex scope bypass fix, signtool thumbprint mode, permission-aware patterns
- DB: connection pooling, session index, WAL URI escaping
- Docs: governance standards, roadmap alignment, ADR-0021 SOM

### Dogfooding findings (22+ issues filed)
- D6: concurrent sessions in shared checkout move each other's refs — needs session isolation
- D8: Gemini safety filters block defensive audit prompts — use defensive framing
- Drain-race: telemetry batchProcessor stop path raced eventChan — fixed (#363)
- Worker error-tail: WORKER_COMPLETED even when stream ends in error_message — fixed (#343)

## Verification

- Dual-pass CI (CGO=0 test + CGO=1 -race) green on every merged PR
- Pre-push 12-gate suite: formatting, layer-ownership, AI anti-pattern, brief anti-pattern, doc-contract, vet, golangci-lint, dual-pass tests, dogfooding roundtrip, cross-platform build
- Windows E2E green on main (verified after #356, #362, #364, #386)
- Live E2E delegated-write: receipt consumed=1, host file materialized, task completed (#352/#355)
- Live Jev triage: 15+ calls across the campaign, risk 0.01–2.5, breach ≤0.09, source=jev
- v0.11.0 release: GoReleaser success, 11 assets, version-sync gate passed

## Open risks and blockers

| # | Risk | Severity | Owner | Mitigation |
|---|------|----------|-------|-----------|
| R1 | Concurrent supervisor sessions in shared checkout (D6) | HIGH | Needs ADR-0022 + session isolation | Worktree-per-worker default; repo-lock; per-session state namespace |
| R2 | Telemetry batch ingest on Windows (#342) | MEDIUM | Operator (agy-Windows machine) | Drain-race fix in #363 may resolve; verify on Windows |
| R3 | Worker output prompt injection (S7, #383) | MEDIUM | Next session | Content-level validation design needed |
| R4 | Eval BLOCKED semantics for live models (#379) | LOW | Next session | 3 options proposed in issue |
| R5 | F1→F2 recursive spawning | ARCH | Needs OpenSpec delta | 3 blockers: binary access, credentials, receipt chain |
| R6 | user_version conflict (receipt v3 vs controlplane v10) | ARCH | Documented in code | Needs per-package version tables or DB file split |

## Exact next actions

**First safe step**: Start a STRATEGY session (fresh context, no operational details). Read ADR-0021 + this handoff + the 3 open issues. Brainstorm the following:

1. **ADR-0022: Strategic Session Protocol** — define FSM for T1 (IDEATE → EXPLORE → CHALLENGE → DECIDE), context requirements, session type marker
2. **Context Broker implementation** — internal/context package: ContextPacket assembly from Vault + Telemetry + SOM state; polymorphic TriageRequest v2
3. **L2 Brief quality gate** — Jev hook in brief_issue.go: "is this brief well-formed enough for dispatch?"
4. **L4 Skill routing** — internal/routing package: Jev scores task-to-skill match; enforce-code skill selection
5. **Concurrent dispatch** — goroutine pool in RunLoop; --concurrency flag; per-worker worktree isolation
6. **Session-scoped state** — per-session g8s.db namespace or repo-lock for multi-supervisor safety
7. **#258 Phase B** — A2A Agent Card + Agent Protocol endpoints on internal/server (operator design decision needed)
8. **#379** — Choose BLOCKED scoring semantics from 3 options and implement live adapter
9. **#383** — Choose injection-detection approach from 2 options and implement content validation

**Do NOT start with**: fixing bugs (none), creating issues (none), reviewing PRs (none), cleaning garbage (none). The execution backlog is clean. Start with thinking.

## Source pointers

| Artifact | Path |
|----------|------|
| SOM (Standard Operating Model) | docs/decisions/0021-standard-operating-model.md |
| Reflex-gated campaign ADR | docs/decisions/0020-reflex-gated-debt-campaign.md |
| Campaign ledger | plans/260925-debt-campaign/plan.md |
| L3 plan (post-run Jev gate) | plans/260926-1030-l3-post-run-jev/plan.md |
| Exec plan (prioritized backlog) | Issue #385 |
| Constitution | spec/constitution.md |
| OpenSpec deltas | spec/openspec/ |
| Jev sensor | internal/reflex/jev.go |
| Telemetry engine | internal/telemetry/engine.go |
| Dialectic FSM | internal/dialectic/dialectic.go |
| Watch primitive | internal/watch/watch.go |
| Eval harness | internal/harness/probe/ |
| Pre-push gates | tools/pre_push.sh |
| Doc-contract gate | tools/ci_doc_contract_check.sh |
| Layer-ownership gate | tools/ci_layer_check.sh |
