# Task: C2 — sibling-scoped effort classes + offer seed (issue #550 DoD 3 follow-up)

Repo: g8s @ main. Improvement from the pilot loop: dispatches into a
sibling repo resolve effort class "unregistered" because the class
registry lookup walks upward from CWD only (the g8s repo). The project
the work happens in should define its classes. Read first:
internal/lane/effortclass.go (W2: LoadEffortClasses /
LoadEffortClassesFile / ResolveClass / findFile-upward) +
cmd/g8s/submit.go (W2b wiring: classPaths + scopeRoots) +
cmd/g8s/offer.go (offer init seeds: trust-boundaries.yml +
lane-bundles.yml embedded strings).

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/lane/effortclass.go (small: a sibling-resolution helper) +
  effortclass_test.go
- cmd/g8s/submit.go (small: consult sibling registries) +
  submit_effort_test.go (extend)
- cmd/g8s/offer.go (add the effort-classes.yml seed to the scaffold +
  the scaffold list) + offer_test.go (extend)

Do NOT run git commit. Do NOT touch internal/routing, internal/worker,
internal/ladder, .g8s/ (the g8s repo's own registry is unchanged).

## Required implementation

1. **Sibling resolution** (lane helper + submit wiring): new function
   `ResolveClassForRoots(classPaths []string, roots []string)
   (className, defaultEffort string)` — semantics, in order:
   - If ALL classPaths resolve under ONE root (filepath base check per
     path against each root), and `<root>/.g8s/effort-classes.yml`
     EXISTS → load that file (LoadEffortClassesFile) and resolve the
     paths against it. Sibling config wins for sibling work.
   - Otherwise → the current behavior (upward findFile from CWD,
     unregistered fail-open floor).
   Keep the pure-function seam: resolution rules table-tested with
   injected paths/roots (no os.Chdir in tests).
2. **submit wiring**: pass the scope roots (the `-scope-root` values
   PLUS cwd) alongside the existing classPaths; behavior otherwise
   unchanged. The resolved class/default flows into the same
   payload keys (effort_class, effort_override, effort_override_down).
3. **Offer seed** (offer.go): the knowledge-profile scaffold also
   writes `.g8s/effort-classes.yml` (same 0o600 pattern) — a minimal
   knowledge-profile registry (docs→low with the pilot-proven comment,
   test→medium, unregistered floor medium) and the seed is added to
   the returned scaffold list so the onboarding report shows it.
4. **Tests (red-first)**: all-paths-under-one-root with a sibling
   registry → sibling class wins; paths spanning two roots → repo
   registry (pinned fallback); root without a registry → repo
   registry; sibling file malformed → typed error surfaced (not
   silently swallowed — a sibling EXPLICITLY shipping a broken
   registry should fail the submit loudly); offer init scaffold
   includes the new file; seed content parses with ParseEffortClasses.

## Constraints

- stdlib; `go test ./internal/lane/ ./cmd/g8s/` green; `go build ./...`
  green; gofumpt + golangci-lint clean;
  `bash tools/ci_doc_contract_check.sh` green.
- Keep the diff surgical — this is one seam, not a redesign.

## Report

The new signature, the precedence rules, the seed content, the test
matrix with pass counts.
