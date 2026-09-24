# Plan: Reflex-Gated Debt Campaign (2026-09-25)

tags: [debt, dogfood, reflex, jev, governance]
adr: [docs/decisions/0020-reflex-gated-debt-campaign.md]

Campaign goal: close the open `debt`-labeled issues (#319, #320, #321) and
review the two open PRs (#325, #324) by running g8s against itself, with every
mutation gated by the Jev reflex sensor (ADR-0020) and every phase anchored by
DoR/DoD so the roadmap (v0.3.0 sprint) stays the reference point.

---

## Phase 0 — Knowledge load & dogfood baseline

**DoR**: AGENTS.md reading order completed (README → constitution → refactor
plan → manifest); g8s binary built from source.

**DoD**: doctor HEALTHY; providers verified; lifecycle sweep executed; findings
converted into issues.

**Result**:
- `g8s doctor` HEALTHY (Zero-CGO true, 6 roles / 3 permissions validated).
- Providers: agy 1.2.10, codex 0.155.1, claude 2.1.220 detected.
- `g8s cleanup --dry-run` found 564 orphan-branch / 69 orphan-wt / 100
  closed-pr-branch items. `--force` pruned all 69 orphan worktrees but deleted
  **zero** branches (see findings D1, D2). 1510 `agy/*` + `blind/*` scratch
  branches were swept by the supervisor after verifying all 47 unique tip
  commits (24 unprotected tips preserved under `keep/scratch-*` tags).
- Jev live canary PASS (`TestLiveJevReflexIntegration`, 699 ms, source=jev).

**Dogfood findings → new issues**:
- D1: `cleanup` closed-pr-branch lists remote-only PR heads; every local
  `git branch -D` fails with "not found" (100/100 skipped).
- D2: `cleanup` never collects unmerged worker-scratch branches (`agy/*`,
  `blind/*`); the merged-only heuristic leaves the exact litter g8s itself
  produces.
- D3: `g8s submit` and `g8s get` print an MCP-shaped envelope with empty text
  payload on stdout (results are present in the SQLite state DB; rendering bug).
- D4 (reflex): live Jev returns `confidence = 0`; grant fast-path (Rule 2) is
  unreachable, every live verdict escalates. Kill rule (Rule 1) unaffected.

## Phase 1 — Parallel worker recon (#319 / #320 / #321)

**DoR**: per-worker packet with objective, allowed scope, output schema, stop
conditions (g8s-supervisor skill contract); tasks submitted `read_only`,
model `gemini-3.8-flash-high`, 3 worker sessions spawned concurrently.

**DoD**: worker evidence collected, verified against the real code paths by the
Brain, and converted into an implementation decision per issue. Worker
failures are classified (evidence gap ≠ defect).

**Result**:
- #321 (f8179c72): root cause = 5 s deadline vs fork/exec + macOS code-sign
  validation under parallel suite load (95% confidence); fix = 5s → 30s in
  `verify_test.go` (success-path test; timeout path has its own test). VERIFIED
  against source.
- #319 (5f882243): confirmed stale literals; produced patch plan with diff
  preview. Re-audit vs `origin/main` showed ci.yml already derives VERSION
  (b503108); residual = packaging template defaults (nsi/wxs 0.4.0), chocolatey
  URL pinned to v0.4.0, missing version-sync installer check. PARTIALLY
  STALE — residual implemented.
- #320 (a51cd2df): worker ran 155 steps and ended in `error_message` without a
  deliverable (WORKER_COMPLETED state — see D5 finding: completion state does
  not reflect error tail). Re-audit vs `origin/main` by the Brain showed the
  drift was already resolved by b503108 (package comment "eleven tools",
  docs at 11, g8s_run honestly documented as typed pending-dependency error).
  Evidence gap, no re-dispatch.

## Phase 2 — PR closeout

**DoR**: PR CI status fetched; review contract = verify findings against real
code paths (autoreview skill), no speculative findings.

**DoD**: review verdicts posted; blocking defects fixed on the PR branch and
pushed; CI green.

**Result**:
- PR #325 (memory facade, #257): 2 Windows check failures
  (`TestWorkingMemory_CRUD_And_Redaction`,
  `TestAdapter_NewAdapter_PreExistingFilePermissions`) root-caused to POSIX
  0600 assertions without GOOS guards. Fixed with `runtime.GOOS` guards
  (precedent: service_test.go:227), focused + race tests green, pushed as
  1115019. Code review of the memory package core (adapter/vector/hybrid):
  dimension-filtered deferred JSON vector search, zero-leak purge with SHA-256
  digest — no blocking findings beyond the Windows guards.

## Phase 3 — Debt implementation

**DoR (per mutation)**: Jev triage verdict recorded (ADR-0020); re-audited
issue delta confirmed against `origin/main`.

**DoD**: dual-pass gates green (`CGO_ENABLED=0 go vet ./... && go test
-count=1 ./...` and `CGO_ENABLED=1 go test -race -count=1 ./...`); commits
reference issue numbers.

| Mutation | Files | Jev verdict (risk/breach) | Adjudication | Verification |
|---|---|---|---|---|
| M1 campaign governance docs | docs/decisions/0020, this file | escalate_hitl (0.01/0.01, conf=0) | proceed per ADR-0020 (risk<1.0, breach<0.20) | docs review in PR |
| M2 #321 test deadline 5s→30s | internal/runtime/verify_test.go | escalate_hitl (0.03/0.01, conf=0) | proceed per ADR-0020 (risk<1.0, breach<0.20) | full suite ×2 + race gate |
| M3 #319 packaging residue | packaging/windows/*.nsi,*.wxs, chocolateyinstall.ps1, version-sync.yml | escalate_hitl (1.18/1.18→1.10/1.12 across 3 runs, breach ≤0.03, conf=0) | **threshold exception**: risk>1.0 in all 3 runs → escalation honored by routing to PR review for operator ratification; supervisor does NOT merge | extraction logic executed locally (all literals 0.10.0); yaml additive-only |

Live-Jev calibration note (finding D4): every triage this campaign returned
`confidence = 0` with `source = jev` (5 triages, latency 0.8–1.0 s), so the
`grant_receipt` fast-path (Rule 2, confidence ≥ 0.80) was never reachable; all
verdicts landed on `escalate_hitl` regardless of risk. The kill rule (Rule 1)
was never approached (max breach 0.03).

## Phase 4 — Retro & roadmap handoff

**DoD**: supervisor metrics queried; ledger finalized; residuals mapped to
v0.3.0 milestone issues (#1–#8 in docs/REFACTORING_PLAN.md).
