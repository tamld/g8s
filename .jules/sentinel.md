## 2025-02-14 - SQLite Connection String Injection via File Paths
**Vulnerability:** SQLite file URIs were constructed using `fmt.Sprintf("file:%s?...", dbPath)` without escaping the `dbPath`.
**Learning:** `modernc.org/sqlite` parses query parameters (like `?_pragma=...`) from the file URI. If a user-provided or dynamically generated file path contains a `?`, it can inject arbitrary SQLite pragma or connection options, potentially leading to security bypasses or unintended database configurations.
**Prevention:** Always use `pathutil.SQLiteURI(dbPath, queryParams)` which constructs canonical RFC-8089 file URIs while escaping query-delimiter characters (`?`, `#`) in path segments to prevent parameter injection across Windows and POSIX.

## 2025-02-14 - Slowloris Vulnerability via Missing ReadHeaderTimeout
**Vulnerability:** Go's `http.Server` does not configure `ReadHeaderTimeout` by default. When the application sets `ReadTimeout`, `WriteTimeout`, and `IdleTimeout` but misses `ReadHeaderTimeout`, the server is vulnerable to Slowloris resource-exhaustion DoS attacks.
**Learning:** `http.Server` in Go requires an explicit `ReadHeaderTimeout` because `ReadTimeout` applies to the entire request body, and a malicious client can open a connection and drip-feed headers indefinitely, exhausting the connection pool.
**Prevention:** Always set `ReadHeaderTimeout` alongside other timeouts in `http.Server` configurations.
## 2025-02-14 - Git Command Option Injection via exec.Command
**Vulnerability:** Calls to `exec.Command("git", ...)` appended paths or branch names without explicitly indicating the end of options using `--`. If an untrusted path or branch name starts with `-`, Git parses it as an option, which can lead to command option injection.
**Learning:** Command-line interfaces like `git` often parse any argument starting with `-` as an option unless `--` is explicitly used. This is especially risky when injecting uncontrolled or dynamic paths.
**Prevention:** When invoking command-line tools that accept options using Go's `exec.Command`, always prepend `--` immediately before variable arguments (like paths or branches) to prevent them from being parsed as flags.
## 2026-10-13 - Inconsistent Go Toolchain Versions Across CI Workflows
**Vulnerability:** The project bumped `go.mod` to Go 1.26.9, but GitHub Actions workflows still hardcoded `go-version: '1.26'` (which resolved to an older cached `1.26.8`). This caused a mismatch where the Go toolchain refused to build or test the code because the runner's Go version (1.26.8) was older than the module requirement (1.26.9), causing widespread CI failures.
**Learning:** When bumping the Go version in `go.mod` to address standard library vulnerabilities, you must also explicitly bump the `go-version` parameters in all GitHub Actions workflow files (`.github/workflows/*.yml`) using `actions/setup-go` to ensure the runners install the matching toolchain.
**Prevention:** Always `grep -rn "go-version" .github/workflows/` when updating `go.mod` to synchronize the runner environments with the new module requirement.
## 2026-10-13 - CI Version Sync Checks Discarding Patch Versions
**Vulnerability:** A CI job (`Version Sync Validation`) in `.github/workflows/version-sync.yml` intentionally truncated `go.mod` versions to major/minor components (e.g., `1.26`) before comparing them against workflow `go-version` arguments. This incorrectly normalized the comparison when workflows were updated to exact patch versions (like `1.26.9`), leading to CI failures due to false positive version mismatches.
**Learning:** `actions/setup-go` resolves minor versions (like `1.26`) to older cached builds (e.g. `1.26.8`). To enforce exact Go compiler versions (e.g. `1.26.9`), you must pin patch versions in workflows *and* ensure validation scripts perform exact string comparisons rather than truncating them with `cut -d. -f1,2`.
**Prevention:** Remove custom version normalizations in CI/CD guardrails to support strict patch-version pinning.
