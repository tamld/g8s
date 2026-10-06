# Task: R4 — Red-cell verification of submit + ladder CLI surfaces (v0.16 gate G1)

Repo: g8s @ main. Hard verification gate (D-01) before the v0.16.0
tag. Your job: falsify the CLI flag validation, payload wiring, and
output envelopes of the submit effort knob and the ladder
subcommands. You are the reason defects do not ship.

Read first (in order):

- cmd/g8s/submit.go — the --effort flag, default medium, parse-time
  validation, payload keys (effort omitted when applied is "",
  effort_requested/effort_applied always when the adapter ran,
  effort_budget_tokens / effort_mismatch when non-zero/true,
  hard-refuse exits BEFORE store.SubmitTask)
- cmd/g8s/ladder.go (or wherever the ladder subcommand lives —
  locate it) — status / advance / gauges subcommands, --json
  envelope, --out evidence file
- existing submit_route_test.go / submit_effort_test.go (whichever
  exist) — do NOT duplicate, target what they miss

## Delivery protocol

Scratch worktree. Write ONLY to:

- cmd/g8s/redtest_submit_ladder_test.go (new file — the only file
  you may create or modify)

Do NOT modify any non-test file. Do NOT run git commit. A case that
fails against current code is a FINDING: mark it
`t.Skip("FINDING-R4-k: <one-line mismatch>")` with a preceding
comment stating expected vs actual, keep the file compiling and
green. Never patch the implementation to make a case pass.

## Required verification dimensions (table-driven)

1. --effort validation: default lands as medium in the payload;
   every level on the ladder accepted; off-ladder and empty-string
   levels rejected with the typed usage error naming the allowed
   ladder; validation happens at parse time (before any store
   call).
2. Payload wiring: effort key ABSENT from payload when applied is
   ""; effort_requested/effort_applied present exactly when the
   adapter ran; budget tokens and mismatch flags only when
   non-zero/true — confirm omitempty holds for each key.
3. Hard-refuse path: mandatory provider + --effort none exits
   BEFORE queueing (no task row, clean usage exit naming provider
   and model); every other refused combination stays queueable.
4. Ladder subcommands: status on a nonexistent task id (typed
   error, JSON envelope shape intact); gauges with and without a
   class filter (empty-state output shape); advance on a
   nonexistent task; --out writes evidence only under the allowed
   scope; --json vs default output both parse.
5. Flag edges: repeated --effort flags (last-wins or error per the
   convention in neighboring flags); --effort with an unknown
   provider/model in a dry-shaped invocation; interplay with
   --permission validation errors (permission error must not mask
   the effort error).
6. Envelope discipline: every error path emits the same JSON
   envelope schema (kind, cmd, error fields) as success paths.

Follow existing test conventions in cmd/g8s for invoking the
command layer (helpers, temp stores) — reuse them.

## Constraints

- stdlib only; `go test ./cmd/g8s/` green; `go build ./...` green;
  gofumpt clean.
- Every table case carries a comment naming the rule it verifies.
- Keep runtime modest: the cmd/g8s package suite is timing-sensitive
  on 2-core CI; avoid sleeps and heavy loops.

## Report

- Number of cases per dimension, pass/skip counts.
- The FINDING list (R4-1, R4-2, ...): rule, expected, actual,
  severity guess (release-blocker / polish).
- If ZERO findings, say so explicitly and list the three strongest
  falsification cases you ran.
