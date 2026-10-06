# Two-project operations — brainstorm report (stress test strategy)

**Status**: supervisor recommendation applied (operator declined the
fork prompt; defaults marked, one-line flippable). Skill:
ck:brainstorm. **Session type**: T1 advisory.

## Problem

Run 2 projects (g8s v0.16 cut + aegis mercenary) — different languages,
designs, goals — without context interleaving, with optimal resource
use, without goal drift, and without performative completion.

## Evaluated approaches (sequencing)

| Model | Pros | Cons | Verdict |
|---|---|---|---|
| A. strictly sequential (release fully done, then aegis) | zero cross-contamination | wall-clock loss; no code dependency justifies the wait | safe but slow |
| B. day-alternation | flexible | each switch = context load/unload; thread-head loss | motion without value |
| **D. session-sequential + factory-parallel (CHOSEN)** | workers of both projects share the queue timeline (proven: parallel disjoint drains); each session holds exactly ONE project's detail | requires the discipline of closing a handoff before opening the next | recommended default |

The unit that parallelizes safely is the worker queue; the unit that
cannot is the session's detail context. v0.16 cut is short (gated,
half-session); aegis is long (multi-session) — D gives wall-clock
overlap without context overlap.

## Conflict & async doctrine (3 layers, each with an owner rule)

- **context**: handoff = the context-transfer protocol; emergency
  dual-touch rule = "harvest + handoff project N BEFORE opening
  project M" — never "leave it for later".
- **knowledge**: one SSoT per fact (g8s facts in g8s issues/memory;
  aegis facts in its own tracker/docs; cross-project claims on #550
  WITH task-id citations). Evidence contests beat seniority;
  timestamps make staleness visible.
- **runtime**: every shared resource has an incident-derived ownership
  rule — state-dir-per-session (#465/SQLITE_BUSY), identity-scoped
  sweeps (#468), claim-path+push-first (D6), never-reap-while-RUNNING
  (#551), receipt single-use TTL 3600, quota-wall skip+reschedule,
  rebuild-binary-before-trusting-classification. Async = the durable
  queue (workers die with sessions, tasks survive) + signal wakes.

## Guardrail scaling law (2 → N projects)

- per-project contract card (~30 min to write): registry conventions +
  mission line + gate list + DoD. Adding N+1 costs the card, not
  attention.
- invariants across ALL projects: never-trust-verify, merge rule
  pending=0∧fail=0, no-silent, receipt TTL, English artifacts,
  operator ratifies new scope. Variables live in the card.
- **graduation**: onboard → active session work → graduated into
  patrol automation (zero session cost). The brain holds 2-3 ACTIVE;
  unlimited GRADUATED. Bottleneck (measured): CCU 3 write workers +
  review bandwidth — N projects change the queue, not the bottleneck.

## Pre-registered pass criteria (anti-theater)

1. tag v0.16.0 pushed + main CI 5/5
2. aegis Returns Ledger 5/5 items WITH citations (issues, lessons,
   telemetry, ladder, handoff)
3. patrol automation ran on schedule with zero session involvement

Artifact-or-it-didn't-happen; a test with pre-registered criteria
cannot be gamed after the fact. "Hoàn thành stress test" = v0.16
shipped BY DEFINITION — no regression path exists structurally.

## Related experiment (operator proposal, mid-brainstorm): F-recursion ceiling probe

Operator: F0/F1/F2 with n workers self-recursing is "the measurable
ceiling — even if it breaks; we are experimenting". Supervisor opinion:
agree with the epistemics, add the instrumentation requirements:

- what breaks FIRST is measured already: token ceiling (~500K/worker),
  CCU (3 write), quota walls (~1h resets). What is UNMEASURED:
  verification decay per depth level (F1 verifies F2 with less
  context/authority than F0 verifies F1 — silent quality decay is the
  Goodhart risk that multiplies per level) and the coordination tax
  (parent context grows with every F2 result → F1 hits its token
  ceiling FASTER than a flat task).
- containment fact: F2 WRITE-recursion needs receipt delegation —
  ADR-gated, unbuilt. First safe probe = read_only F2 fan-out
  (research/scout), where containment is trivially safe.
- design: fixed task, depth ladder (flat → depth-2 with 1/2/4 F2s),
  telemetry per level, budget cap per run — otherwise "vỡ" burns quota
  without producing the curve. The curve IS the deliverable: the
  exchange rate between tokens, depth, and quality.
- industry tie-in (survey #550): grok-4.20 treats agent COUNT as the
  effort dimension — F-recursion is the second effort axis (depth of
  recursion vs depth of thinking); pass-3 measures one axis, this
  probe would measure the other.
- placement: after v0.16 (self-measuring telemetry is the instrument
  this probe consumes); a read_only probe may ride earlier.

## Next steps

1. Session A: execute plans/261006-v016-cut/plan.md gates G1-G5.
2. Session B (fresh, from the mercenary handoff): aegis.
3. F-recursion probe: brief after v0.16 tag; read_only first.
