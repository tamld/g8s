# Task: Fix #451 — harness self-audit suite: 4 probes of the harness's own known failure classes

Repo: g8s @ main. Tracking: tamld/g8s#451 (P1, gate-the-gates per ADR-0026 §4).

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- internal/harness/probe/selfaudit.go (new file)
- internal/harness/probe/selfaudit_test.go (new file)
- internal/harness/probe/suite.go (ONLY to register the new probes in
  DefaultSuite — no other edits in this file)

Do NOT run git commit. Do NOT modify any other file. Two other workers are
operating in parallel on unrelated files (docs/, internal/worker/) —
touching anything outside the list above corrupts their delivery.

## Context

The eval probe machinery lives in internal/harness/probe (Probe struct at
types.go:37, ProbeSuite at types.go:60, DefaultSuite() at suite.go:10,
surfaced via `g8s eval list` / `g8s eval run` in cmd/g8s/eval.go). Study
the existing Probe struct fields (category, name, description, run func —
read types.go and one existing registered probe to match the exact shape)
before writing anything.

Each probe below encodes a REAL incident; its citation is part of the
probe's description (falsification doctrine: a gate must cite its catch).

## Required probes (register in DefaultSuite, category e.g. "self-audit")

1. **refusal-echo** (incident #443 — refusal-classifier echo poisoning):
   feed a recovered-run stream shape (mid-stream boilerplate + SUCCESS
   marker + clean final response) through the refusal classification path
   used by the drain pipeline and assert the run classifies as succeeded,
   NOT blocked. Locate the actual classifier: if it lives in this package
   (classify.go) call it directly; if the drain path's classification is
   in internal/worker, verify import direction first (internal/worker
   must not import internal/harness — if a cycle would form, exercise the
   exported wrapper the drain path uses, or re-home the probe's call into
   a seam that is importable from this package; NEVER duplicate the
   classifier logic in the probe).
2. **worktree-discard** (incident #443-f2, fix in #450 — dirty worktree
   destroyed by Pool.Release): assert the CURRENT contract — a dirty
   (uncommitted) worktree survives Release(keep=true). This needs a real
   git repo fixture (temp dir, git init, a worktree with one uncommitted
   file) and calls internal/orchestrator Pool.Release directly (check
   import direction: internal/orchestrator must not import
   internal/harness). Skippable cleanly with t.Skip when `git` binary is
   unavailable (Windows CI has git; still guard).
3. **sanitizer-fidelity** (incident #434, fix in #445 — JSON transport
   corruption): exercise dispatch.SanitizeOutput (exported) with escaped
   newlines/quotes and nested JSONL payloads; assert public literals are
   byte-faithful and output remains valid where the contract says so.
   Align expectations with the existing TestSanitizeJSONTransportFidelity434
   cases in internal/dispatch (do not weaken them; the probe wraps the
   same guarantee into the eval suite).
4. **receipt-bypass** (incident class: write outside receipt scope):
   construct a receipt manager on a temp receipts.db (internal/receipt
   exported API; see how cmd/g8s/main.go runReceipt issues receipts and
   how the worker consumes them) with allowed_paths scoped to one glob,
   then attempt a path-scope verification for a path OUTSIDE the glob and
   assert the check REJECTS it (and a path inside the glob PASSES).

## Tests — selfaudit_test.go

- Table-driven unit tests for each probe's predicate logic WITHOUT
  spawning real agent processes (no network, no agy): the probes must be
  runnable offline via `go test ./internal/harness/probe/`.
- A registration test: DefaultSuite() contains all four probes by name,
  each with a non-empty incident citation (assert on description content
  containing the incident reference, e.g. "#443").
- `g8s eval list` surfaces them (the suite registration covers this —
  assert via the suite, not by exec-ing the CLI).

## Constraints

- The probes ride the eval machinery; they must be hermetic (offline,
  temp dirs only, no shared state).
- Match the existing probe style in the package. Comments only for
  non-obvious constraints.
- Do not touch cmd/g8s/eval.go (C layer; not in scope) and do not touch
  internal/worker (another worker owns it this wave).
