# Orca CI/CD Architecture & Pipeline Audit for g8s (DEBT-56, DEBT-57, DEBT-58)

> **Document SSoT**: CI/CD Architecture & Automation Strategy Reference  
> **Source Repository Studied**: [`stablyai/orca`](https://github.com/stablyai/orca) (`.github/workflows/`)  
> **Target System**: `g8s` (Zero-CGO, Pure-Go Orchestrator)  
> **Date**: 2026-08-30  

> ## Legal Disclaimer
> 
> This document is an **independent architectural study** of the GitHub Actions CI/CD workflows in `stablyai/orca` (https://github.com/stablyai/orca) for the purpose of identifying resilient pipeline patterns for `g8s`.
> 
> **NO workflow code or proprietary scripts from orca are reproduced, copied, translated, or redistributed.** Only architectural automation patterns (concurrency management, change-set path scoping, fail-closed aggregate required checks, disposable-runner subprocess survival testing, and two-stage binary signing pipelines) are analyzed and designed from first principles for g8s's Pure-Go stack.
> 
> orca is the property of Lovecast Inc. (licensed under MIT). Clean-room architectural analysis is maintained to guarantee total autonomy and zero licensing contamination of `g8s`.

---

## 1. Executive Summary

Orca maintains an industrial-grade CI/CD pipeline comprising **32 GitHub Actions workflows** spanning pull request validation, test sharding, headless rendering tests, multi-platform artifact packaging, macOS notarization, Windows inner-binary code signing rehearsals, and disposable-runner crash/update survival harnesses.

While Orca builds an Electron/Node-based multi-agent desktop application and g8s builds a statically compiled, Zero-CGO Go CLI orchestrator, both systems share critical lifecycle requirements:
1. **Uninterrupted Subprocess Management**: Orchestrating long-running autonomous CLI agents and daemon workers without orphaned child processes.
2. **Cross-Platform Reliability**: Robust execution across Linux, macOS, and Windows.
3. **High Development Velocity**: Ensuring CI runs complete quickly without blocking PR queues while strictly enforcing quality gates.

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                       ORCA CI/CD PIPELINE TAXONOMY                          │
├──────────────────────┬──────────────────────┬───────────────────────────────┤
│ PR & QUALITY GATES   │ E2E & HARDWARE SUITES│ DISTRIBUTION & RELEASE        │
├──────────────────────┼──────────────────────┼───────────────────────────────┤
│ • pr.yml (18 jobs)   │ • e2e.yml (sharded)  │ • release-cut.yml (2.1k lines)│
│ • pr-test-loc.yml    │ • computer-e2e.yml   │ • release-mac-build.yml       │
│ • node-next-compat   │ • terminal-ime-e2e   │ • dev-channel-win-build.yml   │
│ • unit-tests.yml     │ • terminal-perf.yml  │ • windows-signing-rehearsal   │
│                      │ • wayland-gpu-sandbox│ • homebrew-bump.yml           │
├──────────────────────┼──────────────────────┼───────────────────────────────┤
│ LIFECYCLE SURVIVAL   │ AUTOMATION & BOTS    │ MOBILE CLIENTS                │
├──────────────────────┼──────────────────────┼───────────────────────────────┤
│ • win-crash-survival │ • track-community-prs│ • mobile.yml                  │
│ • win-update-survival│ • issue-os-labeler   │ • mobile-android-release.yml  │
│ • win-update-e2e     │ • readme-badge       │ • mobile-ios-release.yml      │
│ • win-terminal-rest. │ • pullfrog           │                               │
└──────────────────────┴──────────────────────┴───────────────────────────────┘
```

---

## 2. Complete Catalog of Orca Workflows (32 Workflows)

| Workflow File | Name / Purpose | Triggers | Concurrency Policy | Key Pipeline Characteristics |
| :--- | :--- | :--- | :--- | :--- |
| `pr.yml` (930 lines) | **PR Checks** (Central PR Gate) | `pull_request` | `group: pr-checks-${{ pr }}`, `cancel: true` | 18 jobs; `code_paths` diff analysis; skips expensive jobs on docs; fail-closed aggregate `verify` job. |
| `pr-test-loc.yml` (43 lines) | **PR Test LoC** | `pull_request` | `group: pr-test-loc-${{ pr }}`, `cancel: true` | Fast line-of-code ratio sanity check (< 2 min timeout). |
| `unit-tests.yml` (60 lines) | **Unit Tests** | `workflow_call` | Inherited | 8-way parallel test sharding (`shard: [1..8]`) across Node matrix. |
| `e2e.yml` (347 lines) | **E2E Integration** | `workflow_call`, `dispatch` | None | Matrix sharding, native build cache caching, Linux X11 virtual display. |
| `computer-e2e.yml` (196 lines) | **Computer-Use E2E** | `pull_request`, `schedule`, `dispatch` | None | Cross-OS OS-interaction testing (macOS native owner smoke, Windows, Linux). |
| `golden-e2e-experiment.yml` (116 lines) | **Golden E2E Experiment** | `pull_request`, `dispatch` | None | Snapshot testing of terminal / agent rendering outputs. |
| `terminal-ime-e2e.yml` (87 lines) | **Terminal IME E2E** | `workflow_call`, `dispatch`, `schedule` | None | Headless Linux X11 ibus input-method editor interaction test. |
| `terminal-perf.yml` (149 lines) | **Terminal Perf** | `workflow_dispatch` | None | Measures typing latency, redraw frame rates, and PTY backpressure. |
| `linux-wayland-gpu-sandbox.yml` (88 lines) | **Wayland GPU Sandbox** | `pull_request`, `dispatch` | None | Headless Wayland compositor GPU acceleration tests in CI container. |
| `node-next-compat.yml` (21 lines) | **Node Next Compat** | `pull_request`, `push` | `group: node-next-compat`, `cancel: true` | Forward compatibility validation with unreleased Node runtimes. |
| `win-crash-survival-e2e.yml` (153 lines) | **Windows Crash-Survival E2E** | `workflow_dispatch` | `group: win-crash-survival-e2e-${{ pr\|\|ref }}`, `cancel: true` | Kills main app process (no tree-kill), asserts detached daemon & shell survive, verifies relaunch reconnection. |
| `win-update-survival-e2e.yml` (117 lines) | **Windows Update-Survival (Branch)** | `push` (feature branch), `dispatch` | `group: win-update-survival-e2e-${{ ref }}`, `cancel: true` | Installs branch build, updates over live process, asserts daemon PID persists without console flashes. |
| `win-update-e2e.yml` (146 lines) | **Windows Update-Survival E2E** | `push` (feature branch), `dispatch` | `group: win-update-e2e-${{ ref }}`, `cancel: true` | Tests update transitions between release tags on disposable Windows runners. |
| `windows-terminal-restart-e2e.yml` (73 lines) | **Windows Terminal Restart E2E** | `workflow_dispatch` | `group: win-term-restart-${{ pr\|\|ref }}`, `cancel: true` | Regression testing for terminal session restoration across restarts. |
| `windows-signing-rehearsal.yml` (354 lines) | **Windows Signing Rehearsal** | `workflow_dispatch` | None | 2-phase signing test: signs unpacked PE binaries first, builds NSIS installer, signs installer wrapper. |
| `dev-channel-win-build.yml` (318 lines) | **Dev Channel Windows Build** | `push`, `dispatch` | `group: dev-channel-win-build-${{ ref }}`, `cancel: false` | Automated nightly/dev build compilation and NSIS packaging. |
| `adhoc-mac-build.yml` (453 lines) | **Adhoc macOS + Win Dev Build** | `workflow_dispatch` | `group: adhoc-mac-build-${{ ref }}`, `cancel: false` | Ephemeral test builds uploaded to adhoc distribution repo with 30-day retention. |
| `daily-mac-build.yml` (540 lines) | **Daily macOS Dev Build** | `schedule`, `dispatch` | `group: daily-mac-build`, `cancel: false` | Daily scheduled builds with Apple notarization and post-build E2E smoke tests. |
| `hourly-mac-build.yml` (468 lines) | **Hourly macOS Dev Build** | `schedule`, `dispatch` | `group: hourly-mac-build`, `cancel: false` | Frequent integration builds ensuring trunk is always releasable. |
| `release-mac-build.yml` (180 lines) | **Release macOS Build** | `workflow_dispatch` | `group: release-mac-build-${{ tag }}`, `cancel: false` | Builds, signs, notarizes, and attaches macOS DMG/zip assets to GitHub release draft. |
| `release-cut.yml` (2141 lines) | **Cut Release** (Release Orchestrator) | `workflow_dispatch` | `group: release-cut`, `cancel: false` | Unified release pipeline: semver bump, tag push, cross-platform builds, SignPath signing, asset release. |
| `release-policy.yml` (99 lines) | **Release Policy** | `release` (published/edited) | `group: release-policy`, `cancel: false` | Enforces release tagging consistency, validates release asset presence. |
| `homebrew-bump.yml` (200 lines) | **Homebrew Cask Bump** | `workflow_call`, `dispatch` | None | Automatically computes sha256 of released artifacts and creates PR to Homebrew tap. |
| `skill-update-roundtrip.yml` (59 lines) | **Skill Update Roundtrip** | `pull_request`, `push`, `merge_group` | None | Tests skill sync and symlink vs. copy behavior across OS matrix. |
| `daemon-relocation-spike.yml` (98 lines) | **Daemon Relocation Spike** | `push`, `dispatch` | `group: daemon-reloc-spike-${{ ref }}`, `cancel: true` | Experimental testbed for background daemon architecture relocation. |
| `mobile.yml` (101 lines) | **Mobile Checks** | `pull_request` | None | React Native / Expo linting and typecheck. |
| `mobile-android-release.yml` (115 lines) | **Mobile Android Release** | `workflow_dispatch` | None | Gradle AAB release generation, keystore signing, Google Play upload. |
| `mobile-ios-release.yml` (196 lines) | **Mobile iOS Release** | `workflow_dispatch` | None | Fastlane TestFlight distribution and Apple certificate management. |
| `issue-os-labeler.yaml` (62 lines) | **Label Issues by OS** | `issues` (opened, edited) | None | Automated issue triaging by operating system mention (macOS, Windows, Linux). |
| `track-community-prs.yaml` (120 lines) | **Track Community PRs** | `pull_request_target`, `dispatch` | None | Projects v2 automation via GitHub App bot token (`bufo-bot`). |
| `pullfrog.yml` (57 lines) | **Pullfrog** | `workflow_dispatch` | None | Automated agent task runner for PR generation. |
| `readme-downloads-badge.yml` (49 lines) | **README Downloads Badge** | `schedule`, `release`, `dispatch` | `group: readme-downloads-badge`, `cancel: false` | Computes cumulative GitHub release download stats and updates repo README badge. |

---

## 3. Core Architectural Patterns Identified in Orca

### Pattern 1: Concurrency Strategy & Queue Depth Optimization

Orca enforces an explicit, differentiated concurrency control policy across all workflows:

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                      CONCURRENCY DISPATCH STRATEGY                          │
├──────────────────────────────────────┬──────────────────────────────────────┤
│ PR & Ephemeral Testing               │ Release, Publishing & Mutating State │
│ (cancel-in-progress: true)           │ (cancel-in-progress: false)          │
├──────────────────────────────────────┼──────────────────────────────────────┤
│ • Group: ${{ workflow }}-${{ pr }}   │ • Group: ${{ workflow }}             │
│ • Pushing commit N+1 immediately     │ • Runs are strictly queued/serialized│
│   terminates run N                   │ • Prevents partial tags, corrupted   │
│ • Prevents runner pool starvation    │   releases, or git push collisions   │
└──────────────────────────────────────┴──────────────────────────────────────┘
```

1. **PR & Iterative Workflows**:
   - Workflows like `pr.yml`, `win-crash-survival-e2e.yml`, and `windows-terminal-restart-e2e.yml` set `concurrency.cancel-in-progress: true` keyed by PR number (`${{ github.event.pull_request.number || github.ref }}`).
   - *Rationale*: A new push immediately renders pending/running CI jobs obsolete. Killing them instantly frees GitHub Actions runner minutes and shortens PR feedback loops.
2. **Release, Tagging & State-Mutating Workflows**:
   - Workflows like `release-cut.yml`, `release-mac-build.yml`, `release-policy.yml`, and `readme-downloads-badge.yml` set `concurrency.cancel-in-progress: false` with a static group name.
   - *Rationale*: Release publishing and git-tag fast-forwarding must be strictly serialized to prevent split-brain state, duplicate tag creation, or interrupted artifact uploads.

---

### Pattern 2: Dynamic Path Scoping & Single Required Status Check (`verify` Gate)

A major challenge in GitHub Actions is combining path filtering (`paths:` filters) with Required Status Checks in Branch Protection Rules. If a workflow uses top-level `on.pull_request.paths`, PRs that modify only markdown or documentation will NOT trigger the workflow, causing GitHub Branch Protection to hang indefinitely waiting for required status checks.

Orca resolves this with an elegant, fail-closed two-tier gating pattern in `pr.yml`:

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                     FAIL-CLOSED STATUS GATE ARCHITECTURE                    │
│                                                                             │
│  PR Triggered ───► [ code_paths ] (Detect changed files via diff scope)     │
│                         │                                                   │
│       ┌─────────────────┼─────────────────┬────────────────┐                │
│       ▼                 ▼                 ▼                ▼                │
│ [ static_analysis ] [ typecheck ]    [ test_shards ]   [ package ]          │
│   (if: code_paths.    (if: code_paths.  (if: code_paths. (if: code_paths.   │
│    outputs.sa)         outputs.tc)       outputs.test)    outputs.pkg)      │
│       │                 │                 │                │                │
│       └─────────────────┼─────────────────┴────────────────┘                │
│                         ▼                                                   │
│                  [ verify ] (if: always())                                  │
│                  - Code PR: requires all active jobs == 'success'           │
│                  - Docs PR: verifies skipped jobs == 'skipped'              │
│                  - Branch protection only requires [ verify ]!              │
└─────────────────────────────────────────────────────────────────────────────┘
```

1. **Step 1 — Scope Detection (`code_paths` job)**:
   - Evaluates changed files against dependency glob rules (`src/**`, `config/**`, `*.json`, etc.).
   - Emits boolean outputs (`static_analysis: true|false`, `test: true|false`, `package: true|false`).
2. **Step 2 — Conditional Execution**:
   - Downstream jobs declare `if: needs.code_paths.outputs.<step> == 'true'`.
   - Docs-only PRs skip heavy test shards, packaging, and linters.
3. **Step 3 — Fail-Closed Aggregate Verification (`verify` job)**:
   - Runs with `if: always()` after all jobs.
   - Inspects `needs.<job>.result` alongside `needs.code_paths.outputs.<job>`:
     - If a job was scheduled to run (`should == 'true'`), it MUST report `success`.
     - If a job was skipped (`should == 'false'`), it MUST report `skipped`.
     - If `code_paths` itself failed, `verify` exits non-zero.
4. **Branch Protection Simplicity**:
   - The repository requires **only one single status check (`verify`)** in GitHub repository settings.

---

### Pattern 3: Subprocess & Daemon Crash/Update Survival Harness on Disposable Runners

Orca orchestrates background daemon processes and interactive terminal shells. To prove that background workers survive parent crashes and application updates, Orca uses dedicated GitHub Actions workflows (`win-crash-survival-e2e.yml`, `win-update-survival-e2e.yml`):

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│              DAEMON & SUBPROCESS SURVIVAL INTEGRATION HARNESS               │
│                                                                             │
│ 1. Fresh Runner ──► 2. Install App ──► 3. Spawn Daemon & Worker Process     │
│                                                     │                       │
│ 4. Force-Kill Parent Main Process (No Tree Kill) ◄──┘                       │
│    (Simulate crash / unexpected SIGKILL)                                    │
│         │                                                                   │
│         ▼                                                                   │
│ 5. Assert: Daemon & Shell PIDs remain ALIVE and healthy in OS process table │
│         │                                                                   │
│         ▼                                                                   │
│ 6. Relaunch Application ──► Re-adopt existing daemon & worker handle        │
│         │                                                                   │
│         ▼                                                                   │
│ 7. Verify: Standard I/O, session state, and execution receipt integrity     │
└─────────────────────────────────────────────────────────────────────────────┘
```

- **Why CI Runners Are Essential**: Testing real installer execution (NSIS/MSI) uninstalls system registry entries and wipes app data. Disposable CI runners provide an isolated, disposable sandbox for destructive lifecycle tests.
- **Verification Criteria**:
  1. Main orchestrator process termination does not trigger cascade SIGKILL down the process tree.
  2. Workers continue executing autonomous tasks without parent pipe hangs.
  3. Reconnecting orchestrator adopts orphaned handles seamlessly.

---

### Pattern 4: Two-Stage Windows Inner-Binary & Installer Signing Rehearsal Pipeline

Code signing Windows desktop applications that package auxiliary CLI executables and native modules is prone to subtle verification failures if only the outer installer `.exe`/`.msi` is signed.

Orca’s `windows-signing-rehearsal.yml` implements a 2-stage signing pipeline:

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                 TWO-STAGE WINDOWS CODE SIGNING PIPELINE                     │
│                                                                             │
│  [ Stage 1: Build Binaries ] ──► [ Stage 2: Sign Inner PE Executables ]     │
│  (g8s.exe, helpers, DLLs)        (SignPath / Signtool SHA256 Timestamped)  │
│                                                   │                         │
│  [ Stage 4: Sign Outer Installer ] ◄── [ Stage 3: Package Installer ]      │
│  (Sign final .exe / .msi)              (Embed pre-signed inner binaries)    │
│            │                                                                │
│            ▼                                                                │
│  [ Stage 5: Signtool Verification ] ──► Validates SmartScreen & Authenticode│
└─────────────────────────────────────────────────────────────────────────────┘
```

- **Stage 1 (Inner Signing)**: Signs unpacked `.exe` and `.dll` files before packaging.
- **Stage 2 (Packaging)**: Bundles the signed binaries into NSIS/WiX installers.
- **Stage 3 (Outer Signing)**: Signs the resulting installer executable or MSI package.
- **Rehearsal Isolation**: `windows-signing-rehearsal.yml` allows developers to test the signing pipeline against test certificates on feature branches without touching production releases.

---

### Pattern 5: Matrix Sharding, Hardware Sandboxing & Dedicated Test Separation

Orca partitions tests across specialized workflows rather than running all checks in a single monolithic job:
- **Fast Unit Tests**: `unit-tests.yml` uses 8 parallel shards (`shard: [1, 2, 3, 4, 5, 6, 7, 8]`) to execute thousands of unit tests in under 3 minutes.
- **Hardware Sandboxing**: Dedicated workflows for GUI and display protocols (`linux-wayland-gpu-sandbox.yml`, `terminal-ime-e2e.yml` with headless X11 and ibus).
- **Performance Profiling**: `terminal-perf.yml` measures execution times, prompt redraw latency, and PTY backpressure.

---

## 4. Comparative Gap Analysis: Orca vs. g8s

| Dimension | `stablyai/orca` Workflows | `g8s` Current Workflows | Gap Assessment & Impact on g8s |
| :--- | :--- | :--- | :--- |
| **Concurrency Controls** | Ubiquitous across all 32 workflows. `cancel-in-progress: true` on PRs; `cancel-in-progress: false` on releases. | **Zero concurrency groups** defined in any workflow (`ci.yml`, `quality.yml`, `windows-e2e.yml`, `hygiene-guard.yml`). | **High**: Rapid pushes queue redundant runs, wasting GitHub Actions runner quota and slowing developer feedback. |
| **Path Scoping & Skip Optimization** | `code_paths` job detects changed files; skips heavy jobs on doc changes; single `verify` check satisfies branch protection. | Monolithic runs: `quality.yml` and `ci.yml` run on all PR pushes regardless of changed files. `dist-validation.yml` uses top-level `paths:` (risks hung checks). | **Medium**: Linting, security checks, and cross-platform compilation run even on markdown-only edits. |
| **Status Check Architecture** | 1 unified required status check (`verify`) per PR. | Multiple disparate checks (`Quality Gate`, `Test ubuntu-latest`, `Test macos-latest`, `Test windows-latest`, `Windows E2E Verification`). | **Medium**: Adding/removing matrix targets requires updating GitHub branch protection settings manually. |
| **Quality Gate Structure** | Fast static analysis, typechecking, and sharded unit tests split across discrete parallel jobs. | Monolithic `quality.yml` (288 lines, 15+ linters + coverage + dogfooding in a single 15-minute serial script). | **Medium**: A minor linter failure (e.g. `gocognit`) halts security scans (`govulncheck`, `gosec`) and coverage reporting. |
| **Windows Lifecycle & Survival** | Dedicated survival harnesses for process crash, application update, and restart adoption on disposable runners. | Basic `windows-e2e.yml` checks CLI command flags and service install/uninstall. | **High**: g8s supervisor/worker process isolation and orphan-kill safety are not tested under real Windows crash/kill conditions in CI. |
| **Code Signing & Packaging** | Two-stage inner-binary signing + NSIS packaging + rehearsal workflow (`windows-signing-rehearsal.yml`). | Basic packaging in `ci.yml` and `dist-validation.yml`; signing step in `release.yml` is an unverified stub. | **Medium**: Installer binaries built on Windows are not pre-signed before WiX/NSIS packaging. |
| **Test Sharding & Matrix** | 8 parallel shards for unit suites; parameterized matrix across node versions and OSes. | Dual-pass matrix in `ci.yml` (`CGO_ENABLED=0` vs `CGO_ENABLED=1 -race`), but tests run serially per OS. | **Low**: Go test suite in g8s is fast (< 30s), so test sharding is not currently a bottleneck. |

---

## 5. Recommended Patterns for g8s Adoption

We select **5 high-leverage architectural patterns** tailored for g8s's Pure-Go orchestrator stack:

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                 5 HIGH-LEVERAGE PATTERNS FOR g8s ADOPTION                   │
├─────────────────────────────────────────────────────────────────────────────┤
│ 1. Differentiated Concurrency Groups (PR cancel vs. Release serialize)      │
│ 2. Dynamic Change Scoping & Aggregate `verify` Status Gate                  │
│ 3. Subprocess Crash-Survival & Orphan Recovery Integration Harness          │
│ 4. Two-Stage Inner-Binary Pre-Signing for Windows NSIS/WiX Installers       │
│ 5. Modular Quality Gate Job Decomposition (Parallel Lint / Sec / Dogfood)   │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Pattern 1: Differentiated Concurrency Groups across all Workflows
- Add `concurrency` blocks to `ci.yml`, `quality.yml`, `windows-e2e.yml`, `hygiene-guard.yml`, and `dist-validation.yml`:
  ```yaml
  concurrency:
    group: ${{ github.workflow }}-${{ github.event.pull_request.number || github.ref }}
    cancel-in-progress: true
  ```
- Add non-cancelling serialization to `release.yml` and `dogfood.yml`:
  ```yaml
  concurrency:
    group: ${{ github.workflow }}
    cancel-in-progress: false
  ```

### Pattern 2: Dynamic Path Scoping with Fail-Closed `verify` Gate
- Introduce a lightweight `filter` job in `ci.yml` / `quality.yml` using `git diff` or `dorny/paths-filter`.
- Omit heavyweight Go linters, race detector passes, and Windows installer builds when PRs only touch documentation (`docs/**`, `*.md`).
- Introduce an aggregate `verify` job running `if: always()` that guarantees branch protection rules are satisfied deterministically.

### Pattern 3: Subprocess Crash-Survival & Supervisor Adoption Integration Test
- Create an automated test workflow `supervisor-survival-e2e.yml` running on Linux, macOS, and Windows runners:
  1. Spawns `g8s orchestrate` with long-running worker tasks.
  2. Issues an unhandled `SIGKILL` / `taskkill /F` exclusively to the supervisor PID.
  3. Verifies that child worker processes continue executing without crashing or deadlocking.
  4. Relaunches `g8s` and verifies that the new supervisor detects active heartbeats, re-adopts workers, and collects execution receipts.

### Pattern 4: Two-Stage Windows Inner-Binary Pre-Signing Pipeline
- Update Windows packaging in `.goreleaser.yaml` and `release.yml` to sign `g8s.exe` before invoking `makensis` and `candle`/`light`.
- Add a manual `workflow_dispatch` rehearsal workflow to test signing and verification without creating production GitHub releases.

### Pattern 5: Decompose Monolithic `quality.yml` into Parallel Checks
- Decompose `quality.yml` into 3 concurrent jobs:
  1. `lint` (gofumpt, staticcheck, gocognit, errcheck, ai_lint, brief_lint)
  2. `security` (gosec, govulncheck)
  3. `dogfood` (brief-issue -> brief-consume -> orchestrate loop)
- Enables fast failure isolation: a security vulnerability or dogfood regression is reported immediately even if linters are running.

---

## 6. Actionable Follow-Up Issues Roadmap

We open **3 prioritized technical debt tracking issues** for incremental implementation:

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                       ACTIONABLE TRACKING ISSUES                            │
├───────────┬─────────────────────────────────────────────────┬───────────────┤
│ Issue ID  │ Title                                           │ Priority      │
├───────────┼─────────────────────────────────────────────────┼───────────────┤
│ DEBT-56   │ CI/CD Concurrency Controls & Cancel-in-Progress │ High (P1)     │
│ (#194)    │ Optimization across all GitHub Workflows        │               │
├───────────┼─────────────────────────────────────────────────┼───────────────┤
│ DEBT-57   │ PR Dynamic Path Scoping & Fail-Closed Aggregate  │ High (P1)     │
│ (#195)    │ Status Check Gate (`verify` Job)                │               │
├───────────┼─────────────────────────────────────────────────┼───────────────┤
│ DEBT-58   │ Subprocess Crash-Survival & Supervisor Adoption │ Medium (P2)   │
│ (#196)    │ Cross-Platform E2E Integration Suite            │               │
└───────────┴─────────────────────────────────────────────────┴───────────────┘
```

### Issue 1: [DEBT-56 (#194)](https://github.com/tamld/g8s/issues/194) — CI/CD Concurrency Controls & Cancel-in-Progress
- **Target**: All `.github/workflows/*.yml` files.
- **Scope**:
  - Add PR-scoped `cancel-in-progress: true` concurrency groups to `ci.yml`, `quality.yml`, `windows-e2e.yml`, `hygiene-guard.yml`, and `dist-validation.yml`.
  - Add serialized `cancel-in-progress: false` concurrency groups to `release.yml` and `dogfood.yml`.
- **Expected Outcome**: Instant cancellation of stale PR CI runs on new commits; elimination of runner queue contention.

### Issue 2: [DEBT-57 (#195)](https://github.com/tamld/g8s/issues/195) — PR Dynamic Path Scoping & Fail-Closed Status Gate (`verify`)
- **Target**: `ci.yml`, `quality.yml`, `dist-validation.yml`.
- **Scope**:
  - Implement a fast change detection job evaluating commit diffs.
  - Bypass expensive test passes, race detectors, and linters on documentation-only changes.
  - Implement fail-closed aggregate `verify` job running `if: always()`.
- **Expected Outcome**: < 30s feedback on doc PRs while maintaining 100% branch protection reliability.

### Issue 3: [DEBT-58 (#196)](https://github.com/tamld/g8s/issues/196) — Subprocess Crash-Survival & Supervisor Adoption E2E Suite
- **Target**: `.github/workflows/crash-survival-e2e.yml`, `internal/supervisor/`, `internal/process/`.
- **Scope**:
  - Implement a disposable-runner test harness verifying worker survival when the supervisor process is abruptly terminated (`kill -9` / `taskkill /F`).
  - Assert PID mapping, heartbeat continuity, and new supervisor handle re-adoption.
- **Expected Outcome**: Continuous regression protection for g8s's Zero-Trust process isolation and lifecycle guarantees.

---

## 7. Anti-Patterns & Exclusions (What We Do NOT Adopt)

In accordance with g8s's Zero-CGO and Pure-Go architectural principles, we explicitly reject the following patterns from Orca:
1. **No Heavyweight Multi-Gigabyte Build Environments**: Orca requires Node, pnpm, Python, electron-builder, and native C++ toolchains. g8s remains strictly Pure-Go with zero runtime dependencies.
2. **No Monolithic Multi-Hour Release Pipelines**: Orca's `release-cut.yml` spans 2,141 lines with 6-hour timeouts. g8s relies on declarative, fast GoReleaser pipelines completing in < 5 minutes.
3. **No Unpinned Shell Script Dependencies**: All CI helper scripts in g8s must remain self-contained within `tools/` with strict unit self-tests (`tools/*_test.sh`).
