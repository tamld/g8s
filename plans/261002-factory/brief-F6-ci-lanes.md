# Task: Wave F6 — CI/CD two lanes (product build vs docs), ADR-0031

Repo: g8s @ main. Design: docs/decisions/0031-ci-lanes.md (ACCEPTED —
read it first; the binding constraints are §4 job-name stability and
§1 deny-by-default lane detection). Tracking: plans/261002-factory/.

## Delivery protocol

Scratch worktree. Write ONLY to these receipt-scoped files:

- tools/ci_lane_detect.sh (new)
- tools/ci_lane_detect_test.sh (new)
- tools/pre_push.sh (docs-lane fast path)
- .github/workflows/quality.yml (lane-aware Quality Gate)
- .github/workflows/verify.yml (only if the aggregate needs adjusting
  to count skipped-not-failed jobs — check first)

Do NOT run git commit. Do NOT touch docs/, internal/, cmd/.

## Context (grep-verified at main)

- quality.yml runs the dual-pass battery + coverage ratchet
  (`tools/ci_coverage_ratchet.sh --baseline .g8s/coverage-baseline
  --current <n>`) + fuzz seeds (quality.yml:329 area).
- CI computes coverage as the per-package mean from `ok` lines,
  excluding cmd/g8s (quality.yml:180-200).
- The Verify Gate workflow aggregates job results by NAME.
- pre_push.sh orchestrates the local gates (doc-contract, layer
  check, version sync, dual-pass tests, dogfood roundtrip, ...).
- Repo test convention: tools/ci_*_test.sh pairs (see
  ci_link_integrity_test.sh) run in CI — mirror that shape.

## Required implementation

1. **tools/ci_lane_detect.sh** — input: a base ref (default
   origin/main); output on stdout exactly `lane=docs` or
   `lane=build`; exit 0 always (detection never breaks a run).
   Docs-only iff EVERY changed path matches the docs set:
   `*.md`, `docs/*`, `docs/**`, `plans/**`, `skills/**`,
   `assets/**`, `offer/**`. One non-docs path ⇒ build. Empty diff
   (no commits vs base) ⇒ build (deny-by-default). Use
   `git diff --name-only "$base"...HEAD` plus `git diff --name-only
   --cached` fallback when the branch has no upstream diff (local
   pre-push case: use `origin/main...HEAD` when it exists, else
   staged+unstaged files). Keep it POSIX bash 3.2 compatible
   (see the #466 lesson: `${arr[@]+"${arr[@]}"}` guards).
2. **ci_lane_detect_test.sh** — build fixture git repos with
   `git init` in a temp dir; cases: docs-only diff → docs; one .go
   file → build; empty tree → build; nested docs path → docs;
   mixed md+go → build. Assert exact stdout.
3. **quality.yml** — at the top of the Quality Gate job (and the
   Windows Quality job), add a detect step: `lane=$(bash
   tools/ci_lane_detect.sh origin/main || true)`; export
   `CI_LANE=$lane`. When `CI_LANE=docs`: run ONLY — golangci-lint
   (unchanged, zero-Go diffs are trivially green), doc-contract
   check, link integrity, claims_check, version-sync — then echo
   `::notice::docs lane: go test/race/fuzz/ratchet skipped (not
   applicable)` and exit 0 BEFORE the go test/ratchet steps. The
   coverage-ratchet step must not run its go test on docs diffs.
   Job names, step identities that other workflows reference, and
   required-check names stay EXACTLY as-is. Build lane = byte-for-
   byte current behavior.
4. **E2E workflows** (Crash-Survival E2E, Golden-Loop E2E, Windows
   E2E Verification, GoReleaser Check & Snapshot) — same detect
   step; docs lane = early exit 0 with the notice marker. Do NOT
   change workflow/job names.
5. **pre_push.sh** — detect once at the top; docs lane runs:
   doc-contract battery + AI-lint + link integrity only (print
   "DOCS LANE: N gates"); build lane = current full battery.
   Keep `--no-verify` semantics untouched.
6. **Verify Gate** — inspect verify.yml; if it aggregates by job
   conclusion it needs NO change (skipped-by-early-exit jobs still
   conclude success). Only adjust if it greps for specific step
   outputs.

## Tests

- ci_lane_detect_test.sh green locally (bash it directly).
- `bash tools/pre_push.sh` on THIS branch (docs+tools diff) must
  take the docs lane and pass; verify the fast path prints the
  lane marker. (Note: your own diff touches tools/*.sh + workflows
  — that is a BUILD-lane diff by definition; test the docs path by
  creating a throwaway local commit flipping only a README line,
  then reset it.)
- YAML validity: `python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/quality.yml'))"`.

## Constraints

- No new deps. No changes to gate NAMES or branch-protection-relevant
  identifiers. Deny-by-default: ambiguity resolves to BUILD lane.
- Report: the exact early-exit step names added per workflow, the
  detect helper's fixture matrix results, and the pre-push fast-path
  output.
