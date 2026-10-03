# Task: Wave H2a — autonomy_level setting + the gated merger tool (issue #516, SCORECARD S-8)

Repo: g8s @ main. Context: the first unattended closed round needs an
auto-merge executor that is honest about trust: it exists, it is gated,
and at the default level it does NOTHING. Read first: issue #516 (the
contract), docs/decisions/0029-budgeted-auto-retry.md D3 (:66-84 — the
receipt boundary your tool must respect), docs/decisions/0031-ci-lanes.md
(the DOCS lane + the lane detector you will call),
internal/settings/settings.go (the exact 5-step pattern for adding a key),
internal/verifier/verifier.go (the H1 verdict surface your gate consumes),
tools/ci_lane_detect.sh (CLI: `$1` base ref — prints lane=docs|lane=build).

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/settings/settings.go + internal/settings/settings_test.go
  (add the key via the exact 5-step pattern: AllowedConfigKeys, Config
  field, Get default, validate function, Set wiring)
- tools/merger.sh (new)
- tools/merger_test.sh (new — every tools/ script has a paired test)

Do NOT run git commit. Do NOT touch internal/verifier, cmd/, docs/,
.g8s/, or any workflow file.

## Required implementation

1. **Setting `autonomy_level`**: integer, allowed values exactly {0, 1}
   (anything else — including 2+ — refused by the validator with a
   message saying higher levels are reserved and unratified). Default 0.
   Document on the key (validator comment): level 0 = today (no
   auto-merge anywhere), level 1 = the merger tool may squash-merge a
   docs-lane PR whose full gate set passes; the flip 0→1 is an operator
   decision recorded on the round, never a default.
2. **tools/merger.sh** — the gate executor. Interface:
   `tools/merger.sh --pr <num> --task <task-id> [--dry-run]`.
   Fail-closed order (every gate result is PRINTED with a stable
   `GATE <name>: pass|fail — <detail>` line so the round log can quote
   them):
   a. **Autonomy gate**: `bin/g8s config get autonomy_level` (run from
      the repo root) must be exactly `1`; at 0 the tool refuses with
      "autonomy_level=0: the merger refuses to act (operator flip
      required, ADR-0029/0024 posture)" and exit 3 — before ANY other
      work.
   b. **Lane gate**: fetch the PR ref, `bash tools/ci_lane_detect.sh
      origin/main` against the PR diff → must be `lane=docs`; a single
      code file ⇒ refuse (this round is docs-class only, issue #516).
   c. **Verifier gate**: `bin/g8s verify --task <task-id>` — parse the
      JSON envelope; require registered=true, class=docs, outcome=pass,
      status=advisory|hard (advisory verdicts are ACCEPTED here because
      the operator flip to level 1 IS the recorded human decision for
      this round — print that reasoning verbatim in the gate detail).
   d. **CI gate**: `gh pr checks <num>` → pending=0 AND failure=0 (the
      operator's measured merge rule; PR-green ≠ main-green is a known
      gap, the docs battery is what ADR-0031 promises for a docs PR).
   ALL gates pass ⇒ `gh pr merge <num> --squash --delete-branch` and
   print `MERGED <num> at <iso-ts>`; any gate fail ⇒ exit 1, NO merge,
   and the failing gate line is the last output. `--dry-run` runs every
   gate and prints what it WOULD do, but never merges (this is how the
   paired test exercises the tool without a live PR).
   **D3 boundary (hard)**: the merger never mints receipts, never
   submits/resubmits tasks, never touches the controlplane state — it
   reads config and verdicts, runs read-only checks, and merges a PR
   whose checks are already green. Say this in the script header.
3. **tools/merger_test.sh** — cover at least: refuses at level 0 (exit 3,
   before other gates); refuses when lane=build; refuses when verifier
   says unregistered or outcome=fail; refuses when checks pending;
   dry-run never merges; all-green prints all four GATE lines then the
   MERGED line. Mock `bin/g8s` and `gh` via a PATH-shim sandbox
   (fixtures in a temp dir); do not hit the network.
4. **Tests red-first, table-driven** for the setting: default is 0; set
   1 persists; set 2 / "high" / -1 refused with the reserved message;
   the JSON envelope round-trips.

## Constraints

- The merger is bash (tools/ convention) + the paired test; the setting
  is Go. Both in one PR is fine (the layer gate treats tools/ scripts
  and internal/settings at the same layer — if the pre-push layer gate
  disagrees, STOP and report instead of splitting on your own).
- `go test ./internal/settings/` green; `go build ./...` green;
  `bash tools/merger_test.sh` green; gofumpt clean on touched Go files.
- Run `bash tools/ci_doc_contract_check.sh` and `bash
  tools/ai_lint.sh .` before finishing; fix or report.
- Report: the exact gate order implemented, the dry-run semantics, the
  D3 statement in your own words, and anything you had to interpret.
