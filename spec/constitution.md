# g8s Project Constitution (Spec Kit Framework)

> **Governing Authority**: TamLD (`github.com/tamld/g8s`)  
> **Status**: Active & Mandatory  
> **Standard**: Spec Kit Project Constitution Model  

---

## 1. Foundational Axioms

1. **Axiom of Two-Tier Governance**:
   - High-tier reasoning models (**Brain**: Claude 3.7 Sonnet / Opus, GPT-4o, DeepSeek R1) hold exclusive authority over system architecture, git commits, permanent state promotion, and write receipt issuance.
   - Low-tier mechanical models (**Worker**: Gemini Flash, Claude Haiku, Ollama) execute strictly inside bounded sandboxes with explicit role contracts and time-limited receipts.

2. **Axiom of Zero-Trust Capability (Capability Receipts)**:
   - No worker process may mutate the filesystem unless presented with a cryptographically tracked, time-limited ($\le 3600s$), path-scoped (`allowed_paths` glob) **Write Receipt**.
   - Receipts are strictly single-use (`one-time use`) and are invalidated atomically upon consumption.

3. **Axiom of Pure Go & Zero-CGO (Portability Standard)**:
   - The entire `g8s` codebase MUST compile with `CGO_ENABLED=0`.
   - SQLite is driven exclusively via pure-Go implementations (`modernc.org/sqlite`).
   - Binaries must have zero dynamic C library dependencies, enabling 100% static compilation across macOS (Darwin arm64/amd64), Linux (amd64/arm64), and Windows (amd64).

4. **Axiom of Process & State Containment**:
   - Every worker invocation is isolated inside its own OS process group (`Setpgid: true` on Unix / `CREATE_NEW_PROCESS_GROUP` on Windows) to guarantee complete, zero-orphan process termination upon timeout or cancellation.
   - All state databases, log files, and receipt artifacts must adhere to POSIX `0600` file permissions and `0700` directory permissions.
   - Prompts containing potential confidential material are redacted into SHA-256 hashes upon task completion.

5. **Axiom of the Self-Describing Executable (CLI as Living Documentation)**:
   - The CLI binary and its runtime schemas are the **Single Source of Truth (SSoT)**.
   - An AI agent or human MUST be able to discover 100% of capabilities, flags, roles, permissions, exit codes, and JSON schemas directly via `--help`, `--json`, and subcommands without reading static markdown manuals.
   - All CLI flags must declare explicit types, default values, and valid enums. Every error must return actionable machine-parseable exit codes and diagnostic remediation hints.

---

## 2. Coding & Architecture Standards

* **Language**: Go 1.22+.
* **CLI Library**: `spf13/cobra` for command routing; `spf13/viper` for configuration.
* **Testing Standard**: 100% boundary testing for security filters, race condition validation (`go test -race ./...`), mock clock testing for TTL expiry.
* **Licensing**: All files are distributed under the MIT License.
