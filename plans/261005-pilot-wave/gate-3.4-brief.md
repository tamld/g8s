# GATE 3.4 — real sibling dispatch E2E at `--effort low`

**Goal**: prove the effort machinery on REAL workload (not in-garage):
one read_only dispatch into the sibling (tamld-llm-wiki) submitted with
`--effort low`, flowing submit → adapter → worker → verified report.

## Setup (supervisor, before submit)

- binary rebuilt from main after W2b merges (has `--effort` + classes)
- permission read_only (NO receipt needed), provider agy, model
  gemini-3.8-flash-low (baked-name tier matching the requested level)
- `-add-dir <wiki>/3-Automations -scope-root <wiki>` (scope jail #348)
- the dispatch data point lands on #550 + the SCORECARD row regardless
  of findings (honest no-catch is a valid M2 outcome)

## Task prompt (bounded slice)

Docs-freshness audit of ONE directory: `3-Automations/` (~48 md/py
files) in the sibling wiki. Report ONLY (read_only — nothing writes to
the sibling). Checks, per the Phase 2 finding classes:

1. stale version refs (text citing versions/branches that no longer
   match the sibling's production baseline)
2. date-rot (entries older than 4 months presented as current state)
3. link-rot (relative links whose targets no longer exist)
4. orphans (files nothing links to, candidate for archival)

Output: evidence-grade findings — each with file, line-ish location,
the stale claim, and the observed truth. If nothing is stale: an
honest no-catch report with the files covered.

## Recorded metrics (the actual point of the gate)

- effort_requested=low, effort_applied (adapter output), effort_class
  (unregistered — the sibling path is outside the g8s globs),
  effort_override_down (low < unregistered-medium → true, advisory)
- duration_seconds + input/output tokens (W2 telemetry, first
  real-workload row) → cost-per-class evidence on #550
- latency vs the medium-tier C1 dispatch (408s) and low-tier Phase 2
  audit (218s) — third low-tier data point
