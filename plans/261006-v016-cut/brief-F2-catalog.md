# Task: F2 — catalog ground truth: register the agy platform as a
# baked-name effort provider (companion to F1, issue #568)

Repo: g8s @ main. The effort machinery's live-dispatch failure
(issue #568) has TWO layers: the adapter's unknown-entry
pass-through (F1 adds a coercion backstop) and the catalog's
missing agy platform entry — which also disables the C3 baked-name
model/effort alignment in submit.go (its precondition is "provider
whose catalog style is baked-name", and no such provider exists in
the catalog today). This task fixes the data layer so the ratified
C3 semantics actually fire.

Read first (in order):

- .g8s/agent-models.yml — the effort catalog (schema
  agent-models.v1, providers keyed by name, effort_style per
  provider)
- internal/config/catalog.go — the loader (#556): what it validates
  and what it ignores per entry
- plans/261004-effort-optimization/brief-C3-ground-truth.md section
  2 — the baked-name model/effort alignment rule that consumes this
  catalog entry
- internal/config/catalog_test.go — existing catalog coverage

## Required change (data + tests)

1. .g8s/agent-models.yml — add an `agy` provider entry, transcribed
   from the LIVE providers.json (the platform manifest) and the
   worker CLI's verified contract:

   - effort_style: baked-name
   - notes documenting the platform contract: model ids carry the
     effort suffix; the worker CLI accepts --effort only when it
     equals the id suffix; mismatched combos are refused
     ("invalid model selection") — the exact staleness-marker text
     from brief-C3 section 3.
   - three model entries, one per observed variant, each with
     supported_efforts = [its own level] and default_effort = that
     level: gemini-3.8-flash-low, gemini-3.8-flash-medium,
     gemini-3.8-flash-high. Do NOT invent levels outside the
     suffix; do not add variants not observed in the platform
     manifest.
   - keep YAML style/format identical to existing provider entries
     (field order, quoting, comment placement).

2. internal/config/catalog_test.go — table rows proving: the agy
   provider loads; each variant resolves with a single-element
   supported list; FindProvider/FindModel hit for
   (agy, gemini-3.8-flash-high); unknown agy variant (e.g.
   gemini-3.8-flash-max) resolves to nil entry (F1's backstop
   territory); the loader still tolerates the file being absent
   (fail-open, existing behavior unchanged).

## Delivery protocol

Scratch worktree. Write ONLY to:

- .g8s/agent-models.yml
- internal/config/catalog_test.go

Do NOT run git commit. Do NOT touch internal/routing,
internal/dispatch, cmd/, or providers.json (the platform manifest
is runtime state, not repo data).

## Constraints

- `go test ./internal/config/` green; `go build ./...` green;
  gofumpt clean; `bash tools/ci_doc_contract_check.sh` green.
- The catalog is an SSoT mirror: cite the source of each value in a
  YAML comment (providers.json model list; agy --help effort
  levels; live task evidence 237be821).

## Report

- The exact YAML block added; test rows added; loader validation
  results; confirmation that existing catalog tests stay green.
