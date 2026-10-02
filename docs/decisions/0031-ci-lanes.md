# ADR-0031: CI/CD Runs in Two Lanes — Product Build vs Docs

- **Status**: Accepted (2026-10-02) — operator directive ("Tách ra 2
  lane CI/CD: lane build sản phẩm vs lane docs. Nếu cùng chung 1 flow,
  nó rất quan liêu, tốn thời gian").
- **Issue**: none (factory program Wave F6, plans/261002-factory/).
- **Supersedes**: none. **Depends on**: ADR-0024's lane principle
  (route by blast radius), D-06 (coverage ratchet), the pre-push gate
  battery.

## Context

One CI flow serves both code and docs today. Measured costs this
session: a docs-only PR (#494) waited on the full dual-pass battery
(go test ×3 OS, race, GoReleaser snapshot, E2E) for zero signal — none
of those gates can fail on a markdown change; a docs-only handoff push
could not pass the code gates at all and needed `--no-verify`
(recorded exception, 2026-10-02). The gate time is pure bureaucracy
when the blast radius is prose.

## Decision

1. **Two lanes, self-detected by changed paths** — no labels, no
   human routing:
   - **DOCS lane**: every changed file is prose/asset — `*.md`,
     `docs/**`, `plans/**`, `skills/**`, `assets/**`, `offer/**`.
   - **BUILD lane**: anything else (`*.go`, `go.mod`, `tools/`,
     `.github/`, `packaging/`, `schemas/`, Makefile, ...). One build
     file anywhere in the diff ⇒ BUILD lane (deny-by-default, same
     posture as ADR-0029 D1).
2. **DOCS lane battery** (fast, ~2-3 min): doc-contract check
   (incl. structure sync + root hygiene), link integrity,
   `claims_check.sh`, version-sync, AI-lint on markdown. Coverage
   ratchet and go tests are NOT APPLICABLE — they report an explicit
   `docs lane: skipped (not applicable)` marker, not a silent pass.
3. **BUILD lane battery**: unchanged (dual-pass, race, fuzz seeds,
   E2E, Windows matrix, GoReleaser snapshot, coverage ratchet).
4. **Job names and required-check names DO NOT change.** The lane
   decision happens INSIDE jobs (early-exit steps), never as job-level
   `if:` conditions — branch protection keeps its required checks, a
   docs PR just clears them fast. The Verify Gate aggregate keeps
   aggregating the same job names.
5. **pre_push.sh gains the same fast path**: docs-only local diffs run
   the docs battery only; one code file ⇒ full battery. The
   `--no-verify` exception class this ADR retires: 2026-10-02
   (handoff push f9bbe0f).
6. **The detector is a tested tool**, not inline YAML:
   `tools/ci_lane_detect.sh` with a `_test.sh` suite (repo convention)
   — fixtures for docs-only, build-mixed, and empty diffs.

## Falsification clock

If a docs-lane PR ever lands a change that breaks the build (the
detector misrouted), the detector's deny-by-default rule is wrong and
gets fixed or the lane split is re-narrowed. Each lane escape cites
the PR that escaped.

## Consequences

Docs iterations drop from ~12-15 min to ~2-3 min; the handoff/docs
push path stops needing --no-verify; the build lane's signal gets
cleaner (no docs noise in the required checks). First live probe: the
next docs-only PR after this lands runs the docs lane end to end.

## Related

- plans/261002-factory/plan.md (Wave F6), ADR-0024 (lane principle),
  D-06 (ratchet), tools/pre_push.sh, .github/workflows/quality.yml.
