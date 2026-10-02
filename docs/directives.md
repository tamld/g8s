# Directive Clocks Ledger

Every directive in g8s carries its own falsification clock: the incident it
was born from, when it expires, and the condition under which it is
simplified or removed. **A directive that stops catching its incident is
simplified by amendment** (ADR-0024 / ADR-0026). One row per directive —
grep-friendly, single source of truth (ratified ADR-0026 §5.2, 2026-09-30).

Format: `ID | Directive | Born from | Clock / simplify-if | Status`

| ID | Directive | Born from | Clock / simplify-if | Status |
| :--- | :--- | :--- | :--- | :--- |
| D-01 | Red Cell pass before every release (public surface only); a skip requires a named waiver in the release notes | v0.12.0 field test caught #433–#436 that CI and all campaign gates missed (#433–#436) | Simplify if a Red Cell pass reports zero findings across 3 consecutive releases AND no external report (cf. #435/#466) arrives between releases | **ACTIVE** (hard, ratified 2026-09-30) |
| D-02 | Harness self-audit probes run per-release; results recorded in the release notes | #443 refusal-echo poisoning poisoned 3 deliveries in one day; FAILED-with-delivery class (d4159300) | Simplify a probe that stops catching its incident (each probe cites its incident; see `internal/harness/probe` sa-001…sa-004) | **ACTIVE** (per-release, ratified 2026-09-30; first run: v0.13.0 notes, 4/4 passed) |
| D-03 | PRI release gate stays advisory until PRI ≥ 0.8 on 2 consecutive eval runs | STRIDE threat model scoring 0.625 at first live eval (#379) | Hardens automatically when the threshold is met on 2 consecutive runs | **ADVISORY** (armed) |
| D-04 | L3 Jev gate is advisory-by-default; blocking semantics are earned only after enriched runs prove trust | #411 usage audit; #418 promotion trust-earning path | Harden when enriched-run trust metrics satisfy the #418 promotion path | **ADVISORY** (armed) |
| D-05 | Parallel drain allowed for disjoint receipt scopes; shared-file tasks stay sequential | Sequential directive survived on stale evidence past #427 PR-2 (the clock's founding catch) | Simplify if shared-checkout contention reappears under the #448 delivery contract (would re-narrow the directive) | **ACTIVE** (upgraded 2026-09-29) |
| D-06 | Coverage ratchet: aggregate coverage may not drop more than 0.25% below the committed baseline | Quality workflow coverage floor | Baseline auto-commits upward on main; simplify only if it blocks fixes twice in one campaign | **ACTIVE** |
| D-07 | Event-driven supervision: the supervisor wakes on terminal-event signals (`<state_dir>/signals/tasks.jsonl` via `g8s watch --failed`, #481); polling is the FALLBACK — one one-shot deadline check per wave; heartbeat sampling is diagnostic only. A scheduled check that fires while signals flowed is a bug | Wave-B overnight silent death (2026-10-01): 3 tasks honestly FAILED while the session slept, found only by morning polling; 12-second heartbeat-delta probes and 30-second CI poll loops burned quota all campaign (#481) | Simplify if a terminal transition ever fails to reach the signal file while the fallback deadline check catches it (would mean the post-commit append guarantee regressed) | **ACTIVE** (ratified 2026-10-02) |

## Review ritual

At every release cut: walk this table, run D-02, and check every clock.
A row whose incident can no longer be reproduced by its own probe goes to
the amendment queue with the release notes as the witness.
