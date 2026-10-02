# Enterprise Evidence Ledger

Every enterprise-grade claim in g8s, one row each, with the artifact that
backs it. **PROVEN** = bound to a CI-verifiable test or an applied decision.
**UNPROVEN** = designed but not yet measured or enforced (with the tracking
issue). The registry of quantitative claims lives in `docs/claims.yml`
(verified by `tools/claims_check.sh`); this ledger is the enterprise-facing
view over the same evidence.

Last updated: 2026-10-02 (issue #480 closure: QA Hardening rows + #448 delivery flip).

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
| Cross-session containment on one host (multi-project) | PARTIAL | #465 + hot fix #468 | Host-wide sweeps de-fanged (identity-scoped ghost kill; state-dir worktree root); instance identity + lease resilience are P1 |

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
| Harness self-audit probes registered in the eval suite | PROVEN | #451: 4 probes in `internal/harness/probe` DefaultSuite (incidents #443, #443-f2/#450, #434/#445, receipt-bypass) | Runner wired to release checklist pending (ADR-0026 §1) |
| Enterprise access-audit log | UNPROVEN | docs/designs/access-audit-design.md (#441) | Design only; no code |
| Downtime/availability measurement | UNPROVEN (by design) | docs/decisions/0027-availability-measurement.md | Single-node CP; revisit condition recorded |

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
- UNPROVEN rows are aspirations with owners; they must not appear in sales/marketing material as facts.
- PARTIAL rows ship with documented boundaries (read the Notes column before citing).
