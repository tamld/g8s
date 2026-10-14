# Three-Stage Code Review Report (`ck:code-review` — Issue `#591`)

**Session type**: T2 (execution)

> **Target**: Native Cross-Platform Search Engine (`internal/search`, `cmd/g8s/search.go`, `internal/doctor/doctor.go`, `tools/ci_spec_code_sync.sh`, `offer/SEARCH_GUIDE.md`)  
> **Review Protocol**: `ck:code-review` Three-Stage Protocol (Spec Compliance → Code Quality & Edge Cases → Adversarial Red-Team)

---

## Stage 1 — Spec Compliance Review

| Requirement | Implementation Evidence | Status |
| :--- | :--- | :--- |
| 1. `internal/search` concurrent pure-Go engine (`CGO_ENABLED=0`) | `internal/search/search.go` (`Execute`, `runtime.NumCPU()` worker pool, `filepath.ToSlash` normalization, 512-byte NUL binary skipping, `.gitignore`/`.ignore` + default `node_modules`/`vendor` filtering) | **PASS** |
| 2. `g8s search` CLI with `rg`-compatible flags (`-F`, `-i`, `-q`, `-g`, `--files`, `--hidden`, `--no-ignore`) | `cmd/g8s/search.go`, `cmd/g8s/main.go`, `cmd/g8s/search_test.go` (`TestSearchJSONAndFixedStrings`, `TestSearchFilesModeAndQuiet`, `TestSearchInvalidRegexExitUsage`) | **PASS** |
| 3. `g8s` exclusive `--receipt-id <id>` Zero-Trust scope filter | `cmd/g8s/search.go` (`receipt.VerifyReceipt` + `AllowedPaths`), `cmd/g8s/search_test.go` (`TestSearchReceiptScopeFilter`) | **PASS** |
| 4. `g8s doctor` `Search Engine` diagnostic probe | `internal/doctor/doctor.go` (`checkSearchEngine()`) | **PASS** |
| 5. `offer/` Bundle & `ci_spec_code_sync.sh` integration | `offer/SEARCH_GUIDE.md`, `tools/ci_spec_code_sync.sh` (`./bin/g8s search` → `rg` → `grep`/`find`), `README.md` structure render | **PASS** |

---

## Stage 2 — Code Quality & Static Analysis Review

### Findings & Resolutions
1. **`staticcheck SA9003` (Empty `if closeErr != nil` branches & `else if base == ".git"`) in `internal/search/search.go`**:
   - **Verdict**: **ACCEPTED & FIXED**.
   - **Resolution**: Extracted `scanFile(filePath, opts, lowerPattern, re, buf)` using `defer f.Close()`, eliminating 3 empty error-branch blocks and 1 unused `err` struct field.
2. **Verification Gates**:
   - `golangci-lint run ./internal/search/... ./internal/doctor/... ./cmd/g8s/...`: **0 issues**.
   - `bash tools/ai_lint.sh`: **8/8 anti-pattern checks clean**.
   - `go test -race -cover ./internal/search/...`: **91.4% statement coverage, 0 data races**.

---

## Stage 3 — Adversarial Review (Red-Team)

| # | Attack Vector / Hypothesis | Severity | Verdict | Resolution & Regression Test |
| :--- | :--- | :--- | :--- | :--- |
| **RT-1** | **Path Traversal & Absolute Path Bypass in `MatchesAllowedPaths`**: Passing `../secret.txt`, `/etc/passwd`, or `C:/Windows/System32/cmd.exe` against a permissive receipt (`*` or `**`) could match if `MatchesAllowedPaths` did not explicitly reject parent-directory (`..`) and absolute/drive paths. | **HIGH** | **ACCEPTED & FIXED** | Added explicit guard in `MatchesAllowedPaths` rejecting `.`, `..`, `../`, leading `/`, and Windows drive prefixes (`C:`). Covered by `TestSearchAdversarialRedTeam`. |
| **RT-2** | **Naive `strings.Split(pattern, "**")` Prefix/Suffix Collision in `matchGlobstar`**: Splitting `"pkg/**/*.go"` on `"**"` and checking `HasPrefix`/`HasSuffix` failed on wildcard suffixes (`"/*.go"`) and risked prefix boundary collisions (`"pkg"` matching `"pkg_other"`). | **HIGH** | **ACCEPTED & FIXED** | Replaced naive prefix/suffix check with recursive segment-by-segment globstar matcher (`matchGlobstarSegs`). Covered by `TestSearchAdversarialRedTeam`. |
| **RT-3** | **Subdirectory Search + `--scope-root` Receipt Mismatch**: Running `g8s search --receipt-id <id> --scope-root /repo /repo/pkg/allowed` evaluated `relPath` relative to `/repo/pkg/allowed` (`file.go`) instead of `--scope-root` (`pkg/allowed/file.go`), causing false rejections for valid in-scope subdirectories. | **MEDIUM** | **ACCEPTED & FIXED** | Added `ScopeRoot` field to `search.Options` so `AllowedPaths` is always evaluated relative to `ScopeRoot` when provided. Covered by `TestSearchAdversarialRedTeam`. |

---

## Final Review Verdict

- **Status**: **APPROVED (0 unresolved Critical/High/Medium findings)**.
