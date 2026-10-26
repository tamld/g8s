# OpenSpec DELTA-40: Binary Lifecycle CLI (`g8s update`, `g8s uninstall`) & Merged Worker-Branch Sweep Repair

* **Specification ID**: `DELTA-40`
* **Status**: `APPLIED` (Status: implemented)
* **Implementation Status**: `IMPLEMENTED`
* **Target Packages**: `internal/lifecycle`, `internal/cleanup`, `cmd/g8s`
* **Milestone**: `M6 (LLM Consumer & Fleet)`
* **Release**: `Unreleased` (first release after `v0.20.0`)

---

## 1. Problem Statement

1. **No Self-Update Path**: Operators install g8s through `scripts/install.sh`, release archives, or package managers, but the binary cannot update itself. `g8s update` exited `2` (unknown command), so operators either reran the installer or kept stale binaries.
2. **No Uninstall Path**: `g8s init` writes IDE MCP entries, `g8s service install` writes launchd/systemd/Windows service units, and the runtime creates state directories. Nothing removed them; `g8s uninstall` exited `2`.
3. **Merged Worker Branches Survive `g8s cleanup`**: The supervisor repository accumulated 36 `agy/sup-*` branches after `g8s cleanup --force`. Two defects caused this:
   - Candidates came from `git branch --merged main`, but deletion used `git branch -d`, which git judges against `HEAD` (or the upstream), not `main`. Run from a checkout whose `HEAD` lagged `main`, every candidate failed with "not fully merged" and was reported `skipped` (reproduced: 18 of 27 skipped).
   - Squash- or rebase-merged branches (patch already in `main`, tip not an ancestor) were never candidates (9 of 36 in the field repository).

---

## 2. Normative Requirements

### R1 — Self-Update (`internal/lifecycle/update.go`, `g8s update [--check] [--version vX.Y.Z] [--force] [--json]`)
- `Updater.ResolveRelease` MUST query `GET /repos/tamld/g8s/releases/latest`, or `GET /repos/tamld/g8s/releases/tags/<vX.Y.Z>` when `--version` is given.
- `AssetCandidates` MUST select the goreleaser archive for the host: `g8s_<ver>_darwin_all.tar.gz` (universal, preferred) then `g8s_<ver>_darwin_<arch>.tar.gz` on macOS, `g8s_<ver>_linux_<arch>.tar.gz` on Linux, and `g8s_<ver>_windows_<arch>.zip` on Windows.
- Verification MUST fail closed: a missing `checksums.txt`, an archive absent from it, a malformed or conflicting digest, or a SHA-256 mismatch aborts before the installed binary is touched (`E_UPDATE_CHECKSUM_MISSING`, `E_UPDATE_CHECKSUM_MISMATCH`). Every URL MUST be `https` (`E_UPDATE_INSECURE_URL`) and every download is bounded (`E_UPDATE_DOWNLOAD_TOO_LARGE`, 256 MiB default).
- `ExtractBinary` MUST read only the regular file whose base name is `g8s` / `g8s.exe`; archive paths are never joined onto the filesystem.
- `ReplaceBinary` MUST stage the new binary in a temp file next to the target, keep the previous binary at `<target>.bak`, and finish with a same-directory rename. POSIX: hard-link (copy fallback) backup, then atomic `rename(2)`. Windows: rename the running, locked `.exe` aside to `.bak`, then rename the staged file into place, restoring on failure.
- After replacement `g8s update` MUST run `<target> version --json`; on failure it MUST restore `<target>.bak` and exit non-zero (`E_UPDATE_SMOKE_FAILED`).
- `--check` MUST NOT download archives or write to the filesystem. Without `--version`, only a newer release counts as an update; a pinned `--version` may move to any different release.
- All network access flows through the injectable `HTTPDoer` seam so tests run offline against `httptest` TLS servers.

### R2 — Uninstall (`internal/lifecycle/uninstall.go`, `g8s uninstall [--dry-run] [--purge-state] [--yes] [--json]`)
- `PlanUninstall` MUST enumerate, without mutating anything: the service unit from `g8s service install` (launchd plist, systemd user unit, or Windows service), every IDE MCP config carrying `mcpServers.g8s` (locations from `initwiz.DetectIDEs`, the same table `g8s init` writes), the `<binary>.bak` rollback copy, and the running binary.
- Without `--yes`, or with `--dry-run`, the command MUST only print the plan (`uninstall_plan` envelope). `--yes` applies it (`uninstall_result`).
- State, data, config, cache and log directories (resolved with the `pathutil` defaults used by `g8s doctor --detect-paths`, plus `g8s-sess-*` session state) MUST be removed only with `--purge-state`. `SafeStateDir` MUST refuse non-absolute paths, base names without `g8s`, the filesystem root, the home directory or its ancestors, and any git repository; `ApplyUninstall` re-checks it per item.
- MCP removal MUST delete only the `g8s` server key and preserve every other key; the dedicated Antigravity `mcp/g8s.json` is deleted when nothing else remains.
- The binary MUST be kept (reported under `kept`) when its name is not `g8s`, when it is package-managed (Homebrew Cellar, `/usr/bin`, `/snap`), or when it lives inside a git working tree. On Windows the running `.exe` is renamed to `.uninstall-pending` instead of deleted.
- Each item is applied independently; failures are recorded per item and the command exits non-zero.

