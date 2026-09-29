# ADR-0026: Independent Verification Phases & Directive Falsification Clocks

**Status**: Proposed (operator ratification pending on §5 decision points)
**Date**: 2026-09-29
**Deciders**: Brain/main agent (operator-directed gap-closing program)
**Related**: ADR-0021 (SOM §4 E2E checklist — amended by §3.2 here); ADR-0024 (falsification clock for gates — extended to directives and claims); #434 (escape incident); #443/#444 (self-referential harness defect); #447 (claims registry); #448 (delivery contract); #449 (self-audit suite)

---

## Context

The 2026-09-29 field-fix campaign (#434) surfaced defects that no single
phase owned. One sanitizer regression escaped a released binary (v0.12.0).
Three worker deliveries were destroyed by a harness defect (#443) whose
trigger was the harness's own test fixture. The delivery contract for
workspace_write proved non-deterministic (#448): three same-day runs, three
different delivery outcomes, delivery working only by agent choice.

Mapped to the four delivery phases, the gaps are:

| Phase | Gap (evidence) |
|---|---|
| **Design** | No delivery contract for workspace_write — isolation was designed, materialization of the deliverable was left to agent behavior (#448) |
| **Implement** | Code contradicts its own contract surfaces: `Pool.Release` deletes uncommitted work while the drain log promises "kept for inspection" (#443-f2) |
| **Test** | Path-resident components are tested only in plain encoding: the refusal detector passed all tests while its own fixture string was a production trigger (#443); the sanitizer passed all tests while corrupting JSON-transported payloads (#434) |
| **Validate** | No independent audit phase exists. Every finding that mattered arrived via (a) the operator's manual adversarial field test (#433–#436 — highest yield) or (b) post-failure forensics. Quantitative claims are unbound to verifying artifacts (#447) |

The common root: **verification is self-verification.** The worker tests its
own output and the supervisor — who wrote the brief and the acceptance
oracle — verifies it. No phase independent of the authoring chain inspects
the feature through its public surface.

## Decision

### 1. Red Cell release gate (validate phase — institutionalizes what worked)

Before any release cut, an **independent Red Cell pass** runs: a worker (or
separate session) receives ONLY the public surface (README, release assets,
CLI help, offer bundle) plus an adversarial brief — never the internal
design, briefs, or acceptance criteria of the feature under audit. Its job
is to reproduce the operator's v0.12.0 field test mechanically: install,
exercise documented claims, attempt to make documented behavior diverge
from observed behavior.

- Findings become issues, triaged before release.
- A release ships without a Red Cell pass only under an explicit operator
  waiver recorded in the release notes.
- Rides the existing S6 audit fan-out runner machinery (read_only verifier
  packets) — no new transport.

### 2. Transport-fidelity test rule (test phase)

Any component that sits on the worker-output path — sanitizers, gates,
classifiers, envelope parsers — MUST carry, besides plain-text unit tests,
at least one test that exercises the **actual transport encoding**: JSON
string containment, nested JSONL, escaped newlines/quotes, and content
adjacent to structural delimiters. Plain-encoding tests are necessary but
not sufficient for such components; #434 and #443 both passed full suites
while failing in transport.

Additionally: every field-found incident gets an E2E replay case through
the real transport (as `TestSanitizeJSONTransportFidelity434` does for
task `9e1f301d`), not only a deterministic-isolation unit test.

### 3. Directive falsification clock (operations)

Standing directives constrain behavior for sessions; they are subject to
the same falsification discipline as gates (ADR-0024). Each standing
directive records: (a) the justifying evidence, (b) the mechanism or
condition it constrains, and (c) a re-test trigger — when the underlying
mechanism changes, the directive is re-validated or retired. A directive
whose justifying evidence no longer holds is amended, not inherited.

First application: the "sequential drain for code-writing tasks" directive
(justified by the pre-#427 3-way drain failure) enters re-test the moment
its constraining mechanism changed — per-attempt worktree isolation
(#427 PR-2). The 2026-09-29 parallel-drain experiment (tasks `6c2a3ee5` +
`92a00db3`, `--concurrency 2`, disjoint receipts) is the registered
re-test; its verdict amends or retains the directive.

### 4. Harness self-audit suite (gate-the-gates)

Gates inspect workers; nobody gates the gates. A scheduled self-audit
suite (#449) probes the harness against its own known failure classes —
refusal-classifier echo poisoning, receipt bypass attempts, worktree
discard of non-empty deliverables, sanitizer transport corruption — using
the existing `g8s eval` probe machinery. Each probe encodes a real incident
(falsification citation required, same as gates). A probe that stops
catching its incident is simplified by amendment.

### 5. Cross-references closing the phase gaps

- Design gap → #448 acceptance (delivery contract defined in the harness,
  not the agent).
- Validate gap, claims dimension → #447 Claims Registry + SCORECARD
  (claim ↔ test/eval-ID binding; unmapped quantitative claims are
  labeled aspiration).

## Red Test Proof

Each mechanism cites its incident: Red Cell gate ← the operator's v0.12.0
field test catching #433–#436 that CI and all campaign gates missed;
transport-fidelity rule ← #434 (JSON corruption) + #443 (fixture
self-trigger) both passing full suites; directive clock ← the
sequential-drain directive surviving on stale evidence past #427 PR-2;
self-audit suite ← #443's detector poisoning three deliveries in one day.

## §5 Decision points for operator ratification

1. Red Cell gate becomes a hard release requirement (waiver path as
   described) — or advisory for the next two releases?
2. Directive clocks recorded in the SOM §6 automation loop vs a dedicated
   `docs/directives.md` ledger?
3. Self-audit cadence: per-release, per-campaign, or scheduled (weekly)?
