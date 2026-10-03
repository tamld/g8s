# Task: Red-Cell RC1 — verify the verifier registry and merger under hostile inputs

Repo: g8s @ main. Release red-cell (#517, D-01 HARD): internal/verifier
(#532) and tools/merger.sh (#533) have NEVER been adversarially verified.
Adversarial verification: construct inputs designed to steer decisions
away from their documented contracts, record what actually happens.
Findings are reported, never fixed here.

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/verifier/redtest_verifier_test.go (new)
- tools/redtest_merger_test.go (new, bash 3.2-compatible, PATH-shim
  style of tools/merger_test.sh)

Do NOT run git commit. Do NOT modify production code or the existing
suites.

## Guarantees to verify

1. **Class resolution cannot be steered outside the registry.** Feed
   path sets: case variants (`DOCS/x.md`, `Docs/x.go`), unicode
   look-alikes in extensions (`x.mD`, `x.mD .go` — split carefully),
   a path that IS a directory (`docs/`), traversal shapes
   (`docs/../cmd/x.go`, `././README.md`, `docs//x.md`), 64KB paths,
   NUL bytes, empty strings mixed with valid paths, a path list where
   ONE entry matches docs and the rest match nothing. Verify: verdict
   is either a registered class by the documented all-paths rule, or
   unregistered — never a registered class that the rule table does
   not justify, never a panic, exit semantics per the CLI contract.
2. **The unregistered floor cannot be flipped by input shape.** A
   receipt whose allowed_paths is an empty JSON array, a JSON string
   (not array), an array of non-strings, JSON with a huge array
   (10k paths) — verify unregistered/human-acceptance or a typed
   error, never class=docs.
3. **Hard-status promotion stays citation-gated.** Craft registry
   variants: `founding_catch` = "#", "#abc", "incident:", "INCIDENT:x",
   "#123 #456" (two), whitespace-only, a 1MB citation. Verify status:hard
   without a valid citation degrades to unregistered (fail-closed) in
   EVERY malformed case — and that a valid `#<digits>` citation still
   loads hard (the gate is not over-tight).
4. **The self-grade guard cannot be bypassed.** Verify with caller ==
   target (typed refusal), caller set but target differing only by
   case/whitespace, empty caller with a target (allowed — supervisor
   context), and the `--as-task` flag parsing with weird values.
5. **Merger gates resist crafted PR states.** Against the PATH-SHIM
   sandbox (never network): autonomy_level read returning "1 " (trailing
   space), "01", "true", an empty string; lane detector emitting
   "lane=docs" with extra whitespace/suffix lines; verifier envelope
   with outcome "pass" but registered=false; CI checks with pass=15
   pending=1; a gate order swap attempt (verifier output arriving
   before autonomy is checked). Verify: refuse in every case, the
   four GATE lines still print in order, and NO merge command is
   invoked — including when --dry-run is passed with all gates green.
6. **Merger D3 boundary under stress.** Grep-verify the script never
   invokes receipt issue / submit / resubmit even in the mock
   environment where such commands exist on PATH (the shim logs
   invocations — assert zero).

## Constraints

- stdlib testing; hermetic; no network.
- Mark any guarantee failure `t.Skip` + `// BUG(REDTEST):` + repro;
  list failures FIRST in the report.
- Report: per-guarantee verdict + constructed inputs + exact observed
  behavior vs documented contract.
