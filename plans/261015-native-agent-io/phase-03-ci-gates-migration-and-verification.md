# Phase 3 (WF3): CI Gates Migration (`tools/*.sh`), Documentation & Verification

- **Session type**: T1 (strategy)
- **Parent Plan**: [`plan.md`](plan.md)
- **Issue**: `tamld/g8s#593`
- **Allowed Paths**: `tools/**`, `offer/**`, `README.md`

---

## 1. Migrate `tools/ai_lint.sh` & `tools/brief_lint.sh` (Cat 1 & Cat 4)

1. **`tools/ai_lint.sh`**:
   - Add a helper function `run_pattern_search()` that prefers `./bin/g8s search` (with `-g '*.go' -g '!*_test.go' -g '!reference/**'`) before falling back to `rg` -> `find | xargs grep`.
   - Replace `jq -r` calls on `doctor_out` with `./bin/g8s select` when `./bin/g8s` is available, falling back to `jq`.
2. **`tools/brief_lint.sh`**:
   - Prefer `./bin/g8s search --files -g '*.go' -g '!*_test.go' -g '!reference/**'` before falling back to `find`.
3. **`offer/SEARCH_GUIDE.md` & `README.md`**:
   - Document `g8s read`, `g8s edit`, `g8s select` / `--select`, MCP tools (`g8s_search`, `g8s_read`, `g8s_edit`), and HTTP API endpoints (`/api/v1/fs/*`).
   - Run `bash tools/ci_structure_sync.sh --render` to sync `README.md` directory tree with `internal/fsops`.

---

## 2. Verification Matrix

- `go test -race -cover ./internal/fsops/... ./internal/cli/... ./internal/mcp/... ./internal/server/... ./cmd/g8s/...`
- `bash tools/ai_lint_test.sh && bash tools/brief_lint_test.sh`
- `GOTOOLCHAIN=go1.26.9 ~/go/bin/golangci-lint run ./...`
- 3-Stage `ck:code-review` (Spec Compliance, Static Analysis, Adversarial Red-Team Review).
