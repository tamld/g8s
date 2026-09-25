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
