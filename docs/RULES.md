# g8s Operational Hard Rules & Governance Standards

> **Authority**: Spec Kit Project Constitution (`spec/constitution.md`)  
> **Target Audience**: All Human Contributors & Autonomous AI Agents  

---

## 1. The 8 Non-Negotiable Engineering Rules (The "Iron Laws")

### Rule 1 — Pure-Go Invariant (Zero-CGO)
* **Standard**: The codebase MUST compile cleanly with `CGO_ENABLED=0` across all target architectures (`darwin/arm64`, `darwin/amd64`, `linux/amd64`, `linux/arm64`, `windows/amd64`).
* **Enforcement**: Use `modernc.org/sqlite` exclusively. Never import C-based SQLite libraries (`github.com/mattn/go-sqlite3`).
* **CI Gate**: Multi-OS CI matrix runs `CGO_ENABLED=0 go test -race ./...`, backed by offline Semgrep AST policy `g8s-zero-cgo-import` (`tools/ci_semgrep_check.sh`).

### Rule 2 — The Single-Use Capability Receipt Mandate
* **Standard**: No worker process may modify files unless accompanied by a valid, time-limited ($\le 3600s$), path-scoped (`allowed_paths` glob) Write Receipt.
* **Enforcement**: Receipt consumption is atomic (`consumed = 1` inside an `EXCLUSIVE` transaction). Any subsequent attempt to reuse a consumed or expired receipt fails immediately.

### Rule 3 — Process Group Isolation & Zero-Orphan Cleanup
* **Standard**: Every worker CLI command must be spawned inside its own dedicated Process Group (`Setpgid: true` on Unix / `CREATE_NEW_PROCESS_GROUP` on Windows).
* **Enforcement**: On timeout or cancellation, `g8s` signals the entire process group leader (`syscall.Kill(-pgid, syscall.SIGTERM)`), preventing rogue child subprocesses from continuing in the background.

### Rule 4 — Zero-Leakage & Data Hygiene Standard
* **Standard**: Raw prompts containing potentially confidential user code or credentials must be deleted and replaced with SHA-256 `prompt_hash` upon reaching a terminal state (`SUCCEEDED`, `FAILED`, `CANCELLED`).
* **Enforcement**: SQLite task registry columns strictly store `prompt_hash`. Local state databases and log files MUST have POSIX permissions `0600` (`0o600`), and state directories MUST be `0700` (`0o700`) — enforced statically by `Gosec` (`G306`) and `Semgrep` (`g8s-writefile-perm-0600`).

### Rule 5 — Deterministic Time & Clock Injection
* **Standard**: Code that performs TTL comparisons, lease expirations, or timeout checks MUST NOT call `time.Now()` directly in business logic.
* **Enforcement**: Inject a `clock func() time.Time` dependency to enable sub-millisecond deterministic mock clock testing without real-time `time.Sleep` delays.

### Rule 6 — Post-Run Mutation Scan Standard
* **Standard**: For all `read_only` worker executions, `stdout` and `stderr` MUST be scanned after completion against `READ_ONLY_VIOLATION_PATTERNS`.
* **Enforcement**: If a worker output reveals side-effect mutations (`git commit`, `wiki.py write`, file creations), `g8s` automatically overrides the exit code to `3` (`READ_ONLY_CONTRACT_EXIT`) and logs a contract violation event.

### Rule 7 — Language & Documentation Purity
* **Standard**: 100% of codebase artifacts (source code, tests, docstrings, schema files, documentation, commit messages, and PRs) must be written in standard, professional English with zero non-ASCII diacritics.

### Rule 8 — The Self-Describing Executable (SDE) & Zero Silent Error Swallowing Standard
* **Standard**: The CLI binary itself MUST be the primary living documentation, and runtime/CI error paths must never silently swallow failures or leave AI agents guessing.
* **Enforcement**:
  1. An AI agent running `g8s <cmd> --help` or `g8s <cmd> --json` must receive 100% sufficient metadata to execute the command without reading external markdown files. Exit codes strictly map to unambiguous failure modes (`0`=Success, `1`=System Error, `2`=CLI Argument Syntax, `3`=Read-Only Violation, `4`=Unauthorized/Expired Receipt, `5`=Timeout/Process Killed).
  2. Production code under `cmd/` and `internal/` MUST NOT silently discard filesystem or state errors via `_ = os.WriteFile(...)`, `_ = os.Rename(...)`, or `_, _ = os.OpenFile(...)` (enforced by `errcheck` and Semgrep rule `g8s-no-silent-fs-error-swallow`). Every failure path must either return a wrapped error (`fmt.Errorf("context: %w", err)`) or emit a structured diagnostic to `os.Stderr` (`[warn] <pkg>: <action>: <err>`).
  3. Selector queries (`--select`) and static analysis gates must emit actionable hints (such as available JSON keys or `Hint:` remediation guidance) on failure.
