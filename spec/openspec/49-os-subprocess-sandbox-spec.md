# OpenSpec DELTA-49: OS-Level Subprocess Containment, Secret Scrubbing & Escape Verification

* **Specification ID**: `DELTA-49`
* **Status**: `APPLIED` (Status: implemented)
* **Implementation Status**: `IMPLEMENTED`
* **Target Packages**: `internal/sandbox`, `internal/worker`, `internal/provider`
* **Milestone**: `M6 (Safety Hardening)`
* **Release**: `v0.25.0`

---

## 1. Problem Statement

Prior to `DELTA-49`, worker subprocess isolation for external CLI agents depended on policy flags, `Setpgid` process-group cleanup, Windows Job Objects, and post-run `git status` diff inspection (`internal/worker/diff_firewall.go`). Under an adversarial or malfunctioning child process running as the same OS UID:

1. Arbitrary bash/python calls could write outside `worktreeDir` (for example into the main repository checkout) or read credential stores (`~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.kube`, `~/.config/g8s/providers.json`, `<stateDir>/f0-signing.key`).
2. `read_only` tasks dispatched to external CLI adapters could still receive permissive write-auto-approve flags (`--yolo`).
3. Ambient host environment secrets (`AWS_*`, `GITHUB_TOKEN`, unrelated `*_API_KEY` / `*_TOKEN` / `*_SECRET` variables) leaked into `os.Environ()`.
4. Task results lacked a truthful `containment_mode` field distinguishing OS kernel enforcement from JobObject/env-scrubbed containment and policy-only fallback.

---

## 2. Normative Requirements

### R1 — macOS Seatbelt Per-Attempt Kernel Sandbox (`darwin`)

On macOS (`darwin`), unless `NoSandbox` is set, worker subprocesses MUST launch under `sandbox-exec -p <profile> -- <argv...>`:
- Default deny all `file-write*` except device pseudo-files (`/dev/null`, `/dev/zero`, `/dev/tty`, `/dev/dtracehelper`, `/dev/fd/`), `runDir`, per-attempt isolated `TMPDIR`, `GOCACHE`, `ExtraWriteDirs`, and — ONLY when `permission == "workspace_write"` — canonicalized `WorktreeDir` plus resolved `.git` gitdir (`ResolveGitWorktreeAdminDir`).
- Deny `file-read*` on sensitive credential directories (`~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.kube`, `~/.docker`, `~/.config/gh`, `~/.config/gcloud`) and credential files (`~/.netrc`, `~/.npmrc`, `~/.pypirc`, `~/.git-credentials`, `~/.config/g8s/providers.json`, `~/.config/g8s/.env`, `<stateDir>/f0-signing.key`, `SSH_AUTH_SOCK`). `<stateDir>/f0-signing.pub` and `<stateDir>/receipts.db` remain readable.
- Canonicalize all paths with `CanonicalPath` (`filepath.EvalSymlinks` on longest existing prefix) so `/tmp -> /private/tmp` and `/var -> /private/var` match XNU Seatbelt rules.
- Sweep surviving `G8S_SANDBOX_ID=<id>` processes on cleanup.

### R2 — Linux `bwrap` / Landlock LSM + Subreaper Containment (`linux`)

On Linux (`linux`), unless `NoSandbox` is set:
- Probe live `bwrap --ro-bind / / --dev /dev --proc /proc --tmpfs /tmp -- /bin/true` once; when supported, execute inside `bwrap --die-with-parent --ro-bind / / --dev /dev --proc /proc --tmpfs /tmp` with `--bind` strictly for writable directories, `--tmpfs` masking existing credential directories, and `--ro-bind /dev/null <file>` masking existing credential files.
- When `bwrap` is unavailable or blocked (for example AppArmor userns restriction), fall back fail-closed to a pure-Go Landlock LSM self-executable trampoline (`G8S_INTERNAL_SANDBOX_EXEC=1`, ABI v1+, `PR_SET_NO_NEW_PRIVS` + `SYS_LANDLOCK_RESTRICT_SELF` across all OS threads via `syscall.AllThreadsSyscall`) paired with `PR_SET_CHILD_SUBREAPER` and `PR_SET_PDEATHSIG=SIGKILL` to reap detached `setsid()` grandchildren.

### R3 — Cross-Platform Environment Scrubbing & `read_only` `--yolo` Suppression

