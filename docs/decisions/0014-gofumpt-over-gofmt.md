# ADR-0014: Use gofumpt over gofmt for new Go projects

**Status**: ACCEPTED
**Date**: 2026-08-29
**Deciders**: Sisyphus (orchestrator), agy worker (executor)
**Triggered by**: DELTA-11 + DEBT-22 cleanup, PR #100

## Context

`gofmt` is the canonical Go formatter, but it leaves a few style choices
unspecified (alignment of composite literals, simplification of redundant
expressions, etc.). Teams routinely add `gofumpt` (a stricter
`gofmt`-compatible formatter by the `mvdan` author) to enforce
additional rules.

In DELTA-11 + DEBT-22 cleanup, we observed that:
- `gofumpt` would have caught 3-4 stray formatting issues per package.
- Both tools share the same canonical style; `gofumpt` is a strict
  superset.
- CI gate cost: +2s on a 19-package repo.

## Decision

Adopt `gofumpt -l` as the **strict format gate** in
`.github/workflows/quality.yml`. Keep `gofmt -l` as a **baseline format
gate** (it is the universal ground truth; if `gofumpt` is unavailable
on a runner, the build still passes).

## Consequences

**Positive**
- Catch ~5% more formatting issues per release.
- One less debate in code review about "which style".

**Negative**
- New contributors need `gofumpt` installed locally: `go install
  mvdan.cc/gofumpt@latest`.
- Tool version drift if not pinned. We pin via `go install
  mvdan.cc/gofumpt@latest` in CI; the install step is hermetic.

## Reversal criteria

If a future Go release changes the canonical style such that `gofumpt`
diverges, this ADR is superseded.

## References

- gofumpt: https://github.com/mvdan/gofumpt
- PR #100: https://github.com/tamld/g8s/pull/100
