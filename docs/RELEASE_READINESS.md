# Release Readiness Checklist

Status snapshot for the upcoming release-version stage. The repository is currently
**PRIVATE**; the visibility switch to public and any CI matrix trimming are
owner-gated decisions made at release time.

## Verification gates (all met at MVP close, 2026-08-24)

- [x] Zero-CGO dual-pass verification green in Docker `golang:1.25`:
      `CGO_ENABLED=0 go vet ./... && go test ./...` and
      `CGO_ENABLED=1 go test -race ./...` across all packages.
- [x] Test corpus ≥ 140 collected functions (187 at close).
- [x] `goreleaser release --snapshot --clean` produces five cross-platform archives
      (darwin/linux/windows on amd64/arm64, excluding windows/arm64).
- [x] Final audit receipt maps all board receipts to plan phases with
      `full_outcome_complete: true`.
- [x] CI green on ubuntu/macos/windows (run 32716564641).

## Outstanding items before tagging a version

- [ ] Owner decision on PR #1 (`pterm` TUI polish): blocking stderr-regression fix,
      gofmt cleanup, dependency-proportionality call.
- [ ] Add a `gofmt -l` gate to CI (drafted in `chore/release-prep`; PR-ready).
- [ ] Decide the first tag (`v0.1.0` suggested) and run the six-gate pre-release
      audit per `docs/RELEASE_SOP.md`.
- [ ] Owner flips repo visibility to **public** (rule: only at release-version stage).
- [ ] Trim/disable non-essential workflows or shrink the OS matrix if Actions quota
      becomes a concern for public CI traffic.
- [ ] Verify `LICENSE` (MIT), `README.md`, and archive contents one final time via a
      fresh `goreleaser` snapshot after the last commit.
- [ ] Confirm `.goreleaser.yaml` `ldflags` version stamping renders the chosen tag.

## Post-release

- [ ] Publish GitHub Release with generated archives and checksums.
- [ ] Announce the MCP tool surface (`g8s_*`) in the README quick-start.
