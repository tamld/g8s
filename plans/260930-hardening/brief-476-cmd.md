# Task: #476 wave B1 — cmd/g8s hardening + user-guide for deliver/eval

Repo: g8s @ main. Tracking: tamld/g8s#476 (items: deliver atomicity/C-T-U/
unmarshal/symlink, eval stale model, printUsage, envelope inconsistencies,
user-guide gaps).

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- cmd/g8s/deliver.go
- cmd/g8s/deliver_test.go
- cmd/g8s/eval.go
- cmd/g8s/main.go (ONLY the printUsage function's command list)
- cmd/g8s/autopilot.go (ONLY the --json output paths)
- cmd/g8s/doctor.go (ONLY the --attention-check output mode)
- docs/user-guide/cli-reference.md (add deliver + eval sections)

Do NOT run git commit. Do NOT modify any other file. Two other workers own
internal/doctor/, Makefile, internal/worker/, internal/cleanup/, tools/,
internal/orchestrator/ in parallel.

## Required fixes (audit evidence in #476)

1. **deliver.go hardening**:
   - Porcelain loop: treat status codes C (copied), T (typechange),
     U (unmerged) as explicit skipped-with-warning entries (like R/D) —
     never silent fall-through.
   - Propagate `json.Unmarshal` errors for the task result/request
     (currently `_ =`): a corrupt payload is a runtime error naming the
     task, not a misleading "no deliverable pointer".
   - **Atomic apply**: write each destination via temp file in the
     destination directory + `os.Rename` per file; a mid-loop failure
     removes already-created temps and reports which files landed.
   - **Symlink refusal**: `os.Lstat` the destination first; if it exists
     and is a symlink, skip with a warning (never write through it) —
     receipt path scope is checked on the relative name only.
2. **eval.go**: replace the stale `gemini-3.7-flash-high` default with
   `gemini-3.8-flash-high` (align with submit.go/orchestrate.go/provider.go).
3. **main.go printUsage**: add the missing `memory` and `offer` commands;
   remove the duplicate `watch` and `eval` lines.
4. **Envelope consistency**:
   - autopilot.go: human `fmt.Printf` output must respect `--json`
     (route through the cli envelope helpers like sibling commands).
   - doctor.go `--attention-check`: emit the raw envelope only in json
     mode; human mode gets the formatted output.
   - eval.go top-level usage error: honor the parsed json/jsonl flags
     instead of the hardcoded false.
5. **docs/user-guide/cli-reference.md**: add sections for `g8s deliver`
   (flags: --dry-run; receipt gate semantics; skipped C/T/U/R/D behavior)
   and `g8s eval` (list/run, --category, --provider; the self-audit
   category per ADR-0026). Keep the existing file's table style.

## Tests

- deliver_test.go: extend with (a) C/T/U entries reported skipped,
  (b) corrupt result JSON → explicit error, (c) atomic apply — inject a
  failure (read-only dest dir) and assert no partial delivery state,
  (d) symlinked destination skipped with warning, target file untouched.
- go test ./cmd/g8s/ and go build ./... locally green; gofmt/gofumpt clean.

## Constraints

- stdlib + existing deps; match style; comments only for non-obvious
  constraints. Do not touch internal/ packages (the deliver envelope and
  helpers you need are already exported).
