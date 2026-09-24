## 2025-02-14 - SQLite Connection String Injection via File Paths
**Vulnerability:** SQLite file URIs were constructed using `fmt.Sprintf("file:%s?...", dbPath)` without escaping the `dbPath`.
**Learning:** `modernc.org/sqlite` parses query parameters (like `?_pragma=...`) from the file URI. If a user-provided or dynamically generated file path contains a `?`, it can inject arbitrary SQLite pragma or connection options, potentially leading to security bypasses or unintended database configurations.
**Prevention:** Always wrap variable path segments in `url.PathEscape()` (from `net/url`) before embedding them in a SQLite file URI string.

## 2025-02-14 - Slowloris Vulnerability via Missing ReadHeaderTimeout
**Vulnerability:** Go's `http.Server` does not configure `ReadHeaderTimeout` by default. When the application sets `ReadTimeout`, `WriteTimeout`, and `IdleTimeout` but misses `ReadHeaderTimeout`, the server is vulnerable to Slowloris resource-exhaustion DoS attacks.
**Learning:** `http.Server` in Go requires an explicit `ReadHeaderTimeout` because `ReadTimeout` applies to the entire request body, and a malicious client can open a connection and drip-feed headers indefinitely, exhausting the connection pool.
**Prevention:** Always set `ReadHeaderTimeout` alongside other timeouts in `http.Server` configurations.
## 2025-02-14 - Git Command Option Injection via exec.Command
**Vulnerability:** Calls to `exec.Command("git", ...)` appended paths or branch names without explicitly indicating the end of options using `--`. If an untrusted path or branch name starts with `-`, Git parses it as an option, which can lead to command option injection.
**Learning:** Command-line interfaces like `git` often parse any argument starting with `-` as an option unless `--` is explicitly used. This is especially risky when injecting uncontrolled or dynamic paths.
**Prevention:** When invoking command-line tools that accept options using Go's `exec.Command`, always prepend `--` immediately before variable arguments (like paths or branches) to prevent them from being parsed as flags.

## 2025-02-14 - git rev-parse Double Dash Interpretation
**Vulnerability:** Attempting to prevent command option injection by adding `--` to `git rev-parse` (`git rev-parse -- <ref>`) causes a functional regression.
**Learning:** `git rev-parse` uses `--` to explicitly indicate that the following argument is a file path, not a revision. Passing `--` before a reference causes it to output the `--` literal and treat the reference as a file, completely breaking hash resolution. To verify a revision safely, use `git rev-parse --verify <ref>`.
**Prevention:** Do not use `--` with `git rev-parse` when resolving revisions. Use `--verify` to ensure it only resolves a revision, or `--end-of-options` on newer Git versions, though `--verify` is widely supported for strict reference resolution.
