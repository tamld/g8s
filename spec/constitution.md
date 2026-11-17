# g8s Project Constitution (Spec Kit Framework)

> **Governing Authority**: TamLD (`github.com/tamld/g8s`)  
> **Status**: Active & Mandatory  
> **Standard**: Spec Kit Project Constitution Model  

---

## 1. Foundational Axioms

1. **Axiom of Two-Tier Governance**:
   - High-tier reasoning models (**Brain**: sovereign/high-reasoning tier governed by [`.g8s/agent-models.yml`](../.g8s/agent-models.yml)) hold exclusive authority over system architecture, git commits, permanent state promotion, and write receipt issuance.
   - Bounded execution models and combo pools (**Worker**: model/combo fleet governed by [`.g8s/agent-models.yml`](../.g8s/agent-models.yml), including `9router` NIM combos `fast-agent`, `deep-coding`, and `reasoning-high`, Gemini Flash, Claude Haiku, `opencode`, `codex`, `cursor`, `agy`, and `ollama`) execute strictly inside bounded sandboxes with explicit role contracts and time-limited receipts.

2. **Axiom of Zero-Trust Capability (Capability Receipts)**:
   - No worker process may mutate the filesystem unless presented with an Ed25519-authenticated (`rc1:`), time-limited ($\le 3600s$), path-scoped (`allowed_paths` glob) **Write Receipt** (and, in federated `S0..S2` delegations, Ed25519-signed `StratumCharter`s tagged `ed25519:v1:`).
   - Receipts are strictly single-use (`one-time use`) and are invalidated atomically upon consumption.

3. **Axiom of Pure Go & Zero-CGO (Portability Standard)**:
   - The entire `g8s` codebase MUST compile with `CGO_ENABLED=0`.
   - SQLite is driven exclusively via pure-Go implementations (`modernc.org/sqlite`).
   - Binaries must have zero dynamic C library dependencies, enabling 100% static compilation across macOS (Darwin arm64/amd64), Linux (amd64/arm64), and Windows (amd64).

4. **Axiom of Process & State Containment**:
   - Every worker invocation is isolated with ambient secret scrubbing (`ScrubEnv`) and OS-specific containment (`sandbox-exec` Seatbelt `kernel_enforced` on macOS, `bwrap`/Landlock LSM `kernel_enforced` on Linux, and Win32 `JobObject` `KILL_ON_JOB_CLOSE` + env scrubbing on Windows; explicit degraded `policy_only` reporting via `g8s doctor --json` when kernel primitives are unavailable) inside killable process groups (`Setpgid` on Unix / `CREATE_NEW_PROCESS_GROUP` on Windows).
   - All state databases, signing keys, log files, and receipt artifacts must adhere to POSIX `0600` file permissions and `0700` directory permissions.
   - Prompts containing potential confidential material are redacted into SHA-256 hashes upon task completion.

5. **Axiom of the Self-Describing Executable (CLI as Living Documentation)**:
   - The CLI binary and its runtime schemas are the **Single Source of Truth (SSoT)**.
   - An AI agent or human MUST be able to discover 100% of capabilities, flags, roles, permissions, exit codes, and JSON schemas directly via `--help`, `--json`, and subcommands without reading static markdown manuals.
   - All CLI flags must declare explicit types, default values, and valid enums. Every error must return actionable machine-parseable exit codes and diagnostic remediation hints.

---

## 2. Coding & Architecture Standards

* **Language**: Go 1.26.11 (Zero-CGO, Pure-Go).
* **CLI Architecture**: Standard library `flag.FlagSet` for command routing; pure-Go zero-dependency configuration.
* **Testing Standard**: 100% boundary testing for security filters, race condition validation (`go test -race ./...`), mock clock testing for TTL expiry.
* **Licensing**: All files are distributed under the MIT License.

---

## 3. Platform Containment & Threat-Model Matrix

Because OS sandboxing capabilities differ across kernels, `g8s` publishes the exact falsifiable boundary separating kernel-enforced controls, cryptographic/SQLite-CAS controls, and post-run Git diff firewall controls across macOS, Linux, and Windows — alongside explicit degraded-mode reporting — in [`docs/security/containment-matrix.md`](../docs/security/containment-matrix.md) (`bash tools/assurance_benchmark.sh`).
