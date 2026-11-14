# Release Standard Operating Procedure (SOP)

This document defines the strict, repeatable step-by-step procedure for preparing, validating, and publishing a new release of **g8s**.

---

## 1. Pre-Release Verification Checklist

Before cutting any release tag (`vX.Y.Z`), the release manager (Human or Agent) must verify all gates:

- [ ] **Clean `main` synced with `origin/main`**: `git status` reports working tree clean; local `main` matches `origin/main`.
- [ ] **Full test + `-race` suite**: `CGO_ENABLED=0 go test -v -race ./...` passes 100% on all packages.
- [ ] **Green multi-OS CI**: GitHub Actions workflows `CI`, `Quality`, `Crash-Survival E2E`, and `Windows E2E` on `main` are completely GREEN.
- [ ] **Secret hygiene**: `gitleaks detect` reports 0 secrets in Git history.
- [ ] **OpenSpec parity**: All OpenSpec deltas targeted for this release in `spec/openspec/` are marked `APPLIED` (or `RESERVED` for tombstones).
- [ ] **D-01 Red Cell Pass** (`docs/directives.md`): Independent adversarial surface audit completed across `README.md`, CLI `--help`, and `offer/`; findings triaged before tag.
- [ ] **D-02 Self-Audit probes** (`docs/directives.md`): `g8s eval run --category self-audit` passes and result recorded in release notes.
- [ ] **Local pre-tag checks pass**: `./tools/pre_tag.sh` succeeds locally (version sync, changelog content, Go version parity, remote branch status, and `tools/ci_surface_inventory.sh` all green).

---

## 2. Release Execution Procedure

### Automated Release via `tools/release.sh`

```
┌──────────────────────┐   ┌──────────────────────┐   ┌──────────────────────┐
│ 1. DRY-RUN           │   │ 2. REAL RUN          │   │ 3. POST-TAG SYNC     │
│ ./tools/release.sh   │   │ ./tools/release.sh   │   │ manifest.json amended│
│ <VERSION> --dry-run  │   │ <VERSION>            │   │ with commit SHA/date │
└──────────────────────┘   └──────────────────────┘   └──────────────────────┘
```

### Step 1 — Dry Run (Mandatory First)

```bash
./tools/release.sh <VERSION> --dry-run
```

This prints all intended changes without modifying files:
- Version bump in `cmd/g8s/version.go`
- `CHANGELOG.md` `[Unreleased]` section promotion
- `manifest.json` version and `latest_release.tag`
- Packaging manifests: `packaging/windows/g8s.nsi`, `packaging/windows/g8s.wxs`, `packaging/chocolatey/tools/chocolateyinstall.ps1`, `packaging/winget/manifest.yaml`, `packaging/chocolatey/g8s.nuspec`

### Step 2 — Real Run

```bash
./tools/release.sh <VERSION>
```

The script executes atomically:
1. **Pre-tag gates**: Runs `tools/pre_tag.sh` with `G8S_TARGET_VERSION=<VERSION>` (validates version sync, changelog, Go version, CI status, surface inventory).
2. **Updates version.go**: Bumps `Version` constant in `cmd/g8s/version.go`.
3. **Promotes `CHANGELOG.md`**: Inserts new version header after `## [Unreleased]`, preserving existing notes.
4. **Updates `manifest.json`**: Sets `version` and `latest_release.tag` to `v<VERSION>` (does NOT touch `latest_release.commit` or `date` — those are post-tag).
5. **Updates packaging manifests**:
   - `packaging/windows/g8s.nsi`: `!define VERSION`
   - `packaging/windows/g8s.wxs`: `<?define Version?>`
   - `packaging/chocolatey/tools/chocolateyinstall.ps1`: Download URL and MSI filename
   - `packaging/winget/manifest.yaml`: `PackageVersion` and download URL path
   - `packaging/chocolatey/g8s.nuspec`: `<version>` element
6. **Commits**: Stages all modified files, creates commit `chore: release <VERSION>`.
7. **Tags**: Creates annotated tag `v<VERSION>` with message `Release <VERSION>`.
8. **Atomic push**: `git push --atomic origin main v<VERSION>`.
9. **Post-tag manifest amendment**: Updates `manifest.json` `latest_release.commit` (short SHA of tag commit) and `latest_release.date` (today), commits `chore(release): record v<VERSION> commit/date in manifest latest_release`, pushes to `main`.

### Step 3 — GoReleaser (Triggered by Tag Push)

`.github/workflows/release.yml` executes GoReleaser to:
- Cross-compile static binaries for `darwin/arm64`, `darwin/amd64`, `linux/amd64`, `linux/arm64`, `windows/amd64`, `windows/arm64`.
- Generate Linux packages: `.deb`, `.rpm`, `.apk`.
- Generate SHA-256 `checksums.txt` and Sigstore bundle (`cosign`).
- Generate SPDX SBOM via `syft`.
- Update Homebrew tap (`tamld/homebrew-g8s`) and Scoop bucket (`tamld/scoop-bucket`).

---

## 3. Post-Release Smoke Test

Run on a clean machine after assets are published:

```bash
# 1. Version & Build Info
g8s version

# 2. Roles & Permissions Surface
g8s roles
g8s permissions

# 3. Receipt Issuance & Verification
g8s receipt issue --issuer smoke --path "internal/config/*.go" --ttl 300
```

---

## 4. Rollback Procedure

If a release must be reverted, see [`docs/release/ROLLBACK.md`](release/ROLLBACK.md) for the binary revert and tag deletion procedure.

---

## 5. Versioning Policy

`g8s` follows [Semantic Versioning 2.0.0](https://semver.org/). See [`docs/VERSIONING.md`](VERSIONING.md) for milestone gating and pre-release conventions.