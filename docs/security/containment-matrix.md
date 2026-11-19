# Containment & Threat-Model Matrix (`v0.25.0` — 3-OS Ground Truth)

This matrix defines the falsifiable containment boundary of `g8s`: **what resources are protected when an untrusted AI worker subprocess deliberately ignores prompts, skills, and harness policy, what enforcement mechanism blocks the action, and which runtime tests prove it.**

Every row below corresponds to executable code and automated tests verified across `ubuntu-latest`, `macos-latest`, and `windows-latest` in CI, and is reproducible offline with zero external LLM credentials via [`tools/assurance_benchmark.sh`](../../tools/assurance_benchmark.sh).

---

## 1. Defense-in-Depth Architecture (Three Explicit Enforcement Tiers)

`g8s` combines three distinct enforcement layers instead of relying on prompt obedience alone:

1. **Tier 1 — OS Kernel & Process Primitives (`internal/sandbox`, `internal/worker`)**:
   - Kernel filesystem path write/read restrictions and isolated process-group / job-object containment + environment secret scrubbing applied by the `g8s` worker supervisor before spawning child CLI processes.
2. **Tier 2 — Cryptographic & SQLite WAL CAS Control Plane (`internal/signing`, `internal/receipt`, `internal/charter`, `internal/controlplane`, `cmd/g8s/deliver.go`)**:
   - Ed25519-authenticated monotone-ceiling `WriteReceipt`s (`rc1:`), Ed25519 domain-separated `StratumCharter`s (`ed25519:v1:`), POSIX `0600`/`0700` state directory & key hygiene, single-use receipt consumption (`ValidateAndConsume`), atomic SQLite WAL CAS leases, fail-closed `SUPERVISOR_ACCEPTED` pre-`deliver` gating, and BaseSHA optimistic concurrency locks.
3. **Tier 3 — Isolated Git Worktree Firewall & Epistemic Verification (`internal/worker/diff_firewall.go`, `internal/verifier`, `internal/orchestrator/verify.go`, `internal/supervisor/reviewer.go`)**:
   - Per-attempt private `git worktree` isolation, unified NUL `-z` `VerifyWorktreeDiffScope` across both claim-bound and non-claim attempts (checking both rename/copy source and destination, never exempting `.g8s` governance files, failing closed on `read_only` worktrees missing `.git`, and detecting `.gitignore` delta mutations), seeded-mutant canary detection (`VetoCanarySurvived`), fail-closed stdout error envelope inspection, and disjoint-model-family `S3-A` blind verification (`g8s blind-verify`).

---

## 2. Per-OS Containment Matrix (Hostile Worker Threat Model)

Assuming a **same-UID hostile child process** that attempts to escape worktree scope, harvest secrets, forge receipts, spoof exit code `0`, or orphan background processes:

