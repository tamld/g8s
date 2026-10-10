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
## $(date +%Y-%m-%d) - Bump Go Version to Fix Vulnerabilities
**Vulnerability:** The project was using Go 1.26.0, which contains vulnerabilities in `net/http` (GO-2026-6610, GO-2026-6605, GO-2026-6603), `mime/multipart` (GO-2026-6608), and `crypto/tls` (GO-2026-6607). These were detected by the CI Quality Gate scanner.
**Learning:** Outdated Go patch versions often contain critical standard library vulnerabilities.
**Prevention:** Regularly bump the Go patch version in `go.mod` (e.g., from 1.26.0 to 1.26.9) to incorporate standard library security fixes.
## $(date +%Y-%m-%d) - Bump Go Version in GitHub Actions Workflows
**Vulnerability:** After bumping the Go version in `go.mod` to 1.26.9, GitHub Actions CI failed because the `actions/setup-go@v7` step in multiple workflows was pinned to `1.26` (which resolved to `1.26.8`), causing a mismatch.
**Learning:** When bumping the Go version in `go.mod`, it is crucial to also update the Go version specified in all GitHub Actions workflows (`.github/workflows/*.yml`) to match.
**Prevention:** Use a search-and-replace command across the `.github/workflows/` directory to update `go-version` parameters whenever bumping the project's Go version.
## $(date +%Y-%m-%d) - Version Sync Workflow Normalization Flaw
**Learning:** The version sync check uses the format `GO_MOD_MAJOR_MINOR=$(echo "$GO_MOD_VERSION" | cut -d. -f1,2)`. This logic assumed that `go.mod` files always specify only the MAJOR.MINOR version (e.g., `1.26`). However, if `go.mod` is bumped to explicitly require a patch version (e.g., `go 1.26.9`), `cut -d. -f1,2` truncates the version to `1.26`, leading to an artificial version mismatch failure when compared against `WORKFLOW_VERSION` (which correctly resolves to `1.26.9`).
**Action:** Removed the major/minor truncation logic in `.github/workflows/version-sync.yml` to directly compare the full `GO_MOD_VERSION` against the `WORKFLOW_VERSION`.
## $(date +%Y-%m-%d) - Go Version Documentation Contract Sync
**Learning:** The project implements a Documentation ↔ Code Contract Gate (`ci_doc_contract_check.sh`) which ensures `go.mod`'s Go version strictly matches SSoT documentation (`ARCHITECTURE_ROADMAP.md`, `constitution.md`, `manifest.json`, `README.md`, `README.vi.md`). Bumping the version in `go.mod` without updating these files causes a CI Quality Gate failure.
**Action:** Used `sed` to recursively replace references to the old Go version with the newly bumped patch version across the required documentation and specification files.
