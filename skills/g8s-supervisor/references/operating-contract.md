# g8s Customer-Supervisor Operating Contract

## PRD

**User:** workspace supervisor/key user. **Consumer:** g8s PIC after explicit issue
publication authorization. **Job:** prove a bounded g8s workflow at an identified
executable and scope. **Non-goals:** stabilize g8s, pick provider/model, or replace
project governance.

Metrics: qualified-pilot rate, receipt-substance pass rate, reproduction completeness,
human rework, elapsed time, scope violations, tactic verdict. Missing measurement is
`unknown`, never zero.

## SRS

1. Accept one sanitized pilot JSON record.
2. Validate required fields, state transition, DoR/DoD/DnD, redaction declaration,
   evidence references, and draft packet completeness.
3. Emit sorted diagnostics and render an issue/proposal Markdown draft only from a
   valid record.
4. Do not run g8s, shell, HTTP, GitHub, network, provider selection, dispatch, or
   receipt commands.
5. Render to stdout only. Caller persists a reviewed draft under project artifact rules;
   this tool never writes runtime records.

## Latest is an evidence state, not a command

`QUALIFIED_CURRENT` means exact executable digest matches an immutable upstream artifact
or reproducible build record, upstream ref is fresh at qualification time, and a bounded
regression pilot passed. `QUALIFIED_LIMITED` means any link is missing, stale, or only
partially proven. New upstream commit, release, checksum change, executable digest change,
or elapsed freshness window invalidates current qualification. Re-qualify before dispatch.

Never use checkout `HEAD`, release tag, executable mtime, or `--version` alone as latest
proof. They describe different objects. Release may lag `main`; checkout may lead release;
local executable may match neither.

Qualification receipt must record: remote/ref retrieval timestamp, release tag/asset or
build commit, trusted checksum/digest comparison, local executable digest, capability probe
receipt, and regression pilot verdict. If trusted artifact matching is unavailable, dispatch
stops or downgrades to explicit limited `read_only` regression only.

SSoT is a signed/tagged release artifact or immutable build provenance plus recorded digest;
not mutable local path, documentation, or branch name. The supervisor maintains a
per-pilot qualification receipt and invalidates it on any observed divergence.

### Qualification algorithm

1. Fetch remote default-ref SHA, latest release/tag, release asset checksum, and issue/fix
   baseline without touching g8s runtime state.
2. Hash target executable. Match it to trusted release asset checksum, or record an approved
   reproducible build attestation mapping executable digest to immutable commit.
3. Compare ref ordering: release commit against default-ref SHA. If default ref is ahead,
   label release `latest released`, not `latest project`; require explicit policy choice.
4. Run only an explicitly authorized, safe capability probe and bounded `read_only`
   regression. Capture sanitized result.
5. Write qualification receipt. Set `QUALIFIED_CURRENT` only if every link passes. Otherwise
   set `QUALIFIED_LIMITED`, `UNQUALIFIED`, or `QUARANTINED`; never guess or auto-update.
6. Before each dispatch, re-check expiry and local digest. Re-run steps 1–5 after upstream or
   executable changes.

`latest` must be declared as one policy: **latest released**, **latest default branch build**,
or **approved pinned release**. There is no universal “latest.”

**Inversion:** if a local digest is not tied to a trusted artifact/build and a passing
regression pilot, stale behavior can be accepted as product truth and contaminate every
later issue and tactic metric.


## Admission matrix

| State | Proof | Allowed next action |
|---|---|---|
| `UNQUALIFIED` | identity, provenance, or capability missing | evidence capture or stop |
| `QUALIFIED_LIMITED` | older/partial build proof | explicit low-risk `read_only` regression pilot |
| `QUALIFIED_CURRENT` | immutable build/release provenance plus matching capability evidence | specified pilot after authorization |
| `QUARANTINED` | contract breach, repeated theater, scope breach, or sensitive-data risk | sanitized issue/proposal draft or stop |

Identity evidence hierarchy: canonical executable path plus digest; self-reported
version/build; immutable provenance; capability evidence; regression pilot. Hash only
identifies file. `[HYPO]` A provenance claim remains untrusted until tied to the exact
executable used by the pilot.