| Threat Vector (Disobedient Child Process) | macOS (`darwin`) | Linux (`linux`) | Windows (`windows`) | Primary Verifying Tests |
| :--- | :--- | :--- | :--- | :--- |
| **Write outside assigned worktree / state dirs** | **Kernel-enforced** via XNU Seatbelt (`sandbox-exec`, `mode=kernel_enforced`) | **Kernel-enforced** via `bwrap` userns or pure-Go Landlock LSM trampoline (`mode=kernel_enforced`) | **Worktree diff firewall** (`VerifyWorktreeDiffScope` post-run; no kernel per-worktree path ACL before execution — `mode=job_object_env_scrubbed`) | `internal/sandbox/escape_*_test.go`, `as-001` |
| **Read sensitive host credentials (`~/.ssh`, `~/.aws`, `~/.config/gh`) & `f0-signing.key`** | **Kernel-enforced** read deny in Seatbelt profile (`f0-signing.key`, SSH/cloud/gh keys denied) | **Kernel-enforced** tmpfs/unmounted/Landlock read deny when `bwrap` or Landlock active (`kernel_enforced`) | **Policy + `ScrubEnv` + filesystem ACL** (no kernel per-process same-UID read ACL; secrets scrubbed from child `Env`) | `internal/sandbox/escape_*_test.go`, `internal/signing/ed25519_test.go` |
| **Ambient parent env API keys (`OPENAI_API_KEY`, `GITHUB_TOKEN`, etc.)** | **Enforced** (ambient secret scrubbing with provider-scoped allowlists via `ScrubEnv`) | **Enforced** (ambient secret scrubbing with provider-scoped allowlists via `ScrubEnv`) | **Enforced** (ambient secret scrubbing with provider-scoped allowlists via `ScrubEnv`) | `internal/sandbox/env.go`, `as-001` |
| **Runaway grandchildren on task timeout or cancel** | **Enforced** (`Setpgid` `-pgid` kill + `G8S_SANDBOX_ID` sweep + `sandbox.Cleanup`) | **Enforced** (`-pgid` kill + subreaper + `/proc/<pid>/environ` `G8S_SANDBOX_ID` sweep + `Pdeathsig` on Landlock) | **Enforced** (`JobObject` `KILL_ON_JOB_CLOSE` + `TerminateProcess`) | `TestFaultInjectionE2E_HardTimeoutKillsDetachedProcessTree` |
| **Orphan grandchildren if `g8s worker` itself gets `SIGKILL` (external crash)** | **Best-effort / cleanup required**: XNU lacks Linux `PR_SET_PDEATHSIG`; reap via `g8s cleanup --target ghost-process` (`pdeath` signal N/A on macOS) | **Kernel-enforced under Landlock** (`Pdeathsig: SIGKILL`); **cleared across `/usr/bin/bwrap`** on AppArmor domain transition | **Kernel-enforced** via Win32 `JobObject` `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` handle close | `cmd/g8s/fault_injection_e2e_test.go` |
| **Tampered SQLite receipt/charter or same-UID forgery without `f0-signing.key`** | **Cryptographically rejected** (Ed25519 `rc1:` + `ed25519:v1:`; `0600` key, `0700` state dir) | **Cryptographically rejected** (Ed25519 `rc1:` + `ed25519:v1:`; `0600` key, `0700` state dir) | **Cryptographically rejected** (Ed25519 `rc1:` + `ed25519:v1:` verified before consume/verify) | `internal/receipt`, `internal/charter`, `as-005` |
| **Receipt replay (`1-1` violation) or expired TTL** | **SQLite atomic CAS rejected** (`ValidateAndConsume` + monotone hex ceiling check) | **SQLite atomic CAS rejected** (`ValidateAndConsume` + monotone hex ceiling check) | **SQLite atomic CAS rejected** (`ValidateAndConsume` + monotone hex ceiling check) | `internal/receipt/receipt_test.go`, `as-005` |
| **Worktree escape via `g8s edit`, symlinks, `..`, `.git/**`, `.gitignore` delta, or `git mv`** | **Enforced** (`fsops.VerifyWriteScope` + unified NUL `-z` diff firewall across claim & non-claim paths + `deliver`) | **Enforced** (`fsops.VerifyWriteScope` + unified NUL `-z` diff firewall across claim & non-claim paths + `deliver`) | **Enforced** (`fsops.VerifyWriteScope` + unified NUL `-z` diff firewall across claim & non-claim paths + `deliver`) | `internal/fsops`, `internal/worker/diff_firewall_test.go`, `cmd/g8s/deliver_verified_test.go`, `as-001` |
| **Exit code `0` with JSON error envelope or vacuous tests (`False PASS`)** | **Fail-closed** (`StdoutEnvelopeVerifier` + `RealReviewer` + `RunCanaryCheck` + `SUPERVISOR_ACCEPTED` deliver gate) | **Fail-closed** (`StdoutEnvelopeVerifier` + `RealReviewer` + `RunCanaryCheck` + `SUPERVISOR_ACCEPTED` deliver gate) | **Fail-closed** (`StdoutEnvelopeVerifier` + `RealReviewer` + `RunCanaryCheck` + `SUPERVISOR_ACCEPTED` deliver gate) | `TestFaultInjectionE2E_StdoutErrorEnvelopeRC0FlipsFailed`, `as-002` |
| **Untrusted/failing provider targeting P0 security path (`Lane S`)** | **Fail-closed** (`submit` auto-enforces `premise-check` & `conformance` unless `--override-reason` audited) | **Fail-closed** (`submit` auto-enforces `premise-check` & `conformance` unless `--override-reason` audited) | **Fail-closed** (`submit` auto-enforces `premise-check` & `conformance` unless `--override-reason` audited) | `TestSubmit_LaneS_AutoEscalatesPremiseAndConformance` |

