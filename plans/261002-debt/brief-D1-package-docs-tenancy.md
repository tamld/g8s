# Task: Debt D1 — package doc comments ×8 + normative tenancy docs (#465 closure prep)

Repo: g8s @ main. Two mechanical debt items in one docs-adjacent slice.

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/conv/ internal/harness/ internal/memory/ internal/review/
  internal/routing/ internal/sleep/ internal/telemetry/
  internal/supervisor/ — ONLY the single .go file per package that
  should carry the package doc (add/extend the `// Package <name>`
  line; NO logic changes of any kind)
- docs/user-guide/tenancy.md (new)

Do NOT run git commit. Do NOT touch other packages or docs pages.

## Part 1 — the eight missing package docs

The G2 structure renderer prints "(no package doc — add one)" for:
conv, harness, memory, review, routing, sleep, telemetry, supervisor.
For each: read the package's main files, write an honest one-sentence
`// Package <name> — <what it actually does>` doc comment on the
package clause (the renderer takes the FIRST sentence up to the first
period). Rules: describe what IS, not aspirations; no marketing
adjectives; ≤ 120 chars after the prefix where possible. Note for
`supervisor`: the current doc is the single word "enforcer" — replace
with a real sentence.

## Part 2 — normative tenancy docs (completes the ADR-0028 acceptance)

docs/user-guide/tenancy.md: the operator-facing contract for running
multiple g8s consumers on one host. Content (all from ADR-0028 —
ACCEPTED, do not relitigate):
- The one rule: concurrent sessions use DISTINCT G8S_STATE_DIR
  (the state dir is the ownership boundary); how to set it (env) and
  what lives inside it (g8s.db, receipts.db, signals/, worktrees/,
  runs/, evidence/).
- What identity-scoping means in practice: kills/sweeps/reaps touch
  only your own state dir's resources; `--force-foreign`-class admin
  is explicit + attribution-logged.
- Deliberate shared-queue mode: one queue across repos is a declared
  configuration; destructive primitives stay instance-scoped even
  then.
- The reopen trigger (ADR-0028 D4) and a pointer to ADR-0032 for the
  F0/F1/F2 vocabulary.
Match the existing user-guide page style (see deliver.md /
providers.md for tone + frontmatter if any).

## Constraints

- Part 1: doc comments ONLY — `go build ./...` and
  `go test -short ./...` green; the structure-sync gate re-renders
  with the new summaries (expected: the "(no package doc)" lines
  disappear — run `bash tools/ci_structure_sync.sh --render` and
  include the README diff).
- Part 2: link integrity must stay green (every link you add must
  resolve).
- Report: the 8 doc sentences + the tenancy page outline.