### R3 — Single Registration Line (`cmd/g8s/main.go`)
- `update` and `uninstall` MUST register through one `case "update", "uninstall":` dispatching to `runLifecycle` in `cmd/g8s/lifecycle.go`.

### R4 — Merged Worker-Branch Sweep (`internal/cleanup/cleanup.go`)
- `DefaultCleanupGitRunner` MUST implement the optional `BranchMergeChecker` capability: classify each branch against the integration base (`main`, else `master`, else `HEAD`) as `ancestor` (`git merge-base --is-ancestor`), `patch-equivalent` (`git cherry <base> <branch>` reports only `-` lines), or unmerged.
- `sweepOrphanBranches` MUST consider every local `agy/sup-*` / `agy-sup-*` branch, skip branches present on the remote, never delete an unmerged branch or a branch whose state cannot be determined, and delete verified branches with `git branch -D` so the result no longer depends on the current `HEAD`.
- Runners without `BranchMergeChecker` keep the `git branch --merged` candidate set.

---

## 3. Verification Scenarios

#### Scenario: Update Applies a Verified Release Atomically and Fails Closed
<!-- tests: TestUpdateApplyTable -->
- **GIVEN** an `httptest` TLS server serving release metadata, a tar.gz archive, and `checksums.txt`
- **WHEN** `Updater.Apply` runs for newer, same, forced, pinned, mismatched, missing-checksum, missing-asset, binary-less, smoke-failing, and HTTP-error releases
- **THEN** only verified archives replace the target, the previous binary remains at `<target>.bak`, smoke failure restores it, and every verification failure leaves the target byte-identical.

#### Scenario: Update Check Is Side-Effect Free
<!-- tests: TestUpdateCheckMakesNoChanges -->
- **GIVEN** a newer release on the fake server
- **WHEN** `Updater.Check` runs
- **THEN** it reports `update_available` and the selected `darwin_all` asset, downloads no archive, and writes no file.

#### Scenario: Atomic Replacement and Rollback on POSIX and Windows
<!-- tests: TestReplaceBinaryAndRollbackTable -->
- **GIVEN** an installed binary and a stale `.bak`
- **WHEN** `ReplaceBinary` and `Rollback` run with `goos` `linux` and `windows`
- **THEN** the target holds the new bytes, `.bak` holds the old bytes, no staging file leaks, and rollback restores the original.

#### Scenario: Update CLI Offline Round Trip
<!-- tests: TestRunUpdateCheckAndApplyOffline -->
- **GIVEN** `g8s update` wired to a fake TLS release server and a temp target
- **WHEN** `--check --json`, `--check`, `--json`, and `--force` run
- **THEN** check paths leave the target untouched and apply paths install the verified binary.

#### Scenario: Uninstall Plans Without Mutation and Applies Only Planned Items
<!-- tests: TestPlanUninstallTable -->
- **GIVEN** a fixture home with a binary, `.bak`, two MCP configs, a service unit, state and session directories, and an unrelated git repository
- **WHEN** `PlanUninstall` runs with and without `--purge-state`, then `ApplyUninstall` runs
- **THEN** the plan lists items in apply order without touching disk, state directories are planned only with `--purge-state`, foreign MCP keys survive, and the repository is untouched.

#### Scenario: Uninstall Refuses Unsafe Targets
<!-- tests: TestSafeStateDirTable -->
- **GIVEN** candidate directories including relative paths, home, a home ancestor, and a git repository
- **WHEN** `SafeStateDir` evaluates them
- **THEN** only absolute, `g8s`-named, non-repository directories outside the home ancestry are accepted.

#### Scenario: Uninstall CLI Defaults to Dry-Run
<!-- tests: TestRunUninstallDryRunAndApply -->
- **GIVEN** an isolated `HOME` with an Antigravity MCP config and a state directory
- **WHEN** `g8s uninstall` runs without `--yes`, with `--yes --dry-run`, and with `--yes --purge-state`
- **THEN** only the last invocation removes files, and the running test executable is never selected.

#### Scenario: Merged Worker Branches Are Swept Regardless of HEAD
<!-- tests: TestOrphanBranchSweepMergedAgainstBaseNotHEAD -->
- **GIVEN** a repository with fast-forward-merged, cherry-picked (patch-equivalent), and unmerged `agy/sup-*` branches, a merged non-worker branch, and `HEAD` on a branch behind `main`
- **WHEN** `RunCleanupSweep` runs the `orphan-branch` target in dry-run and apply modes
- **THEN** merged and patch-equivalent worker branches are listed and deleted, the unmerged worker branch and the non-worker branch survive.

#### Scenario: Branch Merge-State Classification
<!-- tests: TestBranchMergeStateClassification -->
- **GIVEN** the same fixture repository
- **WHEN** `BranchMergeState` classifies each branch
- **THEN** it returns `ancestor`, `patch-equivalent`, or unmerged against base `main`, and errors on a missing branch.
