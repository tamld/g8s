# ADR-0017: Use .errcheck_excludes for defer _ = X.Close() pattern

**Status**: ACCEPTED
**Date**: 2026-08-29
**Deciders**: Sisyphus + agy worker
**Triggered by**: DEBT-23 cleanup, PR #100

## Context

The Quality gate runs `errcheck ./...` to enforce that no error
is silently swallowed. The Go convention in CLI subcommands is:

```go
store, err := controlplane.NewControlPlane(dbPath, nil)
failIf(err)
defer func() { _ = store.Close() }()
```

The `defer func() { _ = X.Close() }()` pattern is technically a
silent error swallow, but the alternative (propagating the error
from a deferred close through a CLI subcommand) requires
non-trivial restructuring for marginal value (the process is about
to exit anyway).

A wholesale rewrite of all CLI subcommands to use a typed wrapper
(`cliutil.LogClose`) is out of scope for any single concern
(DEBT-23).

## Decision

Allowlist the `defer func() { _ = X.Close() }()` pattern in
`.errcheck_excludes` at the repo root. Format:

```
// errcheck exclusions
// Pattern: defer func() { _ = X.Close() }() is a known idiom
// for top-level CLI subcommand teardown. The process exits
// after main() returns; propagating the close error through
// requires a custom wrapper which is out of scope.
```

Apply:

```bash
errcheck -blank -asserts -ignoretests -exclude .errcheck_excludes ./...
```

Track the `cliutil.LogClose` refactor as a separate concern (not
DEBT-23, since DEBT-23 is closed by PR #100).

## Consequences

**Positive**
- `errcheck` gate re-enabled with a documented exclusion.
- No widespread CLI subcommand rewrite needed.
- Future PRs that introduce new `defer _ = X.Close()` patterns pass
  the gate but should be flagged in code review.

**Negative**
- The "no silent error swallow" axiom (Constitution Axiom 5) is
  violated in CLI subcommand teardown.
- Future contributors may extend the pattern to non-CLI code where
  it would matter.

## Reversal criteria

When the `cliutil.LogClose` helper is implemented and the
`defer func() { _ = X.Close() }()` calls are migrated, the
`.errcheck_excludes` file is removed.

## References

- DEBT-23: https://github.com/tamld/g8s/issues/95
- PR #100: https://github.com/tamld/g8s/pull/100
- Constitution Axiom 5: `spec/constitution.md`
