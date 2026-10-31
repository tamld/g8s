---
title: "CI/CD depth wave — 9 audit findings + red-test suites"
description: "Implement the CI/CD verification-depth and red-team audit findings (issue #699 comment 2026-10-31) via parallel worker packets."
status: in_progress
priority: P1
branch: debt/cicd-depth-wave
tags: [ci, red-test, debt, backend]
blockedBy: []
blocks: []
**Session type**: T2 (execution)
---

# CI/CD Depth Wave — Plan

**Parent campaign**: `plans/261030-coordinator-interoperation/` (mesh interop — merged #700-#703). This wave implements the CI/CD + red-team audit findings filed on [#699](https://github.com/tamld/g8s/issues/699) (comment 2026-10-31, from 4 research scout reports).

## Evidence base (already gathered — do NOT re-research)

- Scout reports: CI/CD depth audit (`cicd-scout-A-depth-001`), red-team audit (`cicd-scout-B-redtest-001`), agent-reach mechanism + g8s mapping. Evidence JSONs under the mesh runtime evidence dir.
- Key verified facts: 4-pillar AppSec claims match files; gitleaks config EXISTS but unwired; nightly.yml under-delivers vs its comment; no poisoned-ledger/MCP-rogue/model-substitution red-tests; no property-based tests; mutation automation blocked (go-mutesting panics on Go 1.26 — D-08).

## Waves & Packets (parallel drain, disjoint receipts — max 3 workspace_write)

### Packet 1 — CI workflows (paths: `.github/workflows/*` only) [C/neutral layer]
1. **gitleaks wire**: add a gitleaks step to `quality.yml` (uses existing `.gitleaks.toml`); fail on leaks.
2. **Nightly parity**: extend `nightly.yml` — add `-race` test step on `./internal/...` (CGO_ENABLED=1 for race), bounded fuzz step (`go test -fuzz` `-fuzztime 10s` per existing Fuzz* target, iteration-capped), and a job-level note linking crash-survival + golden-loop (no duplication). Fix the job comment to match actual steps exactly.
3. **CodeQL schedule trigger**: add `schedule` cron to `codeql.yml` (weekly) alongside PR trigger.
**Verify**: YAML parse, actionlint if present, `git diff -w` shows only intended files; jobs appear on a manual `workflow_dispatch` dry push (branch push runs them).

### Packet 2 — Red-test suites (tests only; unclassified paths: `internal/routing/`, `internal/mcp/`) [neutral layer]
1. **Telemetry poisoning**: `internal/routing/poisoning_red_test.go` — hostile/scoreboard-poisoned JSONL ledger fixtures (malformed entries, adversarial metric spikes, forged fingerprints) must NOT corrupt `ComputeScorecard` routing decisions; quarantined model stays quarantined; poisoned entries rejected with typed errors.
2. **MCP rogue-tool + schema drift**: `internal/mcp/rogue_tool_red_test.go` — unregistered tool registration attempts, over-broad tools/list injection, schema-drifted tool descriptors, stdio trust-boundary replay — server must reject/ignore per existing handlers, never panic, never widen scope.
3. **Adversarial model substitution**: `internal/routing/model_substitution_red_test.go` — wrong-model worker results, quarantine-bypass attempts (stale scorecard, forged success records), helpful-worker smuggling patterns — routing must hold deny-by-default.
**Verify**: `CGO_ENABLED=0 go test ./internal/routing/ ./internal/mcp/ -count=1` green; each red-test names the OWASP class in a comment (ASI03/ASI08, LLM04/06, ASI02/04).

### Packet 3 — Property-based tests (tests only; unclassified: `internal/fsops/`, `internal/cli/`) [neutral layer]
1. `internal/fsops/scope_property_test.go` — scope-matching invariants: a path inside an allowed glob is always allowed; denying outside; property via `testing/quick` or `rapid` (pick ONE dep — prefer stdlib `testing/quick` to keep zero-dep constitution; rapid only if quick is insufficient, and note the dep in the brief output).
2. `internal/cli/envelope_property_test.go` — envelope decode: arbitrary bytes never panic; roundtrip marshal/unmarshal identity on valid envelopes.
3. `internal/fsops/sanitize_property_test.go` — DLP sanitizer: output never contains input secrets for generated secret-shaped inputs.
**Verify**: `CGO_ENABLED=0 go test ./internal/fsops/ ./internal/cli/ -count=1` green; property tests bounded (no unbounded generation).

### Packet 4 — docs/config (paths: `docs/`, `skills/`) [docs layer]
1. Prompt-injection cadence: document the hybrid policy (deterministic probes in CI = present; LIVE `g8s eval` quarterly/pre-release) in `docs/RELEASE_STRATEGY.md` + a scheduled-verification note in the nightly workflow comment.
2. `receipt issue` doc fix check: verify repo SKILL.md v4.5.0 line 153 correctness (already verified — no change unless found).
**Verify**: doc-contract + ai_lint gates green.

### Deferred (NOT in this wave — recorded)
- Mutation automation (go-mutesting Go-1.26 panic — D-08 quarterly manual continues), SLSA L1 formalization (GoReleaser already emits SPDX+checksums; cosign = v0.40.0), #698 parseAGYResult optimization re-apply (separate slice post-verify).

## Layer discipline (DEBT-34)
- Packet 1 = `.github/` (neutral) — own commit(s).
- Packets 2-3 = tests in unclassified internal packages — no S/W/C mixing; commit per packet.
- No `internal/worker/` or `cmd/g8s/` or `internal/orchestrator/` or `internal/controlplane/` files in ANY packet. Violation = split the commit.

## Execution
- Worker lane: cursor composer-2.5 (9router creds dead). Parallel drain 3 max, disjoint receipts (TTL 1800), deterministic delivery protocol (write DIRECTLY to receipt-scoped paths, no git commit by workers).
- Branch: `debt/cicd-depth-wave` off main. Commits per packet. PR to main after full local battery (gofmt/gofumpt via pre_push --fix, golangci full-repo, ai_lint, doc-contract, `CGO_ENABLED=0 go test ./...`).
- Cross-verify: agy reviews the red-test suites (its adversarial strength) before merge.

## Acceptance
- [ ] All packets merged, full battery green on main
- [ ] Each red-test names its OWASP class and asserts deny-by-default behavior
- [ ] No production code changes beyond `.github/workflows/` (tests-only wave)
- [ ] #699 comment updated with per-item closure
