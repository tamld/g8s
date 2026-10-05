# Task: L1 — config.File effort metadata + agent-models catalog loader (issue #550 DoD 1)

Repo: g8s @ main. The effort wave's W-layer foundation: the provider
manifest (internal/config) gains the effort dimension, and the shipped
catalog (.g8s/agent-models.yml, agent-models.v1) gets a loader. Read
first: plans/261004-effort-optimization/design.md (manifest schema) +
brainstorm-rails.md (ratified decisions) + .g8s/agent-models.yml (the
catalog data).

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/config/config.go (+ the existing test file — follow the
  package convention) — effort fields + validation
- internal/config/catalog.go (new — the agent-models.v1 loader) +
  internal/config/catalog_test.go

Do NOT run git commit. Do NOT touch routing, worker, cmd/, .g8s/ (the
catalog is DATA — the loader READS it; tests use inline fixture YAML).

## Required implementation

1. **Effort fields on model entries** (config.File's model struct):
   `EffortStyle` (named|budget|baked-name|toggle — empty = provider
   default, recorded), `SupportedEfforts []string` (subset of the
   ordered ladder none<minimal<low<medium<high<xhigh<max),
   `DefaultEffort` (a member of SupportedEfforts, or "adaptive"/
   "dynamic"), `Mandatory bool` (thinking cannot be disabled),
   `EffortBudgetMap map[string]int` (level→tokens, budget-style
   providers only). Validation (fail-closed): unknown style, level
   outside the ladder, default not in supported set, budget map for a
   non-budget style ⇒ load error naming the model and field.
2. **Catalog loader** (catalog.go): parse agent-models.v1 YAML (stdlib
   hand-rolled, mirroring the internal/lane parser style — no YAML
   dependency): schema_version must be exactly "agent-models.v1"
   (older/newer ⇒ typed error), verified_at present, providers keyed,
   per-provider effort_style/field/models with the same validation as
   (1). Returns a typed Catalog; merge helper
   `Catalog.MergeInto(manifest *config.File)`: catalog entries FILL
   manifest models' empty effort fields (never overwrite user-set
   values — user manifest wins), unknown-provider catalog entries are
   skipped and counted in the returned report.
3. **Red-first table tests**: valid catalog loads; wrong schema_version
   refused; level outside ladder refused; default not in subset
   refused; budget-map-on-named-style refused; merge fills empties
   without overwriting user values; unknown providers skipped+counted;
   malformed YAML ⇒ typed error not panic.

## Constraints

- stdlib; `go test ./internal/config/` green; `go build ./...` green;
  gofumpt + golangci-lint clean; `bash tools/ci_doc_contract_check.sh`
  green.
- Report: the struct fields added, the validation rules, the merge
  semantics, and the catalog fixture coverage.
