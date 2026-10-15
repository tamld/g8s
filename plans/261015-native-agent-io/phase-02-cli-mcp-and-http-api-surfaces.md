# Phase 2 (WF2): CLI, MCP, and HTTP REST API Surfaces

- **Session type**: T1 (strategy)
- **Parent Plan**: [`plan.md`](plan.md)
- **Issue**: `tamld/g8s#593`
- **Allowed Paths**: `cmd/g8s/**`, `internal/mcp/**`, `internal/server/**`

---

## 1. CLI Surface (`cmd/g8s/fsops.go` & `cmd/g8s/main.go`)

1. **`g8s read <file> [flags]`**:
   - Flags: `--lines <M:N>` (or `--start-line`, `--end-line`), `--scope-root`, `--receipt-id` (auto-falls back to `os.Getenv("G8S_RECEIPT_ID")`), `--select <dot.path>`, `--json`, `--jsonl`.
   - Default text mode prints `1: <line>` format; `--json` emits standard `g8s` envelope (`kind: "read"`).
2. **`g8s edit <file> [flags]`**:
   - Flags: `--old <string>`, `--new <string>`, `--all`, `--if-sha256 <hash>`, `--scope-root`, `--receipt-id` (auto-falls back to `os.Getenv("G8S_RECEIPT_ID")`), `--select <dot.path>`, `--json`, `--jsonl`.
   - Resolves `AllowedPaths` from the SQLite ControlPlane receipt store whenever a receipt ID is provided via flag or `G8S_RECEIPT_ID`.
3. **`g8s select <dot.path>` + Global `--select` Support**:
   - `g8s select <dot.path>` reads JSON from `stdin` (or `--file`) and prints the extracted value.
   - Global `--select <dot.path>` interceptor in `cli.WriteResponse` (or main command wrapper) so any `g8s <cmd> --select .data.receipt_id` emits the selected field directly in 1 process.

---

## 2. MCP Server Surface (`internal/mcp/server.go`)

Register 3 tools in `internal/mcp/server.go`:
- `g8s_search`: wraps `search.Execute` (`pattern`, `roots`, `fixed_strings`, `ignore_case`, `globs`, `files_only`, `receipt_id`).
- `g8s_read`: wraps `fsops.Read` (`path`, `start_line`, `end_line`, `receipt_id`).
- `g8s_edit`: wraps `fsops.Edit` (`path`, `old_string`, `new_string`, `replace_all`, `if_sha256`, `receipt_id` — required or resolved from `G8S_RECEIPT_ID`).

---

## 3. HTTP REST API Surface (`internal/server/server.go`)

Register 3 routes under `registerRoutes(mux)` (protected by `authMiddleware` and `maxRequestBodyBytes`):
- `POST /api/v1/fs/search` -> executes `search.Execute` (resolves `receipt_id` from `s.store` if supplied).
- `POST /api/v1/fs/read` -> executes `fsops.Read` (resolves `receipt_id` from `s.store` if supplied).
- `POST /api/v1/fs/edit` -> requires valid unconsumed/active `receipt_id` in `s.store`, enforces `AllowedPaths`, and executes `fsops.Edit`.
