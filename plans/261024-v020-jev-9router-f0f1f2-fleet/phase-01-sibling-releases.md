# Phase 01 — Release New Versions for Eligible Sibling Projects

- **Status**: `in_progress`
- **Targets**:
  1. `adopter-utility-01` (`adopter-utility-01`) $\to$ `v2.6.0`
  2. `defense-in-depth` (`defense-in-depth`) $\to$ `v1.0.1`
  3. `aegis` (`aegis`) $\to$ `v1.1.0`

## Steps
- [x] **1.1 (`adopter-utility-01 v2.6.0`)**: Update `package.json` version to `2.6.0`, run `npm run sync-versions`, update `CHANGELOG.md`, commit `chore(release): v2.6.0`, create annotated tag `v2.6.0`, and push `master` + `v2.6.0` to `origin`.
- [x] **1.2 (`defense-in-depth v1.0.1`)**: Update version in `package.json` / `pyproject.toml` / `CHANGELOG.md` (if present), commit `chore(release): v1.0.1`, create annotated tag `v1.0.1`, and push `main` + `v1.0.1` to `origin`.
- [x] **1.3 (`aegis v1.1.0`)**: Create annotated tag `v1.1.0` on `origin/main` (`c8c479d50`) and push tag `v1.1.0` to `origin`.
