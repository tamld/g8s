# Task: Fix #441 — Enterprise Evidence Ledger + access-audit/SLA measurement decision

Repo: g8s @ main. Tracking: tamld/g8s#441 (P3 transparency polish; docs-only).

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- docs/ENTERPRISE_LEDGER.md (new file)
- docs/designs/access-audit-design.md (new file)
- docs/decisions/0027-availability-measurement.md (new file — a short ADR
  recording the downtime-measurement decision, following the format of
  docs/decisions/0025-cap-tradeoff-single-node-cp.md)

Do NOT run git commit. Do NOT modify any other file. Two other workers are
operating in parallel on unrelated files (internal/harness/probe/,
internal/worker/) — touching anything outside the list above corrupts
their delivery. NOTE: another worker owns tools/ci_link_integrity.sh this
wave; do not touch tools/ at all.

## Required content (all English, match existing docs tone)

### 1. docs/ENTERPRISE_LEDGER.md — claim inventory ledger

The pattern: every enterprise-grade claim = one row with (claim, evidence
artifact, status PROVEN/UNPROVEN, where the evidence lives). Seed the
inventory by READING these repo artifacts and citing real paths — do not
invent claims:

- docs/claims.yml (the claims registry — map each bound claim to its row;
  unbound claims become UNPROVEN rows)
- docs/security/threat-model-stride.md (containment/process-isolation
  claims)
- docs/decisions/0025-cap-tradeoff-single-node-cp.md (CAP/single-node
  consistency claim)
- spec/constitution.md (Zero-CGO, pure-Go claims)
- manifest.json (permissions/receipts model claims)

Suggested sections: Zero-Trust Delegation / Containment & Process
Isolation / Multi-Provider Dispatch / Observability & Evidence / Auth &
Secrets. Each row: Claim | Status | Evidence artifact (path) | Notes.
End the doc with "How to read this ledger" (PROVEN = bound to a
CI-verifiable test or applied ADR; UNPROVEN = designed but not yet
measured/enforced — with the tracking issue where one exists, e.g.
#441's own access-audit rows start UNPROVEN).

### 2. docs/designs/access-audit-design.md — design note (design only, no code)

- What to log: every control-plane mutation surface (submit, claim,
  finish, cancel, receipt issue/consume/revoke, config set/unset) as
  actor + action + subject + trace_id + timestamp.
- Where: a dedicated append-only audit table/db (name the candidate:
  audit log in the existing g8s.db via a new table, or a separate
  audit.db — recommend one, with the receipts.db PRAGMA user_version
  split-brain lesson from the g8s campaign as the argument for keeping it
  beside operation state or isolating it deliberately).
- Retention: propose a TTL/budget policy consistent with the Evidence
  Lake retention approach.
- Explicitly distinguish from operation telemetry (G8S_TELEMETRY /
  internal/worker telemetry: perf/stream metrics) — access audit answers
  "who did what", telemetry answers "how did it perform".
- Non-goals: no auth/identity provider integration in v1 (actor is the
  existing --actor string; trust model documented).

### 3. docs/decisions/0027-availability-measurement.md — the decision

Record the decision that availability/downtime is **"not measured, by
design"** for the current single-node control plane (per #441's primer
cross-check: no downtime numbers exist today; CP is single-node), with
the context (what 99.9% would mean: 52min/year), what WOULD change the
decision (multi-node CP, enterprise SLA contract), and status: Accepted
with revisit condition. Follow the exact ADR structure used by 0025
(context / decision / consequences / status header).

## Constraints

- Do not modify docs/claims.yml (its schema is enforced by
  tools/claims_check.sh; the LEDGER references it read-only).
- No local filesystem absolute paths in committed content (repo-relative
  paths only).
- Cite issue numbers where the ledger row is backed by a shipped PR
  (#399/#400/#401 containment, #417/#423 lifecycle, #457/#458/#460
  multi-provider, #445 sanitizer, #450 worktree facet, #456 claims
  registry, #458/#460 provider path) — verify each against the repo
  (git log --oneline --grep or the docs) before citing; do not cite PRs
  you cannot verify exist.
