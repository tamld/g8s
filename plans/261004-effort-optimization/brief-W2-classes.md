# Task: W2 — effort-classes.yml + effort/token telemetry (issue #550 DoD 3)

Repo: g8s @ main (after C1 lands). The effort wave's class mapping +
measurement layer: a declarative purpose-class → effort-default file
and per-task effort telemetry. Read first:
plans/261004-effort-optimization/design.md (points 4-5) +
.g8s/verifier-classes.yml (the file pattern to mirror) + the C1 adapter
(internal/routing/effort.go) + cmd/g8s/submit.go (the knob C1 added).

## Delivery protocol

Scratch worktree. Write ONLY to:

- .g8s/effort-classes.yml (new — DATA, mirrors verifier-classes shape)
- internal/lane/effortclass.go (new — loader + resolver, stdlib
  hand-rolled parser mirroring the catalog loader style) +
  internal/lane/effortclass_test.go
- cmd/g8s/submit.go (class detection → default effort when --effort is
  not passed; override-down advisory record) — minimal edit
- internal/worker/worker.go + internal/telemetry/types.go (result +
  telemetry event fields only) — minimal edits

Do NOT run git commit. Do NOT touch internal/routing (the C1 adapter is
DONE — consume it, never fork it), internal/dispatch, .g8s/
agent-models.yml.

## Required implementation

1. **effort-classes.yml**: schema_version "effort-classes.v1", classes
   keyed with `name`, `default_effort`, `paths` globs, `priority`
   (verifier-classes convention; first match by priority wins). Seed
   data per the ratified mapping: docs→low (transcription/audit class),
   test→medium, feature/red-cell/discovery→high (paths "**/_test.go",
   "tools/**", plans red-cell dirs), unregistered→medium (implicit
   default, not a file entry). File-level fail-closed validation:
   unknown effort level or empty class name ⇒ load error naming the
   class and field.
2. **Class resolution** (lane/effortclass.go): LoadEffortClasses
   (findFile upward, like LoadDefaultCatalog) + ResolveClass(paths
   []string) → (className, defaultEffort). Missing file ⇒ empty
   resolver, submit falls back to medium (current behavior — fail-open
   like the catalog wiring).
3. **submit wiring**: the class resolution runs BEFORE the C1 resolver
   (routing.ResolveEffort / EffortResult — consume, never fork). When
   --effort is NOT passed, the class default becomes the requested
   effort (recorded as effort_class in the payload); when --effort IS
   passed it stays the requested effort and the payload records
   effort_class + effort_override=true. An override DOWN (flag effort
   < class default on the ladder) also sets effort_override_down=true
   (advisory signal per design point 4 — recorded, never blocked).
   The C1 payload keys (effort, effort_requested, effort_applied,
   effort_mismatch, effort_budget_tokens) keep their semantics.
4. **Telemetry**: the worker's result envelope ALREADY carries a usage
   block from agy (input_tokens, output_tokens, thinking_tokens,
   cache_read_tokens, total_tokens — observed on task b8109b8e). The
   task result gains effort_class alongside the C1 effort fields and
   duration; a telemetry TraceEvent per completed task carries (class,
   effort_requested, effort_applied, effort_mismatch, input_tokens,
   output_tokens, duration_seconds) — best-effort, failures never
   abort the task path. The SCORECARD cost-per-class row (Phase 5)
   consumes these events.

## Constraints

- stdlib; `go test ./internal/lane/ ./cmd/g8s/` green; `go build ./...`
  green; gofumpt + golangci-lint clean;
  `bash tools/ci_doc_contract_check.sh` green.
- Red-first table tests: class file loads; bad level refused; priority
  ordering first-match; missing file fail-open to medium; override-down
  flag; override-up silent (no flag); telemetry event fields present.

## Report

The schema, resolution rules, submit/telemetry wiring, the agy usage
findings (present/absent), and the test matrix with pass counts.