- `ScrubEnv` strips `SSH_AUTH_SOCK`, `SSH_AGENT_PID`, `GPG_AGENT_INFO`, `AWS_*`, `GITHUB_TOKEN`, `GH_TOKEN`, `GH_CONFIG_DIR`, `GITLAB_TOKEN`, `NPM_TOKEN`, `PYPI_API_TOKEN`, `DOCKER_AUTH_CONFIG`, `DOCKER_CONFIG`, `KUBECONFIG`, `AZURE_CLIENT_SECRET`, `GOOGLE_APPLICATION_CREDENTIALS`, `GCP_SERVICE_ACCOUNT_KEY`, `G8S_F0_*`, `G8S_SERVE_TOKEN`, `G8S_API_TOKEN`, `G8S_SIGNING_KEY`, `G8S_STRATUM_TOKEN`, and unlisted `*_API_KEY`/`*_SECRET`/`*_SECRET_KEY`/`*_TOKEN`/`*_PASSWORD`/`*_PRIVATE_KEY`/`*_CREDENTIALS` keys unless explicitly allowlisted for `ProviderName` (`ProviderAllowedEnvKeys`) or passed via `TaskEnv`.
- Sets isolated per-attempt `TMPDIR`/`TMP`/`TEMP`, `GOCACHE`, and `G8S_SANDBOX_ID`.
- External CLI adapters and `worker.Supervisor` suppress `--yolo` / `--dangerously-skip-permissions` when `permission` is read-only (`read_only`, `automation_read`, `supplied_text`).

### R4 — Truthful `containment_mode` & `sandbox_backend` Telemetry

- `ContainmentMode` reports:
  - `kernel_enforced` (`seatbelt`, `bwrap`, or `landlock`)
  - `job_object_env_scrubbed` (`windows_job_object`)
  - `policy_only` (`NoSandbox: true` or non-sandboxed fallback/fake runners)
- `Supervisor.collect` records `containment_mode` (and `sandbox_backend` when non-empty) on task result envelopes after `validateContract` and in attempt trace payloads.

### R5 — 3-OS Adversarial Subprocess Escape Suite

Real subprocess tests on Linux, macOS, and Windows verify out-of-worktree write rejection, `read_only` worktree mutation rejection, credential and `f0-signing.key` read blocking, cross-platform ambient secret scrubbing, and detached grandchild termination.

---

## 3. Verification Scenarios

#### Scenario: Kernel filesystem write containment blocks out-of-worktree and read-only mutations
<!-- tests: TestSandboxBlocksOutOfWorktreeWrite, TestSandboxBlocksReadOnlyWorktreeMutation -->
- **GIVEN** A subprocess executed via `sandbox.RunContained` under `workspace_write` or `read_only` permission
- **WHEN** the child attempts to create or modify a file outside `WorktreeDir` (or inside `WorktreeDir` while `read_only`)
- **THEN** the kernel sandbox (`seatbelt` on macOS, `bwrap`/`landlock` on Linux) denies the write with an OS permission error and leaves target files untouched

#### Scenario: Credential directory and F0 private signing key read protection
<!-- tests: TestSandboxBlocksCredentialAndF0KeyReads -->
- **GIVEN** Simulated sensitive files in `$HOME/.ssh/id_test`, `$HOME/.aws/credentials`, `<stateDir>/f0-signing.key`, plus `<stateDir>/f0-signing.pub`
- **WHEN** a sandboxed child attempts to read all four files
- **THEN** `f0-signing.pub` remains readable on all platforms while reads of `$HOME/.ssh`, `$HOME/.aws`, and `f0-signing.key` are blocked by the OS kernel sandbox on macOS and Linux

#### Scenario: Cross-platform ambient secret scrubbing and provider-scoped allowlist
<!-- tests: TestSandboxScrubsAmbientSecretsCrossPlatform -->
- **GIVEN** Ambient host environment variables containing `AWS_SECRET_ACCESS_KEY`, `GITHUB_TOKEN`, `G8S_F0_PRIVATE_SECRET`, `G8S_SERVE_TOKEN`, `UNRELATED_API_KEY`, and `OPENAI_API_KEY`
- **WHEN** `ScrubEnv` and `RunContained` launch a subprocess configured for provider `codex`
- **THEN** host secrets are absent from child `env`, `OPENAI_API_KEY` is retained for `codex`, and isolated `TMPDIR`/`TEMP`/`GOCACHE`/`G8S_SANDBOX_ID` are set

#### Scenario: Detached grandchild termination and truthful worker containment telemetry
<!-- tests: TestSandboxTerminatesDetachedGrandchild, TestWorkerRecordsContainmentModeAndSuppressesReadOnlyYolo -->
- **GIVEN** A contained subprocess spawning a detached background grandchild and `Supervisor.RunOnce` driving real `processRunner` attempts plus `read_only` CLI argv builders
- **WHEN** the attempt ends or hits cleanup and `Supervisor.collect` finishes
- **THEN** detached grandchildren are terminated, `task.Result` records truthful `containment_mode` (`kernel_enforced` | `job_object_env_scrubbed` | `policy_only`), and `read_only` argv never passes `--yolo`