## FSM transitions

```text
INTAKE: CLASSIFIED, NOT_APPLICABLE, NEEDS_INFO
CLASSIFIED: READY
READY: IDENTITY_CAPTURED
IDENTITY_CAPTURED: QUALIFIED_CURRENT, QUALIFIED_LIMITED, UNQUALIFIED, QUARANTINED
QUALIFIED_CURRENT/QUALIFIED_LIMITED: PILOT_SPECIFIED
PILOT_SPECIFIED: AWAIT_DISPATCH_AUTH
AWAIT_DISPATCH_AUTH: DISPATCHED, STOPPED
DISPATCHED: RECEIPT_CAPTURED, INCIDENT
RECEIPT_CAPTURED: SUBSTANCE_VERIFIED, THEATER, REQUIREMENT_VIOLATION, EVIDENCE_GAP, INCIDENT
THEATER: ISSUE_DRAFTED(fix), ISSUE_SUPPRESSED, RETEST_PLANNED, STOPPED
REQUIREMENT_VIOLATION: ISSUE_DRAFTED(optimization), ISSUE_SUPPRESSED, RETEST_PLANNED, STOPPED
EVIDENCE_GAP/INCIDENT: RETEST_PLANNED, STOPPED
SUBSTANCE_VERIFIED: PROPOSAL_DRAFTED(new_scope), LEARNING_RECORDED
ISSUE_DRAFTED: AWAIT_PUBLICATION_AUTH
AWAIT_PUBLICATION_AUTH: PUBLISHED, ARCHIVED
ISSUE_SUPPRESSED/PROPOSAL_DRAFTED/PUBLISHED/ARCHIVED/STOPPED: LEARNING_RECORDED

Classification law:
- `receipt_kind:"error"` reaches `THEATER`, then only `issue_type:"fix"`.
- `receipt_kind:"success"` plus complete exact acceptance evidence with a declared `fail`
  reaches `REQUIREMENT_VIOLATION`, then only `issue_type:"optimization"`.
- Successful all-pass evidence reaches `SUBSTANCE_VERIFIED`; a proposal thereafter has
  `proposal_basis:"new_scope"`, never a discovered behavior defect.
- Missing, unknown, or extra acceptance result stays `EVIDENCE_GAP`; no draft route.
- Every issue draft requires caller-supplied sanitized upstream issue cross-check: `clear`,
  fresh at transition, and matching `admission.upstream_ref`. Other status is `ISSUE_SUPPRESSED`.
```

## Gates

### DoR

- One objective, task class, expected result, acceptance checks, timeout, allowed scope,
  worker role, permission, evidence sink, and stop conditions.
- Fresh executable identity and admission evidence.
- `read_only` default. Writes require separate explicit authorization and path-scoped
  receipt proof.
- Sanitization/redaction plan and competing hypotheses: binary drift, g8s defect,
  prompt/role mismatch, environment fault, invalid expectation.

### DoD

- Every transition has timestamped evidence.
- Receipt/output passes shape, content, scope, and declared acceptance checks.
- Claims separate `[CODE]`, `[RUNTIME]`, `[INFER]`, `[HYPO]`; unknowns stay visible.
- Learning verdict is `reuse`, `avoid`, or `retest`, with reason.

### DnD

No completion claim from `SUCCEEDED`, `result.ok`, exit zero, release tag, docs, local
source HEAD, or worker assertion alone. No completion with missing provenance, scope,
redaction, substance, checks, or unresolved contradiction.

## Receipt verdict

Read state as transport metadata. `kind:"error"` produces `THEATER`. A successful run
with complete exact acceptance evidence and a declared failed check produces
`REQUIREMENT_VIOLATION`. Incomplete output, missing evidence, unknown result, or extra result
produces `EVIDENCE_GAP`. None produces `SUBSTANCE_VERIFIED` unless every declared check passes.

## Publication gate

Issue/proposal is draft until human explicitly authorizes publication. Draft contains:
title, severity or target scope, sanitized reproduction/evidence, expected/actual,
impact, requested direction, redactions, and unresolved questions. Never copy secret,
provider configuration, raw prompt, or raw receipt.
