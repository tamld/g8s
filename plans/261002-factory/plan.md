---
plan: 261002-factory
issue: none (operator directive 2026-10-02)
status: active
created: 2026-10-02
mode: hard (red-test-first; ground truth via scorecard)
origin: operator vision — g8s self-evolves into an "AI factory"
---

# Plan: g8s AI Factory — the self-closing round

**Session type**: T1 (strategy)

## 0. The operator's directive (verbatim intent)

g8s evolves into an "AI factory": harness + loop + LLM that
self-allocates, self-splits jobs, self-evaluates, and self-matures
across rounds. Proven by ground truth, not slogans. **Architecture
correction (operator, 2026-10-02):** the orchestrator THINKS, workers
DO, and **Jev is the distributor** — stream-splitting, routing,
tool selection by context. **Jev is OPTIONAL** — it supports, is
measured, benchmarked; never mandatory (not everyone has Jev). We
pioneer it to prove the system's effectiveness.

## 1. The loop (what "factory" means mechanically)

```text
goal → ORCHESTRATOR (decompose, budget, lane)
     → DISTRIBUTOR (route: provider/model/role/tools — deterministic
        rules always; Jev refines when configured — optional, benchmarked)
     → WORKERS (execute in worktrees, receipts scoped)
     → VERIFIER (task-class acceptance checks; trust earned per class)
     → RETROSPECTIVE (itself a factory task: lessons with cited catches)
     → round N+1
```

## 2. North-star metrics (the anti-slogan machine)

Recorded in SCORECARD.md, one row per milestone, PROVEN only with an
artifact (test, eval run, or recorded live round):

- **M1 · unattended closed rounds/week** — a round = goal → merged PR
  with zero foreground human actions. Baseline: 0 (all merges
  supervisor-owned today).
- **M2 · rework rate** — % of dispatched tasks returning
  NEEDS_INFO/rejected/re-dispatched (decomposition quality).
- **M3 · routing delta** — benchmark: deterministic-only vs
  Jev-assisted routing on the same fixture set (success rate, cost,
  wrong-model rate). Jev earns its place ONLY if the delta is real;
  the deterministic path must never regress.
- **M4 · escapes** — verifier-class misses caught later by humans
  (each escape cites the class and earns a new check or kills trust).

## 3. Autonomy ladder (no leap of faith)

Level 0 (today): supervisor-owned merges. Level 1: docs-class
auto-merge on verifier green. Level 2: code-class with Red Cell pass.
Each promotion requires N clean rounds at the level below, recorded in
SCORECARD. Constitution changes (spec/, directives) ALWAYS stay
human-ratified.

## 4. Waves

- **Wave F (this session)** — the safety floor + the distributor seed:
  - WF1: ADR-0029 implementation, controlplane layer (retry
    classification, budget accounting, resubmit primitive) + settings
    keys. [ADR-0029 Accepted]
  - WF2: ADR-0029 implementation, cmd layer (`autopilot tick` retry job,
    `g8s resubmit`, docs). Contract frozen by WF1's brief.
  - WF3: `internal/routing` — deterministic router (always available) +
    optional Jev enhancement following the ReflexGate pattern
    (Source/IsFallback telemetry, fail-open, deny-invalid) + benchmark
    probes as eval category `task_routing`. No cmd changes (integration
    wave G). [ADR-0030]
- **Wave G** — submit-time routing integration (#513) + lane router
  (ADR-0024 S6-3, #514) consuming internal/routing.
- **Wave H** — verifier-class registry (#515) + first docs-class
  closed round (M1: 0→1, #516).
- **Wave I** — retrospective-as-task + lessons pipeline (#519, v0.15).

## 5. Red tests (milestone gates, written BEFORE the code)

- RT-F (Wave F): a fixture task with docs-only paths routes to the
  docs-class model/role deterministically, no network, and the Jev
  path (mocked) returns a recorded rationale; with no Jev config the
  decision is byte-identical and marked `Source: deterministic`.
- RT-H (Wave H): a docs-class goal dispatched → drained → verified →
  merged with zero foreground actions, scorecard row appended.
- RT-I (Wave I): a retrospective task produces a lesson whose cited
  catch is verifiable in the round's telemetry.

## 6. Non-goals

No daemon (ADR-0025). No multi-host (ADR-0027 posture). LLM never
assigns Lane S, never edits spec/ or directives.md, never mints
receipts (ADR-0024 P0). No verifier grades its own class.
