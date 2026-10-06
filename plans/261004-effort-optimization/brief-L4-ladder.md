# Task: L4 — quality-ladder machinery: diagnosis rung, policy, CLI driver, gauges (issue #550 DoD 4-5)

Repo: g8s @ main (C1 + W2 landed). The effort wave's Phase 4 organ:
the quality ladder that routes class-check failures. Read first:
plans/261004-effort-optimization/brainstorm-rails.md (Q2 + pass-2
amendments P1/P2/P4/P6 — the RATIFIED semantics, they win over this
brief where they conflict) + internal/routing/effort.go (C1 view:
ModelEffortView/SupportedEfforts) + internal/lane/effortclass.go (W2
classes) + internal/worker usage capture (W2 telemetry events) +
cmd/g8s/resubmit.go + internal/controlplane/retry.go (the mechanics
the ladder rides; parent_task_id lineage + ListChildTasks).

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/ladder/ladder.go (new — shapes, policy, rung plan, evidence
  packet, gauges; split into classifier.go/policy.go/gauges.go if
  cleaner) + internal/ladder/*_test.go
- cmd/g8s/ladder.go (new — the CLI subcommands) + the minimal
  registration line in cmd/g8s/main.go + cmd/g8s/ladder_test.go

Do NOT run git commit. Do NOT touch internal/worker,
internal/orchestrator, internal/routing, internal/lane,
internal/controlplane (consume their exports; never fork or edit them
— the DEBT-34 layer gate treats worker+CLI as disjoint).

## Required implementation

1. **Failure-shape classifier** (deterministic v1): input = failure
   evidence (error text / failure signature, exit code, optional
   contract-violation report); output = shape ∈ {effort-shaped,
   brief-shaped, env-shaped} + reason. env-shaped markers: transport/
   spawn failures, timeouts, binary/CLI errors (ADR-0029 signature
   classes). brief-shaped markers: contract violations, harness/scope
   rejections, missing inputs, prompt/validation errors. Everything
   else (the task ran and its class checks failed) = effort-shaped —
   that is the ladder's trigger by construction. Non-effort shapes
   route to HITL immediately (never burn rungs on a mis-shaped
   problem).
2. **Ladder policy** (rails Q2 + P2): rung 0 = the diagnosis rung
   (cheap, read_only, classifies + routes). Rungs 1..3 = effort +1
   step along the model's SupportedEfforts subset (use the C1
   ModelEffortView semantics; baked-name agy: low→medium→high).
   Rungs 4..5 = model alternative (next candidate serving the same
   class from the manifest). Rung 6 = HITL mandatory. The cap is 6
   rungs/task — the HARD CEILING, not the target. The budget is
   TOKEN-denominated: optional `ladder_token_budget` per class
   (extend .g8s/effort-classes.yml with the field — fail-open when
   absent); cumulative lineage tokens (from W2 usage telemetry on
   child tasks) exceeding the budget ⇒ HITL early. Early termination:
   checks PASS ⇒ done; non-effort-shaped ⇒ HITL now.
3. **Rung execution rides the existing resubmit mechanics**: each
   rung is a NEW task with parent_task_id lineage (controlplane
   SubmitTaskRequest.ParentTaskID / resubmit path). The ladder
   computes the plan; the CLI executes it. No daemon, no autopilot
   changes.
4. **CLI** (`g8s ladder`):
   - `ladder status <task-id>`: lineage walk (ListChildTasks), rung
     position, shape verdicts so far, tokens per rung, the next-rung
     plan (or HITL verdict).
   - `ladder advance <task-id>`: classify the latest failure and
     execute the next rung (diagnosis dispatch at low/read_only, or
     resubmit with the escalated effort / alternative model). Refuses
     to advance past the ceiling or over the token budget (typed
     error). Refuses when the shape is non-effort (prints the HITL
     packet instead).
   - `ladder gauges [class]`: from telemetry trace events — pass-rate
     per (class, effort), escalation-rate per class (rungs fired /
     tasks), HITL-rate (packets / rounds). These are the two P4
     gauges + P6 metric; default changes stay operator-ratified (the
     gauge only reports).
5. **Evidence packet** (P2/P6): on HITL, emit one packet (JSON on
   stdout, `--out` file optional): ladder history (every rung: shape,
   verdict, effort, tokens), the final verdict, the exact failing
   checks, tokens per rung — the cost receipt of the whole ladder.
6. **Staleness sensor** (DoD 6): when a dispatch fails with a 400-class
   provider rejection naming an effort level, the classifier tags the
   failure `catalog-stale(model, level)` in the event so the canary
   work (P3, not in this slice) can consume it later.

## Constraints

- stdlib; `go test ./internal/ladder/ ./cmd/g8s/` green;
  `go build ./...` green; gofumpt + golangci-lint clean;
  `bash tools/ci_doc_contract_check.sh` green.
- Red-first table tests: classifier matrix (env/brief/effort/ambiguous
  → with the ambiguous rule pinned), rung progression (effort +1
  within a subset, subset exhaustion → model-alternative rungs,
  ceiling enforcement), token-budget early HITL, PASS early
  termination, advance refusals, evidence packet completeness (every
  rung has shape+verdict+tokens), gauge math on synthetic event sets.

## Report

Exported types/signatures, the classifier rule table, the rung state
machine, the CLI surface, and the test matrix with pass counts.
