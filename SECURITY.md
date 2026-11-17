# Security Policy

## Supported versions

| Version | Supported |
| --- | --- |
| 0.25.x / 0.24.x | yes |
| 0.1.x | end-of-life |

## Reporting a vulnerability

Open a private security advisory via GitHub ("Security" tab -> Advisories -> New draft advisory),
or contact the maintainer directly if you cannot use advisories. Please include:

- affected version / commit,
- reproduction steps or proof-of-concept,
- expected vs actual behavior.

We aim to acknowledge reports within 72 hours.

## Hardening posture

- Pure Go, Zero-CGO: no C toolchain attack surface; SQLite via `modernc.org/sqlite`.
- Defense-in-depth containment across 3 layers (see [`docs/security/containment-matrix.md`](docs/security/containment-matrix.md) for OS-specific guarantees and degraded modes):
  - **OS Kernel & Process Containment**: macOS Seatbelt (`sandbox-exec`, `kernel_enforced`), Linux `bwrap` / Landlock LSM (`kernel_enforced`), Windows `JobObject` (`KILL_ON_JOB_CLOSE`), + ambient secret variable scrubbing (`ScrubEnv`).
  - **Ed25519 Capabilities & SQLite CAS**: single-use, TTL-bounded (`<=3600s`), path-scoped `WriteReceipt`s (`rc1:`) and `StratumCharter`s (`ed25519:v1:`) stored with `0600`/`0700` state permissions and validated before consumption and delivery (`SUPERVISOR_ACCEPTED` + `BaseSHA` CAS).
  - **Post-Run Worktree Firewall & Blind Audit**: per-attempt isolated Git worktrees (`VerifyWorktreeDiffScope`, rename checks, `fsops.VerifyWriteScope` `.git/**`/`.g8s/**` protection), fail-closed verifier/reviewer, seeded-mutant canaries, and disjoint `S3-A` blind verification.
- Reproducible offline assurance suite (no LLM credentials required): `bash tools/assurance_benchmark.sh` (`g8s eval run --category assurance --json`).
- Service units are hardened (canonical binary pinning, umask 0077, scrubbed PATH, secret-free plists).

## Scope notes

The dispatch layer spawns official platform CLIs with scrubbed environments and explicit containment flags. Credential handling inside third-party vendor CLIs follows each vendor's own security model.
