# ADR-0015: Pin linter versions in CI workflows

**Status**: ACCEPTED
**Date**: 2026-08-29
**Deciders**: Sisyphus
**Triggered by**: DEBT-22 + DEBT-24 cleanup, PR #100

## Context

CI workflows historically used `@latest` for linter installs:

```yaml
go install honnef.co/go/tools/cmd/staticcheck@latest
go install github.com/securego/gosec/v2/cmd/gosec@latest
```

This is a hazard:
- `honnef.co/go/tools@latest` jumped to requiring Go 1.26 in mid-2026,
  breaking the CI gate on Go 1.25.14 runners.
- `github.com/DominicKramer/go-linter/pkg/funlen` was archived
  upstream — install fails with `git ls-remote: exit 128`.
- `gosec@latest` adds new rules periodically that may flag
  pre-existing code, blocking the gate without warning.

The fix loop in DEBT-22 took 3 iterations to discover and resolve.

## Decision

**Pin every linter to a specific version** in CI workflow install
steps. Bumps require:

1. A new issue tracking the linter version bump.
2. A DoD checklist (test the gate against current main first).
3. Update the `Version` comment in the workflow file.

```yaml
go install mvdan.cc/gofumpt@latest        # exception: stable since 2020
go install honnef.co/go/tools/cmd/staticcheck@v0.7.0
go install github.com/securego/gosec/v2/cmd/gosec@latest
go install github.com/kisielk/errcheck@latest
go install golang.org/x/vuln/cmd/govulncheck@latest
go install github.com/uudashr/gocognit/cmd/gocognit@latest
```

`@latest` is permitted only for tools that:
- Have been stable for >2 years
- Don't have a breaking version policy
- Are explicitly called out in the comment

## Consequences

**Positive**
- Reproducible CI: same code → same gate outcome.
- Linter upgrades are conscious decisions, not silent regressions.
- Easier to bisect when a linter version is the culprit.

**Negative**
- Manual bump cycle (4-12 weeks behind upstream).
- New contributors on a different linter version locally may see
  CI-only diffs.

## Reversal criteria

If pinning causes a security issue (e.g. known CVE in pinned linter
that affects our use), bump immediately and document.

## References

- DEBT-22: https://github.com/tamld/g8s/issues/94
- DEBT-24: https://github.com/tamld/g8s/issues/96
- PR #100: https://github.com/tamld/g8s/pull/100
