# Task: Wave H1 — verifier-class registry (issue #515, SCORECARD S-7, factory plan §Wave H)

Repo: g8s @ main. Context: the factory's self-evaluation organ — a completed
round must be accepted (or refused) WITHOUT human eyes, but trust is earned
per class, never assumed. Read first: issue #515 (the contract), .g8s/lane-bundles.yml
(the registry-file style to mirror), internal/lane/lane.go (the stdlib hand-rolled
YAML loader style + fail-closed conventions), internal/memory/lifecycle.go:185-240
(the advisory→hard gate shape you must echo: allowlisted verdicts, fail-closed on
drift, fail-open only on sensor-down with an observable event).

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/verifier/verifier.go (new package; MUST open with a package doc
  comment — CI structure sync requires it)
- internal/verifier/verifier_test.go (new)
- .g8s/verifier-classes.yml (new — the seeded registry)
- cmd/g8s/verify.go + cmd/g8s/verify_test.go (new subcommand)
- internal/telemetry/types.go — ONLY IF you add the verdict event-type
  constant (smallest possible one-line diff, justified in your report)

Do NOT run git commit. Do NOT touch internal/settings (autonomy_level is a
LATER wave), internal/controlplane, internal/receipt, internal/lane, docs/.

## Required implementation

1. **Registry file** `.g8s/verifier-classes.yml`: top key `classes:`; each
   entry: `name`, `status` (`advisory`|`hard`), `founding_catch` (citation
   string), `priority` (int, lower = matched first), `paths` (glob list),
   `checks` (list of `{type: script, run: <tracked tools/ script>}` or
   `{type: go-test, packages: [...]}`). Seed exactly two classes, both
   `advisory`, each citing a REAL campaign catch:
   - `docs`: paths mirror the ADR-0031 DOCS lane prose set (`*.md`, `docs/**`,
     `plans/**`, `skills/**`, `assets/**`, `offer/**`); checks = doc-contract
     script + link-integrity script; founding_catch cites issue #208 (docs
     drifted from code until the doc-contract gate existed).
   - `test`: globs `*_test.go`, `**/*_test.go`, `tools/**`, `plans/**`;
     checks = go-test on the touched packages (declare `packages: []` meaning
     "packages derived from the touched non-test files" — document the rule);
     founding_catch cites #510 (the red-team suite's own vacuous seeds F1-F3,
     fixed by #512).
2. **Loader (fail-closed)** in internal/verifier: stdlib hand-rolled YAML
   parsing mirroring internal/lane (no new deps). Rules: `status: hard`
   WITHOUT a founding_catch matching a citation pattern (`#<digits>` or
   `incident:<slug>`) is a load error for that entry → the class is treated
   as UNREGISTERED (deny-by-default, never a panic, never a silent hard);
   duplicate names → refused; unknown check type → entry unregistered.
3. **Class resolution (deterministic)**: input = the task's write-scope paths
   (from its receipt `AllowedPaths` — the task Request JSON carries
   `receipt_id`; see cmd/g8s/submit.go:200). An entry matches when ALL paths
   fall inside its globs; the lowest-priority-number matching entry wins;
   none → `unregistered` = human acceptance (today's behavior — this is the
   deny-by-default floor; it is a VERDICT, not an error).
4. **Verify runner**: `Verify(target TaskRef, caller TaskRef) Verdict`.
   Verdict = {class, registered, status, outcome pass|fail, checks []result
   (type, target, ok, detail), scope (the all-paths-match rule result),
   recorded_at}. **Self-grade guard**: caller set and equal to target ⇒
   refused with a typed error ("no verifier grades its own class", issue #515).
   Script checks: run the tracked script with a 5-minute timeout, capture
   exit code; go-test checks: `go test -count=1` on the packages. Every check
   result is recorded even on failure. Fail-open rule: a check's runner error
   (not a check FAILURE — an infrastructure error like a missing binary) ⇒
   outcome `not-run`, status stays advisory-observable; never report a fake
   pass.
5. **Telemetry**: on every verdict emit one trace event — add
   `TraceEventVerifierVerdict TraceEventType = "verifier_verdict"` to
   internal/telemetry/types.go (one line) and payload
   {class, status, outcome, checks_run}. Follow the best-effort ingest
   pattern (internal/worker/worker.go:1525-1544).
6. **CLI** `g8s verify --task <id> [--as-task <id>]` in cmd/g8s/verify.go:
   resolve task from the controlplane (read-only), paths from its receipt,
   run Verify, print the JSON envelope via the internal/cli pattern. No
   receipt / read-only task ⇒ verdict unregistered (still exit 0 — human
   acceptance is a valid outcome). Follow the existing hand-rolled switch
   registration in cmd/g8s/main.go (one line) — do NOT restructure main.
7. **Tests, red-first table-driven**: unregistered-class floor; hard-without-
   citation refused; self-grade refusal; docs slice (all-prose paths)
   resolves docs; test slice (brief + *_test.go) resolves test NOT docs;
   mixed code path → unregistered; malformed YAML → unregistered not panic;
   script check ok/fail paths; go-test check runner-error ⇒ not-run.

## Constraints

- stdlib only; match internal/ package style; no schema bump (new file, new
  package — nothing migrates).
- `go test ./internal/verifier/ ./cmd/g8s/ ./internal/telemetry/` green;
  `go build ./...` green; gofumpt + golangci-lint clean on touched packages.
- Run `bash tools/ci_doc_contract_check.sh` and, if it demands a README tree
  re-render, `bash tools/ci_structure_sync.sh --render` then re-verify BOTH
  green before you finish (the new cmd file changes the tree).
- Report: the registry schema you implemented, the class-resolution rule
  table, the citation pattern accepted, check types + their failure
  semantics, and anything in this brief you had to interpret (with reasoning).