---

## 3. Explicit Non-Guarantees & Degraded Fallback Modes

Transparency about what `g8s` **does not** claim:

1. **Same-UID threat boundary without kernel sandboxing (`mode=policy_only` or Windows pre-run FS)**:
   - On Windows (`mode=job_object_env_scrubbed`), `g8s` enforces process-tree termination (`JobObject`), scrubbed env vars, and post-run Git worktree/diff validation (`VerifyWorktreeDiffScope` + `fsops`), **not** kernel-level same-UID filesystem read/write ACLs prior to child process exit. Consequently, post-run `git status` inside the worktree does not observe files written outside the worktree on Windows or under `mode=policy_only`.
   - **Windows Deployment Hardening Recommendation (`#939`)**: When running untrusted or hostile workers in multi-user or shared Windows environments, run `g8s worker` under a dedicated least-privilege Windows worker account, an isolated Windows/Hyper-V container, or WSL2 (enabling Linux `bwrap`/Landlock `mode=kernel_enforced`); native Win32 AppContainer / Restricted Token confinement is tracked for future post-`v0.25` hardening.
   - If a Linux host disables both `bwrap` user namespaces and kernel Landlock ABI (`< 5.13`), or if an operator explicitly passes `--no-sandbox` / `G8S_NO_SANDBOX=1` on a non-production dev run, `sandbox.Prepare` degrades to `ModePolicyOnly` (`mode=policy_only`), which `g8s doctor --json` surfaces immediately under the `os_sandbox` capability check. Pass `--require-kernel-sandbox` to `g8s worker` (or set `G8S_REQUIRE_KERNEL_SANDBOX=1`) to **fail closed** (`E_SANDBOX_KERNEL_ENFORCED_REQUIRED`) whenever `mode=kernel_enforced` cannot be achieved, and pass `--require-kernel-enforced` to `tools/assurance_benchmark.sh` to enforce the same check on assurance benchmarks.
2. **External host CLI credential stores**:
   - Official vendor CLI binaries (`agy`, `claude`, `codex`, `cursor`) manage their own OAuth/keychain stores; `g8s` performs ambient secret scrubbing with provider-scoped allowlists (`ScrubEnv`, stripping unrelated provider keys such as `OPENAI_API_KEY`/`ANTHROPIC_API_KEY` on `9router` or other non-matching providers) before spawning workers, and protects `~/.ssh`, `~/.aws`, `~/.config/gh`, and `f0-signing.key` via Seatbelt/`bwrap`/Landlock where kernel sandboxing is active.
3. **Host root / UID-0 adversary**:
   - `g8s` assumes an unprivileged user workspace without a hostile kernel-level root attacker sharing the host.

---

## 4. Reproducing Assurance Evidence Offline (Zero API Keys Required)

External reviewers and auditors can verify all 5 assurance categories (`escape-containment`, `seeded-mutant-anti-false-pass`, `crash-lease-recovery-rto`, `control-plane-cas-overhead`, and `audit-trail-cryptographic-completeness`) locally in under 5 seconds with zero external API keys:

```bash
# Human-readable Markdown scorecard + measured metrics
bash tools/assurance_benchmark.sh

# Machine-readable JSON report (schema: g8s.assurance-benchmark/v1)
bash tools/assurance_benchmark.sh --json

# Direct Go CLI probe runner invocation
g8s eval run --category assurance --json

# Live host OS sandbox capability & health check
g8s doctor --json
```
