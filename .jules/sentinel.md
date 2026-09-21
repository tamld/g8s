## 2025-02-14 - SQLite Connection String Injection via File Paths
**Vulnerability:** SQLite file URIs were constructed using `fmt.Sprintf("file:%s?...", dbPath)` without escaping the `dbPath`.
**Learning:** `modernc.org/sqlite` parses query parameters (like `?_pragma=...`) from the file URI. If a user-provided or dynamically generated file path contains a `?`, it can inject arbitrary SQLite pragma or connection options, potentially leading to security bypasses or unintended database configurations.
**Prevention:** Always wrap variable path segments in `url.PathEscape()` (from `net/url`) before embedding them in a SQLite file URI string.

## 2025-02-14 - Slowloris Vulnerability via Missing ReadHeaderTimeout
**Vulnerability:** Go's `http.Server` does not configure `ReadHeaderTimeout` by default. When the application sets `ReadTimeout`, `WriteTimeout`, and `IdleTimeout` but misses `ReadHeaderTimeout`, the server is vulnerable to Slowloris resource-exhaustion DoS attacks.
**Learning:** `http.Server` in Go requires an explicit `ReadHeaderTimeout` because `ReadTimeout` applies to the entire request body, and a malicious client can open a connection and drip-feed headers indefinitely, exhausting the connection pool.
**Prevention:** Always set `ReadHeaderTimeout` alongside other timeouts in `http.Server` configurations.
