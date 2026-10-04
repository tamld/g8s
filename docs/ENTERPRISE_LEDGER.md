# Enterprise Evidence Ledger

Every enterprise-grade claim in g8s, one row each, with the artifact that
backs it. **PROVEN** = bound to a CI-verifiable test or an applied decision.
**UNPROVEN** = designed but not yet measured or enforced (with the tracking
issue). The registry of quantitative claims lives in `docs/claims.yml`
(verified by `tools/claims_check.sh`); this ledger is the enterprise-facing
view over the same evidence.

Last updated: 2026-10-04 (v0.14.0 release + Wave I pipeline live: verifier, merger, autonomy, retry, lessons, multi-project containment).

## Zero-Trust Delegation

| Claim | Status | Evidence artifact | Notes |
| :--- | :--- | :--- | :--- |
| Write capability is delegated via single-use, path-scoped, TTL-bound receipts | PROVEN | `internal/receipt` + `TestReuseOfConsumedReceiptRejected` (docs/claims.yml `claim-containment-receipt-single-use`) | Receipts mandatory for workspace_write regardless of env opt-in (#334) |
| Task payloads never originate invocation templates | PROVEN | `internal/config` (`args` templates are operator-declared only) | DELTA-10 R6; worker-side substitution |
| Delivery is supervisor-owned provenance, not agent choice | PROVEN | #448 closed: slice A `deliverable` pointer (#467) + slice B `g8s deliver` receipt-validated apply (#472), delivery atomicity hardened (#482) | Applies to worktree-isolated attempts; `g8s deliver` verified live |

## Containment & Process Isolation

| Claim | Status | Evidence artifact | Notes |
| :--- | :--- | :--- | :--- |
| 7 containment layers live (attempt, receipt, session, lane, trust-zone, surface, cleanup) | PROVEN | docs/decisions/0021-standard-operating-model.md (docs/claims.yml `claim-containment-layers`) | |
| Per-attempt worktree isolation for workspace_write | PROVEN | `TestWorkspaceWriteAttemptGetsWorktreeIsolation` (#427) | Concurrent attempts clobbering each other fixed |
| Dirty worktrees survive `Release(keep=true)` | PROVEN | `TestReleasePreservesDirtyWorktree` (#443-f2, fix #450) | Preserved-until-reaped contract |
| Worker sessions tracked in SQLite registry with exclusive locking | PROVEN | `TestSessionsRegistryLifecycle` (#393) | |
| Cross-session containment on one host (multi-project) | PROVEN | ADR-0028 accepted; PR #468 (identity-scoped ghost kill, state-dir worktree root) and PR #491 (instance identity, lease resilience) shipped and verified | Multi-project isolation on single host |

## Multi-Provider Dispatch

| Claim | Status | Evidence artifact | Notes |
| :--- | :--- | :--- | :--- |
| providers.json is the single worker/provider manifest (two classes) | PROVEN | `internal/config` + DELTA-10 (APPLIED) | api_call vs platform_dispatch |
| Per-provider claim affinity on the durable queue | PROVEN | #457/#458/#460 — `json_extract(request_json, '$.provider')` claim filter, unit-tested | Strict equality; unfiltered workers claim everything |
| No silent provider fallback | PROVEN | `TestProviderResolutionOrder` (#458) | Unknown explicit provider fails the attempt listing available names |

## Observability & Evidence

| Claim | Status | Evidence artifact | Notes |
| :--- | :--- | :--- | :--- |
| Unbuffered stdout/stderr streaming per attempt | PROVEN | `internal/worker` UnbufferedPipeStreamer | Feeds Evidence Lake export |
| Evidence Lake export per attempt | PROVEN | `worker.ExportReceipt` + `G8S_EVIDENCE_DIR` | |
| Harness self-audit probes registered in the eval suite | PROVEN | #451: 4 probes in `internal/harness/probe` DefaultSuite; RELEASE_SOP Gate 8 (D-02) wired | 4/4 probes passed in v0.14.0 release gate via `g8s eval run --category self-audit` |
| Enterprise access-audit log | UNPROVEN | docs/designs/access-audit-design.md (#441) | Design only; no code |
| Downtime/availability measurement | UNPROVEN (by design) | docs/decisions/0027-availability-measurement.md | Single-node CP; revisit condition recorded |

## Autonomous Operations & Verification (v0.14.0)

| Claim | Status | Evidence artifact | Notes |
| :--- | :--- | :--- | :--- |
| Verifier-class registry | PROVEN | SCORECARD S-7, PR #532 (`g8s verify --task <id>`, `.g8s/verifier-classes.yml`, RC1 red-cell suite passing) | Resolves task write scope from receipt; fail-closed unregistered floor |
| Gated auto-merge | PROVEN | SCORECARD S-8, PR #533 (`tools/merger.sh --pr <num> --task <id>`, four fail-closed gates, RC1 red-cell suite passing) | Four fail-closed gates (autonomy, docs lane, verifier, CI) |
| Autonomy ladder configuration | PROVEN | SCORECARD S-8, PR #533 (`g8s config get/set autonomy_level {0,1}`, RC1 red-cell suite passing) | Level 0 manual default; level 1 docs-lane auto-merge; human flip per round |
| Budgeted auto-retry of failed tasks | PROVEN | ADR-0029, PR #501, #502, #539 (root-lineage budget, 5→20→60m backoff, `g8s resubmit`, RC2 red-cell suite passing) | Max 2 per task, 10/hr per store; receipt-free tasks only for unattended ticks |
| Retrospective lessons pipeline | PROVEN | SCORECARD S-9, PR #545, #546 (`g8s lesson create\|verify\|list`, `docs/lessons/ledger.jsonl`, RT-I red-cell tests passing) | Telemetry-backed structured lessons pipeline; enforces verification before append |

## Auth & Secrets

| Claim | Status | Evidence artifact | Notes |
| :--- | :--- | :--- | :--- |
| Actor identity is the `--actor` string (no auth provider integration) | PROVEN (documented limitation) | spec/constitution.md; access-audit design non-goals | Trust model: single-operator host |
| Per-provider auth via env vars (`auth_env`) | PROVEN | `internal/provider` + `internal/config` | Degrades to UNAVAILABLE without env |

## QA Hardening (#480)

| Claim | Status | Evidence artifact | Notes |
| :--- | :--- | :--- | :--- |
| External-input parsers are fuzz-seeded from the incident corpus | PROVEN | #486: 4 native `Fuzz` targets (`config.Load`, receipt envelope, worker result, `SanitizeOutput`) + 30s CI seed runs | Seeds include the #434 escape-pair and #443 echo-poisoning classes |
| Dependency CVEs affecting code break CI | PROVEN | #486: real `govulncheck ./...` step in the Quality workflow | Replaces the comment-only claim; #480 item 1 |
| Queue operation latencies are measured (sub-ms baseline) | PROVEN | #487: `TestPerfSmokeQueueOps` + docs/user-guide/performance.md (submit/claim/heartbeat/finish p50<0.2ms, p95<0.3ms) | Baseline, not SLA; sized the #488 throttle |
| Old-schema DB → new binary migration is E2E-verified | PROVEN | #487: migration E2E (schema v(n-1) fixture → current binary → read/write round-trip) | #480 item 4 |
| Mutation testing runs quarterly with a recorded score | PROVEN (fallback method) | #480: hand-mutation study #1, 5/5 caught (M1–M5, dispatch/receipt/harness/worker); go-mutesting panics on Go 1.26 — runner gap on the tooling backlog | Quarterly cadence clocked as D-08 |

## How to read this ledger

- PROVEN rows bind to a named test in this repo (search `grep -rn "<TestName>" *_test.go`) or to an
  APPLIED decision/spec. If the binding breaks, `tools/claims_check.sh` fails for claims.yml-backed rows.
- UNPROVEN ledger rows describe designed capabilities lacking CI test proofs; they must not appear in sales/marketing material as facts.
- PARTIAL rows ship with documented boundaries (read the Notes column before citing).
