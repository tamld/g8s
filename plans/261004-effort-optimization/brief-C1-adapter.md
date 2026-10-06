# Task: C1 — effort dispatch adapter + `submit --effort` knob (issue #550 DoD 2)

Repo: g8s @ main. The effort wave's translation layer: ONE adapter turns
`effort_requested` into `effort_applied` per the manifest/catalog styles,
and `submit` gains the effort knob (default medium). Read first:
plans/261004-effort-optimization/design.md (point 3 — translation +
fallback convention) + internal/config/catalog.go (loader #556, no
consumers yet) + internal/routing/router.go (Decision) +
cmd/g8s/submit.go (flag surface).

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/routing/effort.go (new — the adapter) + effort_test.go
- internal/routing/router.go (minimal: Decision fields + catalog merge
  into the route manifest)
- cmd/g8s/submit.go (--effort flag + payload keys) and a new
  cmd/g8s/submit_effort_test.go (or extend submit_route_test.go —
  follow package convention)

Do NOT run git commit. Do NOT touch internal/dispatch, internal/worker,
internal/config, .g8s/ (dispatch already passes the payload `effort`
through to the worker CLI; worker.go consumes payload key `effort`).

## Required implementation

1. **Adapter** (internal/routing/effort.go): pure function taking a
   model-effort view (style + SupportedEfforts + DefaultEffort +
   Mandatory + EffortBudgetMap — normalize from *config.ModelEntry and
   catalog ModelCatalog; a nil entry means "unknown model") plus the
   requested level ("" = not specified), returning: applied level
   ("" = omit the flag, provider decides), an advisory mismatch flag,
   budget tokens (0 for non-budget), and a typed error. Rules (design
   point 3, fail-safe):
   - unknown entry (nil) → pass-through: applied = requested, no
     mismatch ("" stays "").
   - requested "" → applied = entry DefaultEffort when it is a ladder
     level; adaptive/dynamic or empty default → applied = "".
   - named → pass-through when requested is in SupportedEfforts;
     otherwise nearest-supported APPLIED and RECORDED (minimal index
     distance on config.EffortLadder; tie → the LOWER level), mismatch
     = true, never an error.
   - baked-name → always pass-through (the wrapper resolves the
     {effort} variant; no mismatch ever).
   - toggle → nearest-supported, mismatch when requested is outside
     SupportedEfforts.
   - budget → applied = requested when EffortBudgetMap has that key,
     else the nearest level that HAS a budget entry (fallback:
     nearest-supported); mismatch on fallback; tokens =
     EffortBudgetMap[applied] surfaced for telemetry.
   - HARD-REFUSE (the only one): Mandatory == true AND requested ==
     "none" → typed error naming the provider and model, refused
     locally before the task is queued.
2. **Catalog wiring** (router.go): Route (auto path) merges the
   default catalog into the effective manifest via
   config.LoadDefaultCatalog + Catalog.MergeInto (best-effort: missing
   catalog or unknown providers must not fail routing — record and
   continue). Also export a helper that, given a manifest-or-nil,
   provider, model id, and requested level, resolves the entry
   (manifest first, then catalog FindProvider/FindModel direct lookup)
   and runs the adapter — ONE place both routes share.
3. **Decision fields**: Decision gains `EffortRequested`,
   `EffortApplied` (json effort_requested/effort_applied, omitempty),
   plus mismatch and budget-token fields (names per convention,
   omitempty). Fill them on every Decision returned with a resolved
   entry.
4. **submit knob** (submit.go): `--effort` flag, DEFAULT "medium",
   validated against config.IsValidEffortLevel at parse time (typed
   usage exit naming the allowed ladder). BOTH the manual and auto
   paths run the shared helper: payload gains `effort` (the APPLIED
   level — omit the key entirely when applied is ""), plus
   `effort_requested` and `effort_applied` always when the adapter ran
   (advisory record, never silent), `effort_budget_tokens` /
   `effort_mismatch` when non-zero/true. A hard-refuse error exits
   before store.SubmitTask.

## Constraints

- stdlib; `go test ./internal/routing/ ./cmd/g8s/` green;
  `go build ./...` green; gofumpt + golangci-lint clean;
  `bash tools/ci_doc_contract_check.sh` green.
- Red-first table tests covering the full rule matrix: named
  pass-through / nearest-down / nearest-up / tie-picks-lower, toggle
  nearest, baked pass-through, budget exact + nearest-with-entry +
  sparse fallback, requested "" → default/adaptive/"" cases, nil entry
  pass-through, mandatory+none refuse, and the submit flag path
  (default medium lands in payload; bad level → usage error; applied
  recorded; hard-refuse exits before queueing).

## Report

The exported signatures, the Decision/payload fields added, the merge
and lookup semantics, and the test matrix with pass counts.
