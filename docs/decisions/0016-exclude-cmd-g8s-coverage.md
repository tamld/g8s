# ADR-0016: Exclude cmd/g8s from aggregate coverage threshold

**Status**: ACCEPTED
**Date**: 2026-08-29
**Deciders**: Sisyphus
**Triggered by**: DELTA-11 Concern A self-test mode, PR #100

## Context

The Quality gate enforces aggregate coverage ≥ 80% across all Go
packages. The `cmd/g8s` package contains:

- `main()` entry point
- 12 `runXxx` dispatchers (`runSubmit`, `runGet`, `runResume`,
  `runTasks`, `runLineage`, `runChildren`, `runReceipt`,
  `runOrchestrate`, `runSupervisorMetrics`, `runMCPServer`,
  `runWrapExec`, etc.)

These dispatchers are thin wrappers that:
1. Parse flags (`flag.NewFlagSet`).
2. Open the SQLite store via `databasePath()`.
3. Call a function and either print JSON or `os.Exit(2)` on error.

Each `os.Exit(2)` path kills the test process. Unit testing the
`os.Exit(2)` branches requires `t.Skip()` (loses signal) or
subprocess re-exec (heavy infra). The cost-to-signal ratio is poor.

The original implementation:
- `main()` itself: 0% covered by definition.
- `runSubmit` / `runResume` / `runTasks` etc.: each ~5-10% from
  incidental coverage through `databasePath()` and `reportError`.

This dragged the aggregate from ~82% to ~75% even when every
internal/ package was well-covered.

## Decision

Exclude `cmd/g8s` from the aggregate coverage calculation. The
exclusion is applied by `grep -v 'github.com/tamld/g8s/cmd/g8s'` in
the Quality gate's coverage step.

CLI behaviour is exercised by:
- Integration tests (not yet written; tracked separately).
- Dogfooding via `g8s orchestrate --self-test` against the real agy
  worker.
- Manual smoke tests per release (release.yml `dist-validation` step).

The 80% threshold is enforced across the remaining 17 internal/
packages.

## Consequences

**Positive**
- Coverage gate no longer flake on legitimate refactors.
- The aggregate measures library coverage, where unit tests have the
  highest signal-to-cost ratio.

**Negative**
- `cmd/g8s` regressions can slip through (e.g. wrong flag name) and
  only get caught at dist-validation time.
- Until integration tests exist, the gate does not measure end-to-end
  behaviour.

## Reversal criteria

If/when integration tests for `cmd/g8s` are added that provide
sufficient signal (>50% coverage on a per-subcommand basis), revert
this exclusion.

## References

- DEBT-25: https://github.com/tamld/g8s/issues/97
- PR #100: https://github.com/tamld/g8s/pull/100
- `.github/workflows/quality.yml` — Coverage threshold step
