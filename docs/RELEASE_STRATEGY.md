# Release Strategy

This document is the single source of truth for how g8s versions, names, verifies,
and publishes releases across all supported operating systems. It binds together
[VERSIONING.md](VERSIONING.md), [RELEASE_SOP.md](RELEASE_SOP.md), and the GoReleaser
configuration at the repository root.

## 1. Milestone Ladder

Milestones follow the SemVer 2.0.0 ladder defined in [VERSIONING.md](VERSIONING.md).
Foundations M1-M3 (harness, control plane, receipts, MCP surface, providers, worker
supervisor, service manager, packaging) are already shipped and verified; therefore
the first published version is `v0.1.0` directly.

| Milestone | Version | Contents | Status |
| --- | --- | --- | --- |
| M1 Foundation | v0.1.0-alpha *(optional)* | Harness, control plane, receipt delegation | Delivered |
| M2 Capabilities | v0.1.0-beta *(optional)* | stdio MCP server, pluggable providers | Delivered |
| M3 OS Daemon & Packaging | **v0.1.0** | Multi-OS service manager, GoReleaser pipeline | Delivered — next tag |
| Hardening line | v0.1.x | Debt payoffs D1-D5 below, performance work | Planned |
| Feature line | v0.2.0 | Cobra CLI migration (D7), cross-platform service backends (D8) | Planned |
| Production GA | v1.0.0 | 100% parity verification executed (D3), formal security audit (D9) | Planned |

Pre-release suffixes (`-alpha.N`, `-beta.N`, `-rc.N`) remain available but are not
mandatory; they exist for risky internal testing before a minor bump.

## 2. Version Scheme and Artifact Naming

**Consistency rule: the annotated git tag is the single version source.** The
`main.Version` constant is stamped from the tag via `-ldflags "-X main.Version=..."`;
no other mechanism may set the reported version.

GoReleaser produces one archive per supported target using the template
`{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}`. The resulting artifact
matrix for every release:

| OS | Arch | Archive name | Format |
| --- | --- | --- | --- |
| darwin | amd64 | g8s_\<version\>_darwin_amd64.tar.gz | tar.gz |
| darwin | arm64 | g8s_\<version\>_darwin_arm64.tar.gz | tar.gz |
| linux | amd64 | g8s_\<version\>_linux_amd64.tar.gz | tar.gz |
| linux | arm64 | g8s_\<version\>_linux_arm64.tar.gz | tar.gz |
| windows | amd64 | g8s_\<version\>_windows_amd64.zip | zip |

Every release additionally ships a SHA-256 manifest named `checksums.txt`.
Windows/arm64 is intentionally excluded until demand justifies the runner cost.
Naming never changes between releases; only `<version>` moves, guaranteeing that
scripts and package manifests can pin artifacts deterministically.

## 3. Release Channel Pipeline

```
feature branch -> PR -> main (CI green on ubuntu/macos/windows)
      -> release/vX.Y.Z prep branch (changelog finalised, docs bumped)
      -> six-gate audit per RELEASE_SOP.md
           Gate 1 clean worktree        Gate 4 secret hygiene (history scan)
           Gate 2 CGO=0 race tests      Gate 5 spec deltas APPLIED
           Gate 3 multi-OS CI green     Gate 6 docs + version bump
      -> annotated tag vX.Y.Z pushed
      -> goreleaser snapshot re-verified against the tagged commit
      -> GitHub Release published with archives + checksums.txt
      -> post-release smoke: ./g8s version / roles / permissions / receipt issue
```

Rules:

- Only `main` is releasable; it must be green on all three operating systems.
- A `release/vX.Y.Z` branch freezes the candidate while the audit runs; fixes land
  on `main` first and are cherry-picked forward.
- The changelog ([CHANGELOG.md](../CHANGELOG.md)) is finalised on the prep branch;
  the GitHub Release body mirrors its section verbatim.
- Tag pushes trigger the release pipeline (see debt item D5 for the automation
  status); a failed gate blocks the tag, never the reverse.

## 4. Deployment Integration Checklist

Per released version:

- [ ] Archives + `checksums.txt` attached to the GitHub Release.
- [ ] Checksum verification documented per OS: `shasum -a 256 -c checksums.txt`
      (macOS/Linux), `certutil -hashfile <file> SHA256` (Windows).
- [ ] Homebrew tap recipe `tamld/homebrew-tap/g8s.rb` updated with the new version
      and archive URL.
- [ ] README quick-start refreshed: install commands per OS and the MCP client
      configuration block (`g8s_*` tool surface).
- [ ] Post-release smoke executed on one machine per OS family.
- [ ] Optional (future): keyless `cosign sign-blob` on archives + checksum, noted
      here so signing lands as an additive step without changing the pipeline order.

## 5. Technical Debt Register

| ID | Debt | Payoff milestone |
| --- | --- | --- |
| D1 | cmd/g8s lacks a stderr-specific failure-output test | v0.1.0 |
| D2 | BenchmarkHasSuffix discards results (no sink var) | v0.1.0 |
| D3 | Side-by-side Go-vs-Python parity verification (matrix in RELEASE_READINESS.md) not yet executed | Executed against tagged candidate before publishing v0.1.0 |
| D4 | ldflags stamp check (`./g8s version` must equal the tag) | v0.1.0 publish gate |
| D5 | Tag-triggered release automation absent (only ci.yml exists); SOP assumes it | Decide before v0.1.0: add release.yml or codify local-run procedure |
| D6 | pterm dependency tree (~10 modules) binary-size impact unmeasured — MEASURED 2026-08-25: pre-pterm (48d4b90) darwin/arm64 `-trimpath -ldflags "-s -w"` = 10,214,674 B; post-pterm (main 8fd0f39) = 10,740,114 B; delta ≈ 513 KB (+5.1%). Accepted as proportionate for the TUI value; no slim-down planned for v0.1.x; revisit only if v0.2.0 profiling shows material bloat elsewhere | Closed as measured/accepted in v0.1.0 |
| D7 | cobra CLI migration deferred (T012) | v0.2.0 |
| D8 | kardianos cross-platform service backends deferred (T017; launchd-only today) | v0.2.0+ |
| D9 | Formal security audit required for GA claims | v1.0.0 |

Items D1-D2 are paid off alongside this document. D3-D5 gate the v0.1.0 publish
itself; D6-D9 are scheduled into later milestones so the register stays honest
about what "done" means at each version.
