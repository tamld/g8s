# Task: F1 — effort adapter must coerce to the baked-name model level (v0.16 release blocker)

Repo: g8s @ main. Release-blocker fix discovered by live dispatch,
findings recorded on the tracking issue. Read first:

- internal/routing/effort.go — the adapter (the only file you
  modify, plus its test)
- internal/routing/effort_test.go — existing table coverage
- .g8s/agent-models.yml — effort catalog (family
  `gemini-3.8-flash`, no baked-suffix variant ids)
- internal/dispatch/argv.go lines 21-24 and 53-55 — evidence: the
  effort level is emitted verbatim as `--effort` to the worker CLI

## The defect (evidence: tasks 8608dfbf / 237be821, 2026-10-07)

The provider manifest (providers.json) declares the agy model
`gemini-3.8-flash-high` — an effort-BAKED model id. The effort
catalog has no entry for that exact id (only the family
`gemini-3.8-flash`), so the adapter resolves a nil entry and its
unknown-entry rule passes the requested level through verbatim.
Live result: class `docs` requested `low`; dispatch emitted
`--model gemini-3.8-flash-high --effort low`; the worker CLI
rejected the combo ("invalid model selection") and the task died.
Any per-class effort below `high` is currently undeliverable on the
default provider model — the cost-per-class telemetry row this
release exists to ship cannot be measured at all.

The worker CLI contract (verified live): a baked-suffix model id
(`-low`/`-medium`/`-high`/`-xhigh`/`-max`) accepts `--effort` only
when it EQUALS the baked suffix; mismatched levels are rejected
outright; omitting `--effort` defers to the model's own default.

## Required fix (internal/routing/effort.go + effort_test.go)

Introduce baked-suffix awareness BEFORE the unknown-entry
pass-through rule:

1. Detection: model id ends with `-low` / `-medium` / `-high` /
   `-xhigh` / `-max` (the ladder suffix set) → baked level = that
   suffix. Pure helper, table-tested, case-sensitive.
2. Baked coercion rule (replaces pass-through for these ids):
   - applied = the baked level ALWAYS (that is the truth of what
     will run — this is a self-measuring release).
   - requested "" (not specified) → applied = baked, no mismatch.
   - requested == baked → applied = baked, no mismatch.
   - requested != baked (including hard-refuse-exempt levels like
     "none") → applied = baked, mismatch = true, recorded, never
     an error — EXCEPT the existing mandatory+none hard-refuse
     which keeps its current behavior for named styles; for a
     baked id, mandatory+none still hard-refuses (the operator
     asked for none, the model cannot do none, refusing locally is
     correct).
   - budget-token fields: zero for baked ids (no budget map).
3. Non-baked ids: every existing rule row unchanged — unknown nil
   entries still pass through verbatim (that rule now has the
   baked exception documented at its branch).
4. The Decision fields keep their existing names/omitempty
   semantics; only the resolution rule changes.

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/routing/effort.go
- internal/routing/effort_test.go

Do NOT run git commit. Do NOT touch internal/dispatch, .g8s/, or
any other package. Red-first: add the failing table rows BEFORE the
implementation change in the same file set (commit-order does not
apply — both files land together), covering at minimum:

- (gemini-3.8-flash-high, low) → applied high, mismatch true
- (gemini-3.8-flash-high, high) → applied high, no mismatch
- (gemini-3.8-flash-high, "") → applied high, no mismatch
- (gemini-3.8-flash-medium, low) → applied medium, mismatch true
- (some-model-xhigh, xhigh) → applied xhigh, no mismatch
- non-baked unknown id (no suffix) → unchanged pass-through
- baked id + mandatory + none → typed hard-refuse error
- suffix-lookalike that is NOT a ladder suffix (e.g.
  `model-maxv2`, `flow-highx`) → NOT baked, pass-through

## Constraints

- stdlib; `go test ./internal/routing/` green; `go build ./...`
  green; gofumpt + golangci-lint clean.
- `bash tools/ci_doc_contract_check.sh` green (no doc references
  change, but run it).

## Report

- The helper signature, the rule-table diff (old vs new behavior
  per row), test counts before/after, full `go test` output lines
  for the package.
