# Release Rollback and Downgrade Runbook

Immutable release tags are policy: never re-tag, never force-push an existing tag, and never replace GitHub Release assets in place.

## Maintainer incident response

1. Identify the last known-good tag on `main` (for example `vX.Y.Z`).
2. Revert the bad commit(s) on `main` with `git revert` (prefer separate reverts for clarity).
3. Cut a corrective patch release: bump `cmd/g8s/version.go`, tag a new SemVer (`vX.Y.Z+1`), and push the annotated tag so the release workflow runs again.
4. Document the incident in release notes; link the revert SHA and any follow-up issue.

## User downgrade (install pin)

Install a specific release instead of `latest`:

```sh
export G8S_VERSION=vX.Y.Z
curl -fsSL https://raw.githubusercontent.com/tamld/g8s/main/scripts/install.sh | bash
```

`G8S_VERSION` is read by `scripts/install.sh` before asset selection; use the full tag including the `v` prefix.

## Verification before trust

The installer enforces SHA-256 checksum verification against `checksums.txt` (fail-closed). After download, operators may additionally confirm:

**GitHub Artifact Attestations (SLSA Build L2 provenance)**

```sh
gh attestation verify <release-asset> -R tamld/g8s --owner tamld
```

**Keyless Cosign bundle on checksums**

```sh
cosign verify-blob \
  --certificate-identity "https://github.com/tamld/g8s/.github/workflows/release.yml@refs/tags/<TAG>" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --bundle checksums.txt.sigstore.json \
  checksums.txt
```

Then verify individual archives:

```sh
sha256sum --ignore-missing -c checksums.txt
```

Replace `<TAG>` with the release tag you pinned in `G8S_VERSION`.
